package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// EmbeddingAdapter 把 hostcall op 路由到 pluginhost.EmbeddingAPI。
//
// 支持 op:
//   - "embedding.embed" payload: {texts}
//
// pluginhost.EmbeddingAPI 签名匹配 A 的 embedding.Embedder.Embed
// (ctx, texts []string) → ([][]float32, error);wire-up 时直接注入
// embedding.OpenAIEmbedder 等具体实现。
//
// **约束**:hostcall 包禁止 import embedding(embedding 链 cgo
// onnxruntime,会污染 pluginhost 编译选项);A 实际 client 在 Phase 4
// main.go 中包成 pluginhost.EmbeddingAPI 注入,hostcall 不直接接触。
type EmbeddingAdapter struct {
	iface pluginhost.EmbeddingAPI
}

// NewEmbeddingAdapter 构造 EmbeddingAdapter;iface 可为 nil。
func NewEmbeddingAdapter(iface pluginhost.EmbeddingAPI) *EmbeddingAdapter {
	return &EmbeddingAdapter{iface: iface}
}

// embeddingEmbedPayload 是 embedding.embed 的入参。
type embeddingEmbedPayload struct {
	Texts []string `json:"texts"`
}

// Dispatch 解析 payload 并调 EmbeddingAPI.Embed,把 [][]float32 序列化为 JSON。
func (a *EmbeddingAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	if op != "embedding.embed" {
		return nil, fmt.Errorf("embedding adapter: unknown op %q", op)
	}

	var p embeddingEmbedPayload
	if err := unmarshalPayload(payload, &p); err != nil {
		return nil, err
	}
	if len(p.Texts) == 0 {
		return nil, errors.New("embedding.embed: texts required")
	}

	vecs, err := a.iface.Embed(ctx, p.Texts)
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(vecs)
}
