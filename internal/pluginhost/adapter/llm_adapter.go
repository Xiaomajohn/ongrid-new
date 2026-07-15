// Package adapter — llm_adapter.go:把 Kind == KindLLMProvider 的 capability
// 包成 pluginhost 占位的 LLMProvider,通过 lr.RegisterProvider(name, p) 注册。
//
// 与 A 的真实对接(调研结果,plan §14.2 假设 vs 实际):
//
//   - Plan §4 / §6.3 假设 internal/pkg/llm 已 export `Provider` 类型。
//     **实际情况:A 的 llm 包只有 llm.Client(interface)+ llm.ChatReq +
//     llm.ChatResp + llm.Message + llm.ToolCall + llm.Usage + llm.ToolSchema,
//     没有 export Provider 类型。**
//
//   - llm.Client(internal/pkg/llm/client.go:126):
//     Chat(ctx, ChatReq) (*ChatResp, error)
//     这是 OpenAI 风格的单一 Chat contract;Provider 的多模型路由由
//     llm.MultiClient(llm/router.go:67)完成,内部按 ChatReq.Provider
//     字符串派发到预构 sub-client。
//
//   - pluginhost/deps.go:147 已定义占位:
//     type LLMProvider interface { _phantom() }
//     本 adapter 包独立定义同名 LLMProvider(同样 _phantom 占位),
//     与 pluginhost.LLMProvider 同形但不 import pluginhost 包。
//
// Phase 4 main.go wire-up 时,需要:
//  1. 在 llm 包(或 pluginhost 包)补 export `Provider` 类型(或 alias
//     llm.Client 接口)。
//  2. 写 thin adapter 把本地的 LLMProvider 适配到 llm.MultiClient 的
//     sub-client(典型做法:构造一个 llm.Client,Chat 时把 ChatReq 序列化为
//     JSON 透传给 invokeRouter.Invoke,响应反序列化为 ChatResp)。
//
// import 白名单:pluginhost 子包(registry/invoke/manifest) + stdlib。
// **严禁 import pluginhost 主包 / internal/pkg/embedding / internal/pkg/llm
// (避免链式拉 onnxruntime,且 llm 包当前未 export Provider 类型)**。
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/ongridio/ongrid/internal/pluginhost/invoke"
	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// LLMProvider 是 A 包 llm.Provider 的占位 interface,与 pluginhost/deps.go:147
// 的 LLMProvider 同形(空 contract + `_phantom()` 标记,Phase 3 在 A 包补齐
// 真实 contract 后再替换)。
//
// 用 `_phantom()` 方法名而非 `interface{}` 是为了让 Phase 4 wire-up 时,
// 把 pluginhost.LLMProvider 直接当成本地 LLMProvider 使用(同形);main.go
// 写 thin adapter 包装 llm.MultiClient 时,内部 sub-client 仍实现本接口的
// 实际 contract(届时 `_phantom()` 会被 Chat() 等真实方法替换)。
type LLMProvider interface {
	_phantom()
}

// _phantom 是 LLMProvider 标记方法,任何实现 LLMProvider 的类型必须有它。
// 在 adapter 包的 llmAdapter / Evaluator / Node 等占位类型上统一补上,
// 以保证类型契约与 pluginhost/deps.go 的占位对齐。
//
// 实际语义:Phase 3 期间,LLMProvider 没有真实方法;Phase 3+ 真实 contract
// 引入后,该方法被替换(整段 LLMProvider interface 被替换为 Chat() 等)。
func (a *llmAdapter) _phantom() {}

// localLLMRegistrar 是 pluginhost.LLMRegistrar 的本地镜像。
//
// pluginhost/deps.go:107:
//
//	type LLMRegistrar interface { RegisterProvider(name string, p LLMProvider) error }
//
// 同 skill_adapter 的考量,本 adapter 不复用 pluginhost.LLMRegistrar;
// Phase 4 wire-up 时 main.go 写 thin adapter 桥接两者。
type localLLMRegistrar interface {
	RegisterProvider(name string, p LLMProvider) error
}

// llmAdapter 把 plugin capability 包成 LLMProvider 占位实现。
//
// 由于当前 LLMProvider 没有真实 Chat() 方法,本结构只持有 invoke 句柄
// + 标识符(供 Phase 4 wire-up 时回填真实 contract 时使用)。Chat 真实
// 调用链在 main.go 的 thin adapter 里完成:
//
//	llm.MultiClient.Chat → sub-client.Chat → pluginhost.adapter.llmAdapter.Chat
//	  → invokeRouter.Invoke(ctx, plugin.ID, cap.Name, json(ChatReq), opts)
//	  → 反序列化 ChatResp
type llmAdapter struct {
	plugin *registry.PluginInstance
	cap    *registry.Capability
	invoke InvokeFunc
}

// RegisterLLMProviders 把 reg 中所有 Kind == KindLLMProvider 的 capability
// 包成 llmAdapter,通过 lr.RegisterProvider(name, ad) 注册。
//
// 注册名规则:"pluginhost:<pack_id>:<cap_name>",与 A 内置 provider
// (openai / anthropic / zhipu / gemini)不撞名。
//
// 行为约定与 RegisterSkills 对齐(见 skill_adapter.go)。
func RegisterLLMProviders(
	reg *registry.Registry,
	invokeRouter *invoke.Router,
	lr localLLMRegistrar,
) (registered int, err error) {
	if reg == nil || invokeRouter == nil || lr == nil {
		return 0, nil
	}
	log := slog.Default().With(slog.String("component", "pluginhost.adapter.llm"))
	for _, plugin := range reg.List() {
		if plugin == nil {
			continue
		}
		for _, cap := range plugin.Capabilities {
			if cap == nil {
				continue
			}
			if cap.Kind != string(manifest.KindLLMProvider) {
				continue
			}
			pluginCopy := plugin
			capCopy := cap
			inv := func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
				opts := invoke.InvokeOptions{}
				resp, ierr := invokeRouter.Invoke(ctx, pluginCopy.ID, capCopy.Name, params, opts)
				if ierr != nil {
					return nil, ierr
				}
				if resp.Error != "" {
					return nil, errors.New(resp.Error)
				}
				return resp.Result, nil
			}
			ad := &llmAdapter{plugin: pluginCopy, cap: capCopy, invoke: inv}
			name := llmProviderName(pluginCopy, capCopy)
			if rerr := lr.RegisterProvider(name, ad); rerr != nil {
				log.Warn("pluginhost adapter: llm.RegisterProvider failed",
					slog.String("pack", pluginCopy.PackID),
					slog.String("cap", capCopy.Name),
					slog.Any("err", rerr))
				continue
			}
			registered++
		}
	}
	return registered, nil
}

// llmProviderName 构造 provider 注册名。
func llmProviderName(p *registry.PluginInstance, c *registry.Capability) string {
	return "pluginhost:" + p.PackID + ":" + c.Name
}
