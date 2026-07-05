// Package devicessh wires the per-device SSH control plane: a
// WebSocket interactive shell at /v1/devices/{id}/shell and the REST
// SFTP surface at /v1/devices/{id}/fs/*. Both depend on the device-
// SSH biz layer (delivered by A3) which owns the routing decisions
// (direct dial vs tunnel), the audit, and the concurrency budget.
//
// HTTP-only. State (tunnel routing, edge binding, audit) lives in
// internal/manager/biz/devicessh; this file stays HTTP-agnostic so
// the biz layer can be unit-tested with fakes.
package devicessh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// --- service contracts (interfaces) ---
//
// The biz layer (A3) implements these. This file only depends on
// the contract, not the implementation, so the HTTP layer can be
// built before A3 lands. The constructor in main.go is the only
// place that knows the concrete type.

// DevicesshService opens interactive shells over the shell control
// plane (direct-to-edge or tunneled, decided by A3). The returned
// ShellHandle is a minimal io.ReadWriteCloser that the HTTP layer
// pumps to/from the browser WebSocket.
type DevicesshService interface {
	OpenShell(ctx context.Context, deviceID uint64, user *tenantctx.Tenant, opts ShellOpts) (ShellHandle, error)
}

// ShellOpts is the open-frame request shape forwarded to the biz
// layer. We accept the SSH user/pass on every open — the biz layer
// resolves them against the stored credentials plus an optional
// ad-hoc override (the SPA sends overrides when the operator wants
// a one-off user without rotating the stored credentials).
type ShellOpts struct {
	Cols    int
	Rows    int
	Term    string
	SSHUser string
	SSHPass string
}

// ShellHandle is the minimal contract the HTTP layer pumps the
// WebSocket against. The biz layer wraps an ssh.Client + pty + agent
// tunnel (or a direct net.Conn for local-edge boxes) and exposes
// it via this trio so the WS bridge doesn't care which transport
// is underneath.
type ShellHandle interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// --- handler ---

// ShellHandler owns the WS upgrade endpoint. The biz layer is
// injected as an interface so the same HTTP shape can talk to the
// A3 router, a debug direct-dial implementation, or a unit test fake.
type ShellHandler struct {
	svc       DevicesshService
	upgrader  websocket.Upgrader
	log       *slog.Logger

	// PerSession limits mirror the existing webshell server so the
	// SPA doesn't suddenly discover a new (lower) cap after migrating.
	MaxPerUser   int
	MaxPerDevice int
	IdleTimeout  time.Duration

	// Counter hooks — reserved for a future A6 / manager-side metric.
	// Left as no-op by default so the constructor doesn't need them.
	Now func() time.Time
}

