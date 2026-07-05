package devicessh

import (
	"context"
	"errors"
	"io"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ongridio/ongrid/internal/manager/model/device"
)

// Purpose tags a Dial request with the consumer's intent, so the Router
// can apply policy that depends on what the SSH session is for:
//
//   - PurposeShell: interactive WebSSH drawer (terminal emulator in the
//     browser). May take the tunnel-via-edge path when an online edge
//     exists for the device.
//   - PurposeSFTP: file-management API surface (list/read/write/mkdir/
//     rmdir/rename/chmod/stat/upload/download). May take the tunnel path.
//   - PurposeInstallEdge: the one-click install worker. Always direct
//     even when an online edge already exists — the target machine is the
//     pre-install state and any existing edge on it may not be reachable
//     yet.
//
// Adding a new purpose is intentionally a small enum bump; the router's
// policy switch is the only call site that branches.
type Purpose int

const (
	PurposeShell Purpose = iota
	PurposeSFTP
	PurposeInstallEdge
)

// String returns the wire / log form of a Purpose.
func (p Purpose) String() string {
	switch p {
	case PurposeShell:
		return "shell"
	case PurposeSFTP:
		return "sftp"
	case PurposeInstallEdge:
		return "install-edge"
	default:
		return "unknown"
	}
}

// ErrSSHConfigMissing is returned by Router.Pick when no credentials are
// available (neither edge-tunnel nor direct-SSH). It's exported so HTTP
// handlers can map it to 428 Precondition Required via errs.HTTPStatus.
var ErrSSHConfigMissing = errors.New("devicessh: ssh_host/user/credential required")

// Dialer abstracts "open one SSH session against a Device". The Router
// picks one of the two implementations (DirectDialer / TunnelDialer); the
// SFTP service additionally uses the direct path's underlying
// *ssh.Client via MustConnect (see router.go).
//
// Implementations MUST honour ctx cancellation by aborting the underlying
// TCP / geminio session.
type Dialer interface {
	// Dial returns a Session for one-shot shell or one-command execution.
	// Callers Close() the session when done; Dialer may reuse one
	// connection across many sessions in a future optimisation.
	Dial(ctx context.Context, d *device.Device) (Session, error)
}

// Session is the live SSH session surface. The embedded ReadWriteCloser
// hands the caller the interactive stdio (Run writes go through Write,
// command stdout arrives through Read). For SFTP, callers should NOT use
// the embedded RW — they should reach for the underlying *ssh.Client via
// SFTPService's injected sshClient func (see sftp.go).
//
// Closing a Session releases both the SSH session half and the underlying
// client connection (when the dialer owns the connection — true for the
// tunnel path, half-true for direct: see directSession.Close for the
// reference behaviour).
type Session interface {
	io.ReadWriteCloser

	// Run executes cmd remotely; returns nil on exit-code 0 and a non-nil
	// error otherwise. Combined stdout/stderr stream out via Read().
	Run(ctx context.Context, cmd string) error

	// Shell starts an interactive PTY shell with the given terminal size
	// and TERM type. Subsequent stdio is bidirectional over RW until the
	// peer disconnects or Close() is called.
	Shell(ctx context.Context, rows, cols int, term string) error

	// Close shuts down the session and (where appropriate) the underlying
	// connection. Idempotent.
	Close() error
}

// SessionConfig is the per-call knob bundle future dialers may want to
// accept. Kept inline here as a structural placeholder so DirectDialer /
// TunnelDialer can grow without redefining the Dialer interface for every
// new field.
type SessionConfig struct {
	ConnectTimeout time.Duration
	KeepAlive      time.Duration
	Env            []string // sent in shell mode
	PTY            bool     // direct dialer uses this for Shell vs. Run
}

// DefaultSessionConfig returns the default knobs we apply when the caller
// doesn't override.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		ConnectTimeout: 10 * time.Second,
		KeepAlive:      30 * time.Second,
	}
}

// SSHClientConfig is a type alias re-export so other files in this
// package can name the type without importing golang.org/x/crypto/ssh
// directly. Keeping the import centralised in dialer.go avoids a fan
// of crypto/ssh imports across the seven files.
type SSHClientConfig = ssh.ClientConfig
