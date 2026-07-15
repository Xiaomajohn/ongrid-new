package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	pluginManifestFile   = "plugin.json"
	compiledManifestFile = ".compiled-manifest.json"
	buildToolName        = "pluginhost-manifest-builder v1"
)

// BuildOne 将标准 plugin.json 编译为 server 启动时使用的目录索引。
func BuildOne(ctx context.Context, rootDir string) (CompiledManifest, error) {
	if err := ctx.Err(); err != nil {
		return CompiledManifest{}, err
	}

	rootDir = filepath.Clean(rootDir)
	manifestPath := filepath.Join(rootDir, pluginManifestFile)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return CompiledManifest{}, errors.Join(errors.New("manifest: read plugin.json"), err)
	}

	var pluginManifest PluginManifest
	if err := json.Unmarshal(data, &pluginManifest); err != nil {
		return CompiledManifest{}, errors.Join(errors.New("manifest: parse plugin.json"), err)
	}

	files, err := scanFiles(ctx, rootDir)
	if err != nil {
		return CompiledManifest{}, err
	}

	compiled := CompiledManifest{
		PluginManifest: pluginManifest,
		Files:          files,
		BuiltAt:        time.Now(),
		BuildTool:      buildToolName,
	}
	if err := writeJSONAtomic(ctx, filepath.Join(rootDir, compiledManifestFile), compiled); err != nil {
		return CompiledManifest{}, err
	}
	return compiled, nil
}

// BuildFromFormat 将第三方清单转换成标准 plugin.json 后再执行 BuildOne。
func BuildFromFormat(ctx context.Context, rootDir string, fromFmt string) (CompiledManifest, error) {
	if err := ctx.Err(); err != nil {
		return CompiledManifest{}, err
	}

	rootDir = filepath.Clean(rootDir)
	sourcePath, err := sourceManifestPath(rootDir, Format(fromFmt))
	if err != nil {
		return CompiledManifest{}, err
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return CompiledManifest{}, errors.Join(errors.New("manifest: read source manifest"), err)
	}

	var source struct {
		Name    string `json:"name"`
		UUID    string `json:"uuid"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return CompiledManifest{}, errors.Join(errors.New("manifest: parse source manifest"), err)
	}

	// TODO:后续 phase 按各第三方格式补齐 capability、entry、transport 等转换规则。
	pluginManifest := PluginManifest{
		Name:         source.Name,
		UUID:         source.UUID,
		Version:      source.Version,
		Format:       FormatPluginHost,
		Capabilities: []Capability{},
	}
	if err := writeJSONAtomic(ctx, filepath.Join(rootDir, pluginManifestFile), pluginManifest); err != nil {
		return CompiledManifest{}, err
	}
	return BuildOne(ctx, rootDir)
}

func sourceManifestPath(rootDir string, format Format) (string, error) {
	switch format {
	case FormatClaude:
		return filepath.Join(rootDir, ".claude-plugin", pluginManifestFile), nil
	case FormatOpenclaw:
		return filepath.Join(rootDir, "openclaw.plugin.json"), nil
	case FormatBareSkills:
		// bare_skills 暂以根目录 plugin.json 作为转换元数据,详细规则留后续 phase。
		return filepath.Join(rootDir, pluginManifestFile), nil
	default:
		return "", errors.New("manifest: unsupported source format")
	}
}

func scanFiles(ctx context.Context, rootDir string) ([]CompiledFile, error) {
	files := make([]CompiledFile, 0)
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == rootDir {
			return nil
		}
		if isHidden(entry.Name()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		files = append(files, CompiledFile{
			Path: filepath.ToSlash(relativePath),
			Size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, errors.Join(errors.New("manifest: scan plugin files"), err)
	}
	return files, nil
}

func isHidden(name string) bool {
	return len(name) > 0 && name[0] == '.'
}

func writeJSONAtomic(ctx context.Context, targetPath string, value any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.Join(errors.New("manifest: encode json"), err)
	}
	data = append(data, '\n')

	tempPath := targetPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		return errors.Join(errors.New("manifest: write temporary file"), err)
	}
	if err := ctx.Err(); err != nil {
		cleanupErr := os.Remove(tempPath)
		if cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			return errors.Join(err, cleanupErr)
		}
		return err
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		cleanupErr := os.Remove(tempPath)
		if cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			return errors.Join(errors.New("manifest: rename compiled file"), err, cleanupErr)
		}
		return errors.Join(errors.New("manifest: rename compiled file"), err)
	}
	return nil
}
