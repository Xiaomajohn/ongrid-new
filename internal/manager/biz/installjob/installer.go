package installjob

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	devicessh "github.com/ongridio/ongrid/internal/manager/biz/devicessh"
	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
)

// remoteScriptPath is the temporary location of the uploaded install.sh on
// the target host. The script is idempotent (overwrites the env file +
// restarts the service on re-run), but we drop it under /tmp rather than a
// persistent location so a successful install doesn't leave a stale copy
// on every host forever.
const remoteScriptPath = "/tmp/ongrid-install.sh"

// SSHInstaller is the production Installer concrete. It pulls a
// devicessh.Router from the wiring layer; the install path ALWAYS uses
// devicessh.PurposeInstallEdge, which forces the router onto the direct
// dialer (no online edge yet — that's what we're installing).
//
// The install.sh bytes are passed in by main.go so the manager binary
// can stay free of an embed (D1-Wire does
// `//go:embed deploy/install/edge/install.sh` next to the wiring block
// and threads the resulting []byte into NewSSHInstaller). Today the
// bytes are accepted as an opaque payload — the installer does not
// inspect or rewrite them.
type SSHInstaller struct {
	router         *devicessh.Router
	installScript  []byte
	serverEdgeAddr string
	serverHTTPAddr string
	log            *slog.Logger

	// connectTimeout bounds the TCP+SSH handshake per attempt.
	connectTimeout time.Duration
}

// NewSSHInstaller wires the concrete. installScript is the install.sh
// payload (D1-Wire loads it via go:embed from deploy/install/edge/install.sh).
// serverEdgeAddr / serverHTTPAddr are passed through to the script as
// `--server-edge-addr=` / `--server-http-addr=` arguments. log may be nil
// (defaults to slog.Default()).
func NewSSHInstaller(
	router *devicessh.Router,
	installScript []byte,
	serverEdgeAddr string,
	serverHTTPAddr string,
	log *slog.Logger,
) *SSHInstaller {
	if log == nil {
		log = slog.Default()
	}
	return &SSHInstaller{
		router:         router,
		installScript:  installScript,
		serverEdgeAddr: serverEdgeAddr,
		serverHTTPAddr: serverHTTPAddr,
		log:            log.With(slog.String("comp", "installjob-installer")),
		connectTimeout: 30 * time.Second,
	}
}

// Install implements Installer.
//
// Flow:
//
//  1. Build a transient *device.Device snapshot from the Job row (no DB
//     read — host:port:user:credential live on the Job as a snapshot).
//  2. router.MustConnect(ctx, dev, PurposeInstallEdge) → *ssh.Client.
//     The InstallEdge purpose forces the direct path; tunnel is rejected
//     by the router because no edge can be online yet (that's what we're
//     installing).
//  3. client.NewSession() + StdoutPipe/StderrPipe.
//  4. Upload install.sh via a heredoc into /tmp, then run the script with
//     --access-key / --secret-key / --server-edge-addr / --server-http-addr.
//  5. Stream stdout + stderr through onLog as chunks arrive.
//
// The split between "upload install.sh" and "run install.sh" uses two
// separate SSH sessions because the heredoc has a hard upper bound on
// line length (~LINE_MAX on the remote shell), and a multi-kilobyte
// install.sh payload would shred that. Each SSH session is closed
// independently so a write failure leaves the remote in a clean state.
func (i *SSHInstaller) Install(ctx context.Context, job *InstallJob, accessKey, secretKey string, onLog func(chunk string)) error {
	if i.router == nil {
		return fmt.Errorf("installjob: installer: router not wired")
	}
	if len(i.installScript) == 0 {
		return fmt.Errorf("installjob: installer: installScript empty (did main.go wire the embed?)")
	}
	if onLog == nil {
		// Be defensive — Worker always passes a callback, but a test may
		// not. Falling back to a no-op lets the install proceed; log
		// output just won't land anywhere.
		onLog = func(string) {}
	}

	dev := buildDeviceSnapshot(job)
	i.log.Info("installjob: ssh connect start",
		slog.Uint64("device_id", job.DeviceID),
		slog.String("host", dev.SSHHost),
		slog.Int("port", dev.SSHPort),
		slog.String("user", dev.SSHUser),
	)

	// 1) upload install.sh via a short-lived SSH session.
	if err := i.uploadScript(ctx, dev, onLog); err != nil {
		return fmt.Errorf("installjob: upload install.sh: %w", err)
	}

	// 2) run install.sh in a fresh SSH session, streaming stdout/stderr.
	if err := i.runScript(ctx, dev, accessKey, secretKey, onLog); err != nil {
		return fmt.Errorf("installjob: run install.sh: %w", err)
	}
	return nil
}

// buildDeviceSnapshot assembles the transient device row used by the
// devicessh router + dialer. The fields populated here are exactly
// the ones checkDeviceForDirect reads; everything else is left zero.
//
// Notes:
//   - SSHPassword / SSHKey come from the Job's plaintext snapshot, not
//     from the persisted device row, because the user may have rotated
//     the credential after the install was queued.
//   - SSHPort defaults to 22 when the Job snapshot has 0; the install
//     row's port column is NOT NULL but a manually-built row from an
//     older HTTP handler may have slipped 0 through.
func buildDeviceSnapshot(job *InstallJob) *devicemodel.Device {
	port := job.Port
	if port == 0 {
		port = 22
	}
	return &devicemodel.Device{
		ID:          job.DeviceID,
		SSHHost:     job.Host,
		SSHPort:     port,
		SSHUser:     job.User,
		SSHPassword: job.PasswordSnap,
		SSHKey:      job.KeySnap,
	}
}

