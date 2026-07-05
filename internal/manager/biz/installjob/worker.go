package installjob

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// EdgeIssuer is the worker-side handle to the edge credential layer.
//
// The worker mints a fresh access_key / secret_key BEFORE running
// the install script — install.sh is invoked with these credentials
// baked in, so the freshly-installed agent can register with the
// manager on first boot. Re-issuing on every install rotates any
// stale credentials left over from a previous (failed) attempt.
type EdgeIssuer interface {
	CreateEdgeForDevice(ctx context.Context, deviceID uint64) (accessKey, secretKey string, err error)
}

// DeviceSSHSoftDelete is the worker-side handle to the SSH probe
// soft-delete path. Re-install jobs want to drop any SSH probe rows
// left over from a previous attempt so the new edge's auth isn't
// stale-validated against them. A5 wires the concrete; the worker
// just calls it best-effort (a failed soft-delete does not fail the
// install).
type DeviceSSHSoftDelete interface {
	SoftDeleteOldProbes(ctx context.Context, deviceID uint64) error
}

// EdgePresence is the worker-side handle to the "edge came online"
// poll loop. Implemented by *edge.DBEdgePresence.
//
// NB: the worker keeps this as a narrow interface so installjob does
// not need to import internal/manager/biz/edge directly. main.go
// wires *edge.DBEdgePresence into this seam.
type EdgePresence interface {
	WaitOnline(ctx context.Context, deviceID uint64, timeout time.Duration) error
}

// Installer is the SSH-and-script-execute layer. Decoupling it lets
// the worker stay free of any direct dependency on the SSH package —
// A5 can stitch in a real SSH client (with retry / SOCKS proxy /
// timing caps) at wiring time. The contract:
//
//   - Install opens an SSH session using Job.Host/Port/User and the
//     password/key snapshot on the job row.
//   - Uploads install.sh to /tmp and runs it with the four canonical
//     arguments (access-key / secret-key / server-edge-addr /
//     server-http-addr).
//   - Streams stdout/stderr through onLog as they arrive.
//   - Returns nil on a clean exit; non-nil on any SSH / script / exit
//     failure. The worker uses a non-nil return as the trigger to
//     flip Status=Failed (exitCode=-1) and clear the credential snap.
//
// accessKey / secretKey are the freshly-minted edge credentials from
// the EdgeIssuer step — they get baked into the install.sh command
// line so the agent knows how to authenticate with the manager on
// its first register handshake.
//
// The callback is invoked synchronously from inside the installer;
// do NOT block on heavy work in the callback — keep it to "write to
// DB and return".
type Installer interface {
	Install(ctx context.Context, job *InstallJob, accessKey, secretKey string, onLog func(chunk string)) error
}

// WorkerConfig tunes the worker's external IO contract.
//
// ServerEdgeAddr / ServerHTTPAddr are public addresses passed down to
// the install.sh template (e.g. so the freshly-installed edge knows
// where to register). They are not used inside Worker today — they
// are stashed here so A5 can plumb them straight into the SSH
// command without changing the Worker signature.
type WorkerConfig struct {
	ServerEdgeAddr string        // manager's edge 接入地址 (tunnel)
	ServerHTTPAddr string        // manager 的 http 接入地址 (register)
	Timeout        time.Duration // total install cap, default 10min
	WaitOnline     time.Duration // edge-online poll cap, default 20s
}

// Defaults applied when the matching field is zero.
const (
	defaultWorkerTimeout    = 10 * time.Minute
	defaultWaitOnlineWindow = 20 * time.Second
)

// Worker drives one InstallJob through to a terminal state. The
// Runner fans jobs out; Worker is where the actual install logic
// lives. Worker holds no state per-job — every call to Execute is
// self-contained and safe to run concurrently.
type Worker struct {
	repo         Repo
	issuer       EdgeIssuer
	softDelete   DeviceSSHSoftDelete
	installer    Installer
	edgePresence EdgePresence
	cfg          WorkerConfig
	log          *slog.Logger
}

// NewWorker wires the worker. issuer / softDelete / installer /
// edgePresence may be nil; Execute() treats any of those as a no-op
// chain step (logged at WARN). That lets tests and partial wirings
// skip individual legs without crashing.
//
// log may be nil — defaults to slog.Default().
func NewWorker(repo Repo, issuer EdgeIssuer, softDelete DeviceSSHSoftDelete, installer Installer, edgePresence EdgePresence, cfg WorkerConfig, log *slog.Logger) *Worker {
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultWorkerTimeout
	}
	if cfg.WaitOnline == 0 {
		cfg.WaitOnline = defaultWaitOnlineWindow
	}
	if log == nil {
		log = slog.Default()
	}
	return &Worker{
		repo:         repo,
		issuer:       issuer,
		softDelete:   softDelete,
		installer:    installer,
		edgePresence: edgePresence,
		cfg:          cfg,
		log:          log.With(slog.String("comp", "installjob-worker")),
	}
}

