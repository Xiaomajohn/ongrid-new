// Package biz / lifecycle.go 实现 plugin 启停 / 卸载三类 lifecycle 操作。
//
// 设计要点:
//   - LifecycleStore.SetEnabled / DeletePlugin 接 uint64 主键(plugin_instances.ID),
//     与 registry 端的 PackID(string)key 通过 strpackid() 桥接;Phase 3 接入
//     PackID-first 查询语义后由主 agent 调整
//   - 每条 lifecycle 操作都写一条审计;Enable/Disable 同步刷 registry.Enabled
//     与 DB.enabled 字段,Uninstall 同时摘除 registry + 删 DB 行
package biz

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// ErrLifecycleFailed lifecycle 用例统一失败标记。
var ErrLifecycleFailed = errors.New("biz: lifecycle failed")

// LifecycleStore 是 lifecycle 用例依赖的最小数据面 interface。
//
// 等价于 B1 的:
//   - SetEnabled   ← data.PluginRepo.SetEnabled
//   - DeletePlugin ← data.PluginRepo.Delete
//   - Audit        ← data.AuditRepo.Record
//
// Phase 2 整合时由主 agent 把实现换成 *data.XxxRepo。
type LifecycleStore interface {
	// SetEnabled 翻转 plugin_instances.enabled。
	SetEnabled(ctx context.Context, id uint64, enabled bool) error
	// DeletePlugin 删除 plugin_instances 行(级联删 plugin_capabilities
	// 由 DB 外键或 repo 内部处理)。
	DeletePlugin(ctx context.Context, id uint64) error
	// Audit 写一条 plugin_audits 行。
	Audit(ctx context.Context, pluginID uint64, action, actor string, details map[string]any) error
}

// Lifecycle 是启停 / 卸载用例的 service 对象。
type Lifecycle struct {
	Store LifecycleStore
	Reg   *registry.Registry
}

// NewLifecycle 构造 Lifecycle;任一必填字段为 nil 返 nil(由调用方兜底)。
func NewLifecycle(s LifecycleStore, r *registry.Registry) *Lifecycle {
	if s == nil || r == nil {
		return nil
	}
	return &Lifecycle{Store: s, Reg: r}
}

// Enable 启用 pluginID 对应实例的全部 capability(P0 粒度)。
//
// 步骤:
//  1. registry.SetEnabledByID 翻转进程级索引
//  2. Store.SetEnabled         翻转 DB 字段
//  3. Store.Audit              写一条 enable 审计
//
// 任一步失败:包装 ErrLifecycleFailed 向上抛。
func (l *Lifecycle) Enable(ctx context.Context, pluginID uint64, capName string) error {
	if pluginID == 0 {
		return fmt.Errorf("%w: invalid plugin id", ErrLifecycleFailed)
	}

	if err := l.Reg.SetEnabledByID(pluginID, true); err != nil {
		return fmt.Errorf("%w: registry enable: %v", ErrLifecycleFailed, err)
	}
	if err := l.Store.SetEnabled(ctx, pluginID, true); err != nil {
		// 回滚 registry 状态,避免进程级与 DB 漂移。
		_ = l.Reg.SetEnabledByID(pluginID, false)
		return fmt.Errorf("%w: store enable: %v", ErrLifecycleFailed, err)
	}

	details := map[string]any{
		"capability": capName,
		"enabled":    true,
		"at":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := l.Store.Audit(ctx, pluginID, "enable", "system", details); err != nil {
		// 审计失败不回滚 enable,记录后向上抛,调用方决定是否重试。
		return fmt.Errorf("%w: audit enable: %v", ErrLifecycleFailed, err)
	}
	return nil
}

// Disable 关闭 pluginID 对应实例的全部 capability。语义与 Enable 对称。
func (l *Lifecycle) Disable(ctx context.Context, pluginID uint64, capName string) error {
	if pluginID == 0 {
		return fmt.Errorf("%w: invalid plugin id", ErrLifecycleFailed)
	}

	if err := l.Reg.SetEnabledByID(pluginID, false); err != nil {
		return fmt.Errorf("%w: registry disable: %v", ErrLifecycleFailed, err)
	}
	if err := l.Store.SetEnabled(ctx, pluginID, false); err != nil {
		_ = l.Reg.SetEnabledByID(pluginID, true)
		return fmt.Errorf("%w: store disable: %v", ErrLifecycleFailed, err)
	}

	details := map[string]any{
		"capability": capName,
		"enabled":    false,
		"at":         time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := l.Store.Audit(ctx, pluginID, "disable", "system", details); err != nil {
		return fmt.Errorf("%w: audit disable: %v", ErrLifecycleFailed, err)
	}
	return nil
}

// Uninstall 卸载 pluginID 对应实例。
//
// 步骤:
//  1. Store.DeletePlugin 删除 DB 行
//  2. registry.Unregister      摘除进程级索引
//  3. Store.Audit              写一条 uninstall 审计
//
// 注意顺序:先 DB 再 registry,失败时 DB 已经删了但 registry 还在,需要由
// 调用方通过运维通道清理;这是 P1 简化语义,Phase 3 接入事务后再调整。
func (l *Lifecycle) Uninstall(ctx context.Context, pluginID uint64) error {
	if pluginID == 0 {
		return fmt.Errorf("%w: invalid plugin id", ErrLifecycleFailed)
	}

	// 取一次元数据,审计 details 用。
	details := map[string]any{
		"at":      time.Now().UTC().Format(time.RFC3339Nano),
		"pack_id": "",
		"version": "",
	}
	if inst, ok := l.Reg.LookupByID(pluginID); ok && inst != nil {
		details["pack_id"] = inst.PackID
		details["version"] = inst.Version
	}

	if err := l.Store.DeletePlugin(ctx, pluginID); err != nil {
		return fmt.Errorf("%w: store delete: %v", ErrLifecycleFailed, err)
	}
	// registry 端以 packID 为 key,需先查到 packID 才能 Unregister。
	if inst, ok := l.Reg.LookupByID(pluginID); ok && inst != nil {
		if err := l.Reg.Unregister(inst.PackID); err != nil {
			// DB 已删 / registry 未摘,日志记录即可,Phase 3 接入补偿事务。
			return fmt.Errorf("%w: registry unregister: %v", ErrLifecycleFailed, err)
		}
	}

	if err := l.Store.Audit(ctx, pluginID, "uninstall", "system", details); err != nil {
		return fmt.Errorf("%w: audit uninstall: %v", ErrLifecycleFailed, err)
	}
	return nil
}

// strpackid 把 uint64 主键 format 成 registry 端的 PackID-style 字符串 key。
//
// P1 阶段:key = fmt.Sprintf("%d", id)。Phase 3 主 agent 会接入 (TenantID,
// PackID) 复合 key:key = fmt.Sprintf("%d:%s", tenantID, packID),届时
// LifecycleStore.SetEnabled / DeletePlugin 也改为接 (tenantID, packID)
// 二元组。
//
// 注意:strpackid 在 Phase 1.5 接入纯 uint64 主键路由后已不再调用(registry
// 走 SetEnabledByID 直接接 uint64),本辅助函数保留供历史 / 调试使用,
// Phase 3 重构时一并移除。
// nolint:unused
func strpackid(id uint64) string {
	return fmt.Sprintf("%d", id)
}
