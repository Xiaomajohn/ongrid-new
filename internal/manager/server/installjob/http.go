// Package installjob exposes the install-job HTTP surface — Get /
// Cancel / List-by-device. The biz layer (delivered by A4) owns the
// worker + runner + driver logic; this package stays at the HTTP
// shape so the SPA can integrate while A4 settles.
//
// The Usecase interface is defined here (not in biz/installjob) so
// the wiring layer can plug a stub while A4 hasn't landed; once
// A4 ships, main.go wires the real *installjob.Usecase and the stub
// disappears.
package installjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// --- domain model (used by main.go's stub today, by A4's real
// biz layer tomorrow) ---

// Status values for an install job. Mirrors the biz layer's
// installjob.Status (queued / running / success / failed / cancelled /
// timeout) so HTTP handlers can reference the same wire vocabulary
// without importing the biz type. Status drives the SPA's pill colour
// and the worker's progress events.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusTimeout   = "timeout"
)

// Job is the row shape the JSON serializes. NEVER include any
// SSH credential field on this struct — the install worker carries
// those in transient memory and the audit row, not in the install
// job row. The wire contract scrub list is enforced here.
//
// LogOutput is exposed so the InstallLogPanel (web/src/components/...)
// can render the captured install.sh stream without a separate
// download. It is purely the install output (curl-pipe stdout/stderr
// already redacted by the worker) — it must NEVER include SSH
// credentials, since the worker scrubs the secret_key before persisting
// (see biz/installjob.Worker.Execute -> redactSecretKey).
type Job struct {
	ID         uint64     `json:"id"`
	DeviceID   uint64     `json:"device_id"`
	// EdgeID 可空：某些历史安装任务创建时还没有 edge 记录（一键安装
	// 路径里 edge 由 worker 在 waitEdgeOnline 之后才回写），也有手动
	// 安装走 SSHInstaller 但未创建 edge 的场景。`omitempty` 让 null 不
	// 出现在 JSON 输出里，前端 InstallJob 类型同步为可选。
	EdgeID     *uint64    `json:"edge_id,omitempty"`
	Kind       string     `json:"kind"`     // edge_install | edge_upgrade | patch
	Status     string     `json:"status"`
	Progress   int        `json:"progress"` // 0..100; UI fills bars from this
	Message    string     `json:"message,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
	LogOutput  string     `json:"log_output,omitempty"` // 已脱密的 install.sh 输出尾巴
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Usecase is the server-side contract main.go / A4 satisfies. The
// HTTP handler imports this interface, not the concrete biz type,
// so a swap doesn't ripple into the handler.
type Usecase interface {
	Get(ctx context.Context, id uint64) (*Job, error)
	ListByDevice(ctx context.Context, deviceID uint64, limit int) ([]*Job, error)
	// ListByEdge 按监控设备 edge 维度列安装任务。一台 device 可装多个
	// edge（不同 task_name），监控设备页面要按 edge 而不是 device 取
	// 最近一次的安装日志，所以新增该方法。SPA 端走 GET /v1/edges/{id}
	// /install-jobs。
	ListByEdge(ctx context.Context, edgeID uint64, limit int) ([]*Job, error)
	Cancel(ctx context.Context, id uint64) error
	// Create enqueues a one-shot install-edge job for the given device.
	//
	// taskName 可选。任务名已上改由 CreateEdgeModal 填到 edge.task_name，
	// 本接口不再二次询问。 trim 后为空白视为“未提供”，下游 worker 走
	// no-op 路径：install_sh 只不带 --task-name，BindEdgeFromAccessKey
	// 也不动 edge.task_name（保持 agent register 时可能上报的值）。
	// 非空时依然会写入 install_jobs.options_json（按任务名筛查 / 审计），
	// 并一路透传到：
	//   - biz/edge.InstallEdgeIssuer.CreateEdgeForDevice → edge.task_name
	//     列（Agent1 同步增加；本 PR 用 repo.UpdateTaskName 在创建后追
	//     写一次）
	//   - SSHInstaller.runCurlPipe → install.sh 的 --task-name 参数
	//
	// SSH credentials are pulled from the device row at call time — the
	// request body is ignored for credentials (see plan §凭据策略变更).
	// The worker also captures a snapshot on the row so a mid-flight
	// credential rotation does not break an in-progress install. Returned
	// Job's Status is always "queued" on success; the worker flips it
	// to running once it picks the job up off the Runner queue.
	Create(ctx context.Context, deviceID uint64, taskName, command string) (*Job, error)
}

// CreateResponse is the wire shape POST /v1/devices/{id}/install-edge
// returns. Mirrors the SPA's InstallEdgeResponse in
// web/src/api/devices.ts — only the two fields the install-launchpad
// modal reads (job id + initial status) cross the wire.
type CreateResponse struct {
	InstallJobID uint64 `json:"install_job_id"`
	Status       string `json:"status"`
}

// --- handler ---

// Handler is the HTTP entry point for the install-job resource.
type Handler struct {
	uc  Usecase
	log *slog.Logger
}

// NewHandler builds the handler. uc may be a stub (returns
// ErrNotWiredYet) while A4 hasn't landed, or the real biz usecase.
func NewHandler(uc Usecase, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{uc: uc, log: log}
}

// Register attaches the install-job routes. The device-scoped POST
// kicks off the one-button install workflow; the by-device GET is the
// "show progress on the device page" entry; the standalone GET / POST
// :cancel cover the post-submit polling + abort paths.
//
//	POST /v1/devices/{id}/install-edge
//	GET  /v1/install-jobs/{id}
//	GET  /v1/devices/{id}/install-jobs?limit=20
//	GET  /v1/edges/{id}/install-jobs?limit=20
//	POST /v1/install-jobs/{id}:cancel
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/install-jobs/{id}", h.get)
	r.Post("/v1/devices/{id}/install-edge", h.createInstall)
	r.Get("/v1/devices/{id}/install-jobs", h.listByDevice)
	r.Get("/v1/edges/{id}/install-jobs", h.listByEdge)
	r.Post("/v1/install-jobs/{id}:cancel", h.cancel)
}

// --- helpers ---

func deviceIDFrom(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	return id, nil
}

// edgeIDFrom 与 deviceIDFrom 同语义——仅 chi 路由占位符同名 "id"，
// 这里复用同一套解析逻辑以便给 listByEdge 复用。函数名区分调用点，
// 让 reader 一眼看到是给 edge 路由用的。
func edgeIDFrom(r *http.Request) (uint64, error) {
	return deviceIDFrom(r)
}

func writeJSONStatus(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	code := errs.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": err.Error(),
		"code":  errCode(err),
	})
}

func errCode(err error) string {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return "not-found"
	case errors.Is(err, errs.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		return "forbidden"
	case errors.Is(err, errs.ErrInvalid):
		return "invalid"
	case errors.Is(err, errs.ErrNotWiredYet):
		return "not-wired-yet"
	default:
		return "internal"
	}
}

// --- endpoint implementations ---

// get: GET /v1/install-jobs/{id}
//
// Returns the install-job DTO (never any credential column). The
// SPA uses this both in the device-tab detail panel ("status pill
// + last error") and the post-cancel refresh.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	job, err := h.uc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, job)
}

// listByDevice: GET /v1/devices/{id}/install-jobs?limit=20
//
// Default 20; max 100 — the SPA renders them inline on the device
// detail "install history" panel, so anything larger is a sign the
// operator wants the raw listing (we cap so a typo doesn't blow
// memory by asking for 1e9 rows).
func (h *Handler) listByDevice(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	deviceID, err := deviceIDFrom(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := h.uc.ListByDevice(r.Context(), deviceID, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"items": rows, "total": len(rows)})
}

// listByEdge: GET /v1/edges/{id}/install-jobs?limit=20
//
// 监控设备页面（日志入口在 edge 行右侧）按 edge 维度查最近一次安装
// 日志。一台 device 可能装多个 edge（不同 task_name），按 device 维
// 度的 listByDevice 会把多个 edge 的安装混在一起；这里走 ListByEdge
// 只取该 edge 自己的任务，限流 / 默认值与 listByDevice 对齐。
func (h *Handler) listByEdge(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	edgeID, err := edgeIDFrom(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := h.uc.ListByEdge(r.Context(), edgeID, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"items": rows, "total": len(rows)})
}

// installCreateReq 是 POST /v1/devices/{id}/install-edge 的请求体。
// 字段名与 SPA 的 InstallEdgeOptions（web/src/api/devices.ts）对齐：
//   - task_name 可选。任务名由「新建 Edge」流程在 CreateEdgeModal 填入
//     edge.task_name，本路径不再二次询问。 trim 后为空白时视为未提供，
//     下游 worker 与 BindEdgeFromAccessKey 走 no-op 处理。
//   - ssh_pass / ssh_key_pem 是可选的“覆盖 DB 凭据”字段；当前 v1 wiring
//     不读，保留是为了后续 explicit-mode UX（见 plan §凭据策略变更）
type installCreateReq struct {
	TaskName  string `json:"task_name,omitempty"` // 任务名（可选），由「新建 Edge」填入
	Command   string `json:"command,omitempty"`   // 前端拼好的完整 curl 安装命令，后端不再自己拼
	SSHPass   string `json:"ssh_pass,omitempty"`  // 覆盖 DB 中的 SSH 密码（可选）
	SSHKeyPEM string `json:"ssh_key_pem,omitempty"`
}

// createInstall: POST /v1/devices/{id}/install-edge
//
// Kicks off the one-button edge-install workflow for a single device.
// The handler reads SSH credentials from the device row at call time
// (the request body is ignored for credentials per plan §凭据策略变更),
// persists a fresh install_jobs row whose OptionsJSON 携带 task_name
// （后续 worker 会从 OptionsJSON 解析出来透传给 installer 与 edge
// 创建路径），and Enqueues it on the worker Runner. The returned job
// id is what the SPA polls via the existing
// GET /v1/devices/{id}/install-jobs endpoint.
//
// Error mapping (via errs.HTTPStatus):
//   - 401 when the request has no tenant in context.
//   - 400 when path id is not a uint, body JSON is malformed, or the
//     device row is missing the minimum SSH triple
//     (host + user + (password|key)). task_name 不再必填，空串与缺字段
//     都被允许——binstall 调 GetByAccessKey 后 BindEdgeFromAccessKey 收
//     尾处理。
//   - 404 when the device id doesn't resolve to a live row (natural
//     Repo.Get propagation; soft-deleted devices also 404).
//   - 500 on any other internal failure.
//
// godoc
// @Summary Start one-button edge install for a device
// @Router /v1/devices/{id}/install-edge [post]
// @Success 200 {object} installjob.CreateResponse
func (h *Handler) createInstall(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	deviceID, err := deviceIDFrom(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	// 允许 body 缺失 / 为空。 task_name 改可选：任务名由「新建 Edge」流
	// 程填入 edge.task_name，本路径不再必填；trim 后为空白视为“未提供
	// task_name”，下游 worker / BindEdgeFromAccessKey 走 no-op 路径。
	var req installCreateReq
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, errors.Join(errs.ErrInvalid, fmt.Errorf("decode body: %w", err)))
			return
		}
	}
	taskName := strings.TrimSpace(req.TaskName)
	job, err := h.uc.Create(r.Context(), deviceID, taskName, req.Command)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, CreateResponse{
		InstallJobID: job.ID,
		Status:       job.Status,
	})
}

// cancel: POST /v1/install-jobs/{id}:cancel
//
// Google-style ":cancel" sub-action. Idempotent at the API level
// (cancelling an already-cancelled job returns 200 OK with the
// existing row). The biz layer is responsible for the actual
// worker-stop signal — the HTTP path just translates intent.
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if err := h.uc.Cancel(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	// Return the post-cancel row so the SPA can stop polling without
	// a follow-up GET — saves one roundtrip in the typical flow.
	job, err := h.uc.Get(r.Context(), id)
	if err != nil {
		// Cancellation succeeded but the follow-up read didn't (e.g.
		// row was hard-deleted). 200 with empty body is acceptable —
		// SPA will refetch on its next refresh.
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSONStatus(w, http.StatusOK, job)
}