// Execute runs one job end-to-end. It is a single goroutine entry
// point driven by the Runner — there is no internal concurrency, so
// the Runner is free to spawn as many Execute calls as its pool size
// allows.
//
// State machine:
//
//	queued ──▶ running ──▶ (success | failed | timeout)
//
// The runner enqueues a jobID; the worker fetches the row, flips
// status, mints fresh edge credentials (BEFORE the install script
// runs so install.sh can bake them in), runs the installer,
// soft-deletes old SSH probes, polls until the new edge is online,
// then settles. Every terminal transition is paired with a
// credential-snap clear so plaintext keys/passwords don't linger on
// the row.
//
// All errors are logged, never returned — Execute runs on a worker
// goroutine that has no one to return to.
func (w *Worker) Execute(ctx context.Context, jobID uint64) {
	logCtx := w.log.With(slog.Uint64("job_id", jobID))

	job, err := w.repo.Get(ctx, jobID)
	if err != nil {
		logCtx.Error("installjob: get failed", slog.Any("err", err))
		return
	}
	logCtx = logCtx.With(slog.Uint64("device_id", job.DeviceID))

	// Apply a total-budget deadline on top of the caller-supplied
	// ctx so a single misbehaving install cannot pin a worker slot
	// forever. Cleanup (Cancel), credential-wipe, and final
	// UpdateStatus all run inside this ctx.
	execCtx, cancel := context.WithTimeout(ctx, w.cfg.Timeout)
	defer cancel()

	// 1) queued → running.
	if err := w.repo.UpdateStatus(execCtx, jobID, StatusRunning, nil); err != nil {
		logCtx.Error("installjob: update status→running failed", slog.Any("err", err))
		return
	}
	_ = w.repo.AddEvent(execCtx, jobID, EventKindState, string(StatusRunning))

	// 2) Mint fresh edge credentials BEFORE the installer runs.
	// install.sh is invoked with --access-key=... --secret-key=...
	// baked into the command line; the freshly-installed agent uses
	// them on its first register handshake. Doing this step first
	// also rotates any stale creds left over from a previous
	// attempt — belt-and-braces with the soft-delete step below.
	if w.issuer == nil {
		logCtx.Warn("installjob: no EdgeIssuer wired; cannot mint credentials")
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}
	accessKey, secretKey, err := w.issuer.CreateEdgeForDevice(execCtx, job.DeviceID)
	if err != nil {
		logCtx.Warn("installjob: CreateEdgeForDevice failed", slog.Any("err", err))
		_ = w.repo.AddEvent(execCtx, jobID, EventKindError, "issue edge cred: "+err.Error())
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}

	// 3) direct SSH + write install.sh + run it + stream logs.
	if w.installer == nil {
		logCtx.Warn("installjob: no Installer wired; marking failed")
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}
	installErr := w.installer.Install(execCtx, job, accessKey, secretKey, func(chunk string) {
		// Redact the secret key before persisting — the install
		// script echoes it back at registration time and we don't
		// want it living in log_output forever. redactSecretKey is
		// a no-op when Job.KeySnap is empty.
		safe := redactSecretKey(chunk, job.KeySnap)
		if err := w.repo.AppendLog(execCtx, jobID, safe); err != nil {
			// Best-effort; don't block the installer on log writes.
			logCtx.Warn("installjob: append log failed", slog.Any("err", err))
		}
		// Mirror the redacted chunk into the event timeline so the
		// UI's rolling-log view doesn't have to re-parse log_output.
		_ = w.repo.AddEvent(execCtx, jobID, EventKindLog, safe)
	})

	if installErr != nil {
		logCtx.Warn("installjob: installer returned error", slog.Any("err", installErr))
		_ = w.repo.AddEvent(execCtx, jobID, EventKindError, installErr.Error())
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}

	// 4) DB-level soft-delete of old SSH probes (belt-and-braces
	// alongside whatever the installer did at the file level).
	if w.softDelete != nil {
		if err := w.softDelete.SoftDeleteOldProbes(execCtx, job.DeviceID); err != nil {
			logCtx.Warn("installjob: soft delete old probes failed", slog.Any("err", err))
			// non-fatal — proceed.
		}
	}

	// 5) wait until the freshly-installed edge comes online so the
	// UI sees a confirmed-success state instead of a race window
	// where install completed but the agent hasn't phoned home yet.
	if err := w.waitEdgeOnline(execCtx, job.DeviceID, w.cfg.WaitOnline); err != nil {
		logCtx.Warn("installjob: edge did not come online in time", slog.Any("err", err))
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusTimeout, nil)
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}

	// 6) success.
	_ = w.repo.UpdateStatus(execCtx, jobID, StatusSuccess, ptr(0))
	_ = w.repo.AddEvent(execCtx, jobID, EventKindState, string(StatusSuccess))
	_ = w.repo.ClearCredentialSnap(execCtx, jobID)
	logCtx.Info("installjob: job settled success")
}

// waitEdgeOnline delegates to the wired EdgePresence. Returns nil
// when an online edge is observed; non-nil (typically
// context.DeadlineExceeded) when the wait window elapses.
func (w *Worker) waitEdgeOnline(ctx context.Context, deviceID uint64, timeout time.Duration) error {
	if w.edgePresence == nil {
		// No presence wiring in tests / partial builds — treat as
		// instant success so the rest of the chain (success state,
		// credential clear) still runs. Logged at WARN so a wiring
		// miss is visible in the installjob log.
		w.log.Warn("installjob: no EdgePresence wired; skipping online wait")
		return nil
	}
	return w.edgePresence.WaitOnline(ctx, deviceID, timeout)
}

// redactSecretKey scrubs occurrences of secretKey from chunk,
// replacing each with "****". A blank secretKey is a no-op (lets the
// caller pass through password-only jobs without conditional logic).
func redactSecretKey(chunk, secretKey string) string {
	if secretKey == "" {
		return chunk
	}
	return strings.ReplaceAll(chunk, secretKey, "****")
}

// ptr is a tiny generic helper so the worker can stash literal
// exit codes into *int without declaring per-call temporaries.
func ptr[T any](v T) *T { return &v }
