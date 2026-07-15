package data

import (
	"context"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pluginhost/model"
)

// AuditRepo 操作 plugin_audits 表,记录插件生命周期/安全相关事件。
type AuditRepo struct {
	db *gorm.DB
}

// NewAuditRepo 构造仓库。
func NewAuditRepo(db *gorm.DB) *AuditRepo {
	return &AuditRepo{db: db}
}

// Record 写入一行审计事件。
func (r *AuditRepo) Record(ctx context.Context, a *model.PluginAudit) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// ListByInstance 拉取某实例最近 limit 条审计,按 OccurredAt 倒序。
func (r *AuditRepo) ListByInstance(ctx context.Context, instanceID uint64, limit int) ([]model.PluginAudit, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []model.PluginAudit
	err := r.db.WithContext(ctx).
		Where("plugin_instance_id = ?", instanceID).
		Order("occurred_at DESC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListByTenant 按租户 + 可选 action 过滤。
// action 为空字符串时不加 action 条件,等同于拉取该租户全部审计。
func (r *AuditRepo) ListByTenant(ctx context.Context, tenantID uint64, action string, limit int) ([]model.PluginAudit, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if action != "" {
		q = q.Where("action = ?", action)
	}
	var out []model.PluginAudit
	err := q.Order("occurred_at DESC").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}
