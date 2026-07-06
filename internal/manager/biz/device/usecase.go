// Package device — biz Usecase facade. Wraps Repo + EdgeDeviceRepo so
// the HTTP handler doesn't have to thread two dependencies through.
package device

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	model "github.com/ongridio/ongrid/internal/manager/model/device"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// manualFingerprintPrefix marks Devices seeded by the operator's web UI
// rather than by an edge-agent register call. The UUID suffix keeps the
// row unique within the manual namespace; once an edge actually reports
// in for this host, RebindFingerprint (model/edge DeviceID → real
// machine-id) takes over and the manual:… fingerprint is overwritten
// in place (device.ID / junction / history all carry over).
const manualFingerprintPrefix = "manual:"

// CreateInput is the operator-supplied payload for POST /v1/devices.
// Host facts (OS / CPU / Mem / disk) are intentionally NOT here — those
// arrive when the edge agent registers; we just persist the operator-
// facing name + SSH credentials on day 1.
type CreateInput struct {
	Name        string // required
	Description string
	Hostname    string // optional; falls back to sshHost when blank
	// SSH credentials — exactly one of Password / Key is required,
	// matched to AuthKind. Stored plaintext by design (internal ops tool).
	SSHHost     string // required
	SSHPort     int    // 0 → 22
	SSHUser     string // required
	SSHAuthKind string // "password" | "key"
	SSHPassword string
	SSHKey      string
}

// Create inserts a brand-new device row owned by the calling admin.
// Returns the persisted row (with ID populated) so the handler can echo
// the canonical response. Validation lives here so the wire layer stays
// a thin DTO mapper; the SQL layer (Repo.Create) assumes inputs are
// already clean.
//
// Fingerprint derivation: see manualFingerprintPrefix. We do NOT use
// (sshHost, sshPort, sshUser) as the fingerprint because re-pointing an
// existing device at a new SSH endpoint (e.g. migrated IP) shouldn't
// spawn a new row.
func (u *Usecase) Create(ctx context.Context, in CreateInput, createdBy *uint64) (*model.Device, error) {
	if u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	sshHost := strings.TrimSpace(in.SSHHost)
	if sshHost == "" {
		return nil, fmt.Errorf("%w: ssh_host required", errs.ErrInvalid)
	}
	sshUser := strings.TrimSpace(in.SSHUser)
	if sshUser == "" {
		return nil, fmt.Errorf("%w: ssh_user required", errs.ErrInvalid)
	}
	authKind := strings.TrimSpace(in.SSHAuthKind)
	switch authKind {
	case "password":
		if strings.TrimSpace(in.SSHPassword) == "" {
			return nil, fmt.Errorf("%w: ssh_password required when ssh_auth_kind=password", errs.ErrInvalid)
		}
	case "key":
		if strings.TrimSpace(in.SSHKey) == "" {
			return nil, fmt.Errorf("%w: ssh_key required when ssh_auth_kind=key", errs.ErrInvalid)
		}
	case "":
		authKind = "password"
	default:
		return nil, fmt.Errorf("%w: ssh_auth_kind must be password or key, got %q", errs.ErrInvalid, authKind)
	}
	port := in.SSHPort
	if port == 0 {
		port = 22
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("%w: ssh_port out of range", errs.ErrInvalid)
	}

	hostname := strings.TrimSpace(in.Hostname)
	if hostname == "" {
		hostname = sshHost
	}

	d := &model.Device{
		Fingerprint: manualFingerprintPrefix + uuid.NewString(),
		UserID:      createdBy,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Hostname:    hostname,
		// Host facts left at zero-value defaults — the edge agent will
		// overwrite via UpdateHostFacts on its first register.
		SSHHost:     sshHost,
		SSHPort:     port,
		SSHUser:     sshUser,
		SSHAuthKind: authKind,
		SSHPassword: in.SSHPassword,
		SSHKey:      in.SSHKey,
	}
	out, err := u.repo.Create(ctx, d)
	if err != nil {
		return nil, err
	}
	if u.log != nil {
		u.log.Info("device created",
			"id", out.ID, "name", out.Name, "ssh_host", out.SSHHost,
			"ssh_user", out.SSHUser, "auth_kind", out.SSHAuthKind,
			"fingerprint_kind", "manual")
	}
	return out, nil
}

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

