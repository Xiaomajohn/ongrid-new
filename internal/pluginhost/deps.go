// Deps 注入 contract + HostSDK 反向调 A 的 API 集合。
//
// 本文件只定义"接口形状"与"占位类型",不做任何行为实现:
//   - Deps:New(deps) 时校验必填字段;其余 Registrar / HostServices 允许 nil。
//   - 6 个 Registrar:消费方定义,Phase 3 的 adapter 子包提供实现(包成 A 现有
//     register 函数)。
//   - 14 个 HostSDK API:Plugin / Runtime 反向调 A 能力的最小方法集。
//
// 关于 A 包类型的对齐:
//   - skill.Metadata / skill.Executor / notify.Sender / embedding.Embedder:
//     A 包已 export,直接 import 使用。
//   - llm.Provider / llm.ChatRequest / llm.ChatResponse / alertconfig.Evaluator
//     / flow.Node:Plan §4 / §6 假设 A 包已 export,但当前 A 包未 export 这些
//     类型(llm 包只有 llm.Client / llm.ChatReq / llm.ChatResp,flow 包只有
//     NodeSpec / NodeResult / NodeKind,alertconfig 包路径与 Plan 假设不符)。
//     本文件以 pluginhost 内部 stub interface 占位(LLMProvider / Evaluator
//     / Node / LLMChatRequest / LLMChatResponse),Phase 3 adapter 阶段在 A
//     包补齐真实类型后,把本文件 alias 切到真实类型即可,接口形状不变。
package pluginhost

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pkg/notify"
	"github.com/ongridio/ongrid/internal/skill"
)

// ===== Deps & HostServices =====

// Deps 是 pluginhost 启动所需的注入项。
//
// 必填字段:DB / Logger / Ctx / Mux,任一为 nil 即 New 返 ErrDepsIncomplete。
// 可选字段(6 个 Registrar + Audit + HostServices)允许 nil,缺失时:
//   - 对应 adapter 跳过注册(不会注入到 A)
//   - 对应 HostSDK 调用返 host_call_undeclared / not wired
type Deps struct {
	// A 共享资源(必填)
	DB     *gorm.DB
	Logger *slog.Logger
	Ctx    context.Context
	Mux    *chi.Mux

	// 注册器:A 子系统的 register 函数被包成这些 interface(消费方定义)
	Skills         SkillRegistrar
	Notifiers      NotifyRegistrar
	LLMProviders   LLMRegistrar
	EmbedProviders EmbeddingRegistrar
	Evaluators     EvaluatorRegistrar
	Workflow       WorkflowRegistrar

	// 审计入口:pluginhost lifecycle / invoke 全链路写审计。
	Audit AuditSink

	// HostServices:HostSDK 反向调 A 能力的总集。
	HostServices HostServices
}

// HostServices 暴露 A 的查询与执行能力给 plugin / plugin runtime。
//
// 每个字段对应 1 个 HostSDK API 接口;Plugin 进程内通过 deps.HostServices.<Xxx>
// 调用,HostSDK(scope + rate-limit + audit)包装后真正打到 A 现有 client。
type HostServices struct {
	Loki   LokiQueryAPI
	Prom   PromQueryAPI
	Tempo  TempoQueryAPI
	Qdrant QdrantQueryAPI

	Skills   SkillLookupAPI
	LLM      LLMCompleteAPI
	Embedder EmbeddingAPI

	Edge     EdgeCommandAPI
	Audit    AuditAPI
	Topology TopologyAPI

	Notify  NotifyAPI
	Config  ConfigAPI
	Vault   VaultAPI
	Metrics MetricsAPI
}

// ===== 6 个 Registrar(消费方定义) =====

// SkillRegistrar 把 plugin 的 ai.tool / skill.runner 包成 skill.Executor
// 注册到 A 的 skill.Registry。
type SkillRegistrar interface {
	RegisterOne(skill.Executor) error
}

// NotifyRegistrar 把 plugin 的 notifier 包成 notify.Sender 注册到 A 的 notify.Router。
type NotifyRegistrar interface {
	RegisterSender(notify.Sender) error
}

