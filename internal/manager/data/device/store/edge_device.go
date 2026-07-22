package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	biz "github.com/ongridio/ongrid/internal/manager/biz/device"
	model "github.com/ongridio/ongrid/internal/manager/model/device"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// EdgeDeviceRepo is the GORM-backed biz/device.EdgeDeviceRepo.
type EdgeDeviceRepo struct {
	db *gorm.DB
}

// NewEdgeDeviceRepo constructs the junction repo around an opened *gorm.DB.
func NewEdgeDeviceRepo(db *gorm.DB) *EdgeDeviceRepo { return &EdgeDeviceRepo{db: db} }

var _ biz.EdgeDeviceRepo = (*EdgeDeviceRepo)(nil)

// Link upserts the (edge, device, type) row. Duplicate triple is
// silently a no-op (ON CONFLICT DO NOTHING) so callers can call this
// every register without first checking existence.
func (r *EdgeDeviceRepo) Link(ctx context.Context, edgeID, deviceID uint64, t model.EdgeDeviceRelationType) error {
	if edgeID == 0 || deviceID == 0 {
		return errs.ErrInvalid
	}
	row := model.EdgeDevice{EdgeID: edgeID, DeviceID: deviceID, Type: t}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "edge_id"}, {Name: "device_id"}, {Name: "type"}, {Name: "delete_marker"},
		},
		DoNothing: true,
	}).Create(&row).Error
}

// Unlink soft-deletes the (edge, device, type) row. Idempotent.
func (r *EdgeDeviceRepo) Unlink(ctx context.Context, edgeID, deviceID uint64, t model.EdgeDeviceRelationType) error {
	res := r.db.WithContext(ctx).
		Where("edge_id = ? AND device_id = ? AND type = ?", edgeID, deviceID, t).
		Delete(&model.EdgeDevice{})
	if res.Error != nil {
		return res.Error
	}
	return nil
}

// LookupHostDevice resolves edge_id → host device_id via the type=Host
// junction row.
func (r *EdgeDeviceRepo) LookupHostDevice(ctx context.Context, edgeID uint64) (uint64, error) {
	var row model.EdgeDevice
	if err := r.db.WithContext(ctx).
		Where("edge_id = ? AND type = ?", edgeID, model.EdgeDeviceRelationHost).
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errs.ErrNotFound
		}
		return 0, err
	}
	return row.DeviceID, nil
}

// LookupEdgeForDevice resolves device_id → owning edge_id (for the given
// relation type). When more than one edge has a junction to the same
// device under the same type (a multi-agent host), the most recently
// created junction wins.
//
// 防御性 JOIN edges：junction 行没软删 (delete_marker=0)，但被指向
// 的 edge 行可能被软删 (edges.delete_marker=1)。如果不加 JOIN，
// 历史数据会返回"指向已删除 edge"的悬挂 id，导致 devicessh.Router
// 内部 edges.Get 报 "record not found"（用户表现：WebSSH 打不开）。
// GORM 的 soft_delete 插件已为 edges 自动加 delete_marker=0 过滤，
// 这里再显式 Where 一下保证两条路径行为一致 —— 走原生 GORM 查询时
// 默认走 *model.Edge 的 soft_delete 钩子；LEFT JOIN 的兜底是兜住
// 未来误关掉插件或绕开软删插件写入的场景。返回 ErrNotFound 让 router
// fall through 到 direct SSH 分支（devicessh.Router.Pick 的现有逻辑）。
func (r *EdgeDeviceRepo) LookupEdgeForDevice(ctx context.Context, deviceID uint64, t model.EdgeDeviceRelationType) (uint64, error) {
	var row struct {
		EdgeID uint64
	}
	err := r.db.WithContext(ctx).
		Table("edge_devices AS ed").
		Select("ed.edge_id AS edge_id").
		Joins("JOIN edges e ON e.id = ed.edge_id AND e.delete_marker = 0").
		Where("ed.device_id = ? AND ed.type = ? AND ed.delete_marker = 0", deviceID, t).
		Order("ed.id DESC").
		Limit(1).
		Scan(&row).Error
	if err != nil {
		return 0, err
	}
	if row.EdgeID == 0 {
		return 0, errs.ErrNotFound
	}
	return row.EdgeID, nil
}