// NewShellHandler builds the handler around the biz-layer service.
// svc may be a real A3 router or a dev stub — OpenShell is what the
// SPA triggers via the WS open frame, never on construction.
func NewShellHandler(svc DevicesshService, log *slog.Logger) *ShellHandler {
	if log == nil {
		log = slog.Default()
	}
	return &ShellHandler{
		svc:          svc,
		log:          log,
		MaxPerUser:   5,
		MaxPerDevice: 5,
		IdleTimeout:  15 * time.Minute,
		Now:          func() time.Time { return time.Now().UTC() },
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 16384,
			Subprotocols:    []string{"ongrid.shell.v1"},
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// Register attaches the WS shell route on r. Single endpoint, kept
// out of the (auth-required) group so a future public-tenant mode
// can decorate it with its own authz; today the manager's existing
// auth.Middleware wraps the whole /api/* group.
func (h *ShellHandler) Register(r chi.Router) {
	r.Get("/v1/devices/{id}/shell", h.handle)
}

// --- wire frames ---

// openFrame is the first text frame the browser sends after upgrade.
// Mirrors the existing manager/server/webshell openMsg shape so
// frontends can reuse the same library client.
type openFrame struct {
	Type    string `json:"type"` // "open"
	Cols    uint16 `json:"cols"`
	Rows    uint16 `json:"rows"`
	Term    string `json:"term,omitempty"`
	SSHUser string `json:"ssh_user"`
	SSHPass string `json:"ssh_pass,omitempty"`
}

// ctlFrame is any subsequent text-frame control message.
type ctlFrame struct {
	Type string `json:"type"` // "resize" | "close"
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// handle is the WS upgrade entry point. Mirrors the existing
// manager/server/webshell.openShell flow but delegates the
// device→edge→ssh plumbing to the A3-injected DevicesshService.
func (h *ShellHandler) handle(w http.ResponseWriter, r *http.Request) {
	tenant, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	deviceID, err := strconv.ParseUint(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if h.svc == nil {
		// Service hasn't been wired yet (A3 not landed). Degrade
		// gracefully — 503 lets the SPA show "shell unavailable"
		// instead of a 500.
		http.Error(w, "shell service unavailable", http.StatusServiceUnavailable)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Warn("devicessh: upgrade", slog.Any("err", err))
		return
	}
	defer conn.Close()

	// Read the open frame within 10s — far longer than any healthy
	// browser can produce it, short enough that a half-open WS
	// doesn't pin the goroutine.
	conn.SetReadDeadline(h.Now().Add(10 * time.Second))
	mt, payload, err := conn.ReadMessage()
	conn.SetReadDeadline(time.Time{})
	if err != nil || mt != websocket.TextMessage {
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "expected open frame"))
		return
	}
	var open openFrame
	if err := json.Unmarshal(payload, &open); err != nil || open.Type != "open" {
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseProtocolError, "bad open frame"))
		return
	}

	cols, rows := int(open.Cols), int(open.Rows)
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	term := open.Term
	if term == "" {
		term = "xterm-256color"
	}

	handle, err := h.svc.OpenShell(r.Context(), deviceID, &tenant, ShellOpts{
		Cols:    cols,
		Rows:    rows,
		Term:    term,
		SSHUser: open.SSHUser,
		SSHPass: open.SSHPass,
	})
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, jsonMust(map[string]any{
			"type":    "auth_error",
			"message": "open shell: " + err.Error(),
		}))
		_ = conn.Close()
		return
	}
	if open.SSHPass != "" {
		// Wipe the in-memory copy asap — the biz layer already has
		// its own copy if it needs to replay; ours is the only one
		// sitting on the goroutine stack.
		open.SSHPass = ""
	}

	// Tell the SPA the shell is ready before forwarding any bytes so
	// the UI can flip "connecting" → "live" deterministically.
	_ = conn.WriteJSON(map[string]any{"type": "ready"})

	pumpAndTeardown(conn, handle, h.log)
}

// pumpAndTeardown runs the bidirectional forwarder. Mirrors the
// existing webshell pump without re-implementing the idle watchdog
// or audit close — the biz-layer ShellHandle owns session lifecycle
// (it knows when the SSH channel exits and what the exit code was).
func pumpAndTeardown(conn *websocket.Conn, handle ShellHandle, log *slog.Logger) {
	defer handle.Close()

	// SSH/pty → browser (binary frames).
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := handle.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Debug("devicessh: handle read", slog.Any("err", err))
				}
				return
			}
		}
	}()

	// Browser → SSH/pty. Binary frames carry keystrokes; text frames
	// carry resize / close control.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			if _, err := handle.Write(data); err != nil {
				return
			}
		case websocket.TextMessage:
			var ctl ctlFrame
			if err := json.Unmarshal(data, &ctl); err != nil {
				continue
			}
			// The biz-layer handle owns resize / shutdown. Today we
			// only act on close; resize is a hint the biz layer
			// decides what to do with (some transports ignore it
			// when the pty wasn't sized yet).
			if ctl.Type == "close" {
				return
			}
		case websocket.CloseMessage:
			return
		}
	}
}

// jsonMust marshals v or returns a placeholder. Used for one-shot
// WS text frames where a marshal failure is non-actionable (the
// connection is going down anyway).
func jsonMust(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(fmt.Sprintf("{\"error\":\"marshal: %s\"}", err.Error()))
	}
	return b
}

// writeErr renders an error in the same JSON shape device/audit
// handlers use. Kept package-private; FS handler reuses it.
func writeErr(w http.ResponseWriter, err error) {
	status := errs.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
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
	case errors.Is(err, errs.ErrEdgeOffline):
		return "edge-offline"
	case errors.Is(err, errs.ErrNotWiredYet):
		return "not-wired-yet"
	default:
		return "internal"
	}
}
