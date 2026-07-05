// Package device — biz Usecase facade. Wraps Repo + EdgeDeviceRepo so
// the HTTP handler doesn't have to thread two dependencies through.
package device

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/device"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Usecase is the manager/device biz-layer facade.
type Usecase struct {
	repo  Repo
	links EdgeDeviceRepo
	log   *slog.Logger
}

// NewUsecase builds the usecase. links may be nil — junction-aware methods
// will return ErrNotWiredYet so callers degrade gracefully. log may be nil.
func NewUsecase(repo Repo, links EdgeDeviceRepo, log *slog.Logger) *Usecase {
	return &Usecase{repo: repo, links: links, log: log}
}

// Repo returns the underlying device Repo for callers that need direct
// access (e.g. the edge HTTP handler hydrating host_info on the listing
// response).
func (u *Usecase) Repo() Repo { return u.repo }

// Links returns the underlying junction repo. May be nil.
func (u *Usecase) Links() EdgeDeviceRepo { return u.links }

// ReconcilePresence flips orphan "ghost" devices (online=true with no
// online linked edge) back to offline and returns how many it healed.
// Called once at boot and then on a ticker so device presence converges
// even across manager restarts and hard edge deletes — the per-event
// MarkOnline/MarkOffline paths can't see an edge that no longer exists.
func (u *Usecase) ReconcilePresence(ctx context.Context) (int64, error) {
	if u.repo == nil {
		return 0, errs.ErrNotWiredYet
	}
	n, err := u.repo.ReconcileOfflineOrphans(ctx)
	if err != nil {
		return 0, err
	}
	if n > 0 && u.log != nil {
		u.log.Info("device presence reconcile: flipped orphan devices offline", "count", n)
	}
	return n, nil
}

// Get returns one device by id.
func (u *Usecase) Get(ctx context.Context, id uint64) (*model.Device, error) {
	if u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	return u.repo.Get(ctx, id)
}

// List returns devices matching f.
func (u *Usecase) List(ctx context.Context, f ListFilter) ([]*model.Device, error) {
	if u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	return u.repo.List(ctx, f)
}

// UpdateRoles assigns the device-roles bit set used for sidebar grouping
// and AI prompt routing. Names is the canonical wire shape ("server" /
// "storage" / "network" / "database"); the special "unknown" name (or
// an empty list) clears the bit set. Names outside the canonical enum
// are rejected so a silent typo can't park a device in a phantom bucket.
func (u *Usecase) UpdateRoles(ctx context.Context, id uint64, names []string) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !model.IsValidRoleName(n) {
			return fmt.Errorf("%w: invalid role %q", errs.ErrInvalid, n)
		}
	}
	roles := model.EncodeRoles(names)
	if !model.IsValidRoles(roles) {
		return fmt.Errorf("%w: invalid roles bit set", errs.ErrInvalid)
	}
	if err := u.repo.UpdateRoles(ctx, id, roles); err != nil {
		return err
	}
	if u.log != nil {
		u.log.Info("device roles updated", "id", id, "roles", roles, "names", model.DecodeRoles(roles))
	}
	return nil
}

// UpdateNameDescription updates operator-editable display fields.
func (u *Usecase) UpdateNameDescription(ctx context.Context, id uint64, name, description string) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	return u.repo.UpdateNameDescription(ctx, id, strings.TrimSpace(name), strings.TrimSpace(description))
}

// SetSSHCredentials persists the operator-supplied SSH block. The
// minimal validation rule — non-empty user + a recognized auth kind —
// is enforced here so a typo doesn't park a device in a weird state
// (the web-layer handler also checks, but this is the authority).
//
// Empty creds.User is treated as a clear-all: kind/value fields are
// blanked and the operator loses shell access to this device. The
// service layer usually routes that through ClearSSHCredentialsField
// instead, but supporting it here keeps the public surface small.
func (u *Usecase) SetSSHCredentials(ctx context.Context, id uint64, creds SSHCredentials) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	creds.User = strings.TrimSpace(creds.User)
	creds.AuthKind = strings.TrimSpace(creds.AuthKind)
	switch creds.AuthKind {
	case "":
		// empty user + empty kind = clear-all path; just write zeros.
		if creds.User == "" {
			creds = SSHCredentials{Port: 22, AuthKind: "password"}
		} else {
			return fmt.Errorf("%w: auth kind required (password|key)", errs.ErrInvalid)
		}
	case "password", "key":
		if creds.User == "" {
			return fmt.Errorf("%w: user required", errs.ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: auth kind must be password or key, got %q", errs.ErrInvalid, creds.AuthKind)
	}
	if creds.AuthKind == "password" && strings.TrimSpace(creds.Password) == "" {
		return fmt.Errorf("%w: password required when auth=password", errs.ErrInvalid)
	}
	if creds.AuthKind == "key" && strings.TrimSpace(creds.Key) == "" {
		return fmt.Errorf("%w: key required when auth=key", errs.ErrInvalid)
	}
	if creds.Port == 0 {
		creds.Port = 22
	}
	if err := u.repo.SetSSHCredentials(ctx, id, creds); err != nil {
		return err
	}
	if u.log != nil {
		u.log.Info("device ssh credentials set",
			"id", id, "user", creds.User, "kind", creds.AuthKind)
	}
	return nil
}

// GetSSHCredentials returns the SSH block in a wire-safe shape: never
// the password / key plaintext (those are write-only on purpose — see
// the model comment).
func (u *Usecase) GetSSHCredentials(ctx context.Context, id uint64) (*SSHCredentialsWire, error) {
	if u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	d, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	w := &SSHCredentialsWire{
		Host:       d.SSHHost,
		Port:       d.SSHPort,
		User:       d.SSHUser,
		AuthKind:   d.SSHAuthKind,
		HasPassword: d.SSHPassword != "",
		HasKey:      d.SSHKey != "",
		HostKey:    d.SSHHostKey,
		LastSeenAt: d.SSHLastSeenAt,
		LastError:  d.SSHLastError,
	}
	if w.Port == 0 {
		w.Port = 22
	}
	if w.AuthKind == "" {
		w.AuthKind = "password"
	}
	return w, nil
}

// ClearSSHCredentialsField wipes the operator-set secret the caller
// wants rotated. kind="password" clears SSHPassword, kind="key" clears
// SSHKey. Non-secret fields (host/port/user) are untouched.
func (u *Usecase) ClearSSHCredentialsField(ctx context.Context, id uint64, kind string) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	switch kind {
	case "password", "key":
	default:
		return fmt.Errorf("%w: kind must be password or key", errs.ErrInvalid)
	}
	if err := u.repo.ClearSSHCredentialsField(ctx, id, kind); err != nil {
		return err
	}
	if u.log != nil {
		u.log.Info("device ssh credential field cleared", "id", id, "kind", kind)
	}
	return nil
}

