// Package installjob 提供一键安装 edge 的异步任务队列。
//
// 这是 manager/installjob BC 的 biz 层。一个 InstallJob 代表一次 "在指定
// 设备上 ssh 一把 → 写 install.sh → 引导边缘 agent 上线" 的端到端尝试；
// 任务经 Runner 排队后由 Worker.Execute 串行处理，结果（含日志）持久化
// 在 install_jobs / install_job_events 两张表里（schema 与 Repo 实现在
// sibling 包 internal/manager/data/installjob，biz 层只通过本包的
// type alias 与 Repo interface 接触数据，保持数据层唯一持有 gorm 的
// 分层纪律）。
//
// Phase 1（本文件）只落 biz 内核：
//
//   - 状态机 / 凭据分类常量（Status enum + AuthKind）
//   - Worker.Execute：单 job 的状态机 + 凭据快照清理 + 事件时间线
//   - Runner：goroutine 池，bounded queue + 非阻塞 enqueue
//
// 留给后续 wire（cmd/ongrid main.go 内执行）：
//
//   - 接 Repo（gorm.DB）→ 由 data/installjob.NewRepo 实现 Repo contract
//   - 接 EdgeIssuer / DeviceSSHSoftDelete / Installer 三个外部接口
//   - 在 main.go 的 wiring 阶段替换 Worker.waitEdgeOnline 占位实现
//
// HTTP handler / cron 入口同样落在后续 PR；本 BC 不导出 usecase 入口，
// 由调用方组合 Repo + Runner.Enqueue 即可。
package installjob

import (
	managerinstalldata "github.com/ongridio/ongrid/internal/manager/data/installjob"
)

// 数据 schema 别名（type alias）：biz 内部使用 InstallJob 字面量即可
// 触达 data 包真实 gorm 实体；所有字段、gorm tag、TableName 都从
// data 解析，biz 任何位置写 managerbizinstalljob.InstallJob 与写
// managerinstalldata.InstallJob 完全等价。
type (
	InstallJob      = managerinstalldata.InstallJob
	InstallJobEvent = managerinstalldata.InstallJobEvent
	Status          = managerinstalldata.Status
	EventKind       = managerinstalldata.EventKind
)

// Status enum 重导出。type alias 让以下常量与 data 同 identity；
// gorm Read/Write 都视作同一个 Status 字面量。
const (
	StatusQueued    = managerinstalldata.StatusQueued
	StatusRunning   = managerinstalldata.StatusRunning
	StatusSuccess   = managerinstalldata.StatusSuccess
	StatusFailed    = managerinstalldata.StatusFailed
	StatusCancelled = managerinstalldata.StatusCancelled
	StatusTimeout   = managerinstalldata.StatusTimeout
)

const (
	EventKindLog   = managerinstalldata.EventKindLog
	EventKindState = managerinstalldata.EventKindState
	EventKindError = managerinstalldata.EventKindError
)

// AuthKind 区分凭据套餐（password|key）。biz 独有的 enum，未进 data 层
// —— 因为它只是 InstallJob.AuthKind 字段值的枚举，不存为独立 gorm 实体。
const (
	AuthKindPassword = "password"
	AuthKindKey      = "key"
)
