package pluginhost

import (
	"context"
	"errors"
	"fmt"

	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
	"github.com/ongridio/ongrid/internal/pluginhost/runtime"
)

var (
	ErrDepsIncomplete = errors.New("pluginhost: deps incomplete")
	ErrNotImplemented = errors.New("pluginhost: not implemented yet")
)

type PluginHost struct {
	deps Deps
	reg  *registry.Registry
	pool *runtime.Pool
}

func New(deps Deps) (*PluginHost, error) {
	if deps.DB == nil || deps.Logger == nil || deps.Ctx == nil || deps.Mux == nil {
		return nil, ErrDepsIncomplete
	}
	return &PluginHost{
		deps: deps,
		reg:  registry.New(),
		pool: runtime.NewPool(),
	}, nil
}

func (p *PluginHost) Run(ctx context.Context) error {
	fmt.Println("step1 migrate")

	cfg := manifest.LoadDirsConfig{Dirs: p.deps.PluginDirs}
	rec, _ := manifest.LoadDirs(ctx, cfg)
	fmt.Println("step2 load")
	p.bootstrap(rec)

	fmt.Println("step3 background")
	p.startBackgroundLoops(ctx)

	fmt.Println("step4 register")

	<-ctx.Done()
	return p.shutdown()
}

func (p *PluginHost) bootstrap(rec []manifest.LoadResult) {
	_ = rec
	fmt.Println("bootstrap")
}

func (p *PluginHost) startBackgroundLoops(ctx context.Context) {
	_ = ctx
	fmt.Println("start background loops")
}

func (p *PluginHost) shutdown() error {
	fmt.Println("shutdown")
	return nil
}

func (p *PluginHost) Install(ctx context.Context, spec any) (uint64, error) {
	return 0, ErrNotImplemented
}

func (p *PluginHost) Uninstall(ctx context.Context, id uint64) error {
	return ErrNotImplemented
}

func (p *PluginHost) Enable(ctx context.Context, id uint64, capName string) error {
	return ErrNotImplemented
}

func (p *PluginHost) Disable(ctx context.Context, id uint64, capName string) error {
	return ErrNotImplemented
}

func (p *PluginHost) Invoke(ctx context.Context, pluginID uint64, capName string, req any) (any, error) {
	return nil, ErrNotImplemented
}
