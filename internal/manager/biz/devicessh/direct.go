package devicessh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ongridio/ongrid/internal/manager/model/device"
)

// DirectDialer opens a TCP SSH session straight from the manager to the
// device's ssh_host:ssh_port. It owns the full lifecycle:
//   - parses credentials from device.SSHPassword / device.SSHKey
//     (plaintext at rest — see plan §"凭据策略变更")
//   - negotiates SSH transport with ToFU host-key handling
//   - hands back a Session whose Read/Write track the remote stdio
//
// Concurrency: instances are stateless apart from the timeout knob, so
// they're safe to share across goroutines. Conn pooling is intentionally
// not in v1 — a SFTP browser tab opens one SSH connection per click and
// drops it on close; we can add a pool once the URL surface is stable.
type DirectDialer struct {
	timeout time.Duration
	// hostKeySaved is an optional hook invoked on a fresh ToFU connection
	// with the wire-encoded host key for the manager to persist into
	// devices.ssh_host_key. When nil we silently ignore the key — wiring
	// happens at D1.
	hostKeySaved func(host string, port int, wireKey string) error
}

// NewDirectDialer constructs a direct dialer with the supplied TCP
// connect timeout (used for both the TCP dial and the SSH handshake).
func NewDirectDialer(timeout time.Duration) *DirectDialer {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &DirectDialer{timeout: timeout}
}

// WithHostKeySaver installs a ToFU hook. Tests pass nil; production
// passes a callback that writes to devices.ssh_host_key.
func (d *DirectDialer) WithHostKeySaver(fn func(host string, port int, wireKey string) error) *DirectDialer {
	d.hostKeySaved = fn
	return d
}

// Connect dials, handshakes, and returns the established *ssh.Client.
// The SFTP service reuses this to start the sftp subsystem without
// paying for a second TCP handshake.
func (d *DirectDialer) Connect(ctx context.Context, dev *device.Device) (*ssh.Client, error) {
	if err := checkDeviceForDirect(dev); err != nil {
		return nil, err
	}
	cfg, err := d.buildClientConfig(dev)
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("%s:%d", dev.SSHHost, dev.SSHPort)
	if dev.SSHPort == 0 {
		addr = fmt.Sprintf("%s:22", dev.SSHHost)
	}

	// Honour ctx: open the TCP socket with a timeout, then drive the
	// handshake under a goroutine + ctx.Done race so a cancelled caller
	// doesn't hang on the SSH banner.
	dialer := &net.Dialer{Timeout: d.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("devicessh.direct: dial %s: %w", addr, err)
	}

	type chResult struct {
		c   ssh.Conn
		ch  <-chan ssh.NewChannel
		req <-chan *ssh.Request
		err error
	}
	res := make(chan chResult, 1)
	go func() {
		c, ch, req, e := ssh.NewClientConn(conn, addr, cfg)
		res <- chResult{c, ch, req, e}
	}()

	select {
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	case r := <-res:
		if r.err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("devicessh.direct: ssh client %s: %w", addr, r.err)
		}
		return ssh.NewClient(r.c, r.ch, r.req), nil
	}
}

// Dial returns a Session for the device. Implementation delegates the
// connection to Connect() so the SFTP service can skip the second-hand
// session dance.
func (d *DirectDialer) Dial(ctx context.Context, dev *device.Device) (Session, error) {
	client, err := d.Connect(ctx, dev)
	if err != nil {
		return nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.direct: new session: %w", err)
	}

	stdinW, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.direct: stdin pipe: %w", err)
	}
	stdoutR, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.direct: stdout pipe: %w", err)
	}
	stderrR, err := sess.StderrPipe()
	if err != nil {
		_ = sess.Close()
		_ = client.Close()
		return nil, fmt.Errorf("devicessh.direct: stderr pipe: %w", err)
	}

	// Merge stdout + stderr into a single Read source so callers don't
	// have to interleave two pipes.
	merged := io.MultiReader(stdoutR, stderrR)

	return &directSession{
		client:  client,
		sess:    sess,
		stdinW:  stdinW,
		stdoutR: merged,
		host:    dev.SSHHost,
		port:    dev.SSHPort,
	}, nil
}

// checkDeviceForDirect returns nil-or-err for the precondition that
// every direct call shares: ssh_host + ssh_user + (password OR key).
// The bundled ErrSSHConfigMissing error wraps ErrInvalid so HTTP can map
// it to 428 / 400.
func checkDeviceForDirect(dev *device.Device) error {
	if dev == nil {
		return fmt.Errorf("%w: device is nil", ErrSSHConfigMissing)
	}
	if dev.SSHHost == "" {
		return fmt.Errorf("%w: ssh_host empty", ErrSSHConfigMissing)
	}
	if dev.SSHUser == "" {
		return fmt.Errorf("%w: ssh_user empty", ErrSSHConfigMissing)
	}
	if dev.SSHPassword == "" && dev.SSHKey == "" {
		return fmt.Errorf("%w: ssh_password or ssh_key required", ErrSSHConfigMissing)
	}
	return nil
}

