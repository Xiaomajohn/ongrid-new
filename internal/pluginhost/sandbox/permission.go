// Package sandbox / permission.go 是 pluginhost 安全边界的第二道闸。
//
// 三类副作用的 allowlist 校验:
//   - ValidateNetwork:出网 host:port 是否在 NetworkEgress 白名单内。
//   - ValidateFSWrite:写文件路径是否在 FSWrite 白名单前缀内(且路径在 root 内)。
//   - ValidateEnv:环境变量读取是否在 EnvAccess 白名单内。
//
// 默认策略:空白名单 = 拒绝所有(零信任,不允许"未声明即放行")。
package sandbox

import (
	"errors"
	"net/url"
	"strings"
)

// 三类操作的拒绝错误。
var (
	ErrNetworkDenied = errors.New("sandbox: network denied")
	ErrFSWriteDenied = errors.New("sandbox: fs write denied")
	ErrEnvDenied     = errors.New("sandbox: env denied")
)

// Permission 是 plugin 声明的白名单三件套。
//
// 字段都允许为空切片;空切片的语义是"完全不允许",而不是"全部允许"。
// Phase 2 的 manifest 解析层负责把空切片和"未声明"区分开。
type Permission struct {
	NetworkEgress []string
	FSWrite       []string
	EnvAccess     []string
}

// ValidateNetwork 检查 addr 是否命中 NetworkEgress 白名单。
//
// addr 形态:
//   - "host:port"         — 匹配 host(忽略 port)
//   - "http(s)://host:port/path" — 走 URL 解析取 Hostname()
//   - "*.example.com"     — 后缀通配
//
// NetworkEgress 为空直接拒绝所有,避免"未声明即放行"。
func (p Permission) ValidateNetwork(addr string) error {
	if len(p.NetworkEgress) == 0 {
		return ErrNetworkDenied
	}
	host, err := hostFromAddr(addr)
	if err != nil {
		return ErrNetworkDenied
	}
	for _, allow := range p.NetworkEgress {
		if matchHost(allow, host) {
			return nil
		}
	}
	return ErrNetworkDenied
}

// ValidateFSWrite 检查 path 是否在 FSWrite 白名单任一前缀内,且 root 范围合法。
//
// 流程:
//  1. FSWrite 为空直接拒绝所有。
//  2. path 必须先通过 PathSafeUnderRoot(path, root) 防越狱。
//  3. 对每个白名单前缀:白名单项本身也必须在 root 内(防白名单项把权限
//     圈到 root 之外),且 path 必须以白名单项为前缀。
//
// 第二条保证了"白名单项 = 子根";如果不校验 allow 自身,plugin 可以
// 把白名单写成 "/etc" 然后 path 落在 /etc 下,直接绕过 root 限制。
func (p Permission) ValidateFSWrite(path, root string) error {
	if len(p.FSWrite) == 0 {
		return ErrFSWriteDenied
	}
	if err := PathSafeUnderRoot(path, root); err != nil {
		return ErrFSWriteDenied
	}
	for _, allow := range p.FSWrite {
		// allow 自身必须在 root 内,否则跳过(避免 allow=/etc 这类越权配置)
		if err := PathSafeUnderRoot(allow, root); err != nil {
			continue
		}
		if PathHasPrefix(path, allow) {
			return nil
		}
	}
	return ErrFSWriteDenied
}

// ValidateEnv 检查 env 变量名是否在 EnvAccess 白名单内(精确匹配)。
//
// EnvAccess 为空直接拒绝所有。
func (p Permission) ValidateEnv(name string) error {
	if len(p.EnvAccess) == 0 {
		return ErrEnvDenied
	}
	for _, allow := range p.EnvAccess {
		if allow == name {
			return nil
		}
	}
	return ErrEnvDenied
}

// hostFromAddr 从 addr 提取 host(去掉 port 与 path)。
//
// 优先用 net/url 解析;若 addr 不带 scheme 则补 "http://" 让 url.Parse
// 能正确识别 host:port 形态。解析失败或 host 为空一律视为非法。
func hostFromAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("empty address")
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	if host == "" {
		return "", errors.New("empty host")
	}
	return host, nil
}

// matchHost 判断白名单项 allow 是否覆盖 host。
//
// 规则:
//   - "*.example.com" 形式:host 以 ".example.com" 结尾,或 host 本身等于 "example.com"
//   - "host" / "host:port" 形式:去掉末尾端口后精确比较
//
// 大小写不敏感(host 在 DNS 协议层不敏感,allow 应统一小写书写)。
func matchHost(allow, host string) bool {
	allow = strings.TrimSpace(strings.ToLower(allow))
	host = strings.ToLower(host)
	if allow == "" {
		return false
	}
	// 通配后缀:".example.com"
	if strings.HasPrefix(allow, "*.") {
		suffix := allow[1:] // ".example.com"
		return strings.HasSuffix(host, suffix) || host == allow[2:]
	}
	// 去端口后精确比较
	a := allow
	if i := strings.LastIndex(a, ":"); i >= 0 {
		a = a[:i]
	}
	return a == host
}
