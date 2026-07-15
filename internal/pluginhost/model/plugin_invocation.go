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

// PluginInvocation 一次 capability invoke 的调用流水。
//
// 每次(A 业务 / 反向 host.call)发起的能力调用落一行,用于事后
// 分析、慢查询定位与 SLO 计算。InvokedAt 是 invoke 的实际触发
// 时刻(不是 gorm 创建时间),便于按时间窗口检索。
type PluginInvocation struct {
	gorm.Model
	TenantID         uint64 `gorm:"index"`
	PluginInstanceID uint64 `gorm:"index"`
	CapabilityID     uint64 `gorm:"index"`
	Caller           string `gorm:"size:64"`
	ParamsJSON       string `gorm:"type:text"`
	ResultJSON       string `gorm:"type:text"`
	ErrorMessage     string `gorm:"size:512"`
	LatencyMs        int64
	InvokedAt        time.Time `gorm:"index"`
}

// TableName 指定 GORM 表名。
func (PluginInvocation) TableName() string {
	return "plugin_invocations"
}
