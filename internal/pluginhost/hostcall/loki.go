package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// timeUnixNano 把 unix 纳秒 int64 转为 time.Time;Loki / 通用 hostcall
// 协议层用 unix nano 是为了与 promquery/logquery 内部精度对齐。
func timeUnixNano(nano int64) time.Time {
	if nano <= 0 {
		return time.Time{}
	}
	return time.Unix(0, nano)
}

// LokiAdapter 把 hostcall op 路由到 pluginhost.LokiQueryAPI。
//
// 支持 op:
//   - "loki.query_range" payload: {query, start, end, limit}
type LokiAdapter struct {
	iface pluginhost.LokiQueryAPI
}

// NewLokiAdapter 构造 LokiAdapter;iface 可为 nil。
func NewLokiAdapter(iface pluginhost.LokiQueryAPI) *LokiAdapter {
	return &LokiAdapter{iface: iface}
}

// lokiQueryRangePayload 是 loki.query_range 的入参。
//
// query / start / end 必填;limit 可选(0 走 pluginhost 接口默认值)。
type lokiQueryRangePayload struct {
	Query string `json:"query"`
	Start int64  `json:"start,omitempty"` // unix nano
	End   int64  `json:"end,omitempty"`   // unix nano
	Limit int    `json:"limit,omitempty"`
}

// Dispatch 解析 payload 并调 LokiQueryAPI.QueryRange。
func (a *LokiAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	if op != "loki.query_range" {
		return nil, fmt.Errorf("loki adapter: unknown op %q", op)
	}

	var p lokiQueryRangePayload
	if err := unmarshalPayload(payload, &p); err != nil {
		return nil, err
	}
	if p.Query == "" {
		return nil, errors.New("loki.query_range: query required")
	}

	// Loki 的 start/end 在 plugin 协议层用 unix nano(更精确,与
	// promquery/logquery 内部对齐),LokiQueryAPI 接受 time.Time,
	// 这里做转换。
	start := timeUnixNano(p.Start)
	end := timeUnixNano(p.End)
	if start.IsZero() || end.IsZero() {
		return nil, errors.New("loki.query_range: start/end required (unix nano)")
	}
	if !end.After(start) {
		return nil, errors.New("loki.query_range: end must be after start")
	}

	return a.iface.QueryRange(ctx, p.Query, start, end, p.Limit)
}
