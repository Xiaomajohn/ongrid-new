package data

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pluginhost/model"
)

// InvocationRepo 操作 plugin_invocations 表,记录每一次能力调用流水。
type InvocationRepo struct {
	db *gorm.DB
}

// NewInvocationRepo 构造仓库。
func NewInvocationRepo(db *gorm.DB) *InvocationRepo {
	return &InvocationRepo{db: db}
}

// Record 写入一行调用流水。失败仅返回错误,不抛出业务异常
// (调用流水丢一行不应阻塞业务)。
func (r *InvocationRepo) Record(ctx context.Context, inv *model.PluginInvocation) error {
	return r.db.WithContext(ctx).Create(inv).Error
}

// ListRecent 拉取某实例最近 limit 条调用,按 InvokedAt 倒序。
// instanceID = 0 表示该租户下全部实例。
func (r *InvocationRepo) ListRecent(ctx context.Context, tenantID uint64, instanceID uint64, limit int) ([]model.PluginInvocation, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if instanceID != 0 {
		q = q.Where("plugin_instance_id = ?", instanceID)
	}
	var out []model.PluginInvocation
	err := q.Order("invoked_at DESC").Limit(limit).Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

// StatsByInstance 统计某实例从 since 起的调用总次数与平均延迟。
// avgLatencyMs 当无样本时返回 0(AVG 在 SQL 层 NULL 化,这里用 COALESCE 兜底)。
func (r *InvocationRepo) StatsByInstance(ctx context.Context, tenantID uint64, instanceID uint64, since time.Time) (int64, int64, error) {
	type row struct {
		Count int64
		Avg   *float64
	}
	var got row
	err := r.db.WithContext(ctx).
		Model(&model.PluginInvocation{}).
		Select("COUNT(*) AS count, AVG(latency_ms) AS avg").
		Where("tenant_id = ? AND plugin_instance_id = ? AND invoked_at >= ?", tenantID, instanceID, since).
		Scan(&got).Error
	if err != nil {
		return 0, 0, err
	}
	var avgMs int64
	if got.Avg != nil {
		avgMs = int64(*got.Avg)
	}
	return got.Count, avgMs, nil
}

// PurgeOlderThan 清理早于 cutoff 的调用流水(硬删,不软删,流水类数据)。
// 返回实际删除的行数。
func (r *InvocationRepo) PurgeOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res := r.db.WithContext(ctx).
		Unscoped().
		Where("invoked_at < ?", cutoff).
		Delete(&model.PluginInvocation{})
	return res.RowsAffected, res.Error
}