// UpdateNameDescription updates operator-editable display fields (name /
// description / hostname). name 不允许为空；description 允许空字符串
// （operator 主动清空）；hostname 允许空——为空时由调用方在 UI 层 fallback
// 到 ssh_host。校验在 usecase 层做，避免无效写入穿透到 SQL。
func (u *Usecase) UpdateNameDescription(ctx context.Context, id uint64, name, description, hostname string) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: name required", errs.ErrInvalid)
	}
	return u.repo.UpdateNameDescription(ctx, id, name, strings.TrimSpace(description), strings.TrimSpace(hostname))
}

// UpdateReachability 写入 ping 服务的可达性结果。pinger 定时任务会
// 调用本方法把每台 host 的 ping 结果回填到 devices 表。
func (u *Usecase) UpdateReachability(ctx context.Context, id uint64, reachable bool, at *time.Time) error {
	if u.repo == nil {
		return errs.ErrNotWiredYet
	}
	return u.repo.UpdateReachability(ctx, id, reachable, at)
}

// PingReachable 拉所有有 ssh_host 的设备，对每台跑一次 ping，把结果
// 写回 devices.reachable / last_reachable_at。main.go 的 5 分钟定时器
// 调本函数。pinger 允许 nil——本函数会退化到 NewPinger(log) 的默认参数。
//
// 返回值：实际 ping 的设备数（不含空数据库的 fast-path 0）。
func (u *Usecase) PingReachable(ctx context.Context, pinger *Pinger) (int, error) {
	if u.repo == nil {
		return 0, errs.ErrNotWiredYet
	}
	targets, err := u.repo.ListReachableTargets(ctx)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}
	pingTargets := make([]PingTarget, 0, len(targets))
	for _, d := range targets {
		pingTargets = append(pingTargets, PingTarget{ID: d.ID, Host: d.SSHHost})
	}
	p := pinger
	if p == nil {
		p = NewPinger(u.log)
	}
	results := p.RunAll(ctx, pingTargets)
	now := time.Now().UTC()
	reachable, unreachable := 0, 0
	for i, r := range results {
		var at *time.Time
		if r.Reachable {
			reachable++
			at = &now
		} else {
			unreachable++
		}
		if err := u.repo.UpdateReachability(ctx, pingTargets[i].ID, r.Reachable, at); err != nil {
			// 单台回填失败不能阻塞整轮——只打 warn，留给下个 5min 周期补上
			if u.log != nil {
				u.log.Warn("ping reachability: update failed",
					slog.Uint64("device_id", pingTargets[i].ID),
					slog.String("host", r.Host),
					slog.Bool("reachable", r.Reachable),
					slog.Any("err", err),
				)
			}
		}
	}
	if u.log != nil {
		u.log.Info("ping reachability: round complete",
			slog.Int("scanned", len(results)),
			slog.Int("reachable", reachable),
			slog.Int("unreachable", unreachable),
		)
	}
	return len(results), nil
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

// GetSSHCredentials 以线协议形态返回 SSH 块。内部运维系统：密码 /
// 私钥直接以明文回显（不做信封加密），SPA 直接读取即可；has_password /
// has_key 派生布尔同时回填，便于前端按需作为"已配置"的可见性提示。
func (u *Usecase) GetSSHCredentials(ctx context.Context, id uint64) (*SSHCredentialsWire, error) {
	if u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	d, err := u.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	w := &SSHCredentialsWire{
		Host:        d.SSHHost,
		Port:        d.SSHPort,
		User:        d.SSHUser,
		AuthKind:    d.SSHAuthKind,
		HasPassword: d.SSHPassword != "",
		Password:    d.SSHPassword,
		HasKey:      d.SSHKey != "",
		Key:         d.SSHKey,
		HostKey:     d.SSHHostKey,
		LastSeenAt:  d.SSHLastSeenAt,
		LastError:   d.SSHLastError,
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

// SSHCredentialsWire 是 SPA 读取的 SSH 信息 DTO 形态。内部运维系统：
// 密码 / 私钥直接以明文回显（不做信封加密），供 SPA 直接渲染使用。
// 空字符串代表"该认证方式未配置"——has_password / has_key 派生布尔
// 保留下来，便于前端按需作为"已配置"的可见性提示，或在 UI 切换时
// 复用同一份数据。
type SSHCredentialsWire struct {
	Host        string     `json:"host,omitempty"`
	Port        int        `json:"port"`
	User        string     `json:"user,omitempty"`
	AuthKind    string     `json:"auth_kind"`
	HasPassword bool       `json:"has_password"`
	Password    string     `json:"password,omitempty"`
	HasKey      bool       `json:"has_key"`
	Key         string     `json:"key,omitempty"`
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
