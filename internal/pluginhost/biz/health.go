// Package biz / health.go 实现 plugin 健康检查后台循环。
//
// 设计要点:
//   - P1 阶段:ticker 每 15s 遍历 registry,基于"enabled && health != down"
//     简单判定 healthy,写回 DB(供 server handler / 告警消费)
//   - 真实探针(子进程 liveness、HTTP /healthz)由 Phase 3 接入 runtime
//     Pool 后,在 tick 里调 rt.Ping()
//   - Logger 注入到结构体,允许外部传入;NewHealth 接收 nil 时兜底为
//     slog.Default()
package biz

import (
	"context"
	"log/slog"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// DefaultHealthInterval 默认 ticker 周期。Phase 3 允许通过配置覆盖。
const DefaultHealthInterval = 15 * time.Second

// HealthStore 是 health 用例依赖的最小数据面 interface。
//
// 等价于 B1 的:
//   - SetHealth    ← data.PluginRepo.SetHealth
//   - ListEnabled  ← data.PluginRepo.ListEnabled
type HealthStore interface {
	// SetHealth 写 plugin_instances.health_status。
	SetHealth(ctx context.Context, id uint64, status string) error
	// ListEnabled 列某租户已启用的 plugin 实例(Phase 3 用于跨实例巡检)。
	ListEnabled(ctx context.Context, tenantID uint64) ([]registry.PluginInstance, error)
}

// Health 是健康检查 service 对象。
type Health struct {
	Store    HealthStore
	Reg      *registry.Registry
	Logger   *slog.Logger
	Interval time.Duration
}

// NewHealth 构造 Health;Store / Reg 为 nil 返 nil;Logger 为 nil 时用
// slog.Default();Interval 为零值时回落到 DefaultHealthInterval。
func NewHealth(s HealthStore, r *registry.Registry, log *slog.Logger) *Health {
	if s == nil || r == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Health{
		Store:    s,
		Reg:      r,
		Logger:   log,
		Interval: DefaultHealthInterval,
	}
}

// Start 启动后台健康检查循环,立刻跑一次然后按 Interval 周期 tick。
// Start 不阻塞,退出依赖 ctx 取消。
func (h *Health) Start(ctx context.Context) {
	if h == nil {
		return
	}
	go h.loop(ctx)
}

// loop 是后台循环:ctx 取消则退出。
func (h *Health) loop(ctx context.Context) {
	if h.Interval <= 0 {
		h.Interval = DefaultHealthInterval
	}
	ticker := time.NewTicker(h.Interval)
	defer ticker.Stop()

	// 启动后立即跑一次,避免冷启 15s 内 server handler 看不到最新状态。
	h.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.tick(ctx)
		}
	}
}

// tick 单轮健康检查:
//
//  1. 遍历 registry.List(),对每个 instance 计算 status
//  2. 状态变化才写 DB(SetHealth)与日志
//
// P1 阶段 status 规则:
//   - Enabled == false                  → "down"
//   - HealthStatus == "down"           → "down"
//   - 其他                               → "healthy"
//
// Phase 3 在此基础上接入 rt.Ping(ctx, ...) 真实探针。
func (h *Health) tick(ctx context.Context) {
	if h == nil || h.Reg == nil || h.Store == nil {
		return
	}

	for _, p := range h.Reg.List() {
		if p == nil || p.ID == 0 {
			continue
		}
		status := deriveStatus(p)
		if status == p.HealthStatus {
			continue
		}
		if err := h.Store.SetHealth(ctx, p.ID, status); err != nil {
			h.Logger.Warn("biz.health: set health failed",
				slog.String("pack_id", p.PackID),
				slog.Uint64("plugin_id", p.ID),
				slog.String("status", status),
				slog.String("err", err.Error()),
			)
			continue
		}
		// 同步刷新 registry 内部状态(读侧 List() 会拿到新值)。
		p.HealthStatus = status
		h.Logger.Debug("biz.health: status updated",
			slog.String("pack_id", p.PackID),
			slog.String("status", status),
		)
	}
}

// Ping 主动触发一次 tick(供 server handler / 调试用)。
//
// P1 阶段:直接返 nil,语义占位;Phase 3 接入 rt.Ping 真实探针后,本方法
// 改为对该 pluginID 单点探针并返探针结果。
func (h *Health) Ping(ctx context.Context, pluginID uint64) error {
	if h == nil {
		return nil
	}
	_ = pluginID
	h.tick(ctx)
	return nil
}

// deriveStatus 从 PluginInstance 计算当前健康状态。
//
// P1 阶段纯基于静态字段;Phase 3 加入最近一次 rt.Ping 延迟 / 错误率。
func deriveStatus(p *registry.PluginInstance) string {
	if !p.Enabled {
		return "down"
	}
	if p.HealthStatus == "down" {
		return "down"
	}
	if p.HealthStatus == "degraded" {
		return "degraded"
	}
	return "healthy"
}
