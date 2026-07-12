package installjob

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// EdgeIssuer is the worker-side handle to the edge credential layer.
//
// 设计语义（从 2026-07-06 重构）：前端创建 edge + 拼 cmd（cmd 里嵌
// 入 edge.access_key/secret_key），后端 worker 拿到 cmd 后只需把
// 前端创建的 edge 关联到 job.DeviceID。不再 worker 调 CreateEdge
// —— 那会产生第 2 个 edge（凭证浪费 + 关联错乱）。
//
// BindEdgeFromAccessKey 从 cmd 拿到的 access_key 反查 edge.ID，写
// SetDeviceID + edge_devices Link + install_jobs.edge_id，让 agent
// register 时 HandleRegister 能查到正确的 device 关联，waitEdgeOnline
// 也能查到。
type EdgeIssuer interface {
	BindEdgeFromAccessKey(ctx context.Context, deviceID uint64, accessKey, taskName string) error
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
//   - Runs the command string passed from the frontend (via Worker).
//     The frontend has already built the canonical curl | bash command
//     using buildInstallCommand(), so the installer does NOT build
//     the command itself — it just executes it verbatim.
//   - Streams stdout/stderr through onLog as they arrive.
//   - Returns nil on a clean exit; non-nil on any SSH / script / exit
//     failure. The worker uses a non-nil return as the trigger to
//     flip Status=Failed (exitCode=-1) and clear the credential snap.
//
// cmd 是前端拼好的完整 curl 命令。零兜底:cmd 为空时 installer
// 直接返回错误,不尝试自己拼装。
//
// accessKey / secretKey are the freshly-minted edge credentials from
// the EdgeIssuer step — they get baked into the install.sh command
// line so the agent knows how to authenticate with the manager on
// its first register handshake.
//
// taskName 是用户填的“任务名”；installer 负责把它以 --task-name=xxx
// 参数形式传给设备上的 install.sh。非空时才传（避免对老 install.sh
// 引入未识别参数）。
//
// The callback is invoked synchronously from inside the installer;
// do NOT block on heavy work in the callback — keep it to "write to
// DB and return".
type Installer interface {
	Install(ctx context.Context, job *InstallJob, accessKey, secretKey, taskName, cmd string, onLog func(chunk string)) error
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
//
// Timeout 是 install job 整体的硬性上限（防 worker 槽被单个 job 永久 pin
// 死）。改成 24h 后相当于“只要 install.sh 没退 worker 就不退”，符合
// “如果安装不完就一直不退出”的语义；如果未来想重新启用严格超时，改回
// 10*time.Minute 即可，env 里也能 override。
const (
	defaultWorkerTimeout = 24 * time.Hour
	// waitEdgeOnline 改为 best-effort：只短暂探一下 agent 有没有 register，
	// 失败不阻塞 worker 终态判定。install.sh 跑完 self-check passed 后即
	// 视为 install 完成；agent 是否 register 由 edge.presence 后续异步追踪，
	// UI 侧通过 edges.status 自行观察，job 状态不会再出现 "timeout"。
	defaultWaitOnlineWindow = 5 * time.Second
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
//	queued ──▶ running ──▶ (success | failed)
//
// (timeout 分支已废弃：waitEdgeOnline 改为 best-effort，install.sh
// 跑完 self-check passed 后即视为 install 完成，agent 是否 register
// 是 secondary 状态，由 edge.presence 后续异步追踪。详见常量注释。)
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

	// 从 install_jobs.options_json 解析 task_name（HTTP 层写入；上游
	// installjobUsecaseAdapter.Create 把 task_name 嵌进 JSON object）。
	// 解析失败 / 缺字段 → 空串（视为“未提供 task_name”），下游 installer
	// 与 issuer 拿到空串时都做 no-op 处理。
	taskName := parseTaskNameFromOptions(job.OptionsJSON)
	cmd := parseCommandFromOptions(job.OptionsJSON)
	if taskName != "" {
		logCtx = logCtx.With(slog.String("task_name", taskName))
	}
	if cmd != "" {
		logCtx = logCtx.With(slog.String("cmd", cmd))
	}

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
	if cmd == "" {
		// 真正的 install 命令没了，不能继续 — 否则会让 worker 走到
		// ssh run 一个空字符串（sess.Run("") 立即退出且无诊断信息），
		// SPA 看到 failed 但 log_output 是空的，无法定位。
		logCtx.Warn("installjob: command is empty (前端必须通过 POST /install-edge 的 command 字段塞入完整 curl)")
		_ = w.repo.AddEvent(execCtx, jobID, EventKindError, "empty install command")
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}

	// 解析 cmd 里的 --access-key=... —— 前端 buildInstallCommand()
	// 拼出的 cmd 必然带 --access-key=<frontend-created-edge.access_key_id>。
	// worker 拿到 access_key 后反查 edge.ID，把 install_jobs 关联到前
	// 端创建的 edge；waitEdgeOnline(device_id) 之后才能查到。
	//
	// 不再调 issuer.CreateEdgeForDevice —— 那会创建第 2 个 edge（前端
	// 创一个、worker 创一个），浪费凭证，且 agent 实际 register 的是
	// 前端 cmd 嵌入的那个 edge（worker 创的 edge 永远收不到 register，
	// 直接被 worker 软删除），关联错乱、waitEdgeOnline 永远等不到。
	accessKey, err := parseAccessKeyFromCmd(cmd)
	if err != nil {
		logCtx.Warn("installjob: parse --access-key from cmd failed", slog.Any("err", err))
		_ = w.repo.AddEvent(execCtx, jobID, EventKindError, "parse access-key: "+err.Error())
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}
	if w.issuer == nil {
		logCtx.Warn("installjob: no EdgeIssuer wired; cannot bind edge to device")
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}
	// BindEdgeFromAccessKey 拿到前端创建的 edge.ID，按 job.DeviceID
	// 关联起来（SetDeviceID + edge_devices Link + install_jobs.edge_id）。
	// 失败时只记 warn 不中止：agent register 时 HandleRegister 还会
	// 兜底一次（按 edge.DeviceID 优先 Get device），所以关联丢失不会
	// 让 install 失败，只是 waitEdgeOnline 可能等到其他 device 下面。
	if bindErr := w.issuer.BindEdgeFromAccessKey(execCtx, job.DeviceID, accessKey, taskName); bindErr != nil {
		logCtx.Warn("installjob: bind edge to device failed", slog.Any("err", bindErr))
		_ = w.repo.AddEvent(execCtx, jobID, EventKindError, "bind edge: "+bindErr.Error())
	}

	// 3) direct SSH + write install.sh + run it + stream logs.
	if w.installer == nil {
		logCtx.Warn("installjob: no Installer wired; marking failed")
		_ = w.repo.UpdateStatus(execCtx, jobID, StatusFailed, ptr(-1))
		_ = w.repo.ClearCredentialSnap(execCtx, jobID)
		return
	}
	installErr := w.installer.Install(execCtx, job, "", "", taskName, cmd, func(chunk string) {
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

	// 4) 注释掉自动软删除逻辑：安装探针不应该自动删除任何 edge，
	// 无论是旧的还是新的。让用户自行决定是否清理旧的探针。
	// if w.softDelete != nil {
	// 	if err := w.softDelete.SoftDeleteOldProbes(execCtx, job.DeviceID); err != nil {
	// 		logCtx.Warn("installjob: soft delete old probes failed", slog.Any("err", err))
	// 		// non-fatal — proceed.
	// 	}
	// }

	// 5) best-effort wait until the freshly-installed edge comes online.
	//
	// 改了：waitEdgeOnline 不再阻塞 install job 终态判定。
	// install.sh 跑完 self-check passed 后即视为 install 完成；
	// agent 是否 register 是 secondary 状态，由 edge.presence 后续异步追踪，
	// UI 侧通过 edges.status 自行观察。
	//
	// 失败时仅 warn log，不 UpdateStatus StatusTimeout，不 return，
	// 让 worker 走到 success 路径 —— 这样 job 状态不会再卡在
	// "timeout"，符合用户“如果安装不完就一直不退出”的语义。
	// 即便 install.sh 真的卡住，defaultWorkerTimeout（24h）兜底，
	// 不会让 worker 槽永久 pin 死。
	if err := w.waitEdgeOnline(execCtx, job.DeviceID, w.cfg.WaitOnline); err != nil {
		logCtx.Warn("installjob: edge did not come online within wait window (best-effort, not failing job)",
			slog.Any("err", err),
			slog.Duration("wait_window", w.cfg.WaitOnline))
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

// parseTaskNameFromOptions 从 install_jobs.options_json 提取 task_name
// 字段。options_json 当前 schema（最小集）：
//
//	{"task_name": "用户填的任务名"}
//
// 历史行（{} / 缺字段 / JSON 损坏）一律返回空串 —— 调用方会把空串当
// no-op 处理（不再写 edge.task_name、也不再传 --task-name 给
// install.sh），保证不影响老 install_jobs 行的回放 / 升级。
//
// 该函数只读自己 schema 内的字段（不反射全 JSON），对后续追加其它
// options 字段是 forward-compatible 的。
func parseTaskNameFromOptions(optionsJSON string) string {
	if strings.TrimSpace(optionsJSON) == "" {
		return ""
	}
	var opts struct {
		TaskName string `json:"task_name"`
	}
	if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
		return ""
	}
	return strings.TrimSpace(opts.TaskName)
}

// parseCommandFromOptions 从 install_jobs.options_json 提取 command 字段。
// command 是前端 buildInstallCommand() 拼好的完整 curl 命令,installer 直接执行,
// 不再自己拼装。command 为空时 installer 直接报错(零兜底)。
func parseCommandFromOptions(optionsJSON string) string {
	if strings.TrimSpace(optionsJSON) == "" {
		return ""
	}
	var opts struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(optionsJSON), &opts); err != nil {
		return ""
	}
	return strings.TrimSpace(opts.Command)
}

// parseAccessKeyFromCmd 从前端拼装的完整 curl 命令中提取
// --access-key=<ak> 的 ak 值。worker 拿到后反查 edge.ID。
//
// 包含边界：
//   - 用字符串扫描而不是 regex，零额外依赖
//   - 只匹配 `--access-key=VALUE`（等号紧贴），不接受空格分隔（curl/bash
//     的 CLI 习惯是 `--access-key=value`）；frontend buildInstallCommand
//     正是这种格式
//   - VALUE 取到下一个非转义空白；中间转义复杂场景（vaule 含空格）暂未
//     出现，预留为后续增强
//   - 重复出现多个 `--access-key=` 时取第一个（防御性，正常仅一个）
//
// 错误返回：cmd 里根本找不到 `--access-key=` 前缀，或 VALUE 为空。
func parseAccessKeyFromCmd(cmd string) (string, error) {
	const marker = "--access-key="
	i := strings.Index(cmd, marker)
	if i < 0 {
		return "", fmt.Errorf("installjob: cmd missing --access-key= (前端 buildInstallCommand 一定是带上的)")
	}
	rest := cmd[i+len(marker):]
	if rest == "" {
		return "", fmt.Errorf("installjob: cmd has --access-key= but empty value")
	}
	// 截到下一个未转义的空白为止。
	var end int
	for end = 0; end < len(rest); end++ {
		if rest[end] == ' ' || rest[end] == '\t' || rest[end] == '\n' {
			break
		}
	}
	ak := rest[:end]
	if ak == "" {
		return "", fmt.Errorf("installjob: cmd has --access-key= but empty value")
	}
	return ak, nil
}

// ptr is a tiny generic helper so the worker can stash literal
// exit codes into *int without declaring per-call temporaries.
func ptr[T any](v T) *T { return &v }