// ListDevicesForEdge returns every junction row for this edge.
func (r *EdgeDeviceRepo) ListDevicesForEdge(ctx context.Context, edgeID uint64) ([]*model.EdgeDevice, error) {
	var rows []*model.EdgeDevice
	if err := r.db.WithContext(ctx).
		Where("edge_id = ?", edgeID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListEdgesForDevice returns every junction row for this device.
func (r *EdgeDeviceRepo) ListEdgesForDevice(ctx context.Context, deviceID uint64) ([]*model.EdgeDevice, error) {
	var rows []*model.EdgeDevice
	if err := r.db.WithContext(ctx).
		Where("device_id = ?", deviceID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListEdgesForDevices 一次性拉取多台 device 关联的 edge 精简行。一次
// IN 查询带 JOIN，避免 N+1。仅暴露不含敏感字段的子集（id / name /
// status / task_name / last_seen_at），不返 access_key_id 与
// secret_key_hash —— 列表接口的授权面比单查 GET /v1/edges 更广，不能
// 借机泄漏 agent 凭据。
//
// includeDeleted=true 时不过滤已软删除的 edge 行（e.delete_marker != 0），
// 供 Logs 页面"显示已删除"开关联动查询已删除的 edge 任务。此时 junction
// 行也不按 ed.delete_marker=0 过滤——edge 被删除时 biz/edge 会连带软删
// junction（softDeleteJunctions），若仍过滤 ed.delete_marker=0，已删除的
// edge 任务就永远查不到（这是"日志页查不到已删除监控任务"的根因）。
// 结果按 (device_id, edge_id) 去重，避免删除后重新注册产生的多条
// junction 导致同一 edge 重复出现。
//
// 返回值：device_id → 该 device 关联的 edge 列表（保持 edges.id 升序）。
// 未关联 edge 的 device 不在 map 里；deviceIDs 为空时返回空 map。
func (r *EdgeDeviceRepo) ListEdgesForDevices(ctx context.Context, deviceIDs []uint64, includeDeleted bool) (map[uint64][]biz.EdgeMini, error) {
	out := make(map[uint64][]biz.EdgeMini, len(deviceIDs))
	if len(deviceIDs) == 0 {
		return out, nil
	}
	// includeDeleted 控制是否过滤已软删除的 edge。无论 includeDeleted 为何值，
	// 都排除“确认删除”（purge_marker != 0）的 edge：这些行日志页面查询不到。
	var q string
	if includeDeleted {
		// 不过滤 junction 的 delete_marker（见上方注释），只排除 purge 的 edge。
		q = `SELECT ed.device_id AS device_id,
	                  e.id         AS id,
	                  e.name       AS name,
	                  e.status     AS status,
	                  e.task_name  AS task_name,
	                  e.last_seen_at AS last_seen_at
	           FROM edges e
	           JOIN edge_devices ed
	             ON ed.edge_id = e.id
	           WHERE e.purge_marker = 0
	             AND ed.device_id IN (?)`
	} else {
		q = `SELECT ed.device_id AS device_id,
	                  e.id         AS id,
	                  e.name       AS name,
	                  e.status     AS status,
	                  e.task_name  AS task_name,
	                  e.last_seen_at AS last_seen_at
	           FROM edges e
	           JOIN edge_devices ed
	             ON ed.edge_id = e.id AND ed.delete_marker = 0
	           WHERE e.delete_marker = 0
	             AND e.purge_marker = 0
	             AND ed.device_id IN (?)`
	}
	type row struct {
		DeviceID   uint64
		ID         uint64
		Name       string
		Status     string
		TaskName   string
		LastSeenAt *time.Time
	}
	var rows []row
	if err := r.db.WithContext(ctx).Raw(q, deviceIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	// 按 (device_id, edge_id) 去重：includeDeleted 分支不过滤 junction 的
	// delete_marker，同一 edge 可能因“删除后重新注册”同时存在软删与存活
	// 两条 junction；非 includeDeleted 分支也可能因 host/discovered 双 type
	// 关联出现重复。保留第一条即可。
	type dedupeKey struct{ deviceID, edgeID uint64 }
	seen := make(map[dedupeKey]struct{}, len(rows))
	for _, x := range rows {
		k := dedupeKey{x.DeviceID, x.ID}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out[x.DeviceID] = append(out[x.DeviceID], biz.EdgeMini{
			ID:         x.ID,
			Name:       x.Name,
			Status:     x.Status,
			TaskName:   x.TaskName,
			LastSeenAt: x.LastSeenAt,
		})
	}
	return out, nil
}
