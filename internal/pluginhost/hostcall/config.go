package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// ConfigAdapter 把 hostcall op 路由到 pluginhost.ConfigAPI。
//
// 支持 op:
//   - "config.get" payload: {key}                  → {"value": "..."}
//   - "config.set" payload: {key, value}           → {"ok": true}
//
// pluginhost.ConfigAPI 是窄接口(只收 key / value),实际 A 的
// setting/store.Repo 是 (category, key) 双键 — wire-up 时 thin adapter
// 强制把 category 固定为 "pluginhost" 命名空间,避免 plugin 读写 A
// 其他命名空间(category="loki" / "prom" 等)。
type ConfigAdapter struct {
	iface pluginhost.ConfigAPI
}

// NewConfigAdapter 构造 ConfigAdapter;iface 可为 nil。
func NewConfigAdapter(iface pluginhost.ConfigAPI) *ConfigAdapter {
	return &ConfigAdapter{iface: iface}
}

// configGetPayload 是 config.get 的入参。
type configGetPayload struct {
	Key string `json:"key"`
}

// configSetPayload 是 config.set 的入参。
type configSetPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Dispatch 解析 payload 并调 ConfigAPI.Get / Set。
func (a *ConfigAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "config.get":
		var p configGetPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Key == "" {
			return nil, errors.New("config.get: key required")
		}
		v, err := a.iface.Get(ctx, p.Key)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"value": v})

	case "config.set":
		var p configSetPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Key == "" {
			return nil, errors.New("config.set: key required")
		}
		if err := a.iface.Set(ctx, p.Key, p.Value); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"ok":true}`), nil

	default:
		return nil, fmt.Errorf("config adapter: unknown op %q", op)
	}
}
