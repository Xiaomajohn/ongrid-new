// Package webshell turns the edge agent into a generic stream port-
// forwarder. Manager opens a frontier stream into the edge with a
// Meta blob describing the target; the edge dials that local or
// remote TCP socket (decided by the meta's "host:" prefix and an
// injected device_id check), and io.Copy's bytes both ways.
//
// SSH lives entirely on the manager side: manager wraps the stream
// with ssh.NewClientConn, runs PTY + Shell, and pumps to the browser
// WebSocket. The edge has no SSH client, no pty management, no
// session map — it's a one-screen TCP forwarder. This keeps the edge
// tiny and lets the manager be the sole owner of webshell policy /
// audit / concurrency / kick-out logic.
//
// Wire shape (manager → edge stream Meta):
//
//	{"target": "127.0.0.1:22"}                              # default WebSSH path (loopback only)
//	{"target": "host:10.0.0.5:22", "device_id": "fp_…"}    # cross-host SSH via the edge's network namespace
//
// Phase 1 introduces the "host: …" prefix for the per-device
// devicessh + SFTP endpoints. The DeviceID field is an anti-forgery
// guard: the edge compares it to its locally-known device fingerprint
// so a compromised manager can't aim the edge at an unrelated host
// using a device_id the edge has never claimed.
package webshell

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// Acceptor accepts inbound streams. *tunnel.Client satisfies it.
type Acceptor interface {
	AcceptStream() (tunnel.StreamConn, error)
}

// Register kicks off the AcceptStream loop in a goroutine. Each
// accepted stream is dispatched to a forwarder goroutine that lives
// for the duration of the stream. Returns immediately; stops only
// when AcceptStream returns a fatal error (tunnel torn down).
func Register(client Acceptor, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	go acceptLoop(client, log)
	log.Info("webshell: stream forwarder running")
}

// acceptLoop pumps AcceptStream calls forever (until tunnel close).
// Each stream is handed to a separate goroutine — concurrent shells
// don't block one another.
func acceptLoop(client Acceptor, log *slog.Logger) {
	for {
		stream, err := client.AcceptStream()
		if err != nil {
			// Treat "not dialed" / EOF / closed as transient — wait
			// a beat and retry. The tunnel layer drives reconnect.
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "not dialed") || strings.Contains(err.Error(), "closed") {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			log.Warn("webshell: accept stream", slog.Any("err", err))
			time.Sleep(time.Second)
			continue
		}
		go handleStream(stream, log)
	}
}

// streamMeta is the JSON shape the manager puts in the stream's Meta
// blob. Keep field names stable; future fields (ttl, audit_id, ...)
// can be added without breaking older edges as long as we json.Decode
// with allow-unknown-fields semantics (default).
type streamMeta struct {
	Target   string `json:"target"`
	DeviceID string `json:"device_id,omitempty"`
}

// allowedLoopbackTargets restricts legacy loopback-shape requests.
// Today the only sane target is the host's local sshd. The narrow
// scope is the security boundary — without it a compromised manager
// could pivot the edge to any reachable IP.
var allowedLoopbackTargets = map[string]bool{
	"127.0.0.1:22": true,
	"localhost:22": true,
}

// hostTargetPrefix marks the new generic "host:ip:port" shape. We
// pull the dial-target out with strings.TrimPrefix and require the
// remaining string to parse as host:port below. The prefix itself
// is the security-gate label: a bare "127.0.0.1:22" never reaches
// the allowlist-anything branch.
const hostTargetPrefix = "host:"

