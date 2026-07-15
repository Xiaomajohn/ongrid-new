// Package adapter 把 pluginhost 的 capability 包成 A 各子系统已 export 的接口。
//
// 本文件:把 Kind == KindAITool / KindSkillRunner 的 capability 包成
// skill.Executor,通过 sr.RegisterOne(...) 注册到 A 的 skill.Registry。
//
// 与 A 的真实对接(调研结果):
//
//   - A 的 skill.Executor interface(skill/types.go:200):
//     Metadata() Metadata
//     Execute(ctx, params json.RawMessage) (json.RawMessage, error)
//
//   - A 的 skill.Metadata struct(skill/types.go:100):Key 必须满足
//     skill.validKey 的 [a-z0-9_]+ 约束;Name / Description 必填;
//     Class ∈ {ClassSafe, ClassMutating, ClassDangerous, ""};
//     Scope ∈ {ScopeHost, ScopeManager, ""}。
//
//   - A 的 skill.Register(e Executor) Metadata(skill/registry.go:31):
//     是全局注册入口,内部 panic-on-validation-failure / panic-on-duplicate-Key。
//     本 adapter 不直接 import skill.Register;通过 localSkillRegistrar.RegisterOne
//     让 Phase 4 的 main.go wire-up 负责把 RegisterOne 转发到 skill.Register。
//
// import 白名单:pluginhost 子包(registry/invoke/manifest)+ internal/skill +
// stdlib。**严禁 import internal/pkg/embedding(链式拉 onnxruntime_go,
//
//	Windows cgo 不可用);pluginhost 包本身因 deps.go import embedding,
//	在 Windows 上亦无法编译,故本 adapter 不 import pluginhost 主包**。
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
	"github.com/ongridio/ongrid/internal/skill"
)

// InvokeFunc 是 invoke.Router.Invoke 的简化 wrapper:把 pluginID / capName /
// opts 闭包在闭包里,只暴露 (ctx, params) -> (json.RawMessage, error) 形态,
// 与 skill.Executor.Execute / notify.Sender.Send 等 A 接口 contract 对齐。
//
// 失败时:err != nil 直接透传 invoke error;resp.Error 非空时包成 errors.New(resp.Error)。
//
// 本类型在 skill_adapter.go 中声明,供本包其它 5 个 adapter 文件复用(同一包内
// 可见,无重复 import)。
type InvokeFunc func(ctx context.Context, params json.RawMessage) (json.RawMessage, error)

// localSkillRegistrar 是 pluginhost.SkillRegistrar 的本地镜像。
//
// pluginhost/deps.go:93:
//
//	type SkillRegistrar interface { RegisterOne(skill.Executor) error }
//
// 本 adapter 包不复用 pluginhost.SkillRegistrar(因 pluginhost 包因 deps.go
// import embedding → onnxruntime_go,Windows cgo 不可用)。Phase 4 main.go
// wire-up 阶段,会写一个 thin adapter 同时实现 pluginhost.SkillRegistrar +
// 本地 localSkillRegistrar,典型实现是把 RegisterOne(e) 转发到 A 的
// skill.Register(e)。
type localSkillRegistrar interface {
	RegisterOne(skill.Executor) error
}

// skillAdapter 把单个 plugin capability 包成 skill.Executor。
//
// 持有:
//   - plugin:registry 中的 PluginInstance(用于构造 Key 等元数据)
//   - cap:具体 capability 描述
//   - invoke:对 invoke.Router.Invoke 的闭包封装,Execute 时直接调
type skillAdapter struct {
	plugin *registry.PluginInstance
	cap    *registry.Capability
	invoke InvokeFunc
}

// Metadata 实现 skill.Executor。
//
// Key 拼装规则:plugin.PackID + "." + cap.Name 经 [a-z0-9_]+ 归一化
// 后,前缀加 "pluginhost_"(确保不与 A 既有 skill 撞 Key)。归一化策略:
//   - [a-z0-9] 保留
//   - [A-Z] → 小写
//   - 其它字符 → '_'
//
// 兜底 Description:cap.Metadata["description"] 不存在或非 string 时,
//
//	用 fmt.Sprintf 生成 "pluginhost adapter for <pack>.<cap>"。
func (a *skillAdapter) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         skillKey(a.plugin, a.cap),
		Name:        a.cap.Name,
		Description: skillDescription(a.plugin, a.cap),
		Class:       skillClassFromString(a.cap.Class),
		Scope:       skillScopeFromKind(a.cap.Kind),
		Category:    a.cap.Kind,
		// Params 留 nil:registry.Capability.Schema 是 JSON Schema(raw),
		// 与 skill.ParamSchema 的简单结构不一一对应;Phase 4 wire-up 若
		// 需要把 schema 注入到 LLM 工具定义,可以让 SkillRegistrar 实现
		// RawSchemaProvider 扩展(把 schema 原样塞进 JSONSchema())。
		Params: nil,
	}
}

