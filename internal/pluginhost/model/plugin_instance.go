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

// PluginInstance 一个已安装 plugin 的实例行。
//
// PackID 列语义:直接存储 plugin.json.name(PascalCase,如 "AlarmPlugin"),
// 不做 slug 转换,不拼接 version/uuid 后缀;磁盘目录名 =
// /var/lib/ongrid/plugins/<PackID>/,与 PackID 完全相等;API URL 段
// /plugins/<PackID>/... 也直接用 PackID。任何代码路径都不允许重写 pack_id。
type PluginInstance struct {
	gorm.Model
	TenantID         uint64 `gorm:"index"`
	PackID           string `gorm:"size:128;uniqueIndex:idx_pack_tenant"` // = plugin.json.name (PascalCase, 如 "AlarmPlugin");磁盘目录名 = PackID,无后缀
	UUID             string `gorm:"size:64;index"`
	Version          string `gorm:"size:32"`
	Source           string `gorm:"size:32"` // "tarball" | "filesystem"
	InstallPath      string `gorm:"size:512"` // = "/var/lib/ongrid/plugins/<PackID>/",与 PackID 拼接
	ManifestSHA256   string `gorm:"size:64"`  // 可选,本次不动 sha 校验,留空
	SignatureState   string `gorm:"size:16"`  // "unsigned" | "valid" | "invalid"
	Enabled          bool   `gorm:"default:true"`
	HealthStatus     string `gorm:"size:32"`
	LastHealthAt     *time.Time
	CapabilitiesJSON string `gorm:"type:text"` // 序列化 []Capability
	BindingsJSON     string `gorm:"type:text"`
	UIMetadataJSON   string `gorm:"type:text"`
}

// TableName 指定 GORM 表名。
func (PluginInstance) TableName() string {
	return "plugin_instances"
}
