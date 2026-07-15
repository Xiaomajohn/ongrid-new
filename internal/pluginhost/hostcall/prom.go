package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// PromAdapter 把 hostcall op 路由到 pluginhost.PromQueryAPI。
//
// 支持 op:
//   - "prom.query"      payload: {query, time?}
//   - "prom.query_range" payload: {query, start, end, step}
//
// A 实际接口(pluginhost.PromQueryAPI)的 Query / QueryRange 已经返回
// json.RawMessage;hostcall adapter 不做二次反序列化,直接透传给 plugin。
type PromAdapter struct {
	iface pluginhost.PromQueryAPI
}

// NewPromAdapter 构造 PromAdapter;iface 可为 nil(未 wire-up)。
func NewPromAdapter(iface pluginhost.PromQueryAPI) *PromAdapter {
	return &PromAdapter{iface: iface}
}

// promQueryPayload 是 prom.query 的入参。
//
// query 必填;time 可选(零值表示"now")。
type promQueryPayload struct {
	Query string    `json:"query"`
	Time  time.Time `json:"time,omitempty"`
}

// promQueryRangePayload 是 prom.query_range 的入参。
//
// query / start / end 必填;step 形如 "30s" / "1m" / "1h"。
type promQueryRangePayload struct {
	Query string    `json:"query"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Step  string    `json:"step"`
}

// Dispatch 解析 payload 并调 PromQueryAPI 对应方法。
func (a *PromAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "prom.query":
		var p promQueryPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Query == "" {
			return nil, errors.New("prom.query: query required")
		}
		// Phase 4 wire-up:promQueryAPI.Query 签名是 (ctx, promql);
		// A 实际 promquery.Client.Query 收 (ctx, expr, ts) — 把 ts 拆出去
		// 由 thin adapter 处理,pluginhost 接口本身只收 promql。
		return a.iface.Query(ctx, p.Query)

	case "prom.query_range":
		var p promQueryRangePayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Query == "" {
			return nil, errors.New("prom.query_range: query required")
		}
		if p.Start.IsZero() || p.End.IsZero() {
			return nil, errors.New("prom.query_range: start/end required")
		}
		if p.Step == "" {
			return nil, errors.New("prom.query_range: step required")
		}
		return a.iface.QueryRange(ctx, p.Query, p.Start, p.End, p.Step)

	default:
		return nil, fmt.Errorf("prom adapter: unknown op %q", op)
	}
}

// unmarshalPayload 是 hostcall 各 adapter 共用的 payload → struct 助手。
//
// nil / 空 payload 视为 {};其余按 json.Unmarshal 反序列化,字段缺失容忍。
func unmarshalPayload(payload json.RawMessage, dst any) error {
	if len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, dst)
}
