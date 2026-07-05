package devicessh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// --- service contract ---
//
// A3 (the devicessh biz layer) implements SFTPService; A5 stays at
// the HTTP-shape level so the SPA can be developed against a real
// schema while the biz implementation matures.

type SFTPService interface {
	List(ctx context.Context, deviceID uint64, path string, userID uint64) ([]Entry, error)
	Stat(ctx context.Context, deviceID uint64, path string, userID uint64) (Entry, error)
	Read(ctx context.Context, deviceID uint64, path string, userID uint64) (io.ReadCloser, string, error)
	Write(ctx context.Context, deviceID uint64, path string, content io.Reader, userID uint64) error
	Mkdir(ctx context.Context, deviceID uint64, path string, mode uint32, userID uint64) error
	Rmdir(ctx context.Context, deviceID uint64, path string, userID uint64) error
	Rm(ctx context.Context, deviceID uint64, path string, userID uint64) error
	Rename(ctx context.Context, deviceID uint64, oldPath, newPath string, userID uint64) error
	Chmod(ctx context.Context, deviceID uint64, path string, mode uint32, userID uint64) error
	Upload(ctx context.Context, deviceID uint64, path string, r io.Reader, size int64, userID uint64) (int64, error)
	Download(ctx context.Context, deviceID uint64, path string, userID uint64) (io.ReadCloser, int64, string, error)
}

