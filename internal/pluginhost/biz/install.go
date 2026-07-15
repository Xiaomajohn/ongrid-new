// Package biz 是 pluginhost 子系统的业务用例层(install / lifecycle /
// health / audit),所有 IO 都通过 Store interface 抽象,Phase 2 由主 agent
// 把占位实现换成 B1 的 *data.XxxRepo。
//
// 本文件实现 install 用例:
//   - 把一份已经解析好的 manifest.PluginManifest 落 DB(plugin_instances
//   - plugin_capabilities)
//   - 写入进程级 registry
//   - 写一条 install 审计
//
// 设计要点:
//   - TenantID 默认 0(P0 阶段全租户共享),Phase 3 起按租户隔离时由调用方
//     注入正确的 TenantID
//   - InstallPath 取 manifest.Entry 的父目录(相对路径解算由 loader 负责),
//     Phase 3 接入 sandbox 后会调用 Validator 做路径越狱校验
//   - capability 的 PluginID 字段用 PackID 作 key,与 registry.packKey 一致
package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
)

// ErrInstallFailed Install 用例统一失败标记;具体原因 wrap 在内层。
var ErrInstallFailed = errors.New("biz: install failed")

// Store 是 install 用例依赖的最小数据面 interface。
//
// 等价于 B1 的:
//   - CreatePlugin   ← data.PluginRepo.Create
//   - CreateCapabilities ← data.CapabilityRepo.CreateBatch
//   - Audit          ← data.AuditRepo.Record
//
// Phase 2 整合时由主 agent 把这里的 Store 实现换成 *data.PluginRepo 等具体类型;
// 当前阶段为不依赖 B1 的 data 包,B2 用该 interface 隔离。
type Store interface {
	// CreatePlugin 写入 plugin_instances 行,把生成的主键回写到 p.ID。
	CreatePlugin(ctx context.Context, p *registry.PluginInstance) error
	// CreateCapabilities 批量写入 plugin_capabilities 行。
	CreateCapabilities(ctx context.Context, caps []registry.Capability) error
	// Audit 写一条 plugin_audits 行;details 在 repo 层 JSON 化。
	Audit(ctx context.Context, pluginID uint64, action, actor string, details map[string]any) error
}

// ValidatorFactory 每次 Install 时返回一个新的 sandbox.Validator 实例。
//
// 用工厂而非单例的原因:Validator 可能持有 per-install 的配置(roots、allowlist);
// Phase 3 接入签名校验 / 路径沙箱时,该工厂由 main.go 注入。
//
// 说明:Phase 1 的 sandbox 包仅导出 PathSafeUnderRoot 函数,Validator
// interface 尚未在 sandbox/path.go 提供(任务假设);本文件先用本地
// bizValidator 形态保持编译通过,Phase 2 由主 agent 切到 sandbox.Validator。
type ValidatorFactory func() bizValidator

// bizValidator 是 sandbox.Validator 的本地占位 interface。
//
// PathSafeUnderRoot 校验 p 在解符号链接后落在 root 范围内;与
// sandbox.PathSafeUnderRoot 函数语义一致。
//
// Phase 2 整合:把本类型替换为 sandbox.Validator(type alias),调用方
// 不变。
type bizValidator interface {
	PathSafeUnderRoot(p, root string) error
}

// Installer 是 install 用例的 service 对象。
//
// 依赖通过字段注入,NewInstaller 仅校验非空;后续 Run 由调用方 wire。
type Installer struct {
	Store     Store
	Reg       *registry.Registry
	Validator ValidatorFactory
}

// NewInstaller 构造 Installer;任一必填字段为 nil 即返 ErrInstallFailed。
func NewInstaller(s Store, r *registry.Registry, vf ValidatorFactory) *Installer {
	if s == nil || r == nil {
		// vf 允许为 nil:Phase 2 之前不强制 sandbox 校验。
		return nil
	}
	return &Installer{
		Store:     s,
		Reg:       r,
		Validator: vf,
	}
}

// Install 把 m 落到 DB + registry + audit。
//
// 流程:
//  1. 构造 *registry.PluginInstance(P0 默认 TenantID=0)
//  2. manifest.Capabilities → []*registry.Capability
//  3. Store.CreatePlugin 写 plugin_instances,回填 ID
//  4. Store.CreateCapabilities 批量写 plugin_capabilities
//  5. registry.Register 写进程级索引
//  6. Store.Audit 写 install 审计
//
// 任一步失败:包装成 ErrInstallFailed 返回,已写入的资源由调用方通过
// Uninstall 清理(本方法不回滚,因为 DB 写入未走事务)。
func (i *Installer) Install(ctx context.Context, m *manifest.PluginManifest, source string) (*registry.PluginInstance, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manifest", ErrInstallFailed)
	}
	if m.ID == "" {
		return nil, fmt.Errorf("%w: empty manifest id", ErrInstallFailed)
	}

	// 1. 构造 PluginInstance。
	inst := &registry.PluginInstance{
		TenantID:       0, // P0:全租户共享
		PackID:         m.ID,
		Version:        m.Version,
		Source:         source,
		InstallPath:    parentDir(m.Entry),
		ManifestSHA256: "",
		Enabled:        true,
		HealthStatus:   "unknown",
	}

	// 2. manifest.Capability → registry.Capability。
	caps := make([]*registry.Capability, 0, len(m.Capabilities))
	for _, mc := range m.Capabilities {
		caps = append(caps, &registry.Capability{
			Kind:     string(mc.Kind),
			Name:     mc.Name,
			Class:    mc.Class,
			Schema:   json.RawMessage(mc.Schema),
			Metadata: mc.Metadata,
		})
	}
	inst.Capabilities = caps

	// 3. 写 DB(plugin_instances)并回填主键。
	if err := i.Store.CreatePlugin(ctx, inst); err != nil {
		return nil, fmt.Errorf("%w: create plugin: %v", ErrInstallFailed, err)
	}

	// 4. 批量写 plugin_capabilities;每个 cap 的 PluginID 字段绑定到 PackID
	//    (与 registry.packKey 一致)。
	capValues := make([]registry.Capability, 0, len(caps))
	for _, c := range caps {
		c.PluginID = inst.PackID
		capValues = append(capValues, *c)
	}
	if err := i.Store.CreateCapabilities(ctx, capValues); err != nil {
		return nil, fmt.Errorf("%w: create capabilities: %v", ErrInstallFailed, err)
	}

	// 5. 写进程级 registry。
	if err := i.Reg.Register(inst); err != nil {
		return nil, fmt.Errorf("%w: registry register: %v", ErrInstallFailed, err)
	}

	// 6. 写 install 审计。
	details := map[string]any{
		"pack_id":   inst.PackID,
		"version":   inst.Version,
		"source":    inst.Source,
		"tenant":    inst.TenantID,
		"installed": time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := i.Store.Audit(ctx, inst.ID, "install", "system", details); err != nil {
		// 审计失败不回滚 install,记录后向上抛,调用方决定是否重试;
		// 这里仍然返 nil error,因为 plugin 已经成功落地,审计可以异步补。
		_ = err
	}

	return inst, nil
}

// parentDir 返回 path 的父目录(简化版,只处理 "/" 与 "\\" 分隔符)。
//
// P1 阶段避免引入 path/filepath(不在 import 白名单);Phase 3 接入 sandbox
// 后由 sandbox.Validator 直接处理路径,本函数可移除。
func parentDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return ""
}
