package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// MetricsAdapter 把 hostcall op 路由到 pluginhost.MetricsAPI。
//
// 支持 op:
//   - "metrics.write" payload: {name, value, labels?}
//   - "metrics.query" payload: {promql}
//
// pluginhost.MetricsAPI 是 pluginhost 内部 stub interface;A 实际
// `internal/pkg/prom` 包只暴露 NewRegistry / Handler,**没有** write /
// query 方法。wire-up 时 main.go 要么:
//
//   - 复用 prom.NewRegistry() + Prometheus go-client 写自定义 metrics
//   - 或封装一个 pluginhost_intern_metrics_codegen 模式(Phase 3 决定)
//
// 无论如何,hostcall 这一层只调 pluginhost.MetricsAPI 抽象,A 怎么
// 实现由 thin adapter 决定。
//
// **红线**:hostcall 的 metrics 调用 **不打 trace span**,避免污染 A
// 指标基数 — 审计 component 字段固定为 "pluginhost"(由 AuditFunc 写入,
// 与 MetricsAdapter 无关)。
type MetricsAdapter struct {
	iface pluginhost.MetricsAPI
}

// NewMetricsAdapter 构造 MetricsAdapter;iface 可为 nil。
func NewMetricsAdapter(iface pluginhost.MetricsAPI) *MetricsAdapter {
	return &MetricsAdapter{iface: iface}
}

// metricsWritePayload 是 metrics.write 的入参。
//
// name / value 必填;labels 可选(任意 KV,pluginhost.MetricsAPI.Write
// 容忍空 map)。
type metricsWritePayload struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Labels map[string]string `json:"labels,omitempty"`
}

// metricsQueryPayload 是 metrics.query 的入参。
//
// promql 必填,走 pluginhost.MetricsAPI.Query,返 json.RawMessage(Prom
// 响应体或 thin adapter 包装)。
type metricsQueryPayload struct {
	PromQL string `json:"promql"`
}

// Dispatch 解析 payload 并调 MetricsAPI.Write / Query。
func (a *MetricsAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "metrics.write":
		var p metricsWritePayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Name == "" {
			return nil, errors.New("metrics.write: name required")
		}
		if err := a.iface.Write(ctx, p.Name, p.Value, p.Labels); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"ok":true}`), nil

	case "metrics.query":
		var p metricsQueryPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.PromQL == "" {
			return nil, errors.New("metrics.query: promql required")
		}
		raw, err := a.iface.Query(ctx, p.PromQL)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return json.RawMessage(`{"resultType":"unknown","result":null}`), nil
		}
		return raw, nil

	default:
		return nil, fmt.Errorf("metrics adapter: unknown op %q", op)
	}
}
