// Package model 定义 pluginhost 子系统的 gorm 持久化实体。
//
// 四张表(plugin_instances / plugin_capabilities / plugin_invocations /
// plugin_audits)与 A 主项目共享同一个 *gorm.DB,由 Phase 2 的 data/migrate.go
// 在启动时 AutoMigrate 建表。本包只声明实体,不写仓储、不依赖 A 任何子包,
// 也不依赖 pluginhost 内部其他子包(model 是叶子节点)。
package model

import (
	"time"

	"gorm.io/gorm"
)

// PluginAudit 插件生命周期 + 安全性事件的审计流水。
//
// install / uninstall / enable / disable / invoke / host_call 等
// 事件都落一行本表;OccuredAt 是事件实际发生时间(不是 gorm 写入
// 时间),便于按时间窗口检索。同时由 Phase 2 的 biz/audit.go 复制
// 一份到 A 主项目的 audit_logs(comp="pluginhost"),A 既有审计消费
// 者对此无感知。
type PluginAudit struct {
	gorm.Model
	TenantID         uint64 `gorm:"index"`
	PluginInstanceID uint64 `gorm:"index"`
	Action           string `gorm:"size:32;index"` // "install" | "uninstall" | "enable" | "disable" | "invoke" | "host_call"
	Actor            string `gorm:"size:128"`
	DetailsJSON      string `gorm:"type:text"`
	OccurredAt       time.Time `gorm:"index"`
}

// TableName 指定 GORM 表名。
func (PluginAudit) TableName() string {
	return "plugin_audits"
}
