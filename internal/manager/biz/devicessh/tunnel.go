package devicessh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ongridio/ongrid/internal/manager/model/device"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// ErrTunnelNotImplemented was the placeholder sentinel returned by
// TunnelDialer.Dial in the A3 phase. The B1 patch implements the
// tunnel path; the sentinel is retained so any external caller or
// test still pattern-matching on it stays compile-clean. Future
// phases should retire it entirely.
var ErrTunnelNotImplemented = errors.New("devicessh: tunnel dialer not yet implemented (B1)")

// TunnelStreamOpener opens a geminio byte stream against an edge agent.
// The narrow signature matches *managersvcfb.Client.OpenStream so D1
// can wire the production frontierbound client without an adapter; the
// target argument is the Meta target the edge side honours (see plan
// §"openShell stream meta target"). Pass "" to fall back to the edge's
// default 127.0.0.1:22 (today's behaviour, sufficient for v1).
//
// Returned io.ReadWriteCloser must be safe to Read/Write concurrently
// with Close; the SSH client overlay relies on closing once to also tear
// down the underlying geminio stream.
type TunnelStreamOpener interface {
	OpenTunnelStream(ctx context.Context, edgeID uint64, target string) (io.ReadWriteCloser, error)
}

// TunnelEdgeResolver resolves device_id → owning edge_id (Type=Host).
// Production wires *devicebiz.Usecase.LookupEdgeForDevice; tests pass a
// table-driven fake.
type TunnelEdgeResolver interface {
	LookupEdgeForDevice(ctx context.Context, deviceID uint64) (uint64, error)
}

// TunnelDialer is the "route traffic through an edge agent" Dialer.
// v1 prefers the direct path's *ssh.Client for SFTP (a fresh
// connection to host:22 is cheap); the tunnel path is for the
// interactive shell when the device is on a private network the manager
// can't reach directly.
type TunnelDialer struct {
	streamer  TunnelStreamOpener
	resolver  TunnelEdgeResolver
	timeout   SSHTimeout
	keepAlive bool
}

// SSHTimeout is the connect-time ceiling for both the geminio stream
// open and the SSH handshake over the stream. Exported so D1 can inject
// non-default values without reaching into the unexported field.
type SSHTimeout struct {
	Connect time.Duration
	Handshake time.Duration
}

// NewTunnelDialer wires the dependencies. resolver may be nil — Dial
// then surfaces ErrTunnelNotImplemented without attempting the lookup.
func NewTunnelDialer(streamer TunnelStreamOpener, resolver TunnelEdgeResolver, timeout SSHTimeout) *TunnelDialer {
	if timeout.Connect <= 0 {
		timeout.Connect = 10 * time.Second
	}
	if timeout.Handshake <= 0 {
		timeout.Handshake = 10 * time.Second
	}
	return &TunnelDialer{
		streamer:  streamer,
		resolver:  resolver,
		timeout:   timeout,
		keepAlive: true,
	}
}

