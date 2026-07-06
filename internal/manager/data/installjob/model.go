// Package installjob 是 manager/installjob BC 的 data 层。安装任务
// (install_jobs) 与任务事件 (install_job_events) 两张表持久化层。
//
// 此包是单 service / monorepo 中允许的唯一持有 *gorm.DB 真实写入的层：
// 上层 biz/installjob 只通过 Repo interface 接触数据，本包持有 gorm
// schema 实体与 AutoMigrate 注册入口。
package installjob

import (
	"time"

	"gorm.io/plugin/soft_delete"
)

// Status 是 install_job 行状态机的 enum 字符串。biz 层通过 type alias
// 转引，状态机语义判定（Workder.Execute）在 biz 层完成。
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimeout   Status = "timeout"
)

// EventKind 区分 install_job_events 行类型（log / state / error）。
type EventKind string

const (
	EventKindLog   EventKind = "log"
	EventKindState EventKind = "state"
	EventKindError EventKind = "error"
)

// InstallJob 是单次一键安装 edge 的尝试。Worker 通过 Repo.* 操作此行。
//
// 凭据字段（PasswordSnap / KeySnap）是 worker 生命周期内使用的明文快照，
// Repo.ClearCredentialSnap 在任务落定后会清空这两个字段。
type InstallJob struct {
	ID       uint64  `gorm:"primaryKey;autoIncrement"`
	DeviceID uint64  `gorm:"not null;column:device_id;index:idx_install_jobs_device,priority:1"`
	EdgeID   *uint64 `gorm:"column:edge_id"`

	Status Status `gorm:"size:16;not null;default:'queued';column:status;index:idx_install_jobs_status,priority:1"`

	// AuthKind 区分凭据套餐（password|key）。它是 InstallJob 行的
	// 控制列，gorm 实际持久化为 16 字节 varchar，与 biz 层 AuthKind
	// enum 联合使用。
	AuthKind string `gorm:"size:16;not null;column:auth_kind"` // password|key

	// PasswordSnap / KeySnap 在 worker 生命周期内存放，Repo 落定后清空。
	PasswordSnap string `gorm:"size:255;column:password_snap"`
	KeySnap      string `gorm:"type:mediumtext;column:key_snap"`

	Host string `gorm:"size:255;not null;column:host"`
	Port int    `gorm:"not null;column:port"`
	User string `gorm:"size:64;not null;column:user"`

	// OptionsJSON 是 install_job 的可扩展参数（预留字段）。
	//
	// 类型为 string 但语义是 JSON：列在 MySQL 是 JSON 类型，gorm
	// 写空串 "" 会触发 3140 Invalid JSON text。约束：
	//   - 必须始终是合法 JSON 字面量（"{}" / "null" / JSON 对象等）
	//   - 留 NULL 才是“无参数”的语义表达——但 Go string 零值是 "",
	//     写库前必须显式赋值；biz/cmd 层构造函数兜底赋 "{}"。
	// 未来真要接入 options 接入点时，建议改成 *string / sql.NullString
	// 配合 MySQL 的 NULL 语义；现阶段保持 string + "{}" 默认值最简。
	OptionsJSON string `gorm:"type:json;column:options_json"`
	LogOutput   string `gorm:"type:mediumtext;not null;column:log_output"`

	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	ExitCode   *int       `gorm:"column:exit_code"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	DeletedAt *time.Time `gorm:"index;column:deleted_at"`

	// 走 gorm soft_delete 插件：DeleteMarker != 0 表示软删除，毫秒级以
	// 区分亚秒级重复提交。
	DeleteMarker soft_delete.DeletedAt `gorm:"column:delete_marker;not null;default:0;softDelete:milli,DeletedAtField:DeletedAt"`
}

// TableName 锁定 gorm 表名。
func (InstallJob) TableName() string { return "install_jobs" }

// InstallJobEvent 是 install_job 的追加式事件时间线。独立成表，避免长输出
// 让元数据行膨胀。
type InstallJobEvent struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	InstallJobID uint64    `gorm:"not null;column:install_job_id;index:idx_install_job_events,priority:1"`
	Ts           time.Time `gorm:"not null;column:ts;autoCreateTime"`
	Kind         EventKind `gorm:"size:32;not null;column:kind"`
	Payload      string    `gorm:"type:text;not null;column:payload"`
}

// TableName 锁定 gorm 表名。
func (InstallJobEvent) TableName() string { return "install_job_events" }
