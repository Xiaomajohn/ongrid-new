package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// AuditAdapter 把 hostcall op 路由到 pluginhost.AuditAPI。
//
// 支持 op:
//   - "audit.write" payload: {action, resource, actor, details}
//
// pluginhost.AuditAPI.Write 签名是 (ctx, comp, action, resource, actor, details);
// hostcall adapter 把 comp 固定为 "pluginhost"(plugin 端不能伪造),
// 其余字段从 payload 取。
//
// 注意:Sdk.go 在每次 Call 结束后已经通过 AuditFunc 写一条
// component="pluginhost" 的审计(plugin_id / op / latency / err);
// 本 adapter 是"plugin 自身想再写一条业务审计"的额外入口,两条记录
// 互补不冲突。
type AuditAdapter struct {
	iface pluginhost.AuditAPI
}

// auditComponentName hostcall adapter 写审计时固定使用的组件名。
//
// 防止 plugin 通过 payload 注入 comp="system" 等覆盖,污染 A 的审计归类。
const auditComponentName = "pluginhost"

// NewAuditAdapter 构造 AuditAdapter;iface 可为 nil。
func NewAuditAdapter(iface pluginhost.AuditAPI) *AuditAdapter {
	return &AuditAdapter{iface: iface}
}

// auditWritePayload 是 audit.write 的入参。
//
// action / resource / actor 必填;details 任意 JSON 对象。
type auditWritePayload struct {
	Action   string         `json:"action"`
	Resource string         `json:"resource"`
	Actor    string         `json:"actor"`
	Details  map[string]any `json:"details,omitempty"`
}

// Dispatch 解析 payload 并调 AuditAPI.Write,comp 固定为 "pluginhost"。
func (a *AuditAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	if op != "audit.write" {
		return nil, fmt.Errorf("audit adapter: unknown op %q", op)
	}

	var p auditWritePayload
	if err := unmarshalPayload(payload, &p); err != nil {
		return nil, err
	}
	if p.Action == "" {
		return nil, errors.New("audit.write: action required")
	}
	if p.Resource == "" {
		return nil, errors.New("audit.write: resource required")
	}
	if p.Actor == "" {
		// actor 可空(AuditAPI.Write 允许空 actor),但 hostcall 默认填
		// "plugin:{plugin_id}" 在 wire-up 之前这里没有 plugin_id,
		// 留给 AuditAPI 自身的默认值处理。
		p.Actor = "plugin"
	}

	if err := a.iface.Write(ctx, auditComponentName, p.Action, p.Resource, p.Actor, p.Details); err != nil {
		return nil, err
	}
	return json.RawMessage(`{"ok":true}`), nil
}