// Dial implements Dialer for the tunnel route. The handshake mirrors
// internal/manager/server/webshell/http.go's openShell: open a geminio
// stream to the edge, overlay ssh.NewClientConn on top, then drive a
// session via the SSH client.
//
// Returned errors:
//
//   - ErrSSHConfigMissing: device has no edge attached (resolver
//     returned (0, nil) or errs.ErrNotFound). Mapped to 428/503 by
//     HTTP.
//   - ctx.Err(): caller cancelled while we were opening the stream or
//     waiting on the SSH handshake. Partial state is torn down.
//   - any wrapped transport error from the geminio stream or the SSH
//     handshake itself.
func (t *TunnelDialer) Dial(ctx context.Context, dev *device.Device) (Session, error) {
	if t.streamer == nil {
		return nil, fmt.Errorf("devicessh.tunnel: no TunnelStreamOpener wired")
	}
	if t.resolver == nil {
		return nil, fmt.Errorf("devicessh.tunnel: no TunnelEdgeResolver wired")
	}
	if dev == nil {
		return nil, fmt.Errorf("%w: device is nil", ErrSSHConfigMissing)
	}

	client, stream, _, err := t.connect(ctx, dev)
	if err != nil {
		return nil, err
	}

	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close() // propagates to stream via rwcAdapter
		return nil, fmt.Errorf("devicessh.tunnel: new session: %w", err)
	}
	stdinW, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.tunnel: stdin pipe: %w", err)
	}
	stdoutR, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.tunnel: stdout pipe: %w", err)
	}
	stderrR, err := sess.StderrPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.tunnel: stderr pipe: %w", err)
	}

	_ = stream // stream lifetime is owned by client.Close() via rwcAdapter

	return &tunnelSession{
		client:  client,
		sess:    sess,
		stdinW:  stdinW,
		stdoutR: io.MultiReader(stdoutR, stderrR),
		stream:  stream,
		host:    dev.SSHHost,
		port:    dev.SSHPort,
	}, nil
}

// Connect returns a live *ssh.Client without opening a session. The
// SFTP service uses this so it can call sftp.NewClient on the same
// SSH client without paying for a second tunnel/handshake.
//
// The returned client's Close() propagates to the underlying geminio
// stream via the rwcAdapter → net.Conn.Close chain; callers should
// not double-close.
func (t *TunnelDialer) Connect(ctx context.Context, dev *device.Device) (*ssh.Client, error) {
	if t.streamer == nil {
		return nil, fmt.Errorf("devicessh.tunnel: no TunnelStreamOpener wired")
	}
	if t.resolver == nil {
		return nil, fmt.Errorf("devicessh.tunnel: no TunnelEdgeResolver wired")
	}
	if dev == nil {
		return nil, fmt.Errorf("%w: device is nil", ErrSSHConfigMissing)
	}

	client, _, _, err := t.connect(ctx, dev)
	return client, err
}

// connect is the shared plumbing for Dial + Connect: resolver lookup,
// stream-open with timeout, SSH handshake with ctx-aware cancellation.
// On success returns (client, stream, edgeID, nil); on failure partial
// state (any open stream, any partial conn) is torn down before the
// error is returned.
//
// The returned stream is also reachable through the *ssh.Client
// (rwcAdapter holds it), so closing the client closes the stream too.
// We surface the stream handle to callers anyway so tunnelSession can
// hold a back-reference for explicit cleanup.
func (t *TunnelDialer) connect(ctx context.Context, dev *device.Device) (*ssh.Client, io.ReadWriteCloser, uint64, error) {
	edgeID, err := t.resolveEdge(ctx, dev)
	if err != nil {
		return nil, nil, 0, err
	}

	target := tunnelTarget(dev)

	streamCtx, cancel := context.WithTimeout(ctx, t.timeout.Connect)
	stream, err := t.streamer.OpenTunnelStream(streamCtx, edgeID, target)
	cancel()
	if err != nil {
		return nil, nil, edgeID, fmt.Errorf("devicessh.tunnel: open stream to edge %d (target=%q): %w", edgeID, target, err)
	}

	client, err := t.sshHandshake(ctx, dev, stream, edgeID)
	if err != nil {
		return nil, nil, edgeID, err
	}
	return client, stream, edgeID, nil
}

// resolveEdge runs the resolver and translates the documented
// return-contract (id>0 + nil; 0 + nil; ErrNotFound; other) into a
// single ErrSSHConfigMissing for the no-edge branches. Anything else
// bubbles up wrapped.
func (t *TunnelDialer) resolveEdge(ctx context.Context, dev *device.Device) (uint64, error) {
	edgeID, err := t.resolver.LookupEdgeForDevice(ctx, dev.ID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return 0, fmt.Errorf("%w: no edge attached to device %d", ErrSSHConfigMissing, dev.ID)
		}
		return 0, fmt.Errorf("devicessh.tunnel: lookup edge for device %d: %w", dev.ID, err)
	}
	if edgeID == 0 {
		return 0, fmt.Errorf("%w: no edge attached to device %d", ErrSSHConfigMissing, dev.ID)
	}
	return edgeID, nil
}

