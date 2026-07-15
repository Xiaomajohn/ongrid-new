package registry

import (
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrDuplicate = errors.New("registry: duplicate plugin")
	ErrNotFound  = errors.New("registry: plugin not found")
)

type PluginInstance struct {
	ID             uint64
	TenantID       uint64
	PackID         string
	Version        string
	UUID           string
	Source         string
	InstallPath    string
	ManifestSHA256 string
	SignatureState string
	Enabled        bool
	HealthStatus   string
	LastHealthAt   *time.Time
	Capabilities   []*Capability
}

type Capability struct {
	PluginID string
	Kind     string
	Name     string
	Class    string
	Schema   json.RawMessage
	Metadata map[string]any
}

type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*PluginInstance
	caps    map[string]map[string]*Capability
}

func New() *Registry {
	return &Registry{
		plugins: make(map[string]*PluginInstance),
		caps:    make(map[string]map[string]*Capability),
	}
}

func (r *Registry) Register(inst *PluginInstance) error {
	if inst == nil || inst.PackID == "" {
		return errors.New("registry: invalid plugin")
	}

	stored := cloneInstance(inst)
	for _, cap := range stored.Capabilities {
		if cap.PluginID == "" {
			cap.PluginID = stored.PackID
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[stored.PackID]; exists {
		return ErrDuplicate
	}

	r.plugins[stored.PackID] = stored
	capMap := make(map[string]*Capability, len(stored.Capabilities))
	for _, cap := range stored.Capabilities {
		if cap != nil {
			capMap[cap.Name] = cap
		}
	}
	r.caps[stored.PackID] = capMap
	return nil
}

func (r *Registry) Unregister(packID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[packID]; !exists {
		return ErrNotFound
	}
	delete(r.plugins, packID)
	delete(r.caps, packID)
	return nil
}

func (r *Registry) Lookup(packID string) (*PluginInstance, bool) {
	r.mu.RLock()
	inst, exists := r.plugins[packID]
	if !exists {
		r.mu.RUnlock()
		return nil, false
	}
	copy := cloneInstance(inst)
	r.mu.RUnlock()
	return copy, true
}

// LookupCapability 在指定 packID 下按 capName 查找单个 capability。
//
// 用于 invoke/router:Router 拿到 (packID, capName) 二元组后,先拿到 capability,
// 再用 packing 的 PluginInstance 喂给 runtime.Runtime.Invoke。
//
// 不存在返 (nil, false);成功返深拷贝,调用方可安全修改 Schema/Metadata。
func (r *Registry) LookupCapability(packID, capName string) (*Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	byName, ok := r.caps[packID]
	if !ok {
		return nil, false
	}
	cap, ok := byName[capName]
	if !ok {
		return nil, false
	}
	return cloneCapability(cap), true
}

// SetEnabled 翻转某个 packID 对应实例的启用标志。返回 ErrNotFound 时
// 调用方需要区分"实例不存在"vs"持久层失败",自行处理。
func (r *Registry) SetEnabled(packID string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	inst, ok := r.plugins[packID]
	if !ok {
		return ErrNotFound
	}
	copy := *inst
	copy.Enabled = enabled
	r.plugins[packID] = &copy
	if byName, ok := r.caps[packID]; ok {
		for _, cap := range byName {
			if cap != nil {
				capCopy := *cap
				r.caps[packID][cap.Name] = &capCopy
			}
		}
	}
	return nil
}

// LookupByID 通过 PluginInstance.ID(uint64 PK,从 model.PluginInstance.DB)查找实例。
//
// Registry 内部用 packID 字符串作 map key,但外部 API(pluginhost.Install
// 返回 uint64 instanceID 后)需要 uint64 主键查找,用于 lifecycle 等回路。
// O(N) 线性扫描:N 是 plugin 总量,Phase 2 接 data 屉可替换为 reverse index。
func (r *Registry) LookupByID(id uint64) (*PluginInstance, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, inst := range r.plugins {
		if inst != nil && inst.ID == id {
			return cloneInstance(inst), true
		}
	}
	return nil, false
}

// SetEnabledByID 通过 uint64 主键启用/关闭 plugin instance。
//
// 与 SetEnabled 同语义,输入参数是 uint64 主键。返 ErrNotFound 时
// lifecycle 层需区分"实例不存在"vs"persistence 失败"。
func (r *Registry) SetEnabledByID(id uint64, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for packID, inst := range r.plugins {
		if inst == nil || inst.ID != id {
			continue
		}
		inst.Enabled = enabled
		if byName, ok := r.caps[packID]; ok {
			for _, cap := range byName {
				if cap != nil {
					capCopy := *cap
					r.caps[packID][cap.Name] = &capCopy
				}
			}
		}
		return nil
	}
	return ErrNotFound
}

// LookupCapabilityByInstanceID 跨 uint64 instanceID + capName 查找 capability。
//
// 是 LookupCapability + LookupByID 的复合包装,提供给 lifecycle / server 等不
// 愿意先 Lookup 实例再查 cap 的调用方,避免两次遍历。
func (r *Registry) LookupCapabilityByInstanceID(instanceID uint64, capName string) (*Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for packID, inst := range r.plugins {
		if inst == nil || inst.ID != instanceID {
			continue
		}
		byName, ok := r.caps[packID]
		if !ok {
			return nil, false
		}
		cap, ok := byName[capName]
		if !ok {
			return nil, false
		}
		return cloneCapability(cap), true
	}
	return nil, false
}

func (r *Registry) ListByKind(kind string) []*Capability {
	r.mu.RLock()
	out := make([]*Capability, 0)
	for _, byName := range r.caps {
		for _, cap := range byName {
			if cap != nil && cap.Kind == kind {
				out = append(out, cloneCapability(cap))
			}
		}
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].PluginID == out[j].PluginID {
			return out[i].Name < out[j].Name
		}
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

func (r *Registry) List() []*PluginInstance {
	r.mu.RLock()
	out := make([]*PluginInstance, 0, len(r.plugins))
	for _, inst := range r.plugins {
		out = append(out, cloneInstance(inst))
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		return out[i].PackID < out[j].PackID
	})
	return out
}

func cloneInstance(inst *PluginInstance) *PluginInstance {
	if inst == nil {
		return nil
	}
	copy := *inst
	if inst.LastHealthAt != nil {
		lastHealthAt := *inst.LastHealthAt
		copy.LastHealthAt = &lastHealthAt
	}
	if inst.Capabilities != nil {
		copy.Capabilities = make([]*Capability, 0, len(inst.Capabilities))
		for _, cap := range inst.Capabilities {
			if cap != nil {
				copy.Capabilities = append(copy.Capabilities, cloneCapability(cap))
			}
		}
	}
	return &copy
}

func cloneCapability(cap *Capability) *Capability {
	if cap == nil {
		return nil
	}
	copy := *cap
	if cap.Schema != nil {
		copy.Schema = append(json.RawMessage(nil), cap.Schema...)
	}
	if cap.Metadata != nil {
		copy.Metadata = make(map[string]any, len(cap.Metadata))
		for key, value := range cap.Metadata {
			copy.Metadata[key] = value
		}
	}
	return &copy
}
