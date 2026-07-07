package devicessh

import (
	"context"
	"fmt"
	"log/slog"

	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// ShellOpts mirrors the open-frame request shape declared in
// server/devicessh.ShellOpts. The two structs are kept structurally
// identical so the wiring in cmd/ongrid/main.go can convert between them
// without field-by-field translation. Declaring a parallel type here
// avoids the import cycle that would arise if biz/devicessh imported
// server/devicessh (the server package already imports the biz layer).
//
// Route pins the transport the caller wants. Defaults to
// RouteKindAuto (historical behaviour: tunnel when an online edge
// exists, direct otherwise). The /shell-direct endpoint overrides
// this to RouteKindDirect; /shell (the monitor-flow endpoint) leaves
// it as Auto or pins RouteKindTunnel — see server/devicessh/http.go.
type ShellOpts struct {
	Cols    int
	Rows    int
	Term    string
	SSHUser string
	SSHPass string
	Route   RouteKind
}

// ShellHandle is the minimal Read/Write/Close contract the biz layer
// hands back to the HTTP layer's WebSocket bridge. Same parallel-type
// rationale as ShellOpts: structurally identical to
// server/devicessh.ShellHandle, but a local declaration keeps the biz
// package free of an import cycle.
type ShellHandle interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Close() error
}

// DeviceLoader fetches a device by id for OpenShell. The production
// wiring passes *device.Usecase, whose Get method satisfies this
// interface 1:1 — no adapter needed.
type DeviceLoader interface {
	Get(ctx context.Context, id uint64) (*devicemodel.Device, error)
}

// ShellService is the biz/devicessh concrete that satisfies
// server/devicessh.DevicesshService at the wiring layer (main.go
// installs a thin adapter converting between the parallel ShellOpts /
// ShellHandle shapes). It picks tunnel or direct per Router policy and
// returns a thin handle whose Read/Write map to the underlying Session's
// stdio.
//
// The service does NOT own the bizwebshell.Router (active-session
// bookkeeping for the existing edge-only webshell). Devices are opened
// via this service; the older /v1/devices/{device_id}/shell edge-only
// route remains a separate package. Keeping them split means the device
// path is self-contained — no casbin/listener coupling.
type ShellService struct {
	router *Router
	loader DeviceLoader
	log    *slog.Logger
}

// NewShellService wires the dependencies. router and loader must be
// non-nil; log may be nil (defaults to slog.Default()). One instance per
// boot — the service holds no per-call state.
func NewShellService(router *Router, loader DeviceLoader, log *slog.Logger) *ShellService {
	if log == nil {
		log = slog.Default()
	}
	return &ShellService{
		router: router,
		loader: loader,
		log:    log.With(slog.String("comp", "devicessh-shell")),
	}
}

// OpenShell opens an interactive PTY shell on the device.
//
// Steps:
//  1. loader.Get(deviceID) → device row
//  2. router.Pick(ctx, dev, PurposeShell) → Dialer (tunnel or direct)
//  3. dialer.Dial(ctx, dev) → Session (PTY up but Shell not yet requested)
//  4. session.Shell(ctx, rows, cols, term)
//  5. wrap in a shellHandle returning (Read, Write, Close) of the Session
//
// On SSH auth failure, the underlying session.Dial returns an error
// which the HTTP layer maps to "auth_error" frame.
//
// The user parameter is currently unused by the SSH plumbing itself —
// it exists to keep the signature symmetric with
// server/devicessh.DevicesshService so the wiring adapter doesn't have
// to fabricate one. Future per-operator audit hooks (who opened this
// shell) will reach for user.UserID here.
func (s *ShellService) OpenShell(ctx context.Context, deviceID uint64, user *tenantctx.Tenant, opts ShellOpts) (ShellHandle, error) {
	if s.router == nil {
		return nil, fmt.Errorf("devicessh.shell: router not wired")
	}
	if s.loader == nil {
		return nil, fmt.Errorf("devicessh.shell: loader not wired")
	}

	dev, err := s.loader.Get(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("devicessh.shell: load device %d: %w", deviceID, err)
	}

	// If the operator provided SSHUser/SSHPass in the open frame,
	// override the device row for THIS call only. The override is the
	// device biz-layer convention ("ad-hoc user without rotating the
	// stored credential").
	if opts.SSHUser != "" {
		dev.SSHUser = opts.SSHUser
	}
	if opts.SSHPass != "" {
		dev.SSHPassword = opts.SSHPass
	}

	dialer, err := s.router.Pick(ctx, dev, PurposeShell, opts.Route)
	if err != nil {
		return nil, err
	}

	sess, err := dialer.Dial(ctx, dev)
	if err != nil {
		return nil, err
	}

	if err := sess.Shell(ctx, opts.Rows, opts.Cols, opts.Term); err != nil {
		// Tear down the half-open session so the caller doesn't have
		// to chase a leaked ssh.Client. We don't surface the close
		// error — the Shell error is the actionable one.
		_ = sess.Close()
		return nil, fmt.Errorf("devicessh.shell: pty+shell: %w", err)
	}

	userID := uint64(0)
	if user != nil {
		userID = user.UserID
	}
	s.log.Debug("devicessh.shell: opened",
		slog.Uint64("device_id", deviceID),
		slog.Uint64("user_id", userID),
		slog.Int("cols", opts.Cols),
		slog.Int("rows", opts.Rows),
		slog.String("term", opts.Term),
	)

	return &shellHandle{Session: sess}, nil
}

// shellHandle is the thin bridge between biz/devicessh.ShellHandle
// (3-method contract) and devicessh.Session (6-method contract). The
// Session is embedded by interface so the wrapper stays small.
type shellHandle struct {
	Session Session
}

// Read forwards merged stdout/stderr bytes from the remote shell.
func (h *shellHandle) Read(p []byte) (int, error) { return h.Session.Read(p) }

// Write forwards bytes to the remote shell's stdin.
func (h *shellHandle) Write(p []byte) (int, error) { return h.Session.Write(p) }

// Close tears down the underlying SSH session (and the connection it
// owns, per directSession.Close / tunnelSession.Close semantics).
func (h *shellHandle) Close() error { return h.Session.Close() }

// Compile-time guard: ShellService exposes the OpenShell signature
// the wiring adapter targets. Keeps import drift honest while the
// parallel-types arrangement is in place.
var _ interface {
	OpenShell(context.Context, uint64, *tenantctx.Tenant, ShellOpts) (ShellHandle, error)
} = (*ShellService)(nil)