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
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// --- domain model (used by main.go's stub today, by A4's real
// biz layer tomorrow) ---

// Status values for an install job. Status drives the SPA's pill
// colour (pending / running / success / failed / cancelled) and the
// worker's progress events.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job is the row shape the JSON serializes. NEVER include any
// SSH credential field on this struct — the install worker carries
// those in transient memory and the audit row, not in the install
// job row. The wire contract scrub list is enforced here.
type Job struct {
	ID         uint64     `json:"id"`
	DeviceID   uint64     `json:"device_id"`
	Kind       string     `json:"kind"`     // edge_install | edge_upgrade | patch
	Status     string     `json:"status"`
	Progress   int        `json:"progress"` // 0..100; UI fills bars from this
	Message    string     `json:"message,omitempty"`
	LastError  string     `json:"last_error,omitempty"`
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
	Cancel(ctx context.Context, id uint64) error
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

// Register attaches the 3 routes. id-first listing is the typical
// "show progress on the device page" entry; by-device second so
// the device-tab polls live without joining on the install-jobs
// table itself.
//
//	GET  /v1/install-jobs/{id}
//	GET  /v1/devices/{id}/install-jobs?limit=20
//	POST /v1/install-jobs/{id}:cancel
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/install-jobs/{id}", h.get)
	r.Get("/v1/devices/{id}/install-jobs", h.listByDevice)
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
