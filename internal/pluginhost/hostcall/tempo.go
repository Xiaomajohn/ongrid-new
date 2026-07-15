package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// TempoAdapter 把 hostcall op 路由到 pluginhost.TempoQueryAPI。
//
// 支持 op:
//   - "tempo.search" payload: {query, limit}
//   - "tempo.trace"  payload: {trace_id}
type TempoAdapter struct {
	iface pluginhost.TempoQueryAPI
}

// NewTempoAdapter 构造 TempoAdapter;iface 可为 nil。
func NewTempoAdapter(iface pluginhost.TempoQueryAPI) *TempoAdapter {
	return &TempoAdapter{iface: iface}
}

// tempoSearchPayload 是 tempo.search 的入参。
//
// query 是 TraceQL 表达式;q 兼容字段名(plugin 可能传 q)。
// limit 可选(0 走 pluginhost 接口默认值)。
type tempoSearchPayload struct {
	Query string `json:"query"`
	Q     string `json:"q,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// tempoTracePayload 是 tempo.trace 的入参。
//
// traceID 必填。
type tempoTracePayload struct {
	TraceID string `json:"trace_id"`
}

// Dispatch 解析 payload 并调 TempoQueryAPI.Search / Trace。
func (a *TempoAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "tempo.search":
		var p tempoSearchPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		q := p.Query
		if q == "" {
			q = p.Q
		}
		if q == "" {
			return nil, errors.New("tempo.search: query required")
		}
		return a.iface.Search(ctx, q, p.Limit)

	case "tempo.trace":
		var p tempoTracePayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.TraceID == "" {
			return nil, errors.New("tempo.trace: trace_id required")
		}
		return a.iface.Trace(ctx, p.TraceID)

	default:
		return nil, fmt.Errorf("tempo adapter: unknown op %q", op)
	}
}
