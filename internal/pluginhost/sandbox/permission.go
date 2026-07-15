package sandbox

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrNetworkDenied = errors.New("sandbox: network denied")
	ErrFSWriteDenied = errors.New("sandbox: fs write denied")
	ErrEnvDenied     = errors.New("sandbox: env denied")
)

type Permission struct {
	NetworkEgress []string
	FSWrite       []string
	EnvAccess     []string
}

func (p Permission) ValidateNetwork(addr string) error {
	if len(p.NetworkEgress) == 0 {
		return ErrNetworkDenied
	}
	host, port, hasPort, err := parseEndpoint(addr)
	if err != nil {
		return ErrNetworkDenied
	}
	for _, rule := range p.NetworkEgress {
		ruleHost, rulePort, ruleHasPort, err := parseEndpoint(rule)
		if err != nil || (ruleHasPort && (!hasPort || rulePort != port)) {
			continue
		}
		if hostMatches(ruleHost, host) {
			return nil
		}
	}
	return ErrNetworkDenied
}

func (p Permission) ValidateFSWrite(path, root string) error {
	if len(p.FSWrite) == 0 {
		return ErrFSWriteDenied
	}
	if !pathSafeForWrite(path, root) {
		return ErrPathEscape
	}

	for _, allowed := range p.FSWrite {
		allowedPath := allowed
		if !filepath.IsAbs(allowedPath) {
			allowedPath = filepath.Join(root, allowedPath)
		}
		if !pathSafeForWrite(allowedPath, root) {
			continue
		}
		if PathHasPrefix(path, allowedPath) {
			return nil
		}
	}
	return ErrFSWriteDenied
}

func (p Permission) ValidateEnv(name string) error {
	for _, allowed := range p.EnvAccess {
		if allowed == name {
			return nil
		}
	}
	return ErrEnvDenied
}

func pathSafeForWrite(path, root string) bool {
	if PathSafeUnderRoot(path, root) {
		return true
	}

	if _, err := os.Lstat(path); err == nil {
		return false
	}
	resolvedRoot, err := EvalSymlinks(root)
	if err != nil {
		return false
	}

	candidate := filepath.Clean(path)
	for {
		resolved, err := EvalSymlinks(candidate)
		if err == nil {
			return PathHasPrefix(resolved, resolvedRoot)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return false
		}
		candidate = parent
	}
}

func parseEndpoint(value string) (host, port string, hasPort bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false, errors.New("empty address")
	}

	if strings.Contains(value, "://") {
		u, parseErr := url.Parse(value)
		if parseErr != nil || u.Hostname() == "" {
			return "", "", false, errors.New("invalid address")
		}
		return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), u.Port(), u.Port() != "", nil
	}
	if strings.Contains(value, "/") {
		u, parseErr := url.Parse("http://" + value)
		if parseErr != nil || u.Hostname() == "" {
			return "", "", false, errors.New("invalid address")
		}
		return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), u.Port(), u.Port() != "", nil
	}

	if h, p, splitErr := net.SplitHostPort(value); splitErr == nil {
		return strings.ToLower(strings.TrimSuffix(h, ".")), p, true, nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	return strings.ToLower(strings.TrimSuffix(value, ".")), "", false, nil
}

func hostMatches(rule, host string) bool {
	rule = strings.ToLower(strings.TrimSpace(rule))
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if rule == "*" {
		return host != ""
	}
	if strings.HasPrefix(rule, "*.") {
		suffix := strings.TrimPrefix(rule, "*")
		return strings.HasSuffix(host, suffix) || host == strings.TrimPrefix(suffix, ".")
	}
	return rule == host
}