// SetSSHCredentialsIAW is the installed-agent side of the SSH block.
// Separately exposed so the ongrid-edge install worker can write its
// probe results without the web layer inadvertently erasing them.
func (u *Usecase) SetSSHCredentialsIAW(ctx context.Context, id uint64, iaw SSHCredentialsIAW) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	return u.repo.SetSSHCredentialsIAW(ctx, id, iaw)
}

// SSHCredentialsWire is the SSH-info DTO shape the SPA reads. It
// deliberately omits plaintext password / key — has_* flags say
// whether the corresponding field is set so the UI can render
// "configured" / "not configured" without leaking the secret.
type SSHCredentialsWire struct {
	Host        string     `json:"host,omitempty"`
	Port        int        `json:"port"`
	User        string     `json:"user,omitempty"`
	AuthKind    string     `json:"auth_kind"`
	HasPassword bool       `json:"has_password"`
	HasKey      bool       `json:"has_key"`
	HostKey     string     `json:"host_key,omitempty"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
}

// Delete soft-deletes a device. Junction rows are NOT auto-removed —
// caller is responsible (the v1 UI doesn't expose device deletion yet
// so this is a future hook).
func (u *Usecase) Delete(ctx context.Context, id uint64) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	return u.repo.Delete(ctx, id)
}

// LookupHostDevice resolves edge → host device_id. Returns 0,
// ErrNotFound when the edge has no Type=Host junction yet (race during
// register).
func (u *Usecase) LookupHostDevice(ctx context.Context, edgeID uint64) (uint64, error) {
	if u.links == nil {
		return 0, errs.ErrNotWiredYet
	}
	return u.links.LookupHostDevice(ctx, edgeID)
}

// LookupEdgeForDevice resolves device → owning edge_id (type=host).
func (u *Usecase) LookupEdgeForDevice(ctx context.Context, deviceID uint64) (uint64, error) {
	if u.links == nil {
		return 0, errs.ErrNotWiredYet
	}
	return u.links.LookupEdgeForDevice(ctx, deviceID, model.EdgeDeviceRelationHost)
}

// LinkHost upserts the (edge, device, type=host) junction row. Called
// from the edge register flow.
func (u *Usecase) LinkHost(ctx context.Context, edgeID, deviceID uint64) error {
	if u.links == nil {
		return errs.ErrNotWiredYet
	}
	return u.links.Link(ctx, edgeID, deviceID, model.EdgeDeviceRelationHost)
}

// TouchSSHSuccess records that the most recent SSH interaction with
// the device succeeded. Resets the last_error column. Called by the
// devicessh dialer after a successful connect/auth.
//
// This is a thin wrapper over repo.TouchSSHSuccess; the operation is
// fire-and-forget at the wire layer (the dialer doesn't gate on
// success/failure of the bookkeeping write), so we don't return its
// error to the caller — we log and drop instead. That keeps the
// dialer's hot path single-error.
func (u *Usecase) TouchSSHSuccess(ctx context.Context, id uint64) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	if err := u.repo.TouchSSHSuccess(ctx, id); err != nil {
		if u.log != nil {
			u.log.Warn("device touch ssh success failed", "id", id, "err", err)
		}
		return err
	}
	return nil
}

// TouchSSHError records that the most recent SSH interaction with the
// device failed. Stores the (truncated) error message in last_error
// and updates last_seen_at. errMsg is capped at 512 chars here so
// callers can't push arbitrarily large blobs into log lines; the store
// layer also caps at 500 chars before the column write.
func (u *Usecase) TouchSSHError(ctx context.Context, id uint64, errMsg string) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	if len(errMsg) > 512 {
		errMsg = errMsg[:512]
	}
	if err := u.repo.TouchSSHError(ctx, id, errMsg); err != nil {
		if u.log != nil {
			u.log.Warn("device touch ssh error failed", "id", id, "err", err)
		}
		return err
	}
	return nil
}
