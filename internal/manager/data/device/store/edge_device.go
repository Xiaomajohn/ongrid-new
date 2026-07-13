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
func (r *EdgeDeviceRepo) LookupEdgeForDevice(ctx context.Context, deviceID uint64, t model.EdgeDeviceRelationType) (uint64, error) {
	var row model.EdgeDevice
	if err := r.db.WithContext(ctx).
		Where("device_id = ? AND type = ?", deviceID, t).
		Order("id DESC").
		First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errs.ErrNotFound
		}
		return 0, err
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
// 借机泄漏 agent 凭据。Soft-delete 双方都按 delete_marker=0 过滤。
//
// 返回值：device_id → 该 device 关联的 edge 列表（保持 edges.id 升序）。
// 未关联 edge 的 device 不在 map 里；deviceIDs 为空时返回空 map。
func (r *EdgeDeviceRepo) ListEdgesForDevices(ctx context.Context, deviceIDs []uint64) (map[uint64][]biz.EdgeMini, error) {
	out := make(map[uint64][]biz.EdgeMini, len(deviceIDs))
	if len(deviceIDs) == 0 {
		return out, nil
	}
	// 注意：GORM 0.x 的 Raw + IN(?) 会按 driver 展开 slice，参数必须是
	// []uint64；硬编码列名是为了让 MySQL 与 SQLite 走同一条 SQL（与
	// reconcileOfflineOrphansSQL 同策略）。delete_marker=0 是软删除"行
	// 还活着"的 sentinel（参见 model.Device.DeleteMarker 注释）。
	const q = `SELECT ed.device_id AS device_id,
	                  e.id         AS id,
	                  e.name       AS name,
	                  e.status     AS status,
	                  e.task_name  AS task_name,
	                  e.last_seen_at AS last_seen_at
	           FROM edges e
	           JOIN edge_devices ed
	             ON ed.edge_id = e.id AND ed.delete_marker = 0
	           WHERE e.delete_marker = 0
	             AND ed.device_id IN (?)`
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
	for _, x := range rows {
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
