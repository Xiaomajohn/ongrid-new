// Package runtime 是 pluginhost 子系统的 transport 抽象层。
//
// 职责:把 A/B(in-process)调用方与 C 插件(子进程 / 远程 HTTP)
// 之间的"一次能力调用"统一成一个 Runtime interface;Pool 在
// 进程级持有每个插件最多一个 Runtime 实例。
//
// 当前 phase 支持三种 transport(本次只交付前两种,inproc
// 留作后续 phase):
//
//   - SubprocessRuntime:stdin/stdout JSON-RPC,单次 Invoke 即
//     拉起一个短生命周期子进程,30s 超时自动 kill,panic recover,
//     stderr 收集到 stderrBuf。
//   - HTTPRemoteRuntime:POST {url}/invoke + HMAC-SHA256 签名,
//     自带 CircuitBreaker(连续 5 次 5xx → 开 30s;期间直接返
//     ErrBreakerOpen)。
//   - InprocRuntime:后续 phase 由 inproc.go 实现(in-process
//     stub,类型安全的本地 plugin)。
//
// 本包不依赖 A(manager/iam/edgeagent/api/...)任何代码,只定义
// 协议与并发安全,不感知 A 业务。Phase 4 的 invoke/router 会
// 拿 Pool + registry.PluginInstance + registry.Capability +
// Request 直接调 Invoke。
//
// 注意:Go 标准库已有同名包 "runtime",本包被引用时必须用别名,例如
//
//	import rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"
package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// Request 是一次能力调用的入参。
//
// Caller 描述"谁"在调用(发起 invoke 的 A 子系统 + 多租户标识);
// TraceID 是分布式追踪 ID,与 Caller 解耦独立上报 slog / 写 audit。
// Deadline 是可选的截止时间(零值表示无限,transport 内部
// 取 min(Timeout, ctx deadline, Deadline))。
type Request struct {
	ID       string          // 请求 id,stdio 多路复用匹配响应用
	CapName  string          // 调用的 capability 名,如 "issue.create"
	Params   json.RawMessage // capability 入参
	Caller   Caller          // 调用方上下文
	Deadline time.Time       // 可选截止时间(零值 = 无)
	TraceID  string          // 分布式追踪 ID
}

// Response 是一次能力调用的出参;ID 与 req.ID 匹配。
//
// Error 非空时表示调用失败(插件主动返错);Result 在 Error 为空时
// 才有意义;两者互斥。
type Response struct {
	ID     string          // 匹配 req.ID
	Result json.RawMessage // capability 返回值
	Error  string          // 错误描述(空 = 成功)
}

// Caller 描述本次调用的发起方,用于 hostcall 鉴权 / 审计 / 限流。
//
// 注意:Caller 不含 TraceID,因为 TraceID 已经独立放在 Request 上 ——
// Caller 只描述"调用方是谁",不描述"这条调用属于哪条 trace"。
type Caller struct {
	Component string // "alert-pipeline" / "edge-x" / "web:user:42"
	UserID    uint64 // 发起用户 ID(0 = 系统调用)
	TenantID  uint64 // 多租户隔离 ID(0 = 全局)
}

// Runtime 是 pluginhost 与 C 插件之间的 transport 抽象。
//
// 实现必须是并发安全的(由 transport 内部互斥锁保证);Phase 4 的
// InvokeRouter 会保证同一 pluginID 的调用串行执行以避免子进程
// stdio 响应行交错。
type Runtime interface {
	// Invoke 把 req 发给插件的 cap,并同步等待响应。
	// 错误包括但不限于:ctx 超时 / 子进程崩溃 / 远程 5xx /
	// 熔断打开 / 参数序列化失败。
	Invoke(ctx context.Context, plugin *registry.PluginInstance, cap *registry.Capability, req Request) (Response, error)

	// Close 释放底层资源(子进程句柄 / http 连接)。Close 后不应再 Invoke。
	Close() error
}

// Pool 是进程级 Runtime 注册表,key = packID(PluginInstance.PackID,
// 即 plugin.json.name 的 PascalCase)。
//
// 同 pluginID 重复 Add 时**覆盖**原有 Runtime(transport 层面 Pool
// 只关心 holder,不感知生命周期;重复 wire-up 在 biz 层应先 Remove
// / Close 再 Add)。Pool 本身是并发安全的;CloseAll 在进程退出时
// 调用,保证子进程被回收。
type Pool struct {
	mu        sync.Mutex
	instances map[string]Runtime
}

// NewPool 构造空 Pool。
func NewPool() *Pool {
	return &Pool{instances: make(map[string]Runtime)}
}

// Add 注册 packID → Runtime 映射。
//
// packID 为空或 rt 为 nil 时直接丢弃(等价 no-op);已有同名 packID
// 会**覆盖**原值并 Close 旧的 transport(避免泄漏子进程)。
func (p *Pool) Add(packID string, rt Runtime) {
	if packID == "" || rt == nil {
		return
	}
	p.mu.Lock()
	old, existed := p.instances[packID]
	p.instances[packID] = rt
	p.mu.Unlock()
	if existed && old != nil {
		_ = old.Close()
	}
}

// Get 读取 packID 对应的 Runtime;不存在返 (nil, false)。
func (p *Pool) Get(packID string) (Runtime, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rt, ok := p.instances[packID]
	return rt, ok
}

// CloseAll 依次关闭所有 Runtime,常用于进程退出。
//
// 关闭顺序不保证;单个 Runtime 关闭失败不影响其他(错误被吞,
// 不阻断其他 transport 回收)。
func (p *Pool) CloseAll() {
	p.mu.Lock()
	rts := make([]Runtime, 0, len(p.instances))
	for _, rt := range p.instances {
		rts = append(rts, rt)
	}
	p.instances = make(map[string]Runtime)
	p.mu.Unlock()

	for _, rt := range rts {
		_ = rt.Close()
	}
}
