// Package model 定义 pluginhost 子系统的持久化实体(GORM 模型)。
//
// 四张表(plugin_instances / plugin_capabilities / plugin_invocations /
// plugin_audits)与 A 老项目共享同一个 *gorm.DB,经由 Phase 2 的
// data/migrate.go 调 AutoMigrate 建表;本包只做实体定义,不负责迁移、
// 不做仓储、不依赖任何 A 子包。
package model

import (
	"time"

	"gorm.io/gorm"
)

// PluginInstance 已安装的单个插件实例。
//
// 一行 = 一个具体的插件实例(同一 PackID + Version 可以安装多次,各自一行)。
// 通过 (PackID, TenantID) 复合唯一索引保证同一租户下同一插件包不重复登记。
type PluginInstance struct {
	gorm.Model
	TenantID         uint64     `gorm:"index"` // 租户隔离
	PackID           string     `gorm:"size:128;uniqueIndex:idx_pack_tenant,priority:1"`
	Version          string     `gorm:"size:64"`  // 语义化版本
	Source           string     `gorm:"size:64"`  // 安装来源:local / tarball / git / remote / inproc
	InstallPath      string     `gorm:"size:512"` // 物理安装路径(根目录)
	ManifestSHA256   string     `gorm:"size:64"`  // manifest 哈希,用于完整性校验
	SignatureState   string     `gorm:"size:32"`  // 签名校验状态:unsigned / verified / failed
	Enabled          bool       `gorm:"default:true;index"`
	HealthStatus     string     `gorm:"size:32"` // 健康状态:healthy / degraded / down / unknown
	LastHealthAt     *time.Time // 最近一次健康探测时间
	CapabilitiesJSON string     `gorm:"type:text"` // 能力清单的 JSON 快照(冗余,便于审计)
	BindingsJSON     string     `gorm:"type:text"` // 绑定关系(vault 引用、edge target 等)JSON
	UIMetadataJSON   string     `gorm:"type:text"` // UI 展示元数据 JSON
	Format           string     `gorm:"size:32"`   // 插件格式:pluginhost / claude / openclaw / bare_skills
	Transport        string     `gorm:"size:32"`   // 运行传输方式:subprocess / http / inproc
	TimeoutSeconds   int        `gorm:"default:30"`
}

// TableName 指定 GORM 表名。
func (PluginInstance) TableName() string {
	return "plugin_instances"
}
