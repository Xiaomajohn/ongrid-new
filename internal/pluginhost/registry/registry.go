// Package registry 是 pluginhost 的进程级能力注册表。
//
// 一行 PluginInstance = 一份 manifest 解析结果 + 已经过 sandbox 校验,
// 每个 instance 持有一组 Capability;Registry 提供 Register / Unregister /
// Lookup / ListByKind / List / SetEnabled 等基本操作。
//
// Registry 本身是并发安全的,但调用方在批量改完后应通过 SetEnabled /
// Unregister 显式发变更事件给 invoke router(避免同进程内 A 端的 adapter
// 与注册表漂移)。
//
// 本包不依赖 A 的任何子包,纯数据结构 + 互斥锁。
package registry

import (
	"encoding/json"
	"errors"
	"sync"
)

// ErrDuplicatePlugin:PackID 已被注册。
var ErrDuplicatePlugin = errors.New("registry: duplicate plugin id")

// PluginInstance 已注册的插件实例(已通过 sandbox 校验,可在 host 加载)。
type PluginInstance struct {
	ID             uint64
	TenantID       uint64
	PackID         string
	Version        string
	Source         string // local / tarball / git / remote / inproc
	InstallPath    string
	ManifestSHA256 string
	Enabled        bool
	HealthStatus   string // healthy / degraded / down / unknown
	Capabilities   []*Capability
}

// Capability 单个能力点描述。
//
// 与 manifest.Capability 字段集接近(都是 plugin 的能力视图),但独立
// 维护:registry 关心"已注册后的视图",manifest 关心"磁盘上的源数据";
// 二者之间由 Phase 2 的 biz/adopt 流程转译。
type Capability struct {
	PluginID string
	Kind     string
	Name     string
	Class    string // safe / mutating / dangerous
	Schema   json.RawMessage
	Metadata map[string]any
}

// Registry 进程级注册表。mu 保护 plugins / caps;读多写少,RLock 优先。
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*PluginInstance        // key = packKey(tenantID, packID)
	caps    map[string]map[string]*Capability // key1 = pluginID(=packKey), key2 = cap.Name
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]*PluginInstance),
		caps:    make(map[string]map[string]*Capability),
	}
}

// Register 注入一个新 instance;同 PackID 已存在则返 ErrDuplicatePlugin。
//
// 注册成功的同时,把每个 Capability 写入 caps 索引(以 pluginID + cap.Name
// 为 key),Lookup / ListByKind 走索引,不再遍历 plugins。
func (r *Registry) Register(p *PluginInstance) error {
	if p == nil {
		return errors.New("registry: nil plugin")
	}
	if p.PackID == "" {
		return errors.New("registry: empty pack id")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := packKey(p.TenantID, p.PackID)
	if _, exists := r.plugins[key]; exists {
		return ErrDuplicatePlugin
	}

	// 浅拷贝 instance + capability slice,避免外部修改穿透。
	stored := *p
	if p.Capabilities != nil {
		caps := make([]*Capability, len(p.Capabilities))
		for i, c := range p.Capabilities {
			cc := *c
			if cc.PluginID == "" {
				cc.PluginID = key
			}
			caps[i] = &cc
		}
		stored.Capabilities = caps
	}
	r.plugins[key] = &stored

	capMap := make(map[string]*Capability, len(stored.Capabilities))
	for _, c := range stored.Capabilities {
		capMap[c.Name] = c
	}
	r.caps[key] = capMap
	return nil
}

// Unregister 摘除 pluginID 的 instance 与全部 capabilities。
// pluginID 不存在返 registry: plugin not found。
func (r *Registry) Unregister(pluginID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.plugins[pluginID]; !ok {
		return errors.New("registry: plugin not found")
	}
	delete(r.plugins, pluginID)
	delete(r.caps, pluginID)
	return nil
}

// Lookup 按 (pluginID, capName) 取 capability,不存在返 (nil, false)。
func (r *Registry) Lookup(pluginID, capName string) (*Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	m, ok := r.caps[pluginID]
	if !ok {
		return nil, false
	}
	c, ok := m[capName]
	return c, ok
}

// ListByKind 返回所有 Kind 等于 kind 的 capability(用于 Phase 3 adapter
// 按 kind 批量注入到 A 子系统)。
func (r *Registry) ListByKind(kind string) []*Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*Capability, 0)
	for _, m := range r.caps {
		for _, c := range m {
			if c.Kind == kind {
				out = append(out, c)
			}
		}
	}
	return out
}

// List 返回当前所有 instance 的快照(切片内 instance 指针指向内部数据,
// 调用方只读,不可修改)。
func (r *Registry) List() []*PluginInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*PluginInstance, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	return out
}

// SetEnabled 仅修改 instance 的 Enabled 字段,不删除注册项(Enable /
// Disable 操作的语义)。
//
// 调用方在切完 Enabled 后应同步通知 invoke router / adapter 重新分发。
func (r *Registry) SetEnabled(pluginID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[pluginID]
	if !ok {
		return errors.New("registry: plugin not found")
	}
	p.Enabled = enabled
	return nil
}

// packKey 构造注册表内部 key。当前仅用 PackID;Phase 2 起接租户隔离
// 时,把 TenantID 前缀拼入。
func packKey(tenantID uint64, packID string) string {
	_ = tenantID
	return packID
}
