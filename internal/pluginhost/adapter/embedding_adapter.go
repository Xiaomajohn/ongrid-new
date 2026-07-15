// Package adapter — embedding_adapter.go:把 Kind == KindEmbedProvider 的
// capability 包成 pluginhost 内部的 Embedder,通过 er.RegisterEmbedder 注册。
//
// ⚠️ **严禁 import internal/pkg/embedding**(链式拉 onnxruntime_go,Windows
// cgo 不可用;`go list -deps ./internal/pkg/embedding` 末段显示
// `github.com/yalue/onnxruntime_go` / `github.com/anush008/fastembed-go`)。
//
// A 的真实对接(调研结果):
//
//   - A 的 embedding.Embedder(internal/pkg/embedding/embedding.go:40):
//     Dim() int
//     Embed(ctx, texts []string) ([][]float32, error)
//
//   - A 的 embedding 包**没有 Registry 概念**(grep 全包,只有
//     `func New(cfg Config) (Embedder, error)` 单例构造;main.go 通常持有
//     一个全局 *openAIEmbedder / *localEmbedder)。plan §4 / §6.3 假设的
//     `embedding.Registry.RegisterEmbedder` 在 A 包里不存在。
//
//   - pluginhost/deps.go:113 的 EmbeddingRegistrar.RegisterEmbedder 使用
//     `embedding.Embedder` 类型,这意味着任何 import pluginhost 的包都
//     会被 Go 编译器强拉 onnxruntime(失败)。
//
// 解决方案(本文件实施):
//
//  1. 本地定义 `type Embedder interface { Embed(...) }`(故意省略 Dim(),
//     因为 plugin capability 不一定报维度;Phase 4 main.go wire-up 时
//     由 thin adapter 补 Dim 字段,典型做法是查 cap.Metadata["dim"]
//     或写死为 0 表示 unknown)。
//
//  2. 本地定义 localEmbeddingRegistrar,用本地 Embedder 而非
//     embedding.Embedder(避免 import chain)。
//
//  3. embeddingAdapter 持有 invoke 句柄,Embed 时把 texts 序列化为 JSON
//     透传给 invokeRouter.Invoke,响应反序列化为 [][]float32。
//
//  4. plugin protocol 约定(写 plugin 时遵循):
//     request  body: {"texts": ["..."]}
//     response body: [[0.1, 0.2, ...], [0.3, 0.4, ...]]
//
// Phase 4 main.go wire-up 时,需要写一个 thin adapter 同时实现
// pluginhost.EmbeddingRegistrar(RegisterEmbedder(name, embedding.Embedder))
// 和本地的 localEmbeddingRegistrar(RegisterEmbedder(name, adapter.Embedder)),
// 内部维护 name → adapter.Embedder 映射;每次 RegisterEmbedder 把本地
// Embedder 包成 embedding.Embedder(补 Dim 方法),注册到 A 的全局
// embedding.New(...) multiplex pattern(具体由 Phase 4 D2 决定是用
// multi-embedder 路由器还是覆盖式 replace)。
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ongridio/ongrid/internal/pluginhost/invoke"
	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// Embedder 是 plugin capability 的向量化最小 contract。
//
// 与 embedding.Embedder(embedding.go:40)同形但故意不同名,以避免
// import internal/pkg/embedding(链式拉 onnxruntime)。
//
// Dim() 在本接口里**故意省略**:plugin capability 可以不报维度(等调用方
// 自己 cast []float32 长度)。Phase 4 wire-up 时,thin adapter 把本地
// Embedder 适配到 embedding.Embedder 时,Dim 补 0(unknown)或读
// cap.Metadata["dim"]。
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// localEmbeddingRegistrar 是 pluginhost.EmbeddingRegistrar 的本地镜像,用本地
// Embedder 而非 embedding.Embedder。
//
// pluginhost/deps.go:113:
//
//	type EmbeddingRegistrar interface {
//	    RegisterEmbedder(name string, e embedding.Embedder) error
//	}
//
// 因 embedding.Embedder 类型来自 internal/pkg/embedding(链式拉 onnxruntime),
// 本 adapter 不复用 pluginhost.EmbeddingRegistrar。Phase 4 wire-up 时
// main.go 写 thin adapter 桥接两者。
type localEmbeddingRegistrar interface {
	RegisterEmbedder(name string, e Embedder) error
}

// embeddingAdapter 把 plugin capability 包成本地 Embedder。
type embeddingAdapter struct {
	plugin *registry.PluginInstance
	cap    *registry.Capability
	invoke InvokeFunc
}

// Embed 实现本地 Embedder interface:把 texts 序列化为 JSON,透传给 plugin,
// 响应反序列化为 [][]float32。
//
// request body:{"texts": ["..."]}
// response body:[[0.1, 0.2, ...], ...]  ← 必须严格对齐(下标 N 对应 texts[N])
//
// 错误:
//   - resp.Error 非空 → errors.New(resp.Error)
//   - JSON 反序列化失败 → 包装错误(提示 plugin 协议不匹配)
func (a *embeddingAdapter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if a.invoke == nil {
		return nil, errors.New("pluginhost embeddingAdapter: nil invoke func")
	}
	payload := embedRequest{Texts: texts}
	params, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("pluginhost embeddingAdapter: marshal request: %w", err)
	}
	resp, ierr := a.invoke(ctx, params)
	if ierr != nil {
		return nil, ierr
	}
	var out [][]float32
	if uerr := json.Unmarshal(resp, &out); uerr != nil {
		return nil, fmt.Errorf("pluginhost embeddingAdapter: response not [][]float32 (plugin=%s/%s): %w",
			a.plugin.PackID, a.cap.Name, uerr)
	}
	if len(out) != len(texts) {
		return nil, fmt.Errorf("pluginhost embeddingAdapter: response len %d != texts len %d (plugin=%s/%s)",
			len(out), len(texts), a.plugin.PackID, a.cap.Name)
	}
	return out, nil
}

// embedRequest 是 plugin 端看到的 embedding 请求 schema。
type embedRequest struct {
	Texts []string `json:"texts"`
}

// RegisterEmbedders 把 reg 中所有 Kind == KindEmbedProvider 的 capability
// 包成 embeddingAdapter,通过 er.RegisterEmbedder 注册到 Phase 4 main.go
// 提供的本地 embedding registrar。
//
// 注册名规则:"pluginhost:<pack_id>:<cap_name>"。
//
// 行为约定与 RegisterSkills 对齐(见 skill_adapter.go)。
func RegisterEmbedders(
	reg *registry.Registry,
	invokeRouter *invoke.Router,
	er localEmbeddingRegistrar,
) (registered int, err error) {
	if reg == nil || invokeRouter == nil || er == nil {
		return 0, nil
	}
	log := slog.Default().With(slog.String("component", "pluginhost.adapter.embedding"))
	for _, plugin := range reg.List() {
		if plugin == nil {
			continue
		}
		for _, cap := range plugin.Capabilities {
			if cap == nil {
				continue
			}
			if cap.Kind != string(manifest.KindEmbedProvider) {
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
			ad := &embeddingAdapter{plugin: pluginCopy, cap: capCopy, invoke: inv}
			name := embedderName(pluginCopy, capCopy)
			if rerr := er.RegisterEmbedder(name, ad); rerr != nil {
				log.Warn("pluginhost adapter: embedding.RegisterEmbedder failed",
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

// embedderName 构造 embedder 注册名。
func embedderName(p *registry.PluginInstance, c *registry.Capability) string {
	return "pluginhost:" + p.PackID + ":" + c.Name
}