func handleStream(stream tunnel.StreamConn, log *slog.Logger) {
	defer stream.Close()
	var m streamMeta
	if raw := stream.Meta(); len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			writeStreamError(stream, fmt.Sprintf("bad meta: %v", err))
			return
		}
	}
	target := strings.TrimSpace(m.Target)
	if target == "" {
		target = "127.0.0.1:22"
	}

	// Resolve dial address + (optional) anti-forgery check.
	//
	// Two shapes:
	//   1. bare "127.0.0.1:22" / "localhost:22" — loopback only,
	//      no device_id required (legacy WebSSH today).
	//   2. "host:<ip>:<port>" — generic, requires DeviceID to match
	//      this edge's locally known fingerprint. Any IP:port a
	//      routing table allows; a future revision may tighten
	//      to a per-edge configured jumpbox subnet.
	var dialAddr string
	switch {
	case allowedLoopbackTargets[target]:
		dialAddr = target
	case strings.HasPrefix(target, hostTargetPrefix):
		rest := strings.TrimPrefix(target, hostTargetPrefix)
		if rest == "" || !strings.Contains(rest, ":") {
			writeStreamError(stream, fmt.Sprintf("target %q malformed (want host:ip:port)", target))
			log.Warn("webshell: rejected host: target shape", slog.String("target", target))
			return
		}
		if !verifyDeviceID(m.DeviceID) {
			writeStreamError(stream, "device_id missing or does not match this edge")
			log.Warn("webshell: host: target rejected by device_id guard",
				slog.String("device_id_seen", m.DeviceID))
			return
		}
		dialAddr = rest
	default:
		writeStreamError(stream, fmt.Sprintf("target %q not allowed", target))
		log.Warn("webshell: rejected target", slog.String("target", target))
		return
	}

	conn, err := net.DialTimeout("tcp", dialAddr, 5*time.Second)
	if err != nil {
		writeStreamError(stream, fmt.Sprintf("dial %s: %v", dialAddr, err))
		return
	}
	defer conn.Close()

	log.Info("webshell: forwarding", slog.String("target", dialAddr))

	// Bidirectional copy. First side to error closes the other.
	errs := make(chan error, 2)
	go func() {
		_, err := io.Copy(conn, stream)
		errs <- err
	}()
	go func() {
		_, err := io.Copy(stream, conn)
		errs <- err
	}()
	<-errs
	// Closing both ends releases the surviving io.Copy.
	_ = conn.Close()
	_ = stream.Close()
	<-errs
}

// writeStreamError sends a brief plain-text error to the stream so
// the manager-side ssh.NewClientConn fails with a useful message
// rather than a generic "EOF on protocol read".
func writeStreamError(s io.Writer, msg string) {
	_, _ = io.WriteString(s, "ongrid-edge webshell forwarder: "+msg+"\n")
}

// localDeviceID returns the edge's own device fingerprint, or "" if
// the edge hasn't completed register yet (or hasn't been wired with
// a fingerprint — pre-introduction binary). Threads through package
// scope via SetLocalDeviceID; the alternative (lookup against a
// persistent store on every stream) is overkill for a per-stream
// check.
var (
	localDeviceIDMu sync.RWMutex
	localDeviceID   string
)

// SetLocalDeviceID installs the edge's fingerprint at boot. Called
// once the register handshake has produced a fingerprint; until
// then verifyDeviceID rejects every "host: …" request so a stale
// edge doesn't carry cross-host SSH for free.
func SetLocalDeviceID(fp string) {
	localDeviceIDMu.Lock()
	localDeviceID = strings.TrimSpace(fp)
	localDeviceIDMu.Unlock()
}

// verifyDeviceID reports whether seen matches the edge's locally-
// stored fingerprint. Empty seen always returns false (the field is
// mandatory for host: targets).
func verifyDeviceID(seen string) bool {
	if strings.TrimSpace(seen) == "" {
		return false
	}
	localDeviceIDMu.RLock()
	cur := localDeviceID
	localDeviceIDMu.RUnlock()
	if cur == "" {
		// Not yet registered — refuse. A fresh-edge deploy is the
		// one window a forged device_id could otherwise slip in
		// before the manager pairs with us.
		return false
	}
	return seen == cur
}