// tunnelTarget maps a device row onto the geminio Meta target string.
// Empty SSHHost → "" (edge defaults to 127.0.0.1:22). Otherwise
// "host:1.2.3.4:22" — the cross-host shape documented in
// internal/edgeagent/webshell/handler.go (handleStream, hostTargetPrefix).
// Port is hard-coded to 22 for v1 because the tunnel path trusts the
// edge's local SSH config; a future v1.1 may thread device.SSHPort.
func tunnelTarget(dev *device.Device) string {
	if dev.SSHHost == "" {
		return ""
	}
	return "host:" + dev.SSHHost + ":22"
}

// sshHandshake runs ssh.NewClientConn on top of the geminio stream
// under a goroutine + ctx.Done race so cancellation is honoured.
//
// The TCP "address" passed to NewClientConn is "127.0.0.1:22" — SSH
// only uses it for banner / kex logging; the actual bytes are the
// geminio stream.
func (t *TunnelDialer) sshHandshake(
	ctx context.Context,
	dev *device.Device,
	stream io.ReadWriteCloser,
	edgeID uint64,
) (*ssh.Client, error) {
	cfg, err := t.buildClientConfig(dev)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}

	type chResult struct {
		c   ssh.Conn
		ch  <-chan ssh.NewChannel
		req <-chan *ssh.Request
		err error
	}
	res := make(chan chResult, 1)
	go func() {
		c, ch, req, e := ssh.NewClientConn(rwcAdapter{rwc: stream}, "127.0.0.1:22", cfg)
		res <- chResult{c, ch, req, e}
	}()

	select {
	case <-ctx.Done():
		_ = stream.Close()
		return nil, ctx.Err()
	case r := <-res:
		if r.err != nil {
			_ = stream.Close()
			return nil, fmt.Errorf("devicessh.tunnel: ssh handshake over edge %d: %w", edgeID, r.err)
		}
		return ssh.NewClient(r.c, r.ch, r.req), nil
	}
}

// buildClientConfig assembles the SSH config for the tunnel path.
//
// HostKeyCallback is ssh.InsecureIgnoreHostKey because the SSH server
// reachable through the tunnel is one the manager already trusts:
// the bytes terminate at the edge's local 127.0.0.1:22, i.e. the
// edge's own SSH server. Pinning it would require a second known-hosts
// surface; deferred to v2.
//
// SSHUser + (SSHPassword OR SSHKey) must be set — same precondition as
// direct.go's checkDeviceForDirect, modulo SSHHost which is optional
// here (empty SSHHost → loopback target).
func (t *TunnelDialer) buildClientConfig(dev *device.Device) (*ssh.ClientConfig, error) {
	if dev.SSHUser == "" {
		return nil, fmt.Errorf("%w: ssh_user empty", ErrSSHConfigMissing)
	}
	auths := []ssh.AuthMethod{}
	if dev.SSHPassword != "" {
		auths = append(auths, ssh.Password(dev.SSHPassword))
	}
	if dev.SSHKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(dev.SSHKey))
		if err != nil {
			return nil, fmt.Errorf("devicessh.tunnel: parse private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("%w: ssh_password or ssh_key required", ErrSSHConfigMissing)
	}
	return &ssh.ClientConfig{
		User:            dev.SSHUser,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         t.timeout.Handshake,
	}, nil
}

