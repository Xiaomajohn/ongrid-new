package edge

import (
	"context"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

// ListFilter is the parameter object for Repo.List / Usecase.List.
//
// All filters are optional. Status is exact-match ("", "online", "offline").
// Name / Hostname / IP are exact-match pointers to the underlying SQL
// `=` predicate. nil pointer = no filter for that column; non-nil empty
// string would match empty column values (e.g. devices without an
// ip_address set). The HTTP layer passes non-nil only when the query
// string is present so an absent ?name=… does not zero-match rows.
//
// Hostname / IP look up against the linked Device row
// (edges.device_id → devices.id) — when the edge has no host device
// linked yet, the row is filtered out (LEFT JOIN … IS NOT NULL semantics
// at the SQL layer, see data/edge/store.Repo.List).
//
// CreatedBy, when non-nil, restricts to edges created by that user id.
// DeviceID, when non-nil, restricts to edges linked to that host device
// via the edge_devices junction (delete_marker = 0). Limit / Offset
// apply after filtering. Roles filtering moved to model/device.Device
// after the May 2026 split — query the device repo for that.
type ListFilter struct {
	Status    string
	Name      *string
	Hostname  *string
	IP        *string
	CreatedBy *uint64
	DeviceID  *uint64
	Limit     int
	Offset    int
}

// Repo is the manager/edge persistence contract. Implemented in
// internal/manager/data/edge/store. Post-pivot there is no org_id
// parameter.
type Repo interface {
	Create(ctx context.Context, e *model.Edge) error
	GetByID(ctx context.Context, id uint64) (*model.Edge, error)
	GetByAccessKey(ctx context.Context, accessKey string) (*model.Edge, error)
	GetByName(ctx context.Context, name string) (*model.Edge, error)
	List(ctx context.Context, f ListFilter) ([]*model.Edge, error)
	UpdateSecretHash(ctx context.Context, id uint64, hash string) error
	UpdateStatus(ctx context.Context, id uint64, status string, lastSeen time.Time) error
	// UpdateName overwrites the operator-friendly display name. Used
	// by HandleRegister to back-fill empty names with the host's
	// reported hostname on first tunnel handshake.
	UpdateName(ctx context.Context, id uint64, name string) error
	// SetDeviceID links an edge row to its host Device after register.
	// Source of truth for the junction is the edge_devices table; this
	// field is kept in sync as a convenience pointer.
	SetDeviceID(ctx context.Context, edgeID, deviceID uint64) error
	// SetAgentVersion records the agent's self-reported binary version
	// (semver-ish, e.g. "0.7.43"). Updated on register_edge whenever
	// the value changes — empty inputs are filtered upstream.
	SetAgentVersion(ctx context.Context, id uint64, version string) error
	// UpdateTaskName 写入 edge.task_name 任务标识（安装时由 manager 一键
	// 安装流程写入；agent 上报 register_edge 时也可能再次覆盖——以新
	// 上报为准，但空字符串视为 no-op，保留旧值）。列名 task_name 用字
	// 面量引用，不依赖 model.Edge.TaskName 字段是否先就位，便于在 PR
	// 合并顺序上对 Agent1 同步落地的 struct 字段解耦。Caller 已经在上
	// 游 TrimSpace 过滤空字符串。
	UpdateTaskName(ctx context.Context, id uint64, taskName string) error
	Delete(ctx context.Context, id uint64) error // soft delete
	Count(ctx context.Context) (int64, error)
}
