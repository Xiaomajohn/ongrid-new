package hostcall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost"
)

// VaultAdapter 把 hostcall op 路由到 pluginhost.VaultAPI。
//
// 支持 op:
//   - "vault.get_ref" payload: {name}  → {"ref": "..."}
//
// **核心安全保证**:VaultAdapter **永远只返回凭据"引用名"(ref name)**,
// 绝不返明文密码 / token / API key。明文由 A 的 biz 层解密注入到目标
// 进程(例如 skill 执行时),plugin 只能"使用"凭据,不能"看到"凭据。
//
// pluginhost.VaultAPI.GetRef 签名是 (ctx, name) (ref, err) — 本身就是
// "只返引用"语义;wire-up 时 thin adapter 把 A 的 secret/store.Repo
// GetByName 的 sealed Secret 包成 ref 字符串(name or id)。
type VaultAdapter struct {
	iface pluginhost.VaultAPI
}

// NewVaultAdapter 构造 VaultAdapter;iface 可为 nil。
func NewVaultAdapter(iface pluginhost.VaultAPI) *VaultAdapter {
	return &VaultAdapter{iface: iface}
}

// vaultGetRefPayload 是 vault.get_ref 的入参。
type vaultGetRefPayload struct {
	Name string `json:"name"`
}

// Dispatch 解析 payload 并调 VaultAPI.GetRef,只返引用名。
func (a *VaultAdapter) Dispatch(ctx context.Context, op string, payload json.RawMessage) (json.RawMessage, error) {
	if a.iface == nil {
		return nil, ErrNotWired
	}
	if op != "vault.get_ref" {
		return nil, fmt.Errorf("vault adapter: unknown op %q", op)
	}

	var p vaultGetRefPayload
	if err := unmarshalPayload(payload, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, errors.New("vault.get_ref: name required")
	}

	ref, err := a.iface.GetRef(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		return nil, errors.New("vault.get_ref: empty ref returned (likely plugin requested a non-existent credential)")
	}
	return json.Marshal(map[string]string{"ref": ref})
}