// uploadScript opens a fresh SSH session, writes install.sh to
// /tmp/ongrid-install.sh via a heredoc, and closes the session.
//
// We deliberately do NOT keep this session open to also run the script:
// the heredoc payload is bounded by the remote shell's line-length limit,
// and a 20 KB script is too long for a single shell line on some targets
// (notably Alpine's default ash). Splitting into two sessions makes the
// write robust and lets the installer pipe the run-time output cleanly.
func (i *SSHInstaller) uploadScript(ctx context.Context, dev *devicemodel.Device, onLog func(chunk string)) error {
	client, err := i.dial(ctx, dev)
	if err != nil {
		return err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("installjob: new session: %w", err)
	}
	defer sess.Close()

	// Heredoc with a sentinel that won't appear in install.sh's body.
	// 'ONGRID_EOF_UPLOAD' is unique enough that even if the script
	// text contained a stray EOF marker it wouldn't accidentally
	// terminate the heredoc.
	const sentinel = "ONGRID_EOF_UPLOAD"
	writeCmd := fmt.Sprintf(
		"cat > %s <<'%s'\n%s\n%s\nchmod +x %s\n",
		remoteScriptPath,
		sentinel,
		string(i.installScript),
		sentinel,
		remoteScriptPath,
	)
	if err := sess.Run(writeCmd); err != nil {
		return fmt.Errorf("installjob: write heredoc: %w", err)
	}
	onLog(fmt.Sprintf("[INFO] install.sh uploaded to %s (%d bytes)\n", remoteScriptPath, len(i.installScript)))
	return nil
}

// runScript opens a second SSH session, executes install.sh with the
// four canonical args, and streams stdout + stderr through onLog. The
// session is closed by Run() returning (or erroring).
func (i *SSHInstaller) runScript(ctx context.Context, dev *devicemodel.Device, accessKey, secretKey string, onLog func(chunk string)) error {
	client, err := i.dial(ctx, dev)
	if err != nil {
		return err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("installjob: new session: %w", err)
	}
	defer sess.Close()

	stdout, err := sess.StdoutPipe()
	if err != nil {
		return fmt.Errorf("installjob: stdout pipe: %w", err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		return fmt.Errorf("installjob: stderr pipe: %w", err)
	}

	// Build the run command. The four args are mandatory in install.sh;
	// an empty value (e.g. server-edge-addr blank in a misconfigured
	// wiring) makes the script's arg parser exit 2 — surface as a real
	// install failure rather than silently passing empty strings.
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("installjob: empty access-key or secret-key (worker forgot to mint creds first)")
	}
	if i.serverEdgeAddr == "" || i.serverHTTPAddr == "" {
		return fmt.Errorf("installjob: empty server-edge-addr or server-http-addr (WorkerConfig not populated)")
	}
	runCmd := fmt.Sprintf(
		"bash %s --access-key=%s --secret-key=%s --server-edge-addr=%s --server-http-addr=%s",
		remoteScriptPath, accessKey, secretKey, i.serverEdgeAddr, i.serverHTTPAddr,
	)

	// Stream stdout/stderr concurrently; both pipes drain into onLog.
	var wg sync.WaitGroup
	wg.Add(2)
	go i.pumpStream(&wg, stdout, onLog, "[stdout]")
	go i.pumpStream(&wg, stderr, onLog, "[stderr]")

	runErr := sess.Run(runCmd)
	wg.Wait()
	return runErr
}

// pumpStream copies one pipe into onLog until EOF. Prefix is prepended
// to every chunk so the worker's UI can colour-code stdout vs. stderr
// without re-parsing the body. The wait group is decremented after
// the stream is fully drained so the caller's WG.Wait captures any
// late log chunk before it returns.
func (i *SSHInstaller) pumpStream(wg *sync.WaitGroup, r io.Reader, onLog func(string), prefix string) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			onLog(prefix + string(buf[:n]))
		}
		if err != nil {
			return
		}
	}
}

// dial opens an *ssh.Client via the router. The InstallEdge purpose
// forces the direct dialer; the tunnel path is not yet implemented
// for this purpose and would surface as ErrTunnelNotImplemented.
func (i *SSHInstaller) dial(ctx context.Context, dev *devicemodel.Device) (*ssh.Client, error) {
	// Wrap the dial in a timeout so a stuck remote can't pin a worker
	// slot forever; the worker's execCtx is the overall ceiling, this
	// is just the per-attempt ceiling.
	dialCtx, cancel := context.WithTimeout(ctx, i.connectTimeout)
	defer cancel()

	client, err := i.router.MustConnect(dialCtx, dev, devicessh.PurposeInstallEdge)
	if err != nil {
		return nil, fmt.Errorf("installjob: connect %s:%d: %w", dev.SSHHost, dev.SSHPort, err)
	}
	return client, nil
}