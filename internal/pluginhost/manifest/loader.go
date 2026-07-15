// Loader:启动时扫描 cfg.Dirs 下的 plugin 目录,识别 format 并解析出 PluginManifest。
// 沙箱校验只调用入参 Validator 接口(由 Phase 2 sandbox 包实现),loader 不与 A 任
// 何子包耦合。
package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrLoadFailed 是 LoadDirs 的整体失败标记。LoadDirs 不会因单文件失败而中断整体
// 流程;只有路径全部无法读、ctx 取消等致命情形才返该错误。
var ErrLoadFailed = errors.New("manifest: load failed")

// LoadDirsConfig 控制 LoadDirs 的扫描行为。
type LoadDirsConfig struct {
	// Dirs 是允许扫描的根目录列表;为空时默认 ["/etc/ongrid/plugins"]。
	Dirs []string
	// AllowTarball 列出启动时需要解开扫描的 tarball 路径(预留 Phase 2)。
	AllowTarball []string
	// EnableHTTP 控制是否允许 transport=http 的远端 plugin。
	EnableHTTP bool
	// HTTPAllow 远端 plugin URL 的 allowlist。
	HTTPAllow []string
}

// Validator 是 Phase 2 sandbox 包对外暴露的最小校验接口。loader 只用
// PathSafeUnderRoot,后续 Permission Validate 留给 sandbox/permission。
type Validator interface {
	// PathSafeUnderRoot 校验 p 必须在 root 之下且不存在符号链接越狱。
	PathSafeUnderRoot(p, root string) error
}

// LoadDirs 在 cfg.Dirs 列表的每个目录下递归查找含 manifest 的子目录,解析并补齐
// format 字段,可选地调用 val 做入口路径沙箱校验,最后返回 LoadResult 列表。
//
// 中间错误(walk/解析/校验失败)不中断整体流程,而是追加到对应 LoadResult.Warnings;
// 只有 ctx 取消或所有 dir 都无法 stat 时才返回 ctx.Err()/ErrLoadFailed。
func LoadDirs(ctx context.Context, cfg LoadDirsConfig, val Validator) ([]LoadResult, error) {
	if len(cfg.Dirs) == 0 {
		cfg.Dirs = []string{"/etc/ongrid/plugins"}
	}

	results := make([]LoadResult, 0)

	for _, dir := range cfg.Dirs {
		// 1. ctx 兜底
		if err := ctx.Err(); err != nil {
			return results, err
		}

		// 2. dir 必须存在,否则记 warning 跳过。
		info, err := os.Stat(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				results = append(results, LoadResult{
					Path:     dir,
					Warnings: []string{fmt.Sprintf("dir not found: %v", err)},
				})
				continue
			}
			results = append(results, LoadResult{
				Path:     dir,
				Warnings: []string{fmt.Sprintf("stat failed: %v", err)},
			})
			continue
		}
		if !info.IsDir() {
			results = append(results, LoadResult{
				Path:     dir,
				Warnings: []string{"path is not a directory"},
			})
			continue
		}

		// 3. 在 dir 下递归扫:任一层出现 manifest 文件即视为一个 candidate root。
		candidates, err := walkCandidates(ctx, dir)
		if err != nil {
			// walk 失败(权限不足等)记 warning,不中断。
			results = append(results, LoadResult{
				Path:     dir,
				Warnings: []string{fmt.Sprintf("walk failed: %v", err)},
			})
			continue
		}

		for _, cand := range candidates {
			if err := ctx.Err(); err != nil {
				return results, err
			}
			r, err := loadOne(ctx, cand, val)
			if err != nil {
				// loadOne 的错误已并入 r.Warnings;只有当 r 为 nil 时才回到这里。
				results = append(results, LoadResult{
					Path:     cand,
					Warnings: []string{err.Error()},
				})
				continue
			}
			results = append(results, r)
		}
	}

	return results, nil
}

