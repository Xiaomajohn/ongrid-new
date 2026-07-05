// Package installjob 提供一键安装 edge 的异步任务队列。
//
// 这是 manager/installjob BC 的 biz 层。一个 InstallJob 代表一次 "在指定
// 设备上 ssh 一把 → 写 install.sh → 引导边缘 agent 上线" 的端到端尝试；
// 任务经 Runner 排队后由 Worker.Execute 串行处理，结果（含日志）持久化
// 在 install_jobs / install_job_events 两张表里。
//
// Phase 1（本文件）只落 biz 内核：
//
//   - GORM 模型 + Repo（Create / Get / ListByDevice / UpdateStatus /
//     AppendLog / ClearCredentialSnap / AddEvent）
//   - Worker.Execute：单 job 的状态机 + 凭据快照清理 + 事件时间线
//   - Runner：goroutine 池，bounded queue + 非阻塞 enqueue
//
// 留给后续 wire（cmd/ongrid main.go 内执行）：
//
//   - 接 Repo（gorm.DB）→ NewRepo
//   - 接 EdgeIssuer / DeviceSSHSoftDelete / Installer 三个外部接口
//   - 在 main.go 的 wiring 阶段替换 Worker.waitEdgeOnline 占位实现
//
// HTTP handler / cron 入口同样落在后续 PR；本 BC 不导出 usecase 入口，
// 由调用方组合 Repo + Runner.Enqueue 即可。
package installjob
