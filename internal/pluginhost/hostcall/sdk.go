// Package hostcall 是 pluginhost 子系统的 HostSDK 反向调用层。
//
// plugin / runtime 通过 SDK.Call 反向访问 A 的能力(查询日志/指标/链路、
// 调 skill、调 LLM、向远端 edge 下发命令、写审计、发通知、读写配置等),
// 在到达真实 A client 之前要过三道闸:
//
//  1. ScopeValidator.Authorize — plugin 是否声明过该 op(host_dependencies)
//  2. Limiter.Allow — token bucket 限流(默认 60/min)
//  3. 路由到对应 Adapter.Dispatch — adapter 把 json payload 反序列化为
//     对应 HostServices.<XxxAPI> 的强类型参数,调核心方法,把结果再序列化
//
// 每一次 Call(无论成功失败)都会触发 AuditFunc 写一条审计,plugin 拿不到
// A 的 plaintext 凭据(vault 永远只返引用名)。
//
// 本包不 import A 的任何子包,只依赖 pluginhost.HostServices 抽象;
// A 实际 client(manager/data, internal/pkg/*, skill.Executor 等)在
// Phase 4 的 main.go wire-up 时包成 pluginhost.XxxAPI 注入。
package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// 公开错误变量,plugin 端按 errors.Is 区分。
var (
	// ErrPermissionDenied op 未在 plugin manifest 的 host_dependencies 中声明,
	// 或 payload 超出 data_scopes 限制。
	ErrPermissionDenied = errors.New("hostcall: permission denied")
	// ErrRateLimited 触发 token bucket 限流(默认 60/min)。
	ErrRateLimited = errors.New("hostcall: rate limited")
	// ErrUnknownOp 未知 op(没匹配到任何 adapter)。
	ErrUnknownOp = errors.New("hostcall: unknown op")
)

// ErrNotWired 是 Adapter 在依赖的 HostService 为 nil 时返的错误,便于测试
// 与"可选能力未注入"的渐进启用。
var ErrNotWired = errors.New("hostcall: not wired")

// AuditFunc 是 hostcall 链路上的审计回调。
//
// 由 main.go 在 wire-up 时实现:把 plugin_id / op / latency / err 写到
// A 的 audit_logs 表(组件名固定 "pluginhost",与 SDK.Call 的 source 无关)。
//
// 失败/拒绝 都会被记录;audit 写入自身失败不应阻断 plugin 主调用,因此
// 实现需要 swallow 内部 error。
type AuditFunc func(ctx context.Context, pluginID uint64, op string, latency time.Duration, err error)

// SDK 是 plugin → A 反向调用的统一入口。
//
// 一次 Call 链路:
//  1. scope.Authorize(pluginID, op, payload) — 失败返 ErrPermissionDenied
//  2. rate.Allow(pluginID) — 失败返 ErrRateLimited
//  3. dispatch(op, payload) — switch op 路由到对应 adapter
//  4. audit(pluginID, op, latency, err)
type SDK struct {
	services pluginhost.HostServices
	scope    *ScopeValidator
	rate     *Limiter
	audit    AuditFunc

	// 14 个 adapter,NewSDK 内部从 services 字段构造。plugin 进程通过
	// SDK.Call 反向调用,走 dispatch 路由到其中一个。
	prom     *PromAdapter
	loki     *LokiAdapter
	tempo    *TempoAdapter
	qdrant   *QdrantAdapter
	skill    *SkillAdapter
	llm      *LLMAdapter
	embed    *EmbeddingAdapter
	edge     *EdgeAdapter
	auditAdp *AuditAdapter
	notify   *NotifyAdapter
	config   *ConfigAdapter
	vault    *VaultAdapter
	metrics  *MetricsAdapter
}

