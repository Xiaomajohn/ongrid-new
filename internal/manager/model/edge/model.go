// Package edge holds persistence entities for the manager/edge sub-domain.
//
// Post-split (May 2026): host facts (hostname, OS, hardware, roles, live
// usage) live on model/device.Device, linked here through the M:N
// `edge_devices` junction (model/device.EdgeDevice). The Edge row owns
// the agent identity (access key, online/offline, last_seen_at, name).
// Code that needs to display the host alongside an edge should resolve
// the host Device through edge_devices(type=host).
//
// The legacy Edge.DeviceID 1:1 link column is retained at the Go layer
// (read-only convenience pointer to the host device) so existing callers
// keep compiling, but the source of truth for "which device(s) does this
// edge own" is the junction table.
package edge

import (
	"time"

	"gorm.io/plugin/soft_delete"
)

// Edge is a registered edge agent (a tunnel-side identity). Post-pivot
// there is no org_id; CreatedBy records which user registered
// it (nullable FK to users.id, audit only). DeletedAt is a gorm
// soft-delete marker; gorm automatically filters rows with DeletedAt IS
// NOT NULL out of default queries.
//
// Post-split (May 2026): device-y fields (Hostname/OS/HW/Roles/usage)
// have moved to model/device.Device. Edge keeps tunnel-y fields only.
type Edge struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"`
	// Name is the operator-friendly display name. Optional at create
	// time — when left empty, edge.HandleRegister auto-fills it with
	// the host's reported hostname on first tunnel handshake.
	Name          string `gorm:"size:128;not null;default:''"`
	AccessKeyID   string `gorm:"size:32;not null;column:access_key_id;uniqueIndex:idx_edges_access_key_id,priority:1"`
	SecretKeyHash string `gorm:"size:512;not null;column:secret_key_hash"`                     // argon2id
	Status        string `gorm:"size:16;default:offline;check:status IN ('online','offline')"` // online | offline
	Description   string `gorm:"size:255;not null;default:''"`

	LastSeenAt *time.Time `gorm:"column:last_seen_at"`
	// AgentVersion is the binary semver the edge agent self-reported on
	// its most recent register_edge handshake (e.g. "0.7.43"). Empty
	// string for edges registered before the field was introduced or
	// agents that decline to report. Used by the SPA's Edges page so
	// operators can audit version drift across the fleet at a glance.
	AgentVersion string `gorm:"size:32;not null;default:'';column:agent_version"`
	// TaskName is the human-readable task identifier attached by the
	// installjob worker (e.g. "rack-A-2026Q3-patch"). Lets operators
	// group edges by the install campaign in the Edges page. Empty
	// string for edges created without an installjob (manual create,
	// legacy rows). The Edges page filters exact-match on this column.
	TaskName     string  `gorm:"size:128;column:task_name"`
	DeviceID     *uint64 `gorm:"index;column:device_id"`
	CreatedBy    *uint64 `gorm:"column:created_by"` // audit only
	// PurgeMarker 是“确认删除”标记（二次删除）。操作员在历史数据页点
	// “确认删除”后写入非零值（UnixMilli）。行不做物理删除，但：
	//   - include_deleted=true 的列表查询会排除 purge_marker != 0 的行
	//   - 日志页面因此查询不到该任务
	//   - 历史数据页面不再展示该任务
	// 0 = 未确认删除（普通软删除状态，日志勾选“已删除”仍可查）。
	PurgeMarker  int64   `gorm:"column:purge_marker;not null;default:0"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time            `gorm:"index;column:deleted_at"` // soft delete audit time
	DeleteMarker soft_delete.DeletedAt `gorm:"column:delete_marker;not null;default:0;softDelete:milli,DeletedAtField:DeletedAt;uniqueIndex:idx_edges_access_key_id,priority:2"`
}

// TableName pins the SQLite table.
func (Edge) TableName() string { return "edges" }

// Status constants. MVP uses the binary online/offline; "disabled" is not
// used post-pivot — deletion is soft-delete via DeletedAt.
const (
	StatusOnline  = "online"
	StatusOffline = "offline"
)
