// Package invoke 是 pluginhost 子系统的 invoke 路由层。
//
// 职责:把 A/B(in-process)调用方对 plugin 能力的请求,经由
// registry 解析成 (PluginInstance, Capability),再交给 runtime.Pool
// 中对应的 Runtime(子进程 / 远程 HTTP / inproc)执行。
//
// 本包严格限制在 pluginhost 内部子包之间调用(registry / runtime),
// 不依赖 A 的任何子包(manager / iam / edgeagent / pkg/embedding /
// pkg/notify / skill / api),保证 Phase 1.5 可以在不引入 cgo 的前提下
// 单独 build 通过(尤其 Windows onnxruntime_go 不兼容)。
package invoke

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/registry"
	rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"
)

// 默认超时(秒)。Phase 2 接入 model.plugin_instance.timeout_seconds
// 后,优先用 manifest / DB 里的值;此处仅作兜底。
const defaultTimeoutSeconds = 30

// 错误码。调用方用 errors.Is 区分失败类型。
var (
	// ErrUnknownCapability:registry 中不存在 (pluginID, capName) 这条记录。
	ErrUnknownCapability = errors.New("invoke: unknown capability")
	// ErrPluginDisabled:plugin 整体被 SetEnabled(false),不接 Invoke。
	ErrPluginDisabled = errors.New("invoke: plugin disabled")
	// ErrRuntimeUnavailable:registry 有 capability 但 Pool 没加载对应 Runtime
	//(典型场景:Phase 2 Install 之后还没 Add 进 Pool,或进程重启间隙)。
	ErrRuntimeUnavailable = errors.New("invoke: runtime unavailable")
	// ErrInvalidParams:params 不是合法 JSON / 与 capability schema 不匹配。
	// Phase 1.5 暂未实现 schema 强校验,仅占位,Phase 2 接入后启用。
	ErrInvalidParams = errors.New("invoke: invalid params")
)

// AuditFunc 审计回调签名。允许为 nil,Router 在 audit 为 nil 时直接跳过。
//
// 语义:每次 Invoke 结束(成功或失败)调用一次,err == nil 表示成功。
// 由调用方(Phase 4 server handler 或 biz)注入,把 latency / 错误写到
// A 的 audit_logs(comp="pluginhost")或 plugin_audits 表。
type AuditFunc func(ctx context.Context, pluginID uint64, capName string, latency time.Duration, err error)

// Router 是 invoke 子系统的核心路由层。
//
// 依赖:
//   - reg:*registry.Registry,提供 Lookup / List 等只读查询
//   - pool:*rtp.Pool,提供 Get(单个 plugin 的 Runtime 实例)
//   - audit:可选审计回调
//
// 并发安全:audit 字段用 mu 保护(可运行时替换);reg / pool 自身
// 已并发安全,Router 不再额外加锁。
type Router struct {
	mu    sync.RWMutex
	reg   *registry.Registry
	pool  *rtp.Pool
	audit AuditFunc
}

// InvokeOptions 控制单次调用的语义。
//
// Caller:发起方上下文(由 server handler 从 HTTP header / JWT 解析)。
// Deadline:零值时 Router 用 defaultTimeoutSeconds(30s)兜底;
// 非零值时由调用方控制(如 web UI 长任务)。
// TraceID:链路追踪 ID;空字符串时 Router 不参与 trace 关联,
// 由调用方注入。
type InvokeOptions struct {
	Caller   rtp.Caller
	Deadline time.Time
	TraceID  string
}

// NewRouter 构造 Router。reg / pool 不可为 nil,否则在 Invoke 时
// 会 panic(由调用方保证)。本函数零副作用,不做 IO。
func NewRouter(reg *registry.Registry, pool *rtp.Pool) *Router {
	return &Router{
		reg:  reg,
		pool: pool,
	}
}

// WithAudit 链式注入审计回调。返回 *Router 便于一行完成构造:
//
//	NewRouter(reg, pool).WithAudit(auditFn)
//
// Phase 4 server handler 在 wire-up 时调用一次,后续不替换。
func (r *Router) WithAudit(f AuditFunc) *Router {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audit = f
	return r
}

// Invoke 是 Router 的主入口。
//
// 流程:
//  1. registry.Lookup → 拿 *Capability;不存在返 ErrUnknownCapability
//  2. registry.List   → 找 plugin instance,检查 Enabled;
//     plugin 不存在或 disabled → ErrUnknownCapability / ErrPluginDisabled
//  3. pool.Get        → 拿 rtp.Runtime;不存在返 ErrRuntimeUnavailable
//  4. 把 registry.PluginInstance 字段映射到 rtp.PluginInstance(给 runtime 用)
//  5. 构造 rtp.Request,Deadline 零值兜底 30s
//  6. rt.Invoke 并度量 latency,audit 回调(若有)异步无阻塞调用
//
// 错误约定:reg / pool / runtime 内部 error 原样透传;Router 自身
// 只产生上述 4 个 ErrXXX。
func (r *Router) Invoke(
	ctx context.Context,
	pluginID uint64,
	capName string,
	params json.RawMessage,
	opts InvokeOptions,
) (rtp.Response, error) {
	if pluginID == 0 {
		return rtp.Response{}, ErrUnknownCapability
	}

	// 1. Lookup capability(registry 接 uint64 instanceID + capName 二元组查)
	cap, ok := r.reg.LookupCapabilityByInstanceID(pluginID, capName)
	if !ok {
		return rtp.Response{}, ErrUnknownCapability
	}

	// 2. 找 plugin instance 并检查 Enabled
	inst, ok := r.reg.LookupByID(pluginID)
	if !ok {
		return rtp.Response{}, ErrUnknownCapability
	}
	if !inst.Enabled {
		return rtp.Response{}, ErrPluginDisabled
	}

	// 3. Pool Get runtime(Pool key 是 packID 字符串,从 instance 取)
	rt, ok := r.pool.Get(inst.PackID)
	if !ok {
		return rtp.Response{}, ErrRuntimeUnavailable
	}

	// 4. 构造 rtp.Request
	deadline := opts.Deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(time.Duration(defaultTimeoutSeconds) * time.Second)
	}
	req := rtp.Request{
		ID:       generateReqID(),
		CapName:  capName,
		Params:   params,
		Caller:   opts.Caller,
		Deadline: deadline,
		TraceID:  opts.TraceID,
	}

	// 5. 调 runtime 并度量 latency;直接传 registry.PluginInstance 与 registry.Capability
	//(runtime.Runtime 接口按 plan §6.1 接受 registry 类型)
	start := time.Now()
	resp, err := rt.Invoke(ctx, inst, cap, req)
	latency := time.Since(start)

	// audit 回调(若有)同步执行;回调内部 panic 由调用方负责 recover。
	r.mu.RLock()
	audit := r.audit
	r.mu.RUnlock()
	if audit != nil {
		audit(ctx, pluginID, capName, latency, err)
	}

	return resp, err
}

// generateReqID 生成形如 "inv-<unixnano>-<rand4>" 的请求 ID。
//
// unixnano 保证单调递增 + 多调用方不冲突;rand4(4 个十六进制字符)
// 在同一纳秒内并发时避免 ID 撞车。
func generateReqID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("inv-%d-%04x", time.Now().UnixNano(), binary.BigEndian.Uint16(b[:]))
}