// LLMRegistrar 把 plugin 的 llm.provider 包成 llm.Provider 注册到 A 的 llm.MultiClient。
//
// 当前 A 的 internal/pkg/llm 包未 export Provider 类型(仅有 llm.Client /
// llm.ChatReq / llm.ChatResp),Phase 3 在 A 包补齐 llm.Provider 后,
// 本接口的 p 参数类型应切换为 llm.Provider。详见 LLMProvider 占位说明。
type LLMRegistrar interface {
	RegisterProvider(name string, p LLMProvider) error
}

// Embedder is the structural interface satisfied by
// internal/pkg/embedding.Embedder. Defined here (rather than imported)
// so the pluginhost main package stays cgo-free — internal/pkg/embedding
// transitively depends on github.com/yalue/onnxruntime_go (cgo), which
// excludes Windows. Any concrete type that exposes Dim() int and
// Embed(ctx, []string) ([][]float32, error) satisfies this interface.
type Embedder interface {
	Dim() int
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbeddingRegistrar 把 plugin 的 embedding.provider 包成 embedding.Embedder
// 注册到 A 的 embedding.Registry。
type EmbeddingRegistrar interface {
	RegisterEmbedder(name string, e Embedder) error
}

// EvaluatorRegistrar 把 plugin 的 alert.evaluator 包成 alertconfig.Evaluator
// 注册到 A 的 alert 配置中心。
//
// 当前 A 的 internal/manager/biz/alert/alertconfig 路径不存在(实际为
// internal/manager/biz/aiops/alertconfig,且未 export Evaluator 类型),
// Phase 3 在 A 包补齐 alertconfig.Evaluator 后,本接口 e 参数类型应切换为
// alertconfig.Evaluator。详见 Evaluator 占位说明。
type EvaluatorRegistrar interface {
	RegisterEvaluator(name string, e Evaluator) error
}

// WorkflowRegistrar 把 plugin 的 workflow.node 包成 flow.Node 注册到 A 的 flow.Registry。
//
// 当前 A 的 internal/manager/biz/flow 包未 export Node 类型(仅有
// NodeSpec / NodeResult / NodeKind),Phase 3 在 A 包补齐 flow.Node 后,
// 本接口 n 参数类型应切换为 flow.Node。详见 Node 占位说明。
type WorkflowRegistrar interface {
	RegisterNode(name string, n Node) error
}

// ===== A 包类型占位(stub interface,Phase 3 适配) =====

// LLMProvider 是 A 包 llm.Provider 的占位。
//
// TODO(Phase 3):在 internal/pkg/llm 包 export Provider 类型,本定义替换为:
//
//	type LLMProvider = llm.Provider
//
// 当前 A 的 llm 包形态为 llm.Client(单一 Chat 方法),plugin 适配层会在
// LLMProvider 与 llm.Client 之间做适配(类似 skill adapter 的做法)。
type LLMProvider interface {
	// 占位 — Phase 3 对齐 llm.Provider 真实 contract。
	_phantom()
}

// LLMChatRequest 是 A 包 llm.ChatRequest 的占位。
// TODO(Phase 3):对齐 llm.ChatReq / ChatRequest。
type LLMChatRequest interface {
	_phantom()
}

// LLMChatResponse 是 A 包 llm.ChatResponse 的占位。
// TODO(Phase 3):对齐 llm.ChatResp / ChatResponse。
type LLMChatResponse interface {
	_phantom()
}

// Evaluator 是 A 包 alertconfig.Evaluator 的占位。
//
// TODO(Phase 3):在 internal/manager/biz/aiops/alertconfig 包 export
// Evaluator 类型,本定义替换为:
//
//	type Evaluator = alertconfig.Evaluator
//
// 评估器接收一段 metric / log 数据,返回是否触发告警。
type Evaluator interface {
	_phantom()
}

// Node 是 A 包 flow.Node 的占位。
//
// TODO(Phase 3):在 internal/manager/biz/flow 包 export Node 类型,本定义替换为:
//
//	type Node = flow.Node
//
// 工作流节点执行单元,接收 cfg + rc,输出 NodeResult。
type Node interface {
	_phantom()
}

// ===== AuditSink =====

// AuditSink 是 pluginhost 写审计的统一入口。
//
// 由 main.go 注入 A 的 audit.Log 函数;pluginhost 在 install / uninstall /
// enable / disable / invoke 各路径调用 Write,comp 固定为 "pluginhost"。
type AuditSink interface {
	Write(ctx context.Context, comp, action, resource string, latency time.Duration, err error) error
}

// ===== 14 个 HostSDK API =====

// LokiQueryAPI 暴露 Loki 日志查询给 plugin / runtime。
type LokiQueryAPI interface {
	QueryRange(ctx context.Context, query string, start, end time.Time, limit int) (json.RawMessage, error)
}

// PromQueryAPI 暴露 Prometheus 指标查询给 plugin / runtime。
type PromQueryAPI interface {
	Query(ctx context.Context, promql string) (json.RawMessage, error)
	QueryRange(ctx context.Context, promql string, start, end time.Time, step string) (json.RawMessage, error)
}

// TempoQueryAPI 暴露 Tempo 链路查询给 plugin / runtime。
type TempoQueryAPI interface {
	Search(ctx context.Context, q string, limit int) (json.RawMessage, error)
	Trace(ctx context.Context, traceID string) (json.RawMessage, error)
}

// QdrantQueryAPI 暴露 qdrant 向量库读写给 plugin / runtime。
type QdrantQueryAPI interface {
	Search(ctx context.Context, collection string, vector []float32, limit int) (json.RawMessage, error)
	Upsert(ctx context.Context, collection string, id string, vector []float32, payload json.RawMessage) error
}

// SkillLookupAPI 暴露 skill 列表与执行给 plugin / runtime。
type SkillLookupAPI interface {
	List(ctx context.Context) ([]skill.Metadata, error)
	Execute(ctx context.Context, key string, params json.RawMessage) (json.RawMessage, error)
}

// LLMCompleteAPI 暴露 LLM 补全给 plugin / runtime。
type LLMCompleteAPI interface {
	Chat(ctx context.Context, req LLMChatRequest) (LLMChatResponse, error)
}

// EmbeddingAPI 暴露向量化给 plugin / runtime。
type EmbeddingAPI interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// EdgeCommandAPI 暴露远端 edge 的 shell / 文件 / 目录操作给 plugin / runtime。
type EdgeCommandAPI interface {
	RunShell(ctx context.Context, edgeID, cmd string, timeout time.Duration) (string, error)
	CopyFile(ctx context.Context, edgeID, src, dst string) error
	ListDir(ctx context.Context, edgeID, path string) ([]string, error)
}

// AuditAPI 暴露审计写入给 plugin(用于 plugin 自身审计)。
type AuditAPI interface {
	Write(ctx context.Context, comp, action, resource, actor string, details map[string]any) error
}

// TopologyAPI 暴露拓扑查询给 plugin。
type TopologyAPI interface {
	Get(ctx context.Context, id string) (json.RawMessage, error)
	Query(ctx context.Context, expr string) (json.RawMessage, error)
}

// NotifyAPI 暴露告警通知给 plugin(走 A 的 notify.Router)。
type NotifyAPI interface {
	Send(ctx context.Context, channel string, msg json.RawMessage) error
	SendVia(ctx context.Context, channelKind, name string, msg json.RawMessage) error
}

// ConfigAPI 暴露 A 的配置读写给 plugin(只读写 pluginhost 命名空间下的 key)。
type ConfigAPI interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// VaultAPI 暴露 vault 凭据引用给 plugin。**只返引用名**,绝不返明文。
type VaultAPI interface {
	GetRef(ctx context.Context, name string) (ref string, err error)
}

// MetricsAPI 暴露自定义指标写入与查询给 plugin。
type MetricsAPI interface {
	Write(ctx context.Context, name string, value float64, labels map[string]string) error
	Query(ctx context.Context, promql string) (json.RawMessage, error)
}