// walkCandidates 在 root 下递归扫描,凡能识别出 manifest 的目录都作为 candidate。
// 实现策略:走 fs.WalkDir,对每个目录调用 DetectFormat;命中即记录该目录路径。
// walks 只检查一层 detect 不重不漏:DetectFormat 对深层的 .claude-plugin/plugin.json
// 也能命中,因此递归扫描是必要的。
func walkCandidates(ctx context.Context, root string) ([]string, error) {
	out := make([]string, 0)
	// 优先检查 root 自身是否为 candidate,再递归(避免漏掉 root 本身就放 manifest 的场景)。
	if _, err := DetectFormat(root); err == nil {
		out = append(out, root)
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// 单目录读不动不中断整体,跳过这个分支。
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == root {
			// 根目录已经走过,不再重复。
			return nil
		}
		// 对每个子目录快速 stat 一次 plugin.json / .claude-plugin/plugin.json /
		// openclaw.plugin.json,避免每次都全量 scan;bare_skills 需要进一步遍历,所以
		// 这里只对前三种文件做命中剪枝,bare_skills 由 DetectFormat 内部完成。
		if quickHasPluginJSON(path) {
			if _, err := DetectFormat(path); err == nil {
				out = append(out, path)
				// 命中 manifest 的目录,不再深入扫描其子目录(防嵌套 plugin)。
				return fs.SkipDir
			}
		}
		// 即使 quickHas 未命中,也跑一次完整的 DetectFormat 以覆盖
		// skills/<name>/SKILL.md 等 scan-only 形态(成本可控)。
		if _, err := DetectFormat(path); err == nil {
			out = append(out, path)
			return fs.SkipDir
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipDir) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return out, err
	}
	return out, nil
}

// quickHasPluginJSON 不读真实文件内容,只看 3 种已知 manifest 文件是否存在,作为
// detect 前的剪枝;DetectFormat 才是权威来源。
func quickHasPluginJSON(dir string) bool {
	candidates := []string{
		filepath.Join(dir, "plugin.json"),
		filepath.Join(dir, ".claude-plugin", "plugin.json"),
		filepath.Join(dir, "openclaw.plugin.json"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// loadOne 对单个 candidate 目录做一次完整解析,包含 detect、json 解析、format 补齐
// 与入口沙箱校验。警告统一进 Warnings。
func loadOne(ctx context.Context, dir string, val Validator) (LoadResult, error) {
	r := LoadResult{Path: dir}

	fmt2, err := DetectFormat(dir)
	if err != nil {
		// 理论上 walkCandidates 已经筛过,这里再守一道。
		return r, fmt.Errorf("detect format: %w", err)
	}

	fileName := ManifestFileName(fmt2)
	if fileName == "" {
		// bare_skills:暂不构造 manifest,只标记路径与 format=FormatBareSkills。
		// Phase 3 adapter 在消费时按目录扫描 SKILL.md 自构造 Capability。
		r.Warnings = append(r.Warnings,
			fmt.Sprintf("format %q has no central manifest; phase 3 will scan SKILL.md inline", fmt2))
		return r, nil
	}

	manifestPath := filepath.Join(dir, fileName)
	m, err := parseManifestFile(manifestPath, fmt2)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("parse %s: %v", manifestPath, err))
		return r, nil
	}
	r.Manifest = m

	// 入口路径沙箱校验(只在 manifest 显式声明 entry 时启用)。
	if m.Entry != "" && val != nil {
		entryPath := m.Entry
		if !filepath.IsAbs(entryPath) {
			entryPath = filepath.Join(dir, entryPath)
		}
		if err := val.PathSafeUnderRoot(entryPath, dir); err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("entry sandbox reject: %v", err))
		}
	}

	if err := ctx.Err(); err != nil {
		return r, err
	}
	return r, nil
}

// parseManifestFile 读取 path 指向的 JSON,反序列化为 PluginManifest;若
// manifest.Format 为空,补成 fmt。
func parseManifestFile(path string, fmt2 Format) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m PluginManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal %s: %w", path, err)
	}
	if m.Format == "" {
		m.Format = fmt2
	}
	return &m, nil
}
