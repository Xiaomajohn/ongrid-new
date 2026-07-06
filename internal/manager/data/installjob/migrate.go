// 自动建表入口：被 cmd/ongrid/main.go 的 dbx.RunMigrations(...) 链调用。
//
// 签名与 dbx.Migrator 一致：func(db *gorm.DB) error；耗时日志由
// dbx.RunMigrations 内部统一打，避免每个 data 包重复打 "migration
// start/done"。
package installjob

import (
	"gorm.io/gorm"
)

// Migrate 在启动期对 install_jobs / install_job_events 两张表执行
// AutoMigrate。安装任务 BC 的 installjob worker 严重依赖此入口——
// 缺漏会导致 worker 启动后即报 1146 "Table 'ongrid.install_jobs'
// doesn't exist"。
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&InstallJob{}, &InstallJobEvent{})
}
