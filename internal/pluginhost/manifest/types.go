// Package manifest 定义 pluginhost 子系统所识别的插件清单文件的数据结构,以及
// 4 种 manifest 格式(pluginhost / claude / openclaw / bare_skills)的常量。
//
// 该层只关心 JSON 文件的解析与字段语义,不依赖 A 的任何子包,在 Phase 2 之前不参与
// sandbox 校验之外的运行时。
package manifest

import "encoding/json"

// Format 表示一个 plugin 目录使用的清单文件格式。
type Format string

// 4 种受支持的 manifest 格式。
const (
	// FormatPluginHost 是本系统主推的 plugin.json 格式。
	FormatPluginHost Format = "pluginhost"
	// FormatClaude 对应 .claude-plugin/plugin.json(向后兼容)。
	FormatClaude Format = "claude"
	// FormatOpenclaw 对应 openclaw.plugin.json。
	FormatOpenclaw Format = "openclaw"
	// FormatBareSkills 表示目录里只有散落的 skills/<name>/SKILL.md,没有统一 manifest。
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

// PluginManifest 是任意一种 Format 下解析出来的统一 plugin 描述。
//
// 为兼容 4 种 format,只有 ID/Name/Version 为必填;其余字段在缺省时由 runtime 给出
// fallback。
type PluginManifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`

	// Format 为空时,默认 pluginhost;由 loader 在解析阶段补齐。
	Format Format `json:"format,omitempty"`
	// Entry 是子进程入口可执行文件相对路径(subprocess transport)。
	Entry string `json:"entry,omitempty"`
	// Transport:subprocess | http | inproc;缺省时由 Phase 3 的 runtime 推断。
	Transport string `json:"transport,omitempty"`

	Capabilities []Capability `json:"capabilities"`
	Permissions  Permissions  `json:"permissions,omitempty"`

	// Signature 是可选的离线签名串(预留,Phase 4 接入签名校验)。
	Signature string `json:"signature,omitempty"`

	// UIMetadata 是 Phase 3 前端表单渲染依据。
	UIMetadata map[string]any `json:"ui_metadata,omitempty"`

	// HostDependencies 列出 plugin 需要反向调用的宿主能力,例:["prom","loki",
	// "skill","llm","edge.execute_cmd"]。Phase 3 由 hostcall/scope 强校验。
	HostDependencies []string `json:"host_dependencies,omitempty"`

	// DataScopes 按能力/资源维度声明可见范围(每个 key 由 plugin 自己约定),Phase 3
	// 由 hostcall/scope 应用到具体 client 上。
	DataScopes map[string]json.RawMessage `json:"data_scopes,omitempty"`
}

// Capability 描述插件对外暴露的单个能力点。
type Capability struct {
	// Kind 7 类之一,Phase 3 adapter 据此分发。
	Kind Kind `json:"kind"`
	// Name 在 plugin 内部必须唯一;被 Invoke(pluginID, capName=...) 引用。
	Name string `json:"name"`
	// Class:safe(只读) / mutating / dangerous;缺省 safe;Phase 3 用于审批流。
	Class string `json:"class,omitempty"`
	// Schema 是该 cap 接受的入参 JSON Schema,可选。
	Schema json.RawMessage `json:"schema,omitempty"`
	// Metadata 是 cap 级扩展信息,前端/适配器按需消费。
	Metadata map[string]any `json:"metadata,omitempty"`
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

// LoadResult 是 LoadDirs 对单个 plugin 目录的输出;解析期间的告警写到 Warnings。
type LoadResult struct {
	Manifest *PluginManifest `json:"manifest"`
	Path     string          `json:"path"`
	Warnings []string        `json:"warnings,omitempty"`
}
