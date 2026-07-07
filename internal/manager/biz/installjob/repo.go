package installjob

import (
	"context"

	"gorm.io/gorm"

	managerinstalldata "github.com/ongridio/ongrid/internal/manager/data/installjob"
)

// Repo 是 install_jobs / install_job_events 的持久化契约。
//
// 接口在消费方（biz 层）定义；gorm 实现落在 sibling 包
// internal/manager/data/installjob，biz 仅持 Repo interface 与一行
// 委托 NewRepo，不直接编写 SQL / 不持有 *gorm.DB 实体。
//
// 旧版由本包持有 gormRepo 实现导致三连违规：
//   1. biz 直接持有 gorm 模型（InstallJob / InstallJobEvent 定义在
//      biz/installjob/model.go），违反"单服务 cmd → ... → repo →
//      model"分层；
//   2. data 层位置空缺，installjob.Migrate 函数名从未被 cmd 注册，
//      install_jobs / install_job_events 两张表没建出来；
//   3. "全链路跑通"审视直接断在 DataLayer 这一格。
//
// 本版把数据 schema 与 gorm 实现全部搬到 data/installjob；biz 端用
// type alias（model.go）对外继续按 InstallJob / InstallJobEvent 操作，
// 业务侧代码（worker / installer / runner / server adapter）不需要
// 任何改动。
type Repo interface {
	// Create 写入新行（Status=queued），返回自增 ID 填充后的对象。
	Create(ctx context.Context, job *InstallJob) (*InstallJob, error)

	// Get 按主键取行；ErrNotFound 由 errs 翻译。
	Get(ctx context.Context, id uint64) (*InstallJob, error)

	// ListByDevice 按 device 拉最新一批任务，limit<=0 不加 LIMIT。
	ListByDevice(ctx context.Context, deviceID uint64, limit int) ([]*InstallJob, error)

	// ListByEdge 按 edge 维度拉最新一批安装任务。一台 device 可装
	// 0~N 个 edge，监控设备页面需要按 edge 而不是 device 查日志。
	ListByEdge(ctx context.Context, edgeID uint64, limit int) ([]*InstallJob, error)

	// UpdateStatus 一次 UPDATE 完成 status + 时间戳同步翻转。
	UpdateStatus(ctx context.Context, id uint64, status Status, exitCode *int) error

	// AppendLog 用 SQL CONCAT 拼接 log_output，无读改写竞态。
	AppendLog(ctx context.Context, id uint64, chunk string) error

	// ClearCredentialSnap 任务落定后清空凭据字段。
	ClearCredentialSnap(ctx context.Context, id uint64) error

	// AddEvent 追加一行 install_job_events。
	AddEvent(ctx context.Context, jobID uint64, kind EventKind, payload string) error
}

// NewRepo 委托 data/installjob.NewRepo：cmd wiring 调用方仍然只知
// biz.Repo 入口；gorm 实现细节、数据 schema、AutoMigrate 注册都封装
// 在 sibling data 包。
//
// 返回 Repo interface 类型而非 *gormRepo：保证 biz 调用方代码不依赖
// gorm 具象。数据层通过同类契约保证 gormRepo 满足本接口（在 data 包
// 编译期断言）。
func NewRepo(db *gorm.DB) Repo {
	return managerinstalldata.NewRepo(db)
}