// NewSDK 构造 hostcall SDK。
//
// services 字段各 API 可以为 nil(常见于可选能力未 wire-up),对应 adapter
// 的 Dispatch 会返 ErrNotWired 而不是 panic,便于测试与渐进启用。
//
// scope / rate 为 nil 时使用 NewScopeValidator()(无声明即拒绝)与
// NewLimiter(60)(默认 60/min);audit 为 nil 时不写审计(本地调试场景)。
func NewSDK(services pluginhost.HostServices, scope *ScopeValidator, rate *Limiter, audit AuditFunc) *SDK {
	if scope == nil {
		scope = NewScopeValidator()
	}
	if rate == nil {
		rate = NewLimiter(60)
	}
	return &SDK{
		services: services,
		scope:    scope,
		rate:     rate,
		audit:    audit,
		prom:     NewPromAdapter(services.Prom),
		loki:     NewLokiAdapter(services.Loki),
		tempo:    NewTempoAdapter(services.Tempo),
		qdrant:   NewQdrantAdapter(services.Qdrant),
		skill:    NewSkillAdapter(services.Skills),
		llm:      NewLLMAdapter(services.LLM),
		embed:    NewEmbeddingAdapter(services.Embedder),
		edge:     NewEdgeAdapter(services.Edge),
		auditAdp: NewAuditAdapter(services.Audit),
		notify:   NewNotifyAdapter(services.Notify),
		config:   NewConfigAdapter(services.Config),
		vault:    NewVaultAdapter(services.Vault),
		metrics:  NewMetricsAdapter(services.Metrics),
	}
}

// Call 是 hostcall 的统一入口。Plugin 进程 / Runtime 转发过来的 host.call
// 都走这里,返 (json.RawMessage, error);nil err 表示成功,序列化失败
// 或权限/限流/未知 op 都会 wrap 到对应公开错误。
func (s *SDK) Call(ctx context.Context, pluginID uint64, op string, payload json.RawMessage) (json.RawMessage, error) {
	start := time.Now()

	// 1. RBAC 鉴权:op 是否在 plugin manifest 的 host_dependencies 中声明。
	if err := s.scope.Authorize(pluginID, op, payload); err != nil {
		s.fireAudit(ctx, pluginID, op, time.Since(start), err)
		return nil, fmt.Errorf("%w: %v", ErrPermissionDenied, err)
	}

	// 2. token bucket 限流。
	if !s.rate.Allow(pluginID) {
		err := ErrRateLimited
		s.fireAudit(ctx, pluginID, op, time.Since(start), err)
		return nil, err
	}

	// 3. 路由到对应 adapter。
	result, err := s.dispatch(ctx, op, payload)
	s.fireAudit(ctx, pluginID, op, time.Since(start), err)
	return result, err
}

// dispatch 把 op 路由到对应 adapter。未知 op 返 ErrUnknownOp。
//
// 与 plugin 进程的 host.call envelope 协议对应(op 形如
// "loki.query_range" / "prom.query" / "skill.execute" 等)。
func (s *SDK) dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	switch op {
	// 信号类(Loki / Prom / Tempo / Qdrant)
	case "loki.query_range":
		return s.loki.Dispatch(ctx, op, payload)
	case "prom.query", "prom.query_range":
		return s.prom.Dispatch(ctx, op, payload)
	case "tempo.search", "tempo.trace":
		return s.tempo.Dispatch(ctx, op, payload)
	case "qdrant.search", "qdrant.upsert":
		return s.qdrant.Dispatch(ctx, op, payload)

	// 能力类(skill / llm / embedding)
	case "skill.list", "skill.execute":
		return s.skill.Dispatch(ctx, op, payload)
	case "llm.chat":
		return s.llm.Dispatch(ctx, op, payload)
	case "embedding.embed":
		return s.embed.Dispatch(ctx, op, payload)

	// 远端控制类(edge / audit / notify)
	case "edge.run_shell", "edge.copy_file", "edge.list_dir":
		return s.edge.Dispatch(ctx, op, payload)
	case "audit.write":
		return s.auditAdp.Dispatch(ctx, op, payload)
	case "notify.send":
		return s.notify.Dispatch(ctx, op, payload)

	// 配置类(config / vault / metrics)
	case "config.get", "config.set":
		return s.config.Dispatch(ctx, op, payload)
	case "vault.get_ref":
		return s.vault.Dispatch(ctx, op, payload)
	case "metrics.write", "metrics.query":
		return s.metrics.Dispatch(ctx, op, payload)

	default:
		return nil, ErrUnknownOp
	}
}

// fireAudit 调用外部 AuditFunc,nil-safe。
//
// audit 写入失败不影响主调用(主调用结果已就绪);若 audit 自身抛 panic,
// SDK 层兜底 recover 避免拖死 plugin 调用链。
func (s *SDK) fireAudit(ctx context.Context, pluginID uint64, op string, latency time.Duration, err error) {
	if s.audit == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	s.audit(ctx, pluginID, op, latency, err)
}
