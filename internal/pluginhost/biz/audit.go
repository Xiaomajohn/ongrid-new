// Package biz / audit.go 实现 plugin 审计的统一出口。
//
// 双写策略:
//   - sink:A 的 audit.Log 入口(comp="pluginhost",Action / Resource / Latency),
//     走 A 既有 audit_logs 表;A 消费者无感
//   - store:B 的 plugin_audits 表(由 B1 的 AuditRepo 实现),支持 plugin 维度
//     反查 + 详情 JSON 列
//
// 失败兜底:
//   - sink 写入失败 → 整条 AuditEntry 进内存 buffer(上限 1000 条,环形覆盖),
//     等 Flush() 在合适时机重试
//   - store 写入失败 → 直接向上抛 ErrAuditFailed,调用方决定重试或忽略;
//     P1 阶段不把 store 失败也入 buffer,避免与 sink buffer 重复
//
// TODO(Phase 3):
//   - sink 接入 A 的 internal/audit.Log(由 main.go 通过 Deps.Audit 注入,
//     biz 通过把 Sink 类型替换为 pluginhost.AuditSink 实现)
//   - Flush 由 pluginhost 的后台协程每 30s 调一次
//   - buffer 上限 1000 是 P1 经验值,Phase 4 server handler 暴露
//     /pluginhost/audit/buffer_size 便于监控
package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrAuditFailed 审计写入失败标记。
var ErrAuditFailed = errors.New("biz: audit write failed")

// AuditBufferLimit 失败缓冲上限;超出环形覆盖最早一条。
const AuditBufferLimit = 1000

// AuditEntry 单条审计事件。
//
// 字段语义:
//   - TenantID:多租户隔离;P0 阶段为 0,Phase 3 起由调用方注入
//   - PluginID:plugin_instances.ID 主键;0 表示非 plugin 级事件
//   - Action:install / uninstall / enable / disable / health_change /
//     host_call / bind_secret(与 model.PluginAudit.Action 对齐)
//   - Actor:user:<id> / system / api-key:<id>
//   - Details:扩展字段,JSON 序列化后写 details_json 列
//   - OccurredAt:事件发生时间;与 plugin_audits.OccurredAt 对齐
type AuditEntry struct {
	TenantID   uint64
	PluginID   uint64
	Action     string
	Actor      string
	Details    map[string]any
	OccurredAt time.Time
}

// AuditSink 是 A 既有审计入口的最小契约。
//
// Phase 2 由 main.go 注入 pluginhost.Deps.Audit(pluginhost.AuditSink);
// P1 阶段 biz 包独立定义形态一致的 interface,Phase 3 由主 agent 决定
// 是否切到类型别名。
type AuditSink interface {
	Write(ctx context.Context, comp, action, resource string, latency time.Duration, err error) error
}

// StoreAudit 是 plugin_audits 表的写入 interface。
//
// 等价于 B1 的 data.AuditRepo.Record;Phase 2 整合时换为 *data.AuditRepo。
type StoreAudit interface {
	Audit(ctx context.Context, pluginID uint64, action, actor string, details map[string]any) error
}

// Auditor 是双写审计的 service 对象。
type Auditor struct {
	mu     sync.Mutex
	sink   AuditSink
	store  StoreAudit
	buffer []AuditEntry
}

// NewAuditor 构造 Auditor;sink / store 都允许为 nil(分别为降级路径)。
func NewAuditor(sink AuditSink, store StoreAudit) *Auditor {
	return &Auditor{
		sink:   sink,
		store:  store,
		buffer: make([]AuditEntry, 0, AuditBufferLimit),
	}
}

// Record 写一条审计。流程:
//
//  1. 序列化 details(JSON 字符串,sink 失败入 buffer 时使用)
//  2. sink.Write(comp="pluginhost", action=e.Action, resource="plugin:<id>",
//     latency=now-OccurredAt)
//  3. store.Audit(e.PluginID, e.Action, e.Actor, details)
//  4. sink 失败 → e 入 buffer(上限 1000,环形)
//  5. store 失败 → 返 ErrAuditFailed
func (a *Auditor) Record(ctx context.Context, e AuditEntry) error {
	if a == nil {
		return nil
	}

	// 1. details JSON 序列化(失败也允许,降级用空串)。
	detailsJSON, _ := json.Marshal(e.Details)

	// 2. sink.Write:失败入 buffer,不阻断 store 写入。
	if a.sink != nil {
		resource := fmt.Sprintf("plugin:%d", e.PluginID)
		latency := time.Since(e.OccurredAt)
		if latency < 0 {
			latency = 0
		}
		if err := a.sink.Write(ctx, "pluginhost", e.Action, resource, latency, nil); err != nil {
			a.enqueueBuffer(e)
		}
	}

	// 3. store.Audit:失败向上抛。
	if a.store != nil {
		if err := a.store.Audit(ctx, e.PluginID, e.Action, e.Actor, e.Details); err != nil {
			return fmt.Errorf("%w: %v", ErrAuditFailed, err)
		}
	}

	// 序列化结果在本期未使用,留作 sink payload 扩展位的占位。
	_ = detailsJSON
	return nil
}

// Flush 把 buffer 里的 entry 重试一次 sink 写入。已存在的 sink 仍失败
// 的保留在 buffer;成功的从 buffer 移除。
//
// Phase 3 由 pluginhost 后台协程每 30s 调一次。
func (a *Auditor) Flush(ctx context.Context) error {
	if a == nil || a.sink == nil || len(a.buffer) == 0 {
		return nil
	}

	a.mu.Lock()
	pending := a.buffer
	a.buffer = make([]AuditEntry, 0, AuditBufferLimit)
	a.mu.Unlock()

	var firstErr error
	for _, e := range pending {
		resource := fmt.Sprintf("plugin:%d", e.PluginID)
		latency := time.Since(e.OccurredAt)
		if latency < 0 {
			latency = 0
		}
		if err := a.sink.Write(ctx, "pluginhost", e.Action, resource, latency, nil); err != nil {
			// 仍然失败的回收入 buffer(仍然触发上限环形)。
			a.enqueueBuffer(e)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		return fmt.Errorf("%w: flush residual: %v", ErrAuditFailed, firstErr)
	}
	return nil
}

// BufferLen 返回当前 buffer 长度,供 server handler / 监控探针使用。
func (a *Auditor) BufferLen() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.buffer)
}

// enqueueBuffer 把 e 入 buffer;超过 AuditBufferLimit 环形覆盖最早一条。
func (a *Auditor) enqueueBuffer(e AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.buffer) >= AuditBufferLimit {
		// 环形覆盖:把最早一条出队。
		a.buffer = a.buffer[1:]
	}
	a.buffer = append(a.buffer, e)
}
