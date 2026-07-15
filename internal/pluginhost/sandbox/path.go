// Package sandbox 提供 plugin 路径与权限校验,是 pluginhost 安全边界的第一道闸。
//
// 本文件定义路径校验工具:
//   - EvalSymlinks:filepath.EvalSymlinks 的薄包装(便于外部注入 mock)。
//   - PathHasPrefix:统一 Windows / POSIX 分隔符后的字符串前缀判断。
//   - PathSafeUnderRoot:校验 p 在解符号链接后落在 root 范围内,落出则返 ErrPathEscape。
//
// 参考实现:internal/manager/biz/aiops/chatruntime/plugin_container.go 的
// pathSafeUnderRoot(只读,本文件是 pluginhost 的独立版本,不与 chatruntime 耦合)。
package sandbox

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrPathEscape:PathSafeUnderRoot 检测到路径在解符号链接后越过 root 范围。
var ErrPathEscape = errors.New("sandbox: path escapes allowed root")

// EvalSymlinks 包装 filepath.EvalSymlinks。
//
// 单独抽出是为了在测试中可以通过包内变量替换(pack 内的 unexported
// var evalSymlinks = filepath.EvalSymlinks);Phase 2 引入单测时使用。
func EvalSymlinks(p string) (string, error) {
	return filepath.EvalSymlinks(p)
}

// PathHasPrefix 判断 p 是否以 prefix 开头(Windows 路径分隔符已统一)。
//
// 规则:
//   - 先 filepath.Clean 两端,处理 "./" / 重复斜杠等
//   - 相等视为前缀成立
//   - 否则在 prefix 末尾补 OS 分隔符,避免 "/foo" 误匹配 "/foobar"
func PathHasPrefix(p, prefix string) bool {
	pp := filepath.Clean(p)
	pr := filepath.Clean(prefix)
	if pp == pr {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(pr, sep) {
		pr += sep
	}
	return strings.HasPrefix(pp, pr)
}

// PathSafeUnderRoot 校验 p 在解符号链接后落在 root 范围内。
//
// 流程:
//  1. 分别对 p / root 调 EvalSymlinks;任一失败向上传播原 error(让调用方
//     区分"路径不存在"和"路径越狱")。
//  2. 解符号链接后的 p 必须以解符号链接后的 root 为前缀(含相等);不满足
//     返 ErrPathEscape。
//
// 字符串前缀判断有经典的 "/tmp/evil → /etc" 越狱风险,所以必须先
// EvalSymlinks 再做前缀;不省略 EvalSymlinks 的另一好处是路径不存在
// (broken symlink) 会在这里 fail-fast,而不是穿透到下游。
func PathSafeUnderRoot(p, root string) error {
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return err
	}
	rr, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if !PathHasPrefix(rp, rr) {
		return ErrPathEscape
	}
	return nil
}
