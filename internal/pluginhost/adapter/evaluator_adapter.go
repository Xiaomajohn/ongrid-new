// Package adapter — evaluator_adapter.go:把 Kind == KindEvaluator 的
// capability 包成 pluginhost 占位的 Evaluator,通过 er.RegisterEvaluator 注册。
//
// A 的真实对接(调研结果,plan §14.2 假设 vs 实际):
//
//   - Plan §4 / §6.4 假设 internal/manager/biz/aiops/alertconfig export
//     `Evaluator` interface。
//     **实际情况:alertconfig 包只有 alert_rule_manager.go / draft_store_memory.go
//     / draft_validation.go 三个文件,没有任何 Evaluator 类型 export。**
//
//     alert 的 Evaluator 概念由 internal/manager/biz/alert/ 下的
//     PipelineEvaluator(alert/pipeline.go:82) 实现,它是 concrete struct
//     不是 interface;没有 plugin 侧可注入的 hook。
//
//   - pluginhost/deps.go:172 已定义占位:
//     type Evaluator interface { _phantom() }
//     与 LLMProvider / Node 同形的空 contract 占位。
//
// 解决方案(本文件实施):
//  1. 本地定义 Evaluator 接口,与 pluginhost.Evaluator 同形(_phantom 占位)。
//  2. evaluatorAdapter 持有 invoke 句柄,真实 Evaluate contract(传入 metric
//     / log 数据,返回是否触发告警)由 Phase 4 wire-up 时由 main.go 写
//     thin adapter 适配到 alert.PipelineEvaluator.RunOnce / 类似 hook。
//  3. plugin protocol 约定(写 plugin 时遵循):
//     request body: {"input": <任意 JSON,由 alert 评估决定>}
//     response body: {"fire": true, "severity": "...", "labels": {...}}
//
// Phase 4 main.go wire-up 时,thin adapter 同时实现
// pluginhost.EvaluatorRegistrar(RegisterEvaluator(name, pluginhost.Evaluator))
// 和本地的 localEvaluatorRegistrar(RegisterEvaluator(name, adapter.Evaluator)),
// 内部维护 name → adapter.Evaluator 映射;真实调用链可能是:
//
//	alert pipeline → custom-hook → thin adapter.Evaluate(pluginhost.Evaluator)
//	  → invokeRouter.Invoke(...)
//
// import 白名单:pluginhost 子包(registry/invoke/manifest) + stdlib。
// **严禁 import pluginhost 主包 / internal/manager/biz/alertconfig**
// (alertconfig.Evaluator 类型不存在,且 import alertconfig 会拉 alert 子树
// 的诸多 transitive 依赖,Phase 1+2 已用 pluginhost/deps.go 占位规避)。
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

// Evaluator 是 A 包 alertconfig.Evaluator 的占位 interface。
//
// pluginhost/deps.go:172:
//
//	type Evaluator interface { _phantom() }
//
// 本 adapter 包独立定义同名 Evaluator(同样 _phantom 占位),与 pluginhost
// 解耦。Phase 4 wire-up 时 main.go 写 thin adapter 桥接两者。
type Evaluator interface {
	_phantom()
}

// _phantom 是 Evaluator 标记方法(同 LLMProvider,见 llm_adapter.go)。
func (a *evaluatorAdapter) _phantom() {}

// localEvaluatorRegistrar 是 pluginhost.EvaluatorRegistrar 的本地镜像。
//
// pluginhost/deps.go:124:
//
//	type EvaluatorRegistrar interface { RegisterEvaluator(name string, e Evaluator) error }
//
// 同其它 4 个 adapter 的考量,本 adapter 不复用 pluginhost.EvaluatorRegistrar。
type localEvaluatorRegistrar interface {
	RegisterEvaluator(name string, e Evaluator) error
}

// evaluatorAdapter 把 plugin capability 包成 Evaluator 占位实现。
type evaluatorAdapter struct {
	plugin *registry.PluginInstance
	cap    *registry.Capability
	invoke InvokeFunc
}

// RegisterEvaluators 把 reg 中所有 Kind == KindEvaluator 的 capability 包成
// evaluatorAdapter,通过 er.RegisterEvaluator 注册。
//
// 注册名规则:"pluginhost:<pack_id>:<cap_name>"。
//
// 行为约定与 RegisterSkills 对齐(见 skill_adapter.go)。
func RegisterEvaluators(
	reg *registry.Registry,
	invokeRouter *invoke.Router,
	er localEvaluatorRegistrar,
) (registered int, err error) {
	if reg == nil || invokeRouter == nil || er == nil {
		return 0, nil
	}
	log := slog.Default().With(slog.String("component", "pluginhost.adapter.evaluator"))
	for _, plugin := range reg.List() {
		if plugin == nil {
			continue
		}
		for _, cap := range plugin.Capabilities {
			if cap == nil {
				continue
			}
			if cap.Kind != string(manifest.KindEvaluator) {
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
			ad := &evaluatorAdapter{plugin: pluginCopy, cap: capCopy, invoke: inv}
			name := evaluatorName(pluginCopy, capCopy)
			if rerr := er.RegisterEvaluator(name, ad); rerr != nil {
				log.Warn("pluginhost adapter: evaluator.RegisterEvaluator failed",
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

// evaluatorName 构造 evaluator 注册名。
func evaluatorName(p *registry.PluginInstance, c *registry.Capability) string {
	return "pluginhost:" + p.PackID + ":" + c.Name
}
