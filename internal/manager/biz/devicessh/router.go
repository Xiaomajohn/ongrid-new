package devicessh

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/ssh"

	"github.com/ongridio/ongrid/internal/manager/model/device"
	edgemodel "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// LinksLookup is the narrow edge-device junction lookup. Production
// wires *devicebiz.Usecase.LookupEdgeForDevice (or any adapter that
// resolves to a non-nil edgeID on a healthy junction).
//
// Return-contract:
//
//	(id > 0, nil)               — edge attached; router may use it
//	(0, nil)                    — no edge attached (treated as fallthrough)
//	(_, errs.ErrNotFound)       — same as no-edge (store-layer convention)
//	(_, other error)            — DB / transport error; router surfaces it
//
// Tests pass a table-driven fake that maps to this contract directly.
type LinksLookup interface {
	LookupEdgeForDevice(ctx context.Context, deviceID uint64) (uint64, error)
}

// EdgesGet fetches the edge's status by id so the router can read
// "online" and decide whether to take the tunnel route. Production
// wires *edgebiz.Usecase.Get via a tiny adapter that projects
// (*model.Edge).Status onto a string.
//
// Errors:
//
//	("", errs.ErrNotFound) — fall through to direct (same as no-edge)
//	("", other error)      — router surfaces as 500/503
type EdgesGet interface {
	Get(ctx context.Context, id uint64) (status string, err error)
}

// Router picks one Dialer per (Device, Purpose) call. The selection
// policy is documented in the plan §"选路决策核心"; in short:
//   - PurposeInstallEdge always picks direct (the device's edge may not
//     be online yet — we're trying to install it).
//   - Otherwise, if the device has an edge in online status, tunnel.
//   - Finally fall back to direct; the device row must carry valid
//     ssh_host / ssh_user / ssh_password-or-key.
//
// The router is HTTP-agnostic and safe to share across goroutines —
// it carries no per-call state. The SFTPService reaches into it via
// MustConnect to reuse the direct dialer's TCP/SSH client.
type Router struct {
	direct *DirectDialer
	tunnel *TunnelDialer
	links  LinksLookup
	edges  EdgesGet
}

// NewRouter wires the dependencies. direct must be non-nil (the
// fallback path is always direct); tunnel / links / edges may be nil —
// when any of those three is nil the router behaves as if no edge is
// attached, falling through to direct.
func NewRouter(direct *DirectDialer, tunnel *TunnelDialer, links LinksLookup, edges EdgesGet) *Router {
	return &Router{
		direct: direct,
		tunnel: tunnel,
		links:  links,
		edges:  edges,
	}
}

// Pick returns the Dialer appropriate for the call. See struct comment
// for the decision tree.
//
// Errors:
//
//   - ErrSSHConfigMissing  — direct path is the only option but device
//     lacks host/user/credential.
//   - errDialerUnwired (501) — production wiring forgot the direct
//     dialer.
//   - any wrapped DB / transport error from the link or edge lookup.
func (r *Router) Pick(ctx context.Context, d *device.Device, purpose Purpose) (Dialer, error) {
	if r == nil {
		return nil, fmt.Errorf("%w", errRouterNotConstructed)
	}
	if d == nil {
		return nil, fmt.Errorf("%w: device is nil", ErrSSHConfigMissing)
	}
	if r.direct == nil {
		return nil, fmt.Errorf("%w: direct dialer nil", errDialerUnwired)
	}

	// 1) purpose != InstallEdge → prefer tunnel when there is an
	//    online edge for this device. "No edge" (id==0) or a not-found
	//    from the lookup are normal branches and fall through; any
	//    other error bubbles up.
	if purpose != PurposeInstallEdge && r.links != nil && r.edges != nil && r.tunnel != nil {
		edgeID, lerr := r.links.LookupEdgeForDevice(ctx, d.ID)
		switch {
		case lerr == nil && edgeID > 0:
			status, sErr := r.edges.Get(ctx, edgeID)
			switch {
			case sErr == nil && status == edgemodel.StatusOnline:
				return r.tunnel, nil
			case sErr != nil && !errors.Is(sErr, errs.ErrNotFound):
				return nil, fmt.Errorf("devicessh.router: edges.Get(%d): %w", edgeID, sErr)
			}
			// (status != online) or edge vanished mid-flight → fall through.
		case lerr != nil && !errors.Is(lerr, errs.ErrNotFound):
			return nil, fmt.Errorf("devicessh.router: links.LookupEdgeForDevice(%d): %w", d.ID, lerr)
		}
	}

	// 2) direct branch — needs credentials on the device row.
	if err := checkDeviceForDirect(d); err != nil {
		return nil, err
	}
	return r.direct, nil
}

// MustConnect returns a live *ssh.Client for the (Device, Purpose) pair,
// selecting the route via Pick and then negotiating the SSH handshake.
// Used by the SFTP service to start an sftp subsystem without paying for
// a separate tunnel/session dance.
//
// Returns the same error envelopes as Pick. The caller is responsible
// for Close()ing the returned client.
func (r *Router) MustConnect(ctx context.Context, d *device.Device, purpose Purpose) (*ssh.Client, error) {
	dialer, err := r.Pick(ctx, d, purpose)
	if err != nil {
		return nil, err
	}
	switch dl := dialer.(type) {
	case *DirectDialer:
		return dl.Connect(ctx, d)
	case *TunnelDialer:
		// B1 will plumb *ssh.Client through TunnelDialer.Connect once
		// the geminio-overlay plumbing lands. A3 returns the tunnel
		// placeholder error verbatim.
		return nil, fmt.Errorf("%w: tunnel MustConnect pending B1", ErrTunnelNotImplemented)
	default:
		return nil, fmt.Errorf("devicessh: dialer %T does not expose *ssh.Client", dl)
	}
}

// errDialerUnwired is returned when NewRouter was called with a nil
// direct dialer. Surface as 501 via errs.HTTPStatus.ErrNotWiredYet.
var errDialerUnwired = fmt.Errorf("devicessh: %w", errs.ErrNotWiredYet)

// errRouterNotConstructed is the sentinel for a nil-receiver Pick —
// defends against accidental misuse by route handlers.
var errRouterNotConstructed = fmt.Errorf("devicessh: %w", errs.ErrNotWiredYet)
