package data

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pluginhost/model"
)

// 仓库层错误,供 biz 层映射为业务错误码。
var (
	// ErrCreate 表示 Insert 失败。
	ErrCreate = errors.New("data: create failed")
	// ErrNotFound 表示按条件查询不到记录。
	ErrNotFound = errors.New("data: not found")
)

// PluginRepo 操作 plugin_instances 表。
type PluginRepo struct {
	db *gorm.DB
}

// NewPluginRepo 构造仓库。
func NewPluginRepo(db *gorm.DB) *PluginRepo {
	return &PluginRepo{db: db}
}

// Create 插入一行新记录。失败返回 ErrCreate。
func (r *PluginRepo) Create(ctx context.Context, p *model.PluginInstance) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return errors.Join(ErrCreate, err)
	}
	return nil
}

// Get 按主键查询。
func (r *PluginRepo) Get(ctx context.Context, id uint64) (*model.PluginInstance, error) {
	var p model.PluginInstance
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetByPackID 按 (tenant_id, pack_id) 复合索引唯一定位。
func (r *PluginRepo) GetByPackID(ctx context.Context, tenantID uint64, packID string) (*model.PluginInstance, error) {
	var p model.PluginInstance
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND pack_id = ?", tenantID, packID).
		First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// List 按租户分页列出实例,按 id 倒序保证新安装的在前面。
func (r *PluginRepo) List(ctx context.Context, tenantID uint64, limit, offset int) ([]model.PluginInstance, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var out []model.PluginInstance
	err := r.db.WithContext(ctx).
		Where("tenant_id = ?", tenantID).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Update 保存整行(Save 会写所有字段)。
func (r *PluginRepo) Update(ctx context.Context, p *model.PluginInstance) error {
	return r.db.WithContext(ctx).Save(p).Error
}

// Delete 软删除一行(依赖 gorm.DeletedAt)。
func (r *PluginRepo) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&model.PluginInstance{}, id).Error
}

// SetEnabled 仅更新启用位,避免 Save 覆盖其它字段。
func (r *PluginRepo) SetEnabled(ctx context.Context, id uint64, enabled bool) error {
	return r.db.WithContext(ctx).
		Model(&model.PluginInstance{}).
		Where("id = ?", id).
		Update("enabled", enabled).Error
}

// SetHealth 更新健康状态与最近一次健康探测时间。
func (r *PluginRepo) SetHealth(ctx context.Context, id uint64, status string) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&model.PluginInstance{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"health_status":  status,
			"last_health_at": now,
		}).Error
}
