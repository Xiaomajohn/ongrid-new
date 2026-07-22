// Package device is the manager/device biz tier. It owns persistence of
// Device rows and the edge_devices M:N junction, plus the upsert
// primitive used by the edge agent register flow.
package device

import (
	"context"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/device"
)

// ListFilter narrows Device.List results.
//
// RolesAny is a bit mask: list rows whose roles bit-set overlaps with
// this mask. Stays sargable in the SQL impl by translating to a finite
// IN-list of stored values (see model.MatchingRoleValues).
// RolesUnknownOnly, when true, narrows to rows with roles == 0 (the
// "未分类" bucket); it is mutually exclusive with RolesAny — set one or
// the other, not both. Online filters by the live online flag.
// Hostname / Name / IP are substring matches. IncludeDeleted opts out of the
// GORM soft-delete scope so callers (Logs page's "显示已删除" toggle)
// can surface soft-deleted rows in a "已删除" UI list. Default false
// keeps the default-scope behaviour (no soft-deleted rows visible).
type ListFilter struct {
	RolesAny         uint8
	RolesUnknownOnly bool
	Online           *bool
	Hostname         string
	Name             string
	IP               string
	Limit            int
	Offset           int
	IncludeDeleted   bool
}

// EdgeMini is the trimmed edge summary attached to each device in
// GET /v1/devices response. Excludes tunnel-y secrets (access_key_id /
// secret_key_hash) so the device list doesn't leak agent auth material
// to a broader audience than GET /v1/edges does. The data powers the
// SPA's Logs page device / task dropdowns — task_name here is the
// source of truth for the task facet.
type EdgeMini struct {
	ID         uint64
	Name       string
	Status     string
	TaskName   string
	LastSeenAt *time.Time
}

