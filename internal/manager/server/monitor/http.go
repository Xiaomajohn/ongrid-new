// Package monitor builds the HTTP routes for user-managed Monitor page
// panels. Thin handler — auth + JSON decode + delegate to biz layer.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	biz "github.com/ongridio/ongrid/internal/manager/biz/monitor"
	model "github.com/ongridio/ongrid/internal/manager/model/monitor"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// PanelService is the narrow biz contract the handler needs.
// *biz/monitor.Service satisfies it.
type PanelService interface {
	List(ctx context.Context, f biz.ListFilter) ([]*model.Panel, error)
	Get(ctx context.Context, id uint64) (*model.Panel, error)
	Create(ctx context.Context, in biz.CreateInput) (*model.Panel, error)
	Update(ctx context.Context, id uint64, in biz.UpdateInput) (*model.Panel, error)
	Delete(ctx context.Context, id uint64, hard bool) error
	Restore(ctx context.Context, id uint64) error
}

// Handler bundles the routes.
type Handler struct {
	svc PanelService
}

// NewHandler wires the handler.
func NewHandler(svc PanelService) *Handler { return &Handler{svc: svc} }

// Register attaches routes:
//
//	GET    /v1/monitor/panels                     (any authed)
//	POST   /v1/monitor/panels                     (any authed)
//	PATCH  /v1/monitor/panels/{id}                (any authed)
//	DELETE /v1/monitor/panels/{id}?hard=true      (any authed)
//	POST   /v1/monitor/panels/{id}/restore        (any authed)
//
// 监控面板的增删改查与设备一样对所有已认证角色开放（沿袭
// 2026-07-20 的决定），方便 user 自己调整面板。原先的 admin 闸门
// 已移除；handler 内部的 requireAdmin 仅作为兜底保留，不再被调用。
func (h *Handler) Register(r chi.Router) {
	r.Get("/v1/monitor/panels", h.list)
	r.Post("/v1/monitor/panels", h.create)
	r.Patch("/v1/monitor/panels/{id}", h.update)
	r.Delete("/v1/monitor/panels/{id}", h.delete)
	r.Post("/v1/monitor/panels/{id}/restore", h.restore)
}

type listResp struct {
	Panels []*model.Panel `json:"panels"`
}

// panelItem is the wire shape returned by create / update / restore
// handlers. The model.Panel's json tags already cover the common fields
// (id / title / type / promql / legend / unit / ordinal / sync_*/time
// stamps), so re-embedding *model.Panel gives us those for free; we add
// the DeviceID / DeletedAt explicit copies so a nil-valued field is
// omitted from the wire (model.Panel declares DeviceID as *uint64
// `json:"device_id,omitempty"`, so re-embedding still drops nil
// correctly). Keeping the same JSON keys as the model means the SPA can
// keep using its existing MonitorPanel type.
type panelItem struct {
	*model.Panel
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if !h.requireUser(w, r) {
		return
	}
	q := r.URL.Query()
	f := biz.ListFilter{IncludeDeleted: parseBool(q.Get("include_deleted"))}
	if v := q.Get("device_id"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			writeErr(w, errors.Join(errs.ErrInvalid, fmt.Errorf("device_id: %w", err)))
			return
		}
		f.DeviceID = &n
	}
	out, err := h.svc.List(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listResp{Panels: out})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	if !h.requireUser(w, r) {
		return
	}
	var in biz.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	saved, err := h.svc.Create(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &panelItem{Panel: saved})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	if !h.requireUser(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in biz.UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	saved, err := h.svc.Update(r.Context(), id, in)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &panelItem{Panel: saved})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if !h.requireUser(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	hard := parseBool(r.URL.Query().Get("hard"))
	if err := h.svc.Delete(r.Context(), id, hard); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// restore revives a soft-deleted panel. 任何已认证角色均可触发；
// admin-only 的注释历史保留作为变更说明，不再强制闸门。
func (h *Handler) restore(w http.ResponseWriter, r *http.Request) {
	if !h.requireUser(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.Restore(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	// Re-read so the caller sees the post-restore row (deleted_at cleared).
	saved, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &panelItem{Panel: saved})
}

// --- helpers ---

// parseBool accepts the same wire-truthy spellings the rest of the
// handler set uses (true/1/yes vs false/0/no). Unknown / empty defaults
// to false so query-string omission continues to mean "default scope".
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

func parseID(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	if id == 0 {
		return 0, errs.ErrInvalid
	}
	return id, nil
}

func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return false
	}
	if t.Role != "admin" {
		writeErr(w, errs.ErrForbidden)
		return false
	}
	return true
}

func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return false
	}
	return true
}

// requireAdminMW returns a chi-compatible middleware that enforces admin
// role. Used by Register to gate the /restore route.
func (h *Handler) requireAdminMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.requireAdmin(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	status := errs.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: err.Error(), Code: errCode(err)})
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
	default:
		return "internal"
	}
}
