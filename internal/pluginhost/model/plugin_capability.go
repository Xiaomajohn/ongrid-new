package model

import (
	"encoding/json"

	"gorm.io/gorm"
)

// PluginCapability 插件实例暴露的单个能力(capability)。
//
// 一个 PluginInstance 拥有 0..N 个 capability,每行一个能力点。
// 通过 (PluginInstanceID, Kind, Name) 三字段复合唯一索引,保证同一插件
// 实例下不重复注册同 kind+name 的能力(Class 用于安全等级校验,
// 详见 plan §6.6 sandbox/permission 与 hostcall/scope)。
type PluginCapability struct {
	gorm.Model
	PluginInstanceID uint64 `gorm:"index;uniqueIndex:idx_cap_unique,priority:1"`
	TenantID         uint64 `gorm:"index"`
	Kind             string `gorm:"size:32;uniqueIndex:idx_cap_unique,priority:2"` // ai.tool / notifier / workflow.node / skill.runner / llm.provider / embedding.provider / alert.evaluator
	Name             string `gorm:"size:128;uniqueIndex:idx_cap_unique,priority:3"`
	Class            string `gorm:"size:32"` // 安全等级:safe / mutating / dangerous
	SchemaJSON       string `gorm:"type:text"`
	MetadataJSON     string `gorm:"type:text"` // 静态元数据 JSON
	UIMetadataJSON   string `gorm:"type:text"` // UI 渲染元数据 JSON
	Enabled          bool   `gorm:"default:true"`
}

// TableName 指定 GORM 表名。
func (PluginCapability) TableName() string {
	return "plugin_capabilities"
}

// SnapshotJSON 把能力的关键字段与 JSON-text 字段打包为一段可读 JSON,
// 便于 hostcall/adapter/审计日志展示。仅做序列化,不参与 GORM 的 ORM 行为。
func (c PluginCapability) SnapshotJSON() ([]byte, error) {
	return json.Marshal(struct {
		PluginInstanceID uint64
		Kind             string
		Name             string
		Class            string
		Enabled          bool
		Schema           json.RawMessage
		Metadata         json.RawMessage
		UIMetadata       json.RawMessage
	}{
		PluginInstanceID: c.PluginInstanceID,
		Kind:             c.Kind,
		Name:             c.Name,
		Class:            c.Class,
		Enabled:          c.Enabled,
		Schema:           json.RawMessage(c.SchemaJSON),
		Metadata:         json.RawMessage(c.MetadataJSON),
		UIMetadata:       json.RawMessage(c.UIMetadataJSON),
	})
}
