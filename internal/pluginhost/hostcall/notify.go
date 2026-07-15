package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// NotifyAdapter 把 hostcall op 路由到 pluginhost.NotifyAPI。
//
// 支持 op:
//   - "notify.send" payload: {channel, message}
//
// pluginhost.NotifyAPI.Send 签名是 (ctx, channel, msg) error;
// channel 是通知通道名(由 A 的 notify.Router 注册的 sender 名)。
//
// **未暴露**:pluginhost.NotifyAPI.SendVia(DB-stored 通道)在 Phase 1
// 不暴露给 plugin — plugin 通过 channel 名字路由,SendVia 是 admin 工具
// 用的高级入口。Phase 3 视需求再补。
type NotifyAdapter struct {
	iface pluginhost.NotifyAPI
}

// NewNotifyAdapter 构造 NotifyAdapter;iface 可为 nil。
func NewNotifyAdapter(iface pluginhost.NotifyAPI) *NotifyAdapter {
	return &NotifyAdapter{iface: iface}
}

// notifySendPayload 是 notify.send 的入参。
//
// channel 是 notify.Router 已注册的通道名;"message" 是任意 JSON,
// hostcall adapter 透传给 A 的 notify.Sender.Send(Sender.Message 结构
// 由 wire-up thin adapter 决定,Phase 4 写)。
type notifySendPayload struct {
	Channel string          `json:"channel"`
	Message json.RawMessage `json:"message"`
}

// Dispatch 解析 payload 并调 NotifyAPI.Send。
func (a *NotifyAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	if op != "notify.send" {
		return nil, fmt.Errorf("notify adapter: unknown op %q", op)
	}

	var p notifySendPayload
	if err := unmarshalPayload(payload, &p); err != nil {
		return nil, err
	}
	if p.Channel == "" {
		return nil, errors.New("notify.send: channel required")
	}
	if len(p.Message) == 0 {
		return nil, errors.New("notify.send: message required")
	}

	if err := a.iface.Send(ctx, p.Channel, p.Message); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"ok":true}`), nil
}
