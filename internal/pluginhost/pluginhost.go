// Package pluginhost 是 B(插件管理子系统)的顶层容器。
//
// 本文件只定义 New / Run / 占位 API 的契约骨架:
//   - New(deps) 做零副作用 wire-up(只校验引用,不发起任何 IO)。
//   - Run(ctx) 阻塞到 ctx 结束,内部 step 1-4 已在 Phase 1 阶段落地
//     可执行的部分(registry / runtime pool / manifest scan);data
//     migrate、health ticker、server route 留 Phase 2 / 4 接入。
//   - Install / Uninstall / Enable / Disable / Invoke 当前统一返
//     ErrNotImplemented;Phase 2 起的 biz/ 子包会逐个填充。
//
// Phase 1 故意不引入 biz / data / server 子包,本文件不 import 任何
// 未实现的子包(避免与并发 agent 的工作冲突)。
package pluginhost

import (
	"context"
	"errors"

	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
	rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"
)

// 错误定义。
var (
	// ErrDepsIncomplete:New 时必填字段(DB / Logger / Ctx / Mux)任一缺失。
	ErrDepsIncomplete = errors.New("pluginhost: deps incomplete")
	// ErrNotImplemented:Install / Uninstall / Enable / Disable / Invoke 仍
	// 在骨架阶段,Phase 2 起的 biz 子包会替换实现。
	ErrNotImplemented = errors.New("pluginhost: not implemented")
)

// PluginHost 是 B 子系统的顶层容器。
//
// 持有:
//   - deps:A 注入的所有句柄(DB / Logger / Mux / 6 个 Registrar / HostServices)
//   - reg:进程级 capability 注册表(registry.Registry)
//   - pool:进程级 transport pool,key = pluginID
type PluginHost struct {
	deps Deps
	reg  *registry.Registry
	pool *rtp.Pool
}

// New 零副作用构造 PluginHost。仅 wire 引用 + 校验必填字段,不发任何 IO,
// 不修改 deps 内部状态。必填字段(DB / Logger / Ctx / Mux)任一为 nil 即返
// ErrDepsIncomplete;其余 Registrar / HostServices 允许 nil(对应子系统可选)。
func New(deps Deps) (*PluginHost, error) {
	if deps.DB == nil || deps.Logger == nil || deps.Ctx == nil || deps.Mux == nil {
		return nil, ErrDepsIncomplete
	}
	return &PluginHost{
		deps: deps,
		reg:  registry.NewRegistry(),
		pool: rtp.NewPool(),
	}, nil
}

// Run 启动 pluginhost 生命周期并阻塞至 ctx 结束。
//
//	step 1: data.Migrate(p.deps.DB)                  — Phase 2 引入 data 子包后接入
//	step 2: manifest.LoadDirs(ctx, cfg) + bootstrap   — Phase 1 已落地,Phase 2 接入 sandbox validator
//	step 3: p.startBackgroundLoops(ctx)               — Phase 2 实现 health ticker / audit flush
//	step 4: server.Register(p.deps.Mux, p)            — Phase 4 引入 server 子包后接入
//	step 5: <-ctx.Done(); return p.shutdown()
//
// Phase 1 阶段:step 2 调用 manifest.LoadDirs(已实现),传入 nil validator
// 占位,bootstrap 是 no-op(等 Phase 2 接入 registry 适配)。step 1/3/4
// 留注释占位,Run 本身形状稳定,后续 Phase 仅在 step 1-4 处增量插入调用。
func (p *PluginHost) Run(ctx context.Context) error {
	// step 1: data.Migrate(p.deps.DB) — Phase 2 引入
	//   import data "github.com/ongridio/ongrid/internal/pluginhost/data"
	//   if err := data.Migrate(p.deps.DB); err != nil { return err }

	// step 2: 启动期扫描磁盘已 install 的 plugin。
	//   Phase 2 接入 sandbox validator 后,把第三个实参从 nil 换成
	//   sandbox.NewValidator();bootstrap 把 rec 转成 registry.PluginInstance 并 Register。
	cfg := manifest.LoadDirsConfig{}
	rec, _ := manifest.LoadDirs(ctx, cfg, nil)
	p.bootstrap(rec)

	// step 3: p.startBackgroundLoops(ctx) — Phase 2 实现 health ticker / audit flush
	p.startBackgroundLoops(ctx)

	// step 4: server.Register(p.deps.Mux, p) — Phase 4 引入 server 子包后接入
	//   import server "github.com/ongridio/ongrid/internal/pluginhost/server"
	//   server.Register(p.deps.Mux, p)

	<-ctx.Done()
	return p.shutdown()
}

// bootstrap 把 manifest.LoadDirs 的结果转成 registry.PluginInstance 并写入
// registry。当前为 no-op,Phase 2 接入 sandbox validator 与 registry 适配后填充。
func (p *PluginHost) bootstrap(rec []manifest.LoadResult) {
	_ = rec
}

// startBackgroundLoops 启动 health ticker / audit flush 等后台协程。
// Phase 2 实现。
func (p *PluginHost) startBackgroundLoops(ctx context.Context) {
	_ = ctx
}

// shutdown 释放进程级资源(关闭 transport pool,停止后台协程)。
func (p *PluginHost) shutdown() error {
	if p.pool != nil {
		return p.pool.CloseAll()
	}
	return nil
}

// ===== 占位 API(Phase 2 起的 biz 子包逐个填充) =====

// Install 在运行时同步注册一个新插件到 host。返回值 ID 是该 plugin
// instance 的主键;Phase 2 的 biz/install.go 会真正写入 DB + registry
// 并下发 adapter 注册到 A。
func (p *PluginHost) Install(ctx context.Context, spec any) (uint64, error) {
	return 0, ErrNotImplemented
}

// Uninstall 卸载已安装插件并清理 registry / pool / DB。
func (p *PluginHost) Uninstall(ctx context.Context, id uint64) error {
	return ErrNotImplemented
}

// Enable 启用指定 plugin 的某条 capability(重新下发到 A 的子系统)。
func (p *PluginHost) Enable(ctx context.Context, id uint64, capName string) error {
	return ErrNotImplemented
}

// Disable 关闭指定 plugin 的某条 capability(从 A 子系统摘除)。
func (p *PluginHost) Disable(ctx context.Context, id uint64, capName string) error {
	return ErrNotImplemented
}

// Invoke 是 A → C 的入口。A 的 skill/notify/llm/embedding/flow/alert 注册
// 的 adapter 会调用本方法,Phase 1.5 的 invoke/router 会路由到正确的 runtime。
func (p *PluginHost) Invoke(ctx context.Context, pluginID uint64, capName string, req any) (any, error) {
	return nil, ErrNotImplemented
}
