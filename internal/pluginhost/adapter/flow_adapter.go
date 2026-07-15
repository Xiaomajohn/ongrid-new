// Package adapter — flow_adapter.go:把 Kind == KindWorkflowNode 的 capability
// 包成 pluginhost 占位的 Node,通过 wr.RegisterNode 注册。
//
// A 的真实对接(调研结果,plan §14.2 假设 vs 实际):
//
//   - Plan §4 / §6.5 假设 internal/manager/biz/flow export `Node` interface。
//     **实际情况:flow 包的节点类型抽象是 `NodeSpec`(flow/noderegistry.go:50),
//     不是 interface;RegisterNode(*NodeSpec) 是 concrete struct 的注册入口。**
//
//   - NodeSpec 字段(flow/noderegistry.go:50):
//     Type / Kind(NodeKind)/ Category / LabelZh / LabelEn /
//     Ports([]string)/ ConfigFields([]ConfigFieldSpec)/
//     OutputShape([]string)/ Execute(ExecuteFunc)
//
//   - ExecuteFunc(flow/noderegistry.go:33):
//     func(ctx context.Context, x Executors, cfg map[string]any, rc *RunContext)
//     (NodeResult, error)
//
//   - pluginhost/deps.go:183 已定义占位:
//     type Node interface { _phantom() }
//     与 LLMProvider / Evaluator 同形的空 contract 占位。
//
// 解决方案(本文件实施):
//  1. 本地定义 Node 接口,与 pluginhost.Node 同形(_phantom 占位)。
//  2. nodeAdapter 持有 invoke 句柄,真实 Execute contract 由 Phase 4 wire-up
//     时由 main.go 写 thin adapter 把本地 Node 适配到 flow.NodeSpec(典型做法:
//     构造一个 *flow.NodeSpec{Type: "plugin_"+name, Kind: KindAction, ...,
//     Execute: func(ctx, x Executors, cfg, rc) (NodeResult, error) {
//     把 cfg + rc 序列化为 invoke payload,调 invokeRouter.Invoke,
//     响应反序列化为 NodeResult。})。
//  3. plugin protocol 约定(写 plugin 时遵循):
//     request body:  {"cfg": {...}, "rc": {...}}
//     response body: {"output": {...}, "port": "next", "vars": {...}}
//
// Phase 4 main.go wire-up 时,thin adapter 同时实现
// pluginhost.WorkflowRegistrar(RegisterNode(name, pluginhost.Node))
// 和本地的 localWorkflowRegistrar(RegisterNode(name, adapter.Node)),
// 内部维护 name → adapter.Node + 构造的 NodeSpec 映射;每次 RegisterNode
// 把 adapter.Node 包成 *flow.NodeSpec(填 LabelZh / LabelEn / Kind / Ports
// / ConfigFields 等元数据,Execute 桥接到 invokeRouter),然后调
// flow.RegisterNode(spec)。
//
// import 白名单:pluginhost 子包(registry/invoke/manifest) + stdlib。
// **严禁 import pluginhost 主包 / internal/manager/biz/flow**
// (flow.Node interface 不存在,且 import flow 会拉 manager 子树的诸多
// transitive 依赖,Phase 1+2 已用 pluginhost/deps.go 占位规避)。
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

// Node 是 A 包 flow.Node 的占位 interface。
//
// pluginhost/deps.go:183:
//
//	type Node interface { _phantom() }
//
// 本 adapter 包独立定义同名 Node(同样 _phantom 占位),与 pluginhost 解耦。
// Phase 4 wire-up 时 main.go 写 thin adapter 桥接两者,把本地 Node 包成
// *flow.NodeSpec,Execute 桥接到 invokeRouter。
type Node interface {
	_phantom()
}

// _phantom 是 Node 标记方法(同 LLMProvider / Evaluator,见 llm_adapter.go)。
func (a *nodeAdapter) _phantom() {}

// localWorkflowRegistrar 是 pluginhost.WorkflowRegistrar 的本地镜像。
//
// pluginhost/deps.go:133:
//
//	type WorkflowRegistrar interface { RegisterNode(name string, n Node) error }
//
// 同其它 5 个 adapter 的考量,本 adapter 不复用 pluginhost.WorkflowRegistrar。
type localWorkflowRegistrar interface {
	RegisterNode(name string, n Node) error
}

// nodeAdapter 把 plugin capability 包成 Node 占位实现。
type nodeAdapter struct {
	plugin *registry.PluginInstance
	cap    *registry.Capability
	invoke InvokeFunc
}

// RegisterWorkflowNodes 把 reg 中所有 Kind == KindWorkflowNode 的 capability
// 包成 nodeAdapter,通过 wr.RegisterNode 注册。
//
// 注册名规则:"pluginhost:<pack_id>:<cap_name>"。
// 行为约定与 RegisterSkills 对齐(见 skill_adapter.go)。
func RegisterWorkflowNodes(
	reg *registry.Registry,
	invokeRouter *invoke.Router,
	wr localWorkflowRegistrar,
) (registered int, err error) {
	if reg == nil || invokeRouter == nil || wr == nil {
		return 0, nil
	}
	log := slog.Default().With(slog.String("component", "pluginhost.adapter.flow"))
	for _, plugin := range reg.List() {
		if plugin == nil {
			continue
		}
		for _, cap := range plugin.Capabilities {
			if cap == nil {
				continue
			}
			if cap.Kind != string(manifest.KindWorkflowNode) {
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
			ad := &nodeAdapter{plugin: pluginCopy, cap: capCopy, invoke: inv}
			name := workflowNodeName(pluginCopy, capCopy)
			if rerr := wr.RegisterNode(name, ad); rerr != nil {
				log.Warn("pluginhost adapter: workflow.RegisterNode failed",
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

// workflowNodeName 构造 workflow node 注册名。
func workflowNodeName(p *registry.PluginInstance, c *registry.Capability) string {
	return "pluginhost:" + p.PackID + ":" + c.Name
}
