package model

import (
	"time"

	"gorm.io/gorm"
)

// PluginAudit 插件生命周期 + 安全相关的事件审计流水。
//
// 安装 / 卸载 / 启停 / 健康状态翻转 / host.call / secret 绑定 / 审批请求 等
// 都落一行本表;同时由 Phase 2 的 biz/audit.go 同步复制一份到 A 的
// audit_logs(comp="pluginhost"),A 既有审计消费者无感。
type PluginAudit struct {
	gorm.Model
	TenantID         uint64    `gorm:"index"`
	PluginInstanceID uint64    `gorm:"index"`
	Action           string    `gorm:"size:32;index"` // install / uninstall / enable / disable / health_change / host_call / bind_secret / approval_request
	Actor            string    `gorm:"size:128"`      // user_id 或 system
	DetailsJSON      string    `gorm:"type:text"`
	OccurredAt       time.Time `gorm:"index"`
}

// TableName 指定 GORM 表名。
func (PluginAudit) TableName() string {
	return "plugin_audits"
}
