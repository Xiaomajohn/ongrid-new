package data

import (
	"context"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pluginhost/model"
)

// CapabilityRepo 操作 plugin_capabilities 表。
type CapabilityRepo struct {
	db *gorm.DB
}

// NewCapabilityRepo 构造仓库。
func NewCapabilityRepo(db *gorm.DB) *CapabilityRepo {
	return &CapabilityRepo{db: db}
}

// CreateBatch 一次性插入一批能力,常用于安装完成时一次性落库。
// 任一行失败整体回滚(GORM CreateInBatches 自动起事务)。
func (r *CapabilityRepo) CreateBatch(ctx context.Context, caps []model.PluginCapability) error {
	if len(caps) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Create(&caps).Error
}

// ListByInstance 列出某个插件实例的全部能力。
func (r *CapabilityRepo) ListByInstance(ctx context.Context, instanceID uint64) ([]model.PluginCapability, error) {
	var out []model.PluginCapability
	err := r.db.WithContext(ctx).
		Where("plugin_instance_id = ?", instanceID).
		Order("kind ASC, name ASC").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListByKind 按 kind 过滤(用于 ai.tool / notifier 等按类型索引)。
func (r *CapabilityRepo) ListByKind(ctx context.Context, tenantID uint64, kind string) ([]model.PluginCapability, error) {
	var out []model.PluginCapability
	err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND kind = ?", tenantID, kind).
		Order("id ASC").
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetEnabled 单独更新某能力的启停。
func (r *CapabilityRepo) SetEnabled(ctx context.Context, id uint64, enabled bool) error {
	return r.db.WithContext(ctx).
		Model(&model.PluginCapability{}).
		Where("id = ?", id).
		Update("enabled", enabled).Error
}

// DeleteByInstance 软删除某个实例的全部能力(卸载时调用)。
func (r *CapabilityRepo) DeleteByInstance(ctx context.Context, instanceID uint64) error {
	return r.db.WithContext(ctx).
		Where("plugin_instance_id = ?", instanceID).
		Delete(&model.PluginCapability{}).Error
}
