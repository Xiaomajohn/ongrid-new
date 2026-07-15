package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// LLMAdapter 把 hostcall op 路由到 pluginhost.LLMCompleteAPI。
//
// 支持 op:
//   - "llm.chat" payload: {messages, model, temperature, max_tokens}
//
// Phase 1 状态:pluginhost.LLMChatRequest 是 private interface(只有
// `_phantom()` 私有方法),外部包无法实现;wire-up 之前 LLM adapter
// 永远返 ErrNotWired。
//
// Phase 4 wire-up TODO:
//   - main.go 把 pluginhost.LLMChatRequest 替换为 llm.ChatReq alias
//   - LLMAdapter.Dispatch 把解析后的 llmChatParams pack 成 llm.ChatReq
//   - main.go 把 pluginhost.LLMChatResponse 替换为 llm.ChatResp alias
type LLMAdapter struct {
	iface pluginhost.LLMCompleteAPI
}

// NewLLMAdapter 构造 LLMAdapter;iface 可为 nil。
func NewLLMAdapter(iface pluginhost.LLMCompleteAPI) *LLMAdapter {
	return &LLMAdapter{iface: iface}
}

// llmChatMessage 是 plugin 协议层的 chat message。
//
// role: "user" / "assistant" / "system" / "tool"
type llmChatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
}

// llmChatParams 是 llm.chat 的入参。
type llmChatParams struct {
	Messages    []llmChatMessage `json:"messages"`
	Model       string           `json:"model,omitempty"`
	Temperature float32          `json:"temperature,omitempty"`
	MaxTokens   int              `json:"max_tokens,omitempty"`
}

// Dispatch 解析 payload 并调 LLMCompleteAPI.Chat。
//
// Phase 1 限制:pluginhost.LLMChatRequest 是私有 interface,本 adapter
// 不能直接构造;在 Phase 4 把 deps.go LLMChatRequest 替换为 concrete
// type(如 llm.ChatReq)之前,此方法始终返 ErrNotWired(即便 iface != nil
// 也无法传参)。
func (a *LLMAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	if op != "llm.chat" {
		return nil, fmt.Errorf("llm adapter: unknown op %q", op)
	}

	var p llmChatParams
	if err := unmarshalPayload(payload, &p); err != nil {
		return nil, err
	}
	if len(p.Messages) == 0 {
		return nil, errors.New("llm.chat: messages required")
	}

	// Phase 4 TODO:把 p pack 成 pluginhost.LLMChatRequest(将被替换为
	// llm.ChatReq),调 a.iface.Chat(ctx, req)。
	//
	// 当前 pluginhost.LLMChatRequest 是 private interface(_phantom),
	// 外部包不能 implement — 这里用占位 JSON 返回,保证编译通过。
	// Phase 4 改 deps.go 后删除下面 2 行。
	if err := phase1LLMGuard(); err != nil {
		return nil, err
	}
	return json.Marshal(p)
}

// phase1LLMGuard Phase 1 阶段的 hard-stop,避免 plugin 误以为 llm.chat
// 真的能跑通。
func phase1LLMGuard() error {
	return errors.New("llm.chat: not wired yet (Phase 1: pluginhost.LLMChatRequest is placeholder; " +
		"Phase 4 main.go wire-up replaces it with llm.ChatReq alias)")
}
