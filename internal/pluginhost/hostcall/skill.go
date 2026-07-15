package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/skill"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// SkillAdapter 把 hostcall op 路由到 pluginhost.SkillLookupAPI。
//
// 支持 op:
//   - "skill.list"    payload: 空 / {}
//   - "skill.execute" payload: {key, params}
//
// 注意:pluginhost.SkillLookupAPI.List 返回 ([]skill.Metadata, error),
// hostcall adapter 需要把 []Metadata 序列化成 JSON 数组;Execute 直接
// 透传 (json.RawMessage, error)。
type SkillAdapter struct {
	iface pluginhost.SkillLookupAPI
}

// NewSkillAdapter 构造 SkillAdapter;iface 可为 nil。
func NewSkillAdapter(iface pluginhost.SkillLookupAPI) *SkillAdapter {
	return &SkillAdapter{iface: iface}
}

// skillExecutePayload 是 skill.execute 的入参。
type skillExecutePayload struct {
	Key    string          `json:"key"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Dispatch 解析 payload 并调 SkillLookupAPI.List / Execute。
func (a *SkillAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "skill.list":
		mds, err := a.iface.List(ctx)
		if err != nil {
			return nil, err
		}
		return marshalSkillMetadata(mds)

	case "skill.execute":
		var p skillExecutePayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.Key == "" {
			return nil, errors.New("skill.execute: key required")
		}
		return a.iface.Execute(ctx, p.Key, p.Params)

	default:
		return nil, fmt.Errorf("skill adapter: unknown op %q", op)
	}
}

// marshalSkillMetadata 把 []skill.Metadata 序列化为 JSON 数组。
//
// skill.Metadata 已经 export 且 json tag 完备(见 internal/skill/types.go),
// 直接 json.Marshal 即可。
func marshalSkillMetadata(mds []skill.Metadata) (json.RawMessage, error) {
	if len(mds) == 0 {
		return json.RawMessage("[]"), nil
	}
	return json.Marshal(mds)
}
