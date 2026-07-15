package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// EdgeAdapter 把 hostcall op 路由到 pluginhost.EdgeCommandAPI。
//
// 支持 op:
//   - "edge.run_shell" payload: {edge_id, cmd, timeout?}
//   - "edge.copy_file"  payload: {edge_id, src, dst}
//   - "edge.list_dir"   payload: {edge_id, path}
//
// pluginhost.EdgeCommandAPI.RunShell 返回 string(stdout 文本);
// CopyFile / ListDir 没有返回体,hostcall adapter 序列化约定:
//
//   - copy_file 成功返 {"ok": true}
//   - list_dir  成功返 {"entries": [...]}  // 字符串数组
type EdgeAdapter struct {
	iface pluginhost.EdgeCommandAPI
}

// NewEdgeAdapter 构造 EdgeAdapter;iface 可为 nil。
func NewEdgeAdapter(iface pluginhost.EdgeCommandAPI) *EdgeAdapter {
	return &EdgeAdapter{iface: iface}
}

// edgeRunShellPayload 是 edge.run_shell 的入参。
//
// edge_id / cmd 必填;timeout 可选(0 走 pluginhost 接口默认值或 A thin
// adapter 兜底)。
type edgeRunShellPayload struct {
	EdgeID  string        `json:"edge_id"`
	Cmd     string        `json:"cmd"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

// edgeCopyFilePayload 是 edge.copy_file 的入参。
type edgeCopyFilePayload struct {
	EdgeID string `json:"edge_id"`
	Src    string `json:"src"`
	Dst    string `json:"dst"`
}

// edgeListDirPayload 是 edge.list_dir 的入参。
type edgeListDirPayload struct {
	EdgeID string `json:"edge_id"`
	Path   string `json:"path"`
}

// Dispatch 解析 payload 并调 EdgeCommandAPI 对应方法。
func (a *EdgeAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	switch op {
	case "edge.run_shell":
		var p edgeRunShellPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.EdgeID == "" {
			return nil, errors.New("edge.run_shell: edge_id required")
		}
		if p.Cmd == "" {
			return nil, errors.New("edge.run_shell: cmd required")
		}
		out, err := a.iface.RunShell(ctx, p.EdgeID, p.Cmd, p.Timeout)
		if err != nil {
			return nil, err
		}
		// 序列化为 {"stdout": "..."} 以保留未来扩展 exit_code / stderr 字段。
		return json.Marshal(map[string]string{"stdout": out})

	case "edge.copy_file":
		var p edgeCopyFilePayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.EdgeID == "" || p.Src == "" || p.Dst == "" {
			return nil, errors.New("edge.copy_file: edge_id/src/dst required")
		}
		if err := a.iface.CopyFile(ctx, p.EdgeID, p.Src, p.Dst); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"ok":true}`), nil

	case "edge.list_dir":
		var p edgeListDirPayload
		if err := unmarshalPayload(payload, &p); err != nil {
			return nil, err
		}
		if p.EdgeID == "" || p.Path == "" {
			return nil, errors.New("edge.list_dir: edge_id/path required")
		}
		entries, err := a.iface.ListDir(ctx, p.EdgeID, p.Path)
		if err != nil {
			return nil, err
		}
		if entries == nil {
			entries = []string{}
		}
		return json.Marshal(map[string][]string{"entries": entries})

	default:
		return nil, fmt.Errorf("edge adapter: unknown op %q", op)
	}
}
