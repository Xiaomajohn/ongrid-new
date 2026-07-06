// gorm 安装任务持久化实现。Biz 层 Repo contract 在 managerbizinstalljob
// 中定义；data 层实现其全部方法。
//
// gormRepo 保持包内私有；导出别名 GormRepo 用于跨包编译期断言（cmd
// 层 `var _ bizinstalljob.Repo = (*managerinstalldata.GormRepo)(nil)`），
// 防止 Repo contract 演进时漏方法无人发觉。
package installjob

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// gormRepo 是 biz/installjob.Repo contract 的 gorm 实现。
type gormRepo struct {
	db *gorm.DB
}

// GormRepo 导出别名，便于 cmd 层做编译期接口断言。
type GormRepo = gormRepo

// NewRepo 暴露 gorm 实现给 cmd wiring 层；返回 *GormRepo 让调用方可以
// 同时满足 biz Repositioning 与跨包 assertion。
func NewRepo(db *gorm.DB) *GormRepo { return &gormRepo{db: db} }

// Create 写入新行（Status=queued 由调用方字段赋值携带），依赖 gorm
// Create 钩子自动回填 ID。
func (r *gormRepo) Create(ctx context.Context, job *InstallJob) (*InstallJob, error) {
	if err := r.db.WithContext(ctx).Create(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

// Get 按主键取行；ErrRecordNotFound 翻译为 errs.ErrNotFound 让跨 BC
// 调用方共用一个 sentinel。
func (r *gormRepo) Get(ctx context.Context, id uint64) (*InstallJob, error) {
	var j InstallJob
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&j).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &j, nil
}

// ListByDevice 拉取某 device 最新一批任务，limit<=0 不加 LIMIT。gorm
// soft_delete 插件自动过滤 DeleteMarker != 0。
func (r *gormRepo) ListByDevice(ctx context.Context, deviceID uint64, limit int) ([]*InstallJob, error) {
	var jobs []*InstallJob
	q := r.db.WithContext(ctx).Where("device_id = ?", deviceID).Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// UpdateStatus 单 UPDATE 完成 status 翻转与时间戳标记的同步落库，
// 保证行不会处于 status/started_at/finished_at 不一致的中间态。
func (r *gormRepo) UpdateStatus(ctx context.Context, id uint64, status Status, exitCode *int) error {
	now := time.Now().UTC()
	update := map[string]any{
		"status":     status,
		"updated_at": now,
	}
	if status == StatusRunning {
		update["started_at"] = now
	}
	if status == StatusSuccess || status == StatusFailed || status == StatusCancelled || status == StatusTimeout {
		update["finished_at"] = now
	}
	if exitCode != nil {
		update["exit_code"] = *exitCode
	}
	return r.db.WithContext(ctx).Model(&InstallJob{}).Where("id = ?", id).Updates(update).Error
}

// AppendLog 用 SQL CONCAT 拼接 log_output，避免读改写的并发竞态。
func (r *gormRepo) AppendLog(ctx context.Context, id uint64, chunk string) error {
	return r.db.WithContext(ctx).Model(&InstallJob{}).Where("id = ?", id).
		UpdateColumn("log_output", gorm.Expr("CONCAT(log_output, ?)", chunk)).Error
}

// ClearCredentialSnap 一次性抹除凭据快照，任务落定后调用。
func (r *gormRepo) ClearCredentialSnap(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Model(&InstallJob{}).Where("id = ?", id).Updates(map[string]any{
		"password_snap": "",
		"key_snap":      "",
	}).Error
}

// AddEvent 追加一行 install_job_events；Ts 由 autoCreateTime 设置。
func (r *gormRepo) AddEvent(ctx context.Context, jobID uint64, kind EventKind, payload string) error {
	return r.db.WithContext(ctx).Create(&InstallJobEvent{
		InstallJobID: jobID,
		Kind:         kind,
		Payload:      payload,
	}).Error
}
