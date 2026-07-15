package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// QdrantAdapter 把 hostcall op 路由到 pluginhost.QdrantQueryAPI。
//
// 支持 op:
//   - "qdrant.search" payload: {collection, vector, limit}
//   - "qdrant.upsert"  payload: {collection, id, vector, payload}
type QdrantAdapter struct {
	iface pluginhost.QdrantQueryAPI
}

// NewQdrantAdapter 构造 QdrantAdapter;iface 可为 nil。
func NewQdrantAdapter(iface pluginhost.QdrantQueryAPI) *QdrantAdapter {
	return &QdrantAdapter{iface: iface}
}

// qdrantSearchPayload 是 qdrant.search 的入参。
type qdrantSearchPayload struct {
	Collection string    `json:"collection"`
	Vector     []float32 `json:"vector"`
	Limit      int       `json:"limit,omitempty"`
}

// qdrantUpsertPayload 是 qdrant.upsert 的入参。
//
// pluginhost.QdrantQueryAPI.Upsert 签名是 (collection, id, vector, payload);
// A 实际 qdrantx.Client.Upsert 是批量 []Point。wire-up 时由 thin adapter
// 把单点参数 pack 成 1-element slice。
type qdrantUpsertPayload struct {
	Collection string          `json:"collection"`
	ID         string          `json:"id"`
	Vector     []float32       `json:"vector"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// Dispatch 解析 payload 并调 QdrantQueryAPI.Search / Upsert。
func (a *QdrantAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "qdrant.search":
		var p qdrantSearchPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Collection == "" {
			return nil, errors.New("qdrant.search: collection required")
		}
		if len(p.Vector) == 0 {
			return nil, errors.New("qdrant.search: vector required")
		}
		// Search 返回 json.RawMessage(pluginhost 接口透传 qdrant 命中数组)。
		raw, err := a.iface.Search(ctx, p.Collection, p.Vector, p.Limit)
		if err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			return json.RawMessage("[]"), nil
		}
		return raw, nil

	case "qdrant.upsert":
		var p qdrantUpsertPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Collection == "" {
			return nil, errors.New("qdrant.upsert: collection required")
		}
		if p.ID == "" {
			return nil, errors.New("qdrant.upsert: id required")
		}
		if len(p.Vector) == 0 {
			return nil, errors.New("qdrant.upsert: vector required")
		}
		if err := a.iface.Upsert(ctx, p.Collection, p.ID, p.Vector, p.Payload); err != nil {
			return nil, err
		}
		// 写操作无返回体,统一返 {"ok": true}。
		return json.RawMessage(`{"ok":true}`), nil

	default:
		return nil, fmt.Errorf("qdrant adapter: unknown op %q", op)
	}
}