// Execute 实现 skill.Executor:把 params 透传给 invokeRouter,然后把
// resp.Result 直接返回;非空 resp.Error 包成 error。
func (a *skillAdapter) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	if a.invoke == nil {
		return nil, errors.New("pluginhost skillAdapter: nil invoke func")
	}
	return a.invoke(ctx, params)
}

// skillKey 构造满足 skill.validKey 的 Key。
func skillKey(p *registry.PluginInstance, c *registry.Capability) string {
	raw := p.PackID + "." + c.Name
	b := make([]byte, 0, len("pluginhost_")+len(raw))
	b = append(b, "pluginhost_"...)
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			b = append(b, ch)
		case ch >= 'A' && ch <= 'Z':
			b = append(b, ch+'a'-'A')
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}

// skillDescription 从 cap.Metadata["description"] 取值,失败时兜底。
func skillDescription(p *registry.PluginInstance, c *registry.Capability) string {
	if desc, ok := c.Metadata["description"].(string); ok && desc != "" {
		return desc
	}
	return fmt.Sprintf("pluginhost adapter for %s.%s", p.PackID, c.Name)
}

// skillClassFromString 把 registry.Capability.Class 映射到 skill.Class。
// 未识别值兜底 ClassSafe(skill.Metadata.EffectiveClass() 也兜底 safe,
// 但显式写更清晰)。
func skillClassFromString(s string) skill.Class {
	switch s {
	case string(skill.ClassSafe):
		return skill.ClassSafe
	case string(skill.ClassMutating):
		return skill.ClassMutating
	case string(skill.ClassDangerous):
		return skill.ClassDangerous
	default:
		return skill.ClassSafe
	}
}

// skillScopeFromKind:KindSkillRunner 映射到 ScopeManager(进程内执行,
// 不走 edge tunnel);其它(含 KindAITool)默认 ScopeHost。
func skillScopeFromKind(kind string) skill.Scope {
	if kind == string(manifest.KindSkillRunner) {
		return skill.ScopeManager
	}
	return skill.ScopeHost
}

// RegisterSkills 把 reg 中所有 Kind == KindAITool / KindSkillRunner 的
// capability 包成 skillAdapter,通过 sr.RegisterOne 注册到 A 的 skill.Registry。
//
// 行为约定:
//   - reg / invokeRouter / sr 任一为 nil → no-op,返回 (0, nil)
//   - Metadata.Validate() 失败的 cap → log warning + 跳过(避免一个 cap
//     坏掉导致 skill.Register 整批 panic)
//   - sr.RegisterOne 返回 error → log warning + 跳过(不影响其它 cap)
//   - 整体成功数返回 registered;err 永远为 nil(best-effort)
//
// Phase 4 main.go wire-up 时,典型 localSkillRegistrar 实现是直接转发到
// skill.Register(e)。届时若需 panic recover,在线程中加 recover 即可。
func RegisterSkills(
	reg *registry.Registry,
	invokeRouter *invoke.Router,
	sr localSkillRegistrar,
) (registered int, err error) {
	if reg == nil || invokeRouter == nil || sr == nil {
		return 0, nil
	}
	log := slog.Default().With(slog.String("component", "pluginhost.adapter.skill"))
	for _, plugin := range reg.List() {
		if plugin == nil {
			continue
		}
		for _, cap := range plugin.Capabilities {
			if cap == nil {
				continue
			}
			if cap.Kind != string(manifest.KindAITool) && cap.Kind != string(manifest.KindSkillRunner) {
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
			ad := &skillAdapter{plugin: pluginCopy, cap: capCopy, invoke: inv}
			// Metadata.Validate() 失败不 panic,直接跳过。
			if verr := ad.Metadata().Validate(); verr != nil {
				log.Warn("pluginhost adapter: skill metadata invalid; skip",
					slog.String("pack", pluginCopy.PackID),
					slog.String("cap", capCopy.Name),
					slog.Any("err", verr))
				continue
			}
			if rerr := sr.RegisterOne(ad); rerr != nil {
				log.Warn("pluginhost adapter: skill.RegisterOne failed",
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