// rwcAdapter bridges an io.ReadWriteCloser (geminio stream) into the
// net.Conn interface ssh.NewClientConn requires. SSH only really uses
// Read/Write/Close; the addr/deadline methods are stubs.
//
// A copy of this type lives in internal/manager/server/webshell/http.go;
// keeping a private copy here means devicessh doesn't reach into the
// server package for an unexported helper.
type rwcAdapter struct {
	rwc io.ReadWriteCloser
}

func (a rwcAdapter) Read(p []byte) (int, error)       { return a.rwc.Read(p) }
func (a rwcAdapter) Write(p []byte) (int, error)      { return a.rwc.Write(p) }
func (a rwcAdapter) Close() error                     { return a.rwc.Close() }
func (a rwcAdapter) LocalAddr() net.Addr              { return noopAddr{} }
func (a rwcAdapter) RemoteAddr() net.Addr             { return noopAddr{} }
func (a rwcAdapter) SetDeadline(time.Time) error      { return nil }
func (a rwcAdapter) SetReadDeadline(time.Time) error  { return nil }
func (a rwcAdapter) SetWriteDeadline(time.Time) error { return nil }

type noopAddr struct{}

func (noopAddr) Network() string { return "tunnel" }
func (noopAddr) String() string  { return "tunnel" }

// Compile-time guards.
var (
	_ Dialer = (*TunnelDialer)(nil)
)

// tunnelSession implements Session for the tunnel-via-edge path.
// Lifecycle mirrors directSession but Read/Write operate on the SSH
// session's stdio pipes (the geminio stream is owned by client.Close()).
type tunnelSession struct {
	mu      sync.Mutex
	client  *ssh.Client
	sess    *ssh.Session
	stdinW  io.WriteCloser
	stdoutR io.Reader
	stream  io.ReadWriteCloser
	host    string
	port    int
	closed  bool
}

// Read forwards merged stdout/stderr bytes from the remote shell.
// Run-mode callers also see command output through this same path.
func (s *tunnelSession) Read(p []byte) (int, error) {
	if s.stdoutR == nil {
		return 0, io.EOF
	}
	return s.stdoutR.Read(p)
}

// Write forwards bytes to the remote stdin.
func (s *tunnelSession) Write(p []byte) (int, error) {
	if s.stdinW == nil {
		return 0, io.ErrClosedPipe
	}
	return s.stdinW.Write(p)
}

// Run executes cmd on the remote host. Combines stdout/stderr into
// Read(). Returns nil on exit 0; on non-zero, an *ssh.ExitError or a
// transport-level failure wrapped with the devicessh.tunnel prefix.
func (s *tunnelSession) Run(ctx context.Context, cmd string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.sess == nil {
		return fmt.Errorf("devicessh.tunnel: session not initialised")
	}
	if err := s.sess.Run(cmd); err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("devicessh.tunnel: remote exit %d: %w", exitErr.ExitStatus(), err)
		}
		return fmt.Errorf("devicessh.tunnel: run %q: %w", cmd, err)
	}
	return nil
}

// Shell starts an interactive PTY shell.
func (s *tunnelSession) Shell(ctx context.Context, rows, cols int, term string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.sess == nil {
		return fmt.Errorf("devicessh.tunnel: session not initialised")
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := s.sess.RequestPty(term, rows, cols, modes); err != nil {
		return fmt.Errorf("devicessh.tunnel: request pty: %w", err)
	}
	if err := s.sess.Shell(); err != nil {
		return fmt.Errorf("devicessh.tunnel: shell: %w", err)
	}
	return nil
}

// Close shuts down the SSH session, the SSH client (which propagates to
// the geminio stream via rwcAdapter), and the stream itself as a
// belt-and-braces measure. Idempotent.
func (s *tunnelSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var firstErr error
	if s.sess != nil {
		if err := s.sess.Close(); err != nil {
			firstErr = err
		}
		s.sess = nil
	}
	if s.client != nil {
		if err := s.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.client = nil
	}
	if s.stream != nil {
		if err := s.stream.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.stream = nil
	}
	return firstErr
}