// Entry is one directory row. Fields are intentionally narrow so the
// SPA can render with or without a separate Stat call. mtime is any
// so JSON encoding picks RFC3339 when populated, null otherwise.
type Entry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`  // unix perm octal string, e.g. "0644"
	IsDir bool   `json:"is_dir"`
	Mtime any    `json:"mtime"` // RFC3339 string when populated
	Owner string `json:"owner,omitempty"`
}

// --- handler ---

// FSHandler owns the 11 endpoints under /v1/devices/{id}/fs/*. Audit
// is performed inside the biz layer (per-user credit).
type FSHandler struct {
	svc SFTPService
	log *slog.Logger
}

// NewFSHandler builds the handler. svc may be nil — endpoints return
// 503 in that case so the SPA shows a "SFTP unavailable" placeholder
// before A3 lands.
func NewFSHandler(svc SFTPService, log *slog.Logger) *FSHandler {
	if log == nil {
		log = slog.Default()
	}
	return &FSHandler{svc: svc, log: log}
}

// Register attaches the 11 endpoints. None are individually
// admin-gated here — casbin policy at the authz layer scopes
// "can THIS user touch THIS device's filesystem".
func (h *FSHandler) Register(r chi.Router) {
	r.Get("/v1/devices/{id}/fs/list", h.list)
	r.Get("/v1/devices/{id}/fs/stat", h.stat)
	r.Get("/v1/devices/{id}/fs/read", h.read)
	r.Post("/v1/devices/{id}/fs/write", h.write)
	r.Post("/v1/devices/{id}/fs/mkdir", h.mkdir)
	r.Post("/v1/devices/{id}/fs/rmdir", h.rmdir)
	r.Post("/v1/devices/{id}/fs/rm", h.rm)
	r.Post("/v1/devices/{id}/fs/rename", h.rename)
	r.Post("/v1/devices/{id}/fs/chmod", h.chmod)
	r.Post("/v1/devices/{id}/fs/upload", h.upload)
	r.Get("/v1/devices/{id}/fs/download", h.download)
}

// svcOrUnavailable is the unified gate. Returns false (and writes
// 503) when svc hasn't been wired yet; otherwise true.
func (h *FSHandler) svcOrUnavailable(w http.ResponseWriter) bool {
	if h.svc == nil {
		http.Error(w, "sftp service unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// --- helpers ---

// deviceAndPath parses {id} and the ?path= query arg. The query
// form beats the path arg for the REST list/stat/read endpoints
// because the path often contains '/' which doesn't survive a
// chi-route segment.
func deviceAndPath(r *http.Request) (uint64, string, error) {
	id, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		return 0, "", errors.Join(errs.ErrInvalid, err)
	}
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		return id, "", fmt.Errorf("%w: path required", errs.ErrInvalid)
	}
	return id, p, nil
}

// userID pulls the Tenant UserID out of the request context. Returns
// 0 when no tenant is attached — defensive. The biz layer writes 0
// to audit if it sees it (which should never happen behind the
// protected chi group).
func userID(r *http.Request) uint64 {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		return 0
	}
	return t.UserID
}

// jsonBody decodes r.Body into v. Returns ErrInvalid on bad JSON.
func jsonBody(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errors.Join(errs.ErrInvalid, err)
	}
	return nil
}

// --- endpoint implementations ---

// list: GET /v1/devices/{id}/fs/list?path=…
func (h *FSHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	entries, err := h.svc.List(r.Context(), id, path, userID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"items": entries, "total": len(entries)})
}

// stat: GET /v1/devices/{id}/fs/stat?path=…
func (h *FSHandler) stat(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	e, err := h.svc.Stat(r.Context(), id, path, userID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, e)
}

// read: GET /v1/devices/{id}/fs/read?path=… — stream file content.
func (h *FSHandler) read(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rc, name, err := h.svc.Read(r.Context(), id, path, userID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(name)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// write: POST /v1/devices/{id}/fs/write?path=… — body is raw content.
func (h *FSHandler) write(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.Write(r.Context(), id, path, r.Body, userID(r)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mkdir: POST /v1/devices/{id}/fs/mkdir?path=…  body {mode: 0755|...}
func (h *FSHandler) mkdir(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Mode uint32 `json:"mode"`
	}
	if err := jsonBody(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Mode == 0 {
		body.Mode = 0o755
	}
	if err := h.svc.Mkdir(r.Context(), id, path, body.Mode, userID(r)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rmdir: POST /v1/devices/{id}/fs/rmdir?path=…
func (h *FSHandler) rmdir(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.svc.Rmdir(r.Context(), id, path, userID(r)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rm: POST /v1/devices/{id}/fs/rm?path=…  (?recursive=true for dirs)
func (h *FSHandler) rm(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Recursive is a hint the biz layer decides what to do with —
	// today the interface is recursive=false-only; the SPA sends
	// Rmdir when it actually wants to drop a directory.
	if err := h.svc.Rm(r.Context(), id, path, userID(r)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rename: POST /v1/devices/{id}/fs/rename  body {from:"…", to:"…"}
func (h *FSHandler) rename(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, _, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := jsonBody(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.From == "" || body.To == "" {
		writeErr(w, fmt.Errorf("%w: from & to required", errs.ErrInvalid))
		return
	}
	if err := h.svc.Rename(r.Context(), id, body.From, body.To, userID(r)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// chmod: POST /v1/devices/{id}/fs/chmod?path=…  body {mode:0644}
func (h *FSHandler) chmod(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body struct {
		Mode uint32 `json:"mode"`
	}
	if err := jsonBody(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if body.Mode == 0 {
		writeErr(w, fmt.Errorf("%w: mode required", errs.ErrInvalid))
		return
	}
	if err := h.svc.Chmod(r.Context(), id, path, body.Mode, userID(r)); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upload: POST /v1/devices/{id}/fs/upload?path=…  multipart/form-data
// Field "file" carries the binary.
func (h *FSHandler) upload(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MiB header threshold
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, fmt.Errorf("%w: file field required", errs.ErrInvalid))
		return
	}
	defer file.Close()
	n, err := h.svc.Upload(r.Context(), id, path, file, hdr.Size, userID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"bytes_written": n})
}

// download: GET /v1/devices/{id}/fs/download?path=…
func (h *FSHandler) download(w http.ResponseWriter, r *http.Request) {
	if !h.svcOrUnavailable(w) {
		return
	}
	id, path, err := deviceAndPath(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rc, size, name, err := h.svc.Download(r.Context(), id, path, userID(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(name)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// --- shared JSON helpers (re-used by http.go too) ---

func writeJSONStatus(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