// buildClientConfig assembles the SSH client config from the device row.
// ToFU host-key handling:
//
//   - If devices.ssh_host_key is populated, parse it (authorized_keys
//     format) and pin via ssh.FixedHostKey — strict pin from now on.
//   - Otherwise we install an InsecureIgnoreHostKey callback that, on
//     first sight, captures the server's wire key through the optional
//     hostKeySaved hook. The hook is nil in tests.
//
// Fail-closed: if a known-key string exists but fails to parse, we
// return an error rather than silently degrading to ignore (the kind of
// silent TOFU downgrade that bit LinkedIn / Cloudflare in the past).
func (d *DirectDialer) buildClientConfig(dev *device.Device) (*ssh.ClientConfig, error) {
	auths := []ssh.AuthMethod{}
	if dev.SSHPassword != "" {
		auths = append(auths, ssh.Password(dev.SSHPassword))
	}
	if dev.SSHKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(dev.SSHKey))
		if err != nil {
			return nil, fmt.Errorf("devicessh.direct: parse private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}

	callback, err := d.hostKeyCallback(dev)
	if err != nil {
		return nil, err
	}

	return &ssh.ClientConfig{
		User:            dev.SSHUser,
		Auth:            auths,
		HostKeyCallback: callback,
		Timeout:         d.timeout,
	}, nil
}

// hostKeyCallback installs the ToFU-or-pin policy. See buildClientConfig.
func (d *DirectDialer) hostKeyCallback(dev *device.Device) (ssh.HostKeyCallback, error) {
	if dev.SSHHostKey != "" {
		known, err := ssh.ParsePublicKey([]byte(dev.SSHHostKey))
		if err != nil {
			return nil, fmt.Errorf("devicessh.direct: parse stored ssh_host_key: %w", err)
		}
		return ssh.FixedHostKey(known), nil
	}
	// First-connect path. We capture the server's wire key via the
	// hook if the manager wired one in; if not, we just skip the
	// verification step (returning nil error from InsecureIgnoreHostKey).
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if d.hostKeySaved == nil {
			return nil
		}
		wire := string(ssh.MarshalAuthorizedKey(key))
		if err := d.hostKeySaved(dev.SSHHost, dev.SSHPort, wire); err != nil {
			// Hook failures are logged but don't block the first
			// connection — the next connect will retry the save.
			return nil //nolint:nilerr // best-effort ToFU persistence
		}
		return nil
	}, nil
}

// directSession implements Session for direct (non-tunnel) calls.
type directSession struct {
	client  *ssh.Client
	sess    *ssh.Session
	stdinW  io.WriteCloser
	stdoutR io.Reader
	host    string
	port    int
}

// Read forwards merged stdout/stderr bytes from the remote command.
func (s *directSession) Read(p []byte) (int, error) { return s.stdoutR.Read(p) }

// Write forwards bytes to the remote stdin (used for Shell mode input
// and for piping data into Run() via stdin redirection at the wire level).
func (s *directSession) Write(p []byte) (int, error) { return s.stdinW.Write(p) }

// Run executes cmd on the remote host. The command's stdout/stderr has
// already been wired through directSession.stdoutR before this call.
// Returns nil on remote exit-code 0; on any non-zero exit, the error is
// an *ssh.ExitError or a transport-level failure.
//
// NOTE: v1 doesn't surface the exit code through this error path —
// callers that care about the code should use *ssh.ExitError type
// assertion. The shell handler reaches the same number via the
// OnExit(exitCode, errMsg) sink anyway.
func (s *directSession) Run(ctx context.Context, cmd string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Run blocks until the remote command exits. v1 doesn't propagate
	// the exit code through the error here — see comment above.
	if err := s.sess.Run(cmd); err != nil {
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("devicessh.direct: remote exit %d: %w", exitErr.ExitStatus(), err)
		}
		return fmt.Errorf("devicessh.direct: run %q: %w", cmd, err)
	}
	return nil
}

// Shell starts an interactive shell. The browser-side xterm.js pumps
// bytes through Read/Write while the remote PTY echoes them back through
// Read.
func (s *directSession) Shell(ctx context.Context, rows, cols int, term string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := s.sess.RequestPty(term, rows, cols, modes); err != nil {
		return fmt.Errorf("devicessh.direct: request pty: %w", err)
	}
	if err := s.sess.Shell(); err != nil {
		return fmt.Errorf("devicessh.direct: shell: %w", err)
	}
	return nil
}

// Close shuts down the session and (since DirectDialer owns the
// underlying client) the SSH connection itself. Idempotent.
func (s *directSession) Close() error {
	if s.sess == nil {
		// Already closed.
		if s.client != nil {
			return s.client.Close()
		}
		return nil
	}
	sessErr := s.sess.Close()
	clientErr := s.client.Close()
	// Nil out so a second Close returns nil.
	s.sess = nil
	s.client = nil
	switch {
	case sessErr != nil:
		return sessErr
	default:
		return clientErr
	}
}
