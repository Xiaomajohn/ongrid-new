// Package manifest 定义 pluginhost 子系统所识别的插件清单文件的数据结构,以及
// 4 种 manifest 格式(pluginhost / claude / openclaw / bare_skills)的常量。
//
// 该层只关心 JSON 文件的解析与字段语义,不依赖 A 的任何子包,在 Phase 2 之前不参与
// sandbox 校验之外的运行时。
package manifest

import (
	"encoding/json"
	"time"
)

// Format 表示 build 工具接收的原始清单格式。
type Format string

// Format 只是 build 工具的 input 标识;server runtime 不再 runtime 解析 4 种 format,所有 plugin 必须在 build 阶段转换为标准 pluginhost + 生成 .compiled-manifest.json。server 启动只读编译产物。
const (
	FormatPluginHost Format = "pluginhost"
	FormatClaude     Format = "claude"
	FormatOpenclaw   Format = "openclaw"
	FormatBareSkills Format = "bare_skills"
)

// Kind 表示单个 capability 所属的能力大类,用于 Phase 3 的 adapter 分发。
type Kind string

// 7 类受支持的能力类型常量。
const (
	KindAITool        Kind = "ai.tool"
	KindNotifier      Kind = "notifier"
	KindWorkflowNode  Kind = "workflow.node"
	KindSkillRunner   Kind = "skill.runner"
	KindLLMProvider   Kind = "llm.provider"
	KindEmbedProvider Kind = "embedding.provider"
	KindEvaluator     Kind = "alert.evaluator"
)

// PluginManifest 是开发者手写的标准 plugin.json。
type PluginManifest struct {
	Name             string                     `json:"name"`
	ID               string                     `json:"id,omitempty"`
	UUID             string                     `json:"uuid"`
	Version          string                     `json:"version"`
	Description      string                     `json:"description,omitempty"`
	Format           Format                     `json:"format,omitempty"`
	Entry            string                     `json:"entry,omitempty"`
	Transport        string                     `json:"transport,omitempty"`
	Capabilities     []Capability               `json:"capabilities"`
	Permissions      Permissions                `json:"permissions,omitempty"`
	Signature        string                     `json:"signature,omitempty"`
	UIMetadata       map[string]any             `json:"ui_metadata,omitempty"`
	Frontend         *Frontend                  `json:"frontend,omitempty"`
	HostDependencies []string                   `json:"host_dependencies,omitempty"`
	DataScopes       map[string]json.RawMessage `json:"data_scopes,omitempty"`
}

// Capability 描述插件对外暴露的单个能力点。
type Capability struct {
	Kind     Kind            `json:"kind"`
	Name     string          `json:"name"`
	Class    string          `json:"class"`
	Schema   json.RawMessage `json:"schema"`
	Metadata map[string]any  `json:"metadata,omitempty"`
}

// Permissions 声明 plugin 允许执行的副作用集合,Phase 2 由 sandbox/permission 校验。
type Permissions struct {
	// NetworkEgress 允许出网的目的地址,例:["gitlab.com:443"]。
	NetworkEgress []string `json:"network_egress,omitempty"`
	// FSWrite 允许写入的文件路径(相对 plugin 根目录)。
	FSWrite []string `json:"fs_write,omitempty"`
	// EnvAccess 允许读取的环境变量名。
	EnvAccess []string `json:"env_access,omitempty"`
}

// Frontend 描述 plugin 自带的前端产物。
type Frontend struct {
	Format      string          `json:"format"`
	Entry       string          `json:"entry"`
	ElementTag  string          `json:"element_tag,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	Permissions []string        `json:"permissions,omitempty"`
}

// CompiledManifest 是 build 工具生成、server 启动时读取的编译产物。
type CompiledManifest struct {
	PluginManifest
	Files     []CompiledFile `json:"files"`
	BuiltAt   time.Time      `json:"built_at"`
	BuildTool string         `json:"build_tool"`
}

// CompiledFile 是 plugin 根目录下单个文件的相对路径索引。
type CompiledFile struct {
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
}
