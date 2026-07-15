// Package model 定义 pluginhost 子系统的 gorm 持久化实体。
//
// 四张表(plugin_instances / plugin_capabilities / plugin_invocations /
// plugin_audits)与 A 主项目共享同一个 *gorm.DB,由 Phase 2 的 data/migrate.go
// 在启动时 AutoMigrate 建表。本包只声明实体,不写仓储、不依赖 A 任何子包,
// 也不依赖 pluginhost 内部其他子包(model 是叶子节点)。
package model

import "gorm.io/gorm"

// PluginCapability 一个 plugin 实例暴露的单个 capability 行。
//
// 一行 = 一个 (Kind, Name) 能力点;属于 PluginInstance。DB 层不做
// (PluginInstanceID, Kind, Name) 复合唯一约束,由 repo / biz 层
// 应用层校验,DB 仅按列各自加索引。
type PluginCapability struct {
	gorm.Model
	PluginInstanceID uint64 `gorm:"index"`
	TenantID         uint64 `gorm:"index"`
	Kind             string `gorm:"size:32;index"`
	Name             string `gorm:"size:128"`
	Class            string `gorm:"size:32"`
	SchemaJSON       string `gorm:"type:text"`
	UIMetadataJSON   string `gorm:"type:text"`
	Enabled          bool   `gorm:"default:true"`
}

// TableName 指定 GORM 表名。
func (PluginCapability) TableName() string {
	return "plugin_capabilities"
}
