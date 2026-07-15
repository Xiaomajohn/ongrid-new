package hostcall

import (
	"encoding/json"
	"errors"
	"sync"
)

// ErrUndeclared op 未在 plugin manifest 的 host_dependencies 中声明。
//
// Authorize 仅做 Phase 1 的"声明校验",data_scopes 强校验(每个 op 的
// Loki label selector / PromQL metric allowlist / skill key 白名单等)
// 留待 Phase 3 在 Adapter 内部基于各自 Manifest.DataScopes 实现。
var ErrUndeclared = errors.New("scope: op not declared in host_dependencies")

// ScopeValidator 是 plugin → hostcall 的 RBAC 校验器。
//
// 每个 plugin 在 install/adopt 时由 biz 层调 SetPluginScopes 把
// host_dependencies + data_scopes 注入;Call 时 Authorize(pluginID, op, ...)
// 校验该 op 是否在 declares 里,不在 → ErrUndeclared(后续被 SDK 包装成
// ErrPermissionDenied)。
//
// data_scopes 的强校验(Loki label selector / PromQL metric allowlist /
// skill key 白名单等)Phase 1 仅占位 — Authorize 只判 declares 命中,
// scope 字段留作 Phase 3 adapter 实现"在 scope 内"的二次校验。
type ScopeValidator struct {
	mu sync.RWMutex

	// scopes[pluginID][op] = 该 op 的 data_scopes JSON(Phase 3 使用)
	scopes map[uint64]map[string]json.RawMessage

	// declares[pluginID] = plugin manifest.host_dependencies 全集。
	// Authorize 时只检查 op ∈ declares(Phase 1 简化)。
	declares map[uint64][]string
}

// NewScopeValidator 构造空 ScopeValidator。
//
// 空 validator 上 Authorize 对所有 op 返 ErrUndeclared — 必须先
// SetPluginScopes 才能放行;避免 install 漏调时静默放行。
func NewScopeValidator() *ScopeValidator {
	return &ScopeValidator{
		scopes:   make(map[uint64]map[string]json.RawMessage),
		declares: make(map[uint64][]string),
	}
}

// SetPluginScopes 注入或覆盖单个 plugin 的 declares + scopes。
//
// 由 biz/install / biz/lifecycle 在 adopt / scope 变化时调;
// 并发安全,内部写锁。
//
// declares 形如 ["prom", "loki", "skill", "llm", "edge.execute_cmd"];
// scopes 形如 {"prom": {"allowed_metrics": ["node_cpu_*"]}}。
func (v *ScopeValidator) SetPluginScopes(pluginID uint64, declares []string, scopes map[string]json.RawMessage) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// 拷贝 declares,避免外部修改穿透。
	decl := make([]string, len(declares))
	copy(decl, declares)
	v.declares[pluginID] = decl

	if len(scopes) == 0 {
		delete(v.scopes, pluginID)
		return
	}
	m := make(map[string]json.RawMessage, len(scopes))
	for k, raw := range scopes {
		// 复制 raw,避免外部修改穿透。
		if len(raw) == 0 {
			m[k] = json.RawMessage("{}")
			continue
		}
		buf := make(json.RawMessage, len(raw))
		copy(buf, raw)
		m[k] = buf
	}
	v.scopes[pluginID] = m
}

// ClearPluginScopes 在 uninstall / 卸载 plugin 时摘除其所有 scope 与 declare。
// 后续 Authorize 该 pluginID 任何 op 都会返 ErrUndeclared。
func (v *ScopeValidator) ClearPluginScopes(pluginID uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.declares, pluginID)
	delete(v.scopes, pluginID)
}

// Authorize 校验 pluginID 是否声明过 op。
//
// Phase 1 实现:只判 op ∈ declares[pluginID]。
// payload 参数 Phase 3 用于 data_scopes 强校验(目前 ignored)。
//
// 返回 ErrUndeclared 即拒绝;SDK 层把它包装为 ErrPermissionDenied。
func (v *ScopeValidator) Authorize(pluginID uint64, op string, payload json.RawMessage) error {
	_ = payload // Phase 3:基于 op + payload + scopes[pluginID][op] 做精细校验

	v.mu.RLock()
	decls, ok := v.declares[pluginID]
	v.mu.RUnlock()
	if !ok {
		return ErrUndeclared
	}
	for _, d := range decls {
		if d == op {
			return nil
		}
	}
	return ErrUndeclared
}

// DeclaredOps 快照(pluginID 维度)。调试 / admin 接口使用;非热路径。
func (v *ScopeValidator) DeclaredOps(pluginID uint64) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	d, ok := v.declares[pluginID]
	if !ok {
		return nil
	}
	out := make([]string, len(d))
	copy(out, d)
	return out
}
