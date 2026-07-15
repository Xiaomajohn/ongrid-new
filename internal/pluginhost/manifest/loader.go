package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
)

const defaultPluginDir = "/var/lib/ongrid/plugins"

// LoadDirsConfig 控制启动时的 plugin 根目录与开发模式回退。
type LoadDirsConfig struct {
	Dirs    []string
	DevMode bool
}

// LoadResult 是一个已加载的编译 plugin。
type LoadResult struct {
	PackID      string
	InstallPath string
	Manifest    *CompiledManifest
}

// LoadDirs 只列举根目录的一级子目录并读取编译清单。
func LoadDirs(ctx context.Context, cfg LoadDirsConfig) ([]LoadResult, error) {
	dirs := cfg.Dirs
	if len(dirs) == 0 {
		dirs = []string{defaultPluginDir}
	}

	results := make([]LoadResult, 0)
	for _, rootDir := range dirs {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		rootDir = filepath.Clean(rootDir)
		entries, err := os.ReadDir(rootDir)
		if err != nil {
			slog.WarnContext(ctx, "pluginhost:读取插件目录失败,已跳过",
				"root_dir", rootDir,
				"err", err,
			)
			continue
		}

		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return results, err
			}
			if !entry.IsDir() || isHidden(entry.Name()) {
				continue
			}

			pluginDir := filepath.Join(rootDir, entry.Name())
			compiled, ok := loadCompiledManifest(ctx, pluginDir, cfg.DevMode)
			if !ok {
				continue
			}

			fileSet := compiledFileSet(compiled.Files)
			if compiled.Entry != "" && !compiledPathExists(fileSet, compiled.Entry) {
				slog.WarnContext(ctx, "pluginhost:后端入口不在编译清单中,已跳过",
					"plugin_dir", pluginDir,
					"entry", compiled.Entry,
				)
				continue
			}
			if compiled.Frontend != nil && (compiled.Frontend.Entry == "" || !compiledPathExists(fileSet, compiled.Frontend.Entry)) {
				slog.WarnContext(ctx, "pluginhost:前端入口不在编译清单中,已跳过",
					"plugin_dir", pluginDir,
					"entry", compiled.Frontend.Entry,
				)
				continue
			}

			results = append(results, LoadResult{
				PackID:      compiled.Name,
				InstallPath: filepath.Join(rootDir, compiled.Name),
				Manifest:    compiled,
			})
		}
	}
	return results, nil
}

func loadCompiledManifest(ctx context.Context, pluginDir string, devMode bool) (*CompiledManifest, bool) {
	compiledPath := filepath.Join(pluginDir, compiledManifestFile)
	data, err := os.ReadFile(compiledPath)
	if err != nil && devMode && errors.Is(err, os.ErrNotExist) {
		pluginPath := filepath.Join(pluginDir, pluginManifestFile)
		info, statErr := os.Stat(pluginPath)
		if statErr == nil && !info.IsDir() {
			if _, buildErr := BuildOne(ctx, pluginDir); buildErr != nil {
				slog.WarnContext(ctx, "pluginhost:开发模式现场编译失败,已跳过",
					"plugin_dir", pluginDir,
					"err", buildErr,
				)
				return nil, false
			}
			data, err = os.ReadFile(compiledPath)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			slog.WarnContext(ctx, "pluginhost:检查开发清单失败,已跳过",
				"plugin_json", pluginPath,
				"err", statErr,
			)
			return nil, false
		}
	}
	if err != nil {
		slog.WarnContext(ctx, "pluginhost:读取编译清单失败,已跳过",
			"compiled_manifest", compiledPath,
			"err", err,
		)
		return nil, false
	}

	var compiled CompiledManifest
	if err := json.Unmarshal(data, &compiled); err != nil {
		slog.WarnContext(ctx, "pluginhost:解析编译清单失败,已跳过",
			"compiled_manifest", compiledPath,
			"err", err,
		)
		return nil, false
	}
	return &compiled, true
}

func compiledFileSet(files []CompiledFile) map[string]struct{} {
	fileSet := make(map[string]struct{}, len(files))
	for _, file := range files {
		fileSet[normalizeCompiledPath(file.Path)] = struct{}{}
	}
	return fileSet
}

func compiledPathExists(fileSet map[string]struct{}, path string) bool {
	_, ok := fileSet[normalizeCompiledPath(path)]
	return ok
}

func normalizeCompiledPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
}
