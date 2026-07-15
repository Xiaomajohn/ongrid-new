// Package adapter — notify_adapter.go:把 Kind == KindNotifier 的 capability
// 包成 notify.Sender,通过 nr.RegisterSender(...) 注册到 A 的 notify.Router。
//
// 与 A 的真实对接(调研结果):
//
//   - A 的 notify.Sender interface(internal/pkg/notify/notify.go:34):
//     Name() string
//     Send(ctx, msg notify.Message) error
//
//   - A 的 notify.Message struct(notify.go:23):Subject / Body / Severity /
//     Source / DedupeKey / Labels / OccurredAt。Severity ∈ {info, warning,
//     critical};OccurredAt 零值时 Router 自动填 time.Now().UTC()。
//
//   - A 的 notify.Router 结构(notify.go:40):Router 是 concrete struct,不是
//     interface;channels map 在 NewRouter / NewFromConfig 时一次性初始化,
//     **无 RegisterSender 方法**(计划假设的 Router.RegisterSender 不存在)。
//
//     → Phase 4 main.go wire-up 时,需要写一个 NotifyRegistrar 包装:每次
//     RegisterSender 把 sender 存到一个 plugin-sender 表,Send 阶段构造一个
//     notify.Router(在 Send 路径上拼接 plugin sender + 内置 sender)。或更
//     简单的做法:扩展 A 的 notify 包加 RegisterSender(改动 1 个文件,~20 行,
//     与 B 解耦)。两种方案都已在 final report 列出,Phase 4 D2 决定。
//
//   - A 的 notify.Severity 常量:info / warning / critical。
//
// import 白名单:internal/pkg/notify + pluginhost 子包(registry/invoke/manifest)
// + stdlib。**严禁 import pluginhost 主包**(因 deps.go 拉 embedding → onnxruntime)。
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/notify"
	"github.com/ongridio/ongrid/internal/pluginhost/invoke"
	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// localNotifyRegistrar 是 pluginhost.NotifyRegistrar 的本地镜像。
//
// pluginhost/deps.go:99:
//
//	type NotifyRegistrar interface { RegisterSender(notify.Sender) error }
//
// 同 skill_adapter 的考量,本 adapter 不复用 pluginhost.NotifyRegistrar;
// Phase 4 wire-up 时 main.go 写 thin adapter 桥接两者。
type localNotifyRegistrar interface {
	RegisterSender(notify.Sender) error
}

// notifyAdapter 把 plugin capability 包成 notify.Sender。
type notifyAdapter struct {
	plugin *registry.PluginInstance
	cap    *registry.Capability
	invoke InvokeFunc
}

// Name 返回 sender 在 notify.Router 中的 channel name。
//
// 命名规则:"pluginhost:<pack_id>:<cap_name>"。前缀 pluginhost: 与 A
// 既有 channel(webhook/slack/feishu/dingtalk)区分,避免 phase 4 wire-up
// 时撞名。
func (a *notifyAdapter) Name() string {
	return notifyChannelName(a.plugin, a.cap)
}

// Send 把 notify.Message 包成 JSON payload 透传给 plugin 的 notify capability。
//
// payload 字段:
//   - message:原 notify.Message 内容(subject / body / severity / ...)
//   - channel:本 adapter 的 Name()(回传给 plugin 用于记账)
//   - plugin_id / cap_name:plugin 端记日志 / 审计用
//   - timestamp:UTC 当前时间,避免 plugin 端时钟漂移
//
// Send 返 nil 的语义:plugin 成功接收并触发外发。plugin 内部外发失败
// 由 plugin 自己处理(重试 / dead letter),不通过本层冒泡。
func (a *notifyAdapter) Send(ctx context.Context, msg notify.Message) error {
	if a.invoke == nil {
		return errors.New("pluginhost notifyAdapter: nil invoke func")
	}
	payload := notifyEnvelope{
		Message:   msg,
		Channel:   a.Name(),
		PluginID:  a.plugin.PackID,
		CapName:   a.cap.Name,
		Timestamp: time.Now().UTC(),
	}
	params, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pluginhost notifyAdapter: marshal envelope: %w", err)
	}
	if _, ierr := a.invoke(ctx, params); ierr != nil {
		return ierr
	}
	return nil
}

// notifyChannelName 构造 channel name。
func notifyChannelName(p *registry.PluginInstance, c *registry.Capability) string {
	return "pluginhost:" + p.PackID + ":" + c.Name
}

// notifyEnvelope 是 plugin 端看到的请求 schema。
//
// json tag 都对齐 plugin host protocol 的 lower_snake 风格(虽然 Go
// encoding/json 默认就是字段名,这里显式标是为了 plugin 端读代码时一目了然)。
type notifyEnvelope struct {
	Message   notify.Message `json:"message"`
	Channel   string         `json:"channel"`
	PluginID  string         `json:"plugin_id"`
	CapName   string         `json:"cap_name"`
	Timestamp time.Time      `json:"timestamp"`
}

// RegisterNotifiers 把 reg 中所有 Kind == KindNotifier 的 capability 包成
// notifyAdapter,通过 nr.RegisterSender 注册到 A 的 notify.Router。
//
// 行为约定与 RegisterSkills 对齐:
//   - reg / invokeRouter / nr 任一为 nil → no-op
//   - 单个 cap 注册失败 → log warning + 跳过(不影响其它)
//   - 返回 (registered, nil),err 永远为 nil
//
// 备注:plugin 的 notify capability 触发 Send 时,A 端的 notify.Router
// 会用 channel 名查 sender。如果 wire-up 用了 multi-Router-per-send 方案,
// 需要在 Send path 上把 plugin sender 也插到该次构造的 Router 里,见
// final report。
func RegisterNotifiers(
	reg *registry.Registry,
	invokeRouter *invoke.Router,
	nr localNotifyRegistrar,
) (registered int, err error) {
	if reg == nil || invokeRouter == nil || nr == nil {
		return 0, nil
	}
	log := slog.Default().With(slog.String("component", "pluginhost.adapter.notify"))
	for _, plugin := range reg.List() {
		if plugin == nil {
			continue
		}
		for _, cap := range plugin.Capabilities {
			if cap == nil {
				continue
			}
			if cap.Kind != string(manifest.KindNotifier) {
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
			ad := &notifyAdapter{plugin: pluginCopy, cap: capCopy, invoke: inv}
			if rerr := nr.RegisterSender(ad); rerr != nil {
				log.Warn("pluginhost adapter: notify.RegisterSender failed",
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
