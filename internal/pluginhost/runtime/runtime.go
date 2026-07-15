// Package runtime 是 pluginhost 子系统的 transport 抽象层。
//
// 职责:把 A/B(in-process)调用方与 C 插件(子进程 / 远程 HTTP)之间的
// "一次能力调用"统一成一个 Runtime interface;Pool 在进程级持有
// 每个插件最多一个 Runtime 实例。
//
// 当前实现两种 transport:
//
//   - SubprocessRuntime:stdin/stdout JSON-RPC,长生命周期子进程,
//     30s 超时自动 kill,panic recover,stderr 持续收集后写 audit。
//   - HTTPRemoteRuntime:POST {url}/invoke + HMAC-SHA256 签名,
//     自带 CircuitBreaker(连续 5 次 5xx → 开 30s)。
//
// 本包不依赖 A(manager/iam/edgeagent/api/...)任何代码,只定义
// 协议与并发安全,不感知 A 业务。Phase 4 的 invoke/router 会
// 拿 Pool + PluginInstance + Capability + Request 直接调 Invoke。
//
// 注意:Go 标准库已有同名包 "runtime",本包被引用时必须用别名,例如
//
//	import rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Request 是一次能力调用的入参。
type Request struct {
	ID       string          // 请求 id,stdio 多路复用匹配响应行用
	CapName  string          // 调用的 capability 名,如 "issue.create"
	Params   json.RawMessage // capability 入参
	Caller   Caller          // 调用方上下文
	Deadline time.Time       // 截止时间(可选,零值表示无限)
}

// Response 是一次能力调用的出参;ID 与 req.ID 匹配。
type Response struct {
	ID     string          // 匹配 req.ID
	Result json.RawMessage // capability 返回值
	Error  string          // 错误描述(空 = 成功)
}

// Caller 描述本次调用的发起方,用于 hostcall 鉴权 / 审计 / 限流。
type Caller struct {
	Component string // "alert-pipeline" / "edge-x" / "web:user:42"
	UserID    uint64
	TenantID  uint64
	TraceID   string
}

// PluginInstance 描述一个已安装的插件实例,来自 registry 层。
type PluginInstance struct {
	ID             uint64
	TenantID       uint64
	PackID         string
	Version        string
	InstallPath    string
	Entry          string
	ManifestSHA256 string
	Enabled        bool
	TimeoutSeconds int // 默认 30,<=0 时 transport 内部兜底
}

// Capability 描述一个插件暴露的能力点。
type Capability struct {
	PluginID string
	Kind     string
	Name     string
	Class    string
	Schema   json.RawMessage
}

// Runtime 是 pluginhost 与 C 插件之间的 transport 抽象。
//
// 实现必须是并发安全的(由 transport 内部保证),但 Pool 上同一
// pluginID 的 Runtime 通常被 InvokeRouter 串行调用以避免子进程
// stdio 交错。
type Runtime interface {
	// Invoke 把 req 发给插件的 cap,并同步等待响应。错误包括但不限于:
	// ctx 超时 / 子进程崩溃 / 远程 5xx / 熔断打开 / 鉴权失败。
	Invoke(ctx context.Context, plugin *PluginInstance, cap *Capability, req Request) (Response, error)

	// Close 释放底层资源(子进程 / http 连接)。Close 后不应再 Invoke。
	Close() error
}

// Pool 是进程级 Runtime 注册表,key = pluginID(PluginInstance.PackID),
// 同一插件最多持有一个 Runtime 实例;重复 Add 返 ErrDuplicate。
//
// Pool 本身是并发安全的;CloseAll 在进程退出时调用,保证子进程被回收。
type Pool struct {
	mu        sync.Mutex
	instances map[string]Runtime
}

// NewPool 构造空 Pool。
func NewPool() *Pool {
	return &Pool{instances: make(map[string]Runtime)}
}

// ErrDuplicate 同一 pluginID 重复注册。
var ErrDuplicate = errors.New("runtime pool: duplicate plugin id")

// ErrNotFound pluginID 未注册。
var ErrNotFound = errors.New("runtime pool: plugin not found")

// Add 注入 pluginID -> Runtime 映射;pluginID 已存在返 ErrDuplicate。
func (p *Pool) Add(pluginID string, rt Runtime) error {
	if rt == nil {
		return errors.New("runtime pool: nil runtime")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.instances[pluginID]; ok {
		return ErrDuplicate
	}
	p.instances[pluginID] = rt
	return nil
}

// Get 读取 pluginID 对应的 Runtime;不存在返 (nil, false)。
func (p *Pool) Get(pluginID string) (Runtime, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rt, ok := p.instances[pluginID]
	return rt, ok
}

// Remove 摘除 pluginID 并 Close 底层 Runtime;pluginID 不存在则 no-op。
func (p *Pool) Remove(pluginID string) {
	p.mu.Lock()
	rt, ok := p.instances[pluginID]
	if ok {
		delete(p.instances, pluginID)
	}
	p.mu.Unlock()
	if ok && rt != nil {
		_ = rt.Close()
	}
}

// CloseAll 依次关闭所有 Runtime,常用于进程退出。
// 关闭顺序不保证;单个 Runtime 关闭失败不影响其他。
func (p *Pool) CloseAll() error {
	p.mu.Lock()
	rts := make([]Runtime, 0, len(p.instances))
	for _, rt := range p.instances {
		rts = append(rts, rt)
	}
	p.instances = make(map[string]Runtime)
	p.mu.Unlock()

	var firstErr error
	for _, rt := range rts {
		if err := rt.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Count 返回当前已注册 Runtime 数量,用于诊断/health 接口。
func (p *Pool) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.instances)
}
