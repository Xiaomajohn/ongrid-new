// Format 自动识别:在给定 plugin 根目录下,按 pluginhost > claude > openclaw >
// bare_skills 顺序探测,把命中的第一个格式返回;都没有时返回 ErrUnknownManifest。
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnknownManifest 表示目录里没有任何已知的 manifest 文件。
var ErrUnknownManifest = errors.New("manifest: unknown format")

// pluginJSONRelative 是 FormatPluginHost 对应的清单文件相对路径。
const pluginJSONRelative = "plugin.json"

// claudePluginJSONRelative 是 FormatClaude 对应的清单文件相对路径。
const claudePluginJSONRelative = ".claude-plugin/plugin.json"

// openclawPluginJSONRelative 是 FormatOpenclaw 对应的清单文件相对路径。
const openclawPluginJSONRelative = "openclaw.plugin.json"

// skillsSubdir 是 FormatBareSkills 探测的散落技能子目录名。
const skillsSubdir = "skills"

// DetectFormat 按如下优先级识别 rootDir 下的 manifest 格式:
//  1. <root>/plugin.json 存在 → FormatPluginHost(读其 format 字段覆盖默认值)
//  2. <root>/.claude-plugin/plugin.json 存在 → FormatClaude
//  3. <root>/openclaw.plugin.json 存在 → FormatOpenclaw
//  4. <root>/skills/<name>/SKILL.md 任一存在 → FormatBareSkills
//  5. 都未命中 → ErrUnknownManifest
func DetectFormat(rootDir string) (Format, error) {
	// 1. pluginhost(主推):plugin.json
	pj := filepath.Join(rootDir, pluginJSONRelative)
	if exists, err := fileExists(pj); err != nil {
		return "", fmt.Errorf("stat %s: %w", pj, err)
	} else if exists {
		// 读 json,format 字段空则默认 pluginhost;显式给出则尊重原值。
		// 这里只做存在性层面的 format 推断,把真正的字段读解析交给 parseManifestFile。
		f, err := peekFormatField(pj)
		if err != nil {
			// JSON 解析失败但文件存在,仍按 pluginhost 走,后续 loader 会把解析错误写入 Warnings。
			return FormatPluginHost, nil
		}
		if f == "" {
			return FormatPluginHost, nil
		}
		switch Format(f) {
		case FormatPluginHost, FormatClaude, FormatOpenclaw, FormatBareSkills:
			return Format(f), nil
		default:
			// 未知值回退到 pluginhost。
			return FormatPluginHost, nil
		}
	}

	// 2. claude 向后兼容
	cp := filepath.Join(rootDir, claudePluginJSONRelative)
	if exists, err := fileExists(cp); err != nil {
		return "", fmt.Errorf("stat %s: %w", cp, err)
	} else if exists {
		return FormatClaude, nil
	}

	// 3. openclaw
	op := filepath.Join(rootDir, openclawPluginJSONRelative)
	if exists, err := fileExists(op); err != nil {
		return "", fmt.Errorf("stat %s: %w", op, err)
	} else if exists {
		return FormatOpenclaw, nil
	}

	// 4. bare_skills:扫 skills/ 下任一含 SKILL.md 的子目录即视作命中。
	ok, err := hasAnySkillMD(filepath.Join(rootDir, skillsSubdir))
	if err != nil {
		return "", fmt.Errorf("scan %s: %w", filepath.Join(rootDir, skillsSubdir), err)
	}
	if ok {
		return FormatBareSkills, nil
	}

	return "", ErrUnknownManifest
}

// ManifestFileName 返回给定 format 对应的 manifest 文件名(相对路径形式)。
// 只对前 3 种有明确清单文件名的 format 适用;bare_skills 没有集中清单,返回空字符串。
func ManifestFileName(fmt2 Format) string {
	switch fmt2 {
	case FormatPluginHost:
		return pluginJSONRelative
	case FormatClaude:
		return claudePluginJSONRelative
	case FormatOpenclaw:
		return openclawPluginJSONRelative
	case FormatBareSkills:
		// bare_skills 没有集中清单,调用方应自行扫描 skills/<name>/SKILL.md。
		return ""
	default:
		return ""
	}
}

// fileExists 区分"不存在"与其他 stat 错误。
func fileExists(p string) (bool, error) {
	info, err := os.Stat(p)
	if err == nil {
		return !info.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// hasAnySkillMD 在 dir 下深度 1(直接子目录)查找任一 <name>/SKILL.md 命中即返回 true。
// 为避免深递归扫到与 plugin 无关的文件,这里只走一层。
func hasAnySkillMD(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillMD := filepath.Join(dir, e.Name(), "SKILL.md")
		ok, err := fileExists(skillMD)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// peekFormatField 仅读 plugin.json 的 format 字段,不做完整解析;失败不返回错误,
// 由调用方决定是否回退到 pluginhost 默认值。
func peekFormatField(p string) (Format, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	var probe struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", err
	}
	return Format(strings.TrimSpace(probe.Format)), nil
}