// Repo is the device persistence contract. The sqlite/mysql implementation
// lives under internal/manager/data/device.
type Repo interface {
	// Create inserts a fresh Device row. Caller is responsible for
	// setting every required column (Fingerprint, Name, Hostname, OS,
	// Arch, CPU/Mem sizes — see the model comments). Returns the row
	// with ID populated. Duplicate fingerprint → ErrConflict.
	Create(ctx context.Context, d *model.Device) (*model.Device, error)

	// FindOrCreateByFingerprint returns the existing Device for the
	// (Fingerprint) key or creates a fresh row carrying the provided
	// fields. UserID, Hostname/OS/etc. on the seed are only written on
	// initial create — subsequent calls do NOT overwrite host facts;
	// use UpdateHostFacts for that.
	FindOrCreateByFingerprint(ctx context.Context, seed *model.Device) (*model.Device, error)

	// RebindFingerprint moves a device from oldFP to newFP in place when
	// newFP isn't already taken — migrates a device to a new fingerprint
	// algorithm without orphaning it (device.ID and history preserved).
	// No-op when oldFP == newFP, either side is empty, or newFP already
	// exists (the new device already won; nothing to migrate).
	RebindFingerprint(ctx context.Context, oldFP, newFP string) error

	// UpdateHostFacts overwrites Hostname / OS / Arch / KernelVersion /
	// CPUCount / MemTotalBytes / DiskTotalBytes / OSVersion for the
	// device. Called after a fresh register payload arrives so we always
	// keep the latest facts.
	UpdateHostFacts(ctx context.Context, id uint64, facts HostFacts) error

	// UpdateUsage refreshes the live usage gauges (CPU/Mem/Disk %).
	// Called from the metric ingest path so the device list shows live
	// load without a JOIN onto host_metrics every render.
	UpdateUsage(ctx context.Context, id uint64, u Usage) error

	// UpdateRoles sets the operator-assigned device-roles bit mask.
	// Caller is expected to have already masked the value down to the
	// known-bits envelope (model.RolesAllKnownBits); the DB CHECK
	// constraint is the second line of defense.
	UpdateRoles(ctx context.Context, id uint64, roles uint8) error

	// UpdateNameDescription 更新 operator 可编辑的展示字段（name /
	// description / hostname）。三者在 PATCH /v1/devices/{id} 里都允许
	// 局部更新；调用方传空字符串表示清空 description，name / hostname
	// 不允许为空（已由 usecase 层校验）。
	UpdateNameDescription(ctx context.Context, id uint64, name, description, hostname string) error

	// SetSSHCredentials writes the SSH credentials block for a device.
	// Implementing both password and key in one call lets the UI
	// "set both" without a follow-up request. Empty user clears the
	// block; an empty auth kind rejected here is the caller's bug.
	SetSSHCredentials(ctx context.Context, id uint64, creds SSHCredentials) error

	// ClearSSHCredentialsField wipes one of the SSH secret fields so the
	// operator can rotate just the password (or just the key) without
	// touching the other. Kind must be "password" or "key".
	ClearSSHCredentialsField(ctx context.Context, id uint64, kind string) error

	// SetSSHCredentialsIAW — installed-agent-written fields (last-seen,
	// last-error, host-key). The agent emits these on every SSH probe;
	// the web layer never reads them, the install worker + audit page do.
	SetSSHCredentialsIAW(ctx context.Context, id uint64, iaw SSHCredentialsIAW) error

	// SetNodeID writes Device.NodeID — the link to the topology
	// `nodes` table. Called from the edge register flow (via NodeMirror)
	// after a fresh device is created or from the topology migration
	// backfill. Idempotent: writing the same node_id twice is a no-op.
	SetNodeID(ctx context.Context, id, nodeID uint64) error

	// MarkOnline / MarkOffline set the device-level online flag and
	// timestamp. Called from the edge online/offline callbacks.
	MarkOnline(ctx context.Context, id uint64) error
	MarkOffline(ctx context.Context, id uint64) error

	// UpdateReachability 写入 ping 服务的可达性结果。可由 usecase.PingReachable
	// （定时器）在每轮扫描后调用。到达时间 at=nil 时清空 last_reachable_at。
	UpdateReachability(ctx context.Context, id uint64, reachable bool, at *time.Time) error

	// ListReachableTargets 列出"需要被 ping 探活"的设备：ssh_host 非空。
	// 软删除行会被自动过滤。返回的 device 包含 Ping 需要的最小列（id / ssh_host）。
	ListReachableTargets(ctx context.Context) ([]*model.Device, error)

	// Get returns the row by id; ErrNotFound otherwise.
	Get(ctx context.Context, id uint64) (*model.Device, error)
	// GetMany batch-loads devices by id. Missing ids are simply absent
	// from the returned map; callers must handle the no-row case.
	GetMany(ctx context.Context, ids []uint64) (map[uint64]*model.Device, error)
	// List returns devices matching f. Sorted by id DESC (newest first).
	// Soft-deleted rows excluded.
	List(ctx context.Context, f ListFilter) ([]*model.Device, error)
	// Count returns the total non-soft-deleted device count.
	Count(ctx context.Context) (int64, error)
	// Delete soft-deletes a device (does NOT touch its junction rows;
	// callers should remove the junction first if they want a clean cut).
	Delete(ctx context.Context, id uint64) error

	// HardDelete physically removes the row from the table. This is the
	// un-recoverable path; prefer Delete (soft) unless the operator
	// explicitly wants the row gone (e.g. audit remediation, GDPR-style
	// "really scrub this" flows).
	HardDelete(ctx context.Context, id uint64) error

	// Restore un-soft-deletes a previously soft-deleted device row.
	// Idempotent on already-live rows; missing id → ErrNotFound.
	Restore(ctx context.Context, id uint64) error

	// ConfirmDelete 确认删除（二次删除）：将 purge_marker 置为非零值。
	// 行不做物理删除，但 include_deleted=true 的列表查询会排除
	// purge_marker != 0 的行，日志页面因此查询不到该设备及其任务。
	// 同时将该设备关联的所有 edge 也标记为确认删除。
	ConfirmDelete(ctx context.Context, id uint64) error

	// ReconcileOfflineOrphans flips online=true devices back to offline
	// when none of their linked (non-deleted) edges is online. Heals
	// orphan "ghost" devices — a device whose edge was deleted, or whose
	// host re-registered under a new fingerprint, used to stay online
	// forever because only a tunnel-close (HandleOffline) flipped it.
	// Returns the number of rows flipped. Run periodically by the
	// presence reconciler.
	ReconcileOfflineOrphans(ctx context.Context) (int64, error)

	// TouchSSHSuccess 标记 SSH 最近成功时间并清空 last_error。
	// 由 SSH dialer 在一次成功 connect/auth 之后调用。
	TouchSSHSuccess(ctx context.Context, id uint64) error

	// TouchSSHError 记录 SSH 最近失败时间 + 错误信息（截断 512 字符）。
	// errMsg 在 store 层会被截断到 500 字符以适配 VARCHAR(512) 列。
	TouchSSHError(ctx context.Context, id uint64, errMsg string) error
}

// SSHCredentials is the operator-supplied block. Plaintext by design —
// see the comment on devicemodel.Device.SSHHost. SSHPassword / SSHKey
// may be empty when the operator only wants to set one; the usecase
// rejects empty user + empty kind.
type SSHCredentials struct {
	Host     string // optional default host (agent will fall back to "127.0.0.1" on the local edge)
	Port     int    // 0 → default 22
	User     string // required
	AuthKind string // "password" | "key"
	Password string // set when AuthKind=="password"
	Key      string // set when AuthKind=="key"
	HostKey  string // optional expected host-key fingerprint
}

// SSHCredentialsIAW is the agent-written tail of the SSH block (kept
// here so the repo contract doesn't grow per-field setters).
type SSHCredentialsIAW struct {
	LastSeenAt *time.Time
	LastError  string
}

// HostFacts is the subset of Device columns updated on register.
type HostFacts struct {
	Hostname       string
	OS             string
	OSVersion      string
	Arch           string
	KernelVersion  string
	CPUCount       int
	MemTotalBytes  uint64
	DiskTotalBytes uint64
	IPAddress      string
}

// Usage is the live percentage gauges for one device.
type Usage struct {
	CPUPct  float32
	MemPct  float32
	DiskPct float32
}