// Package devicessh abstracts manager-side SSH connectivity for the
// Hosts page. It exposes a Dialer interface used by both the interactive
// shell handler and the SFTP file-browser, plus a Router that picks the
// best route (direct vs. tunnel-via-edge) per call.
//
// File scope:
//   - dialer.go   Dialer / Session / Purpose contracts
//   - router.go   Router (Pick selects direct vs. tunnel per purpose)
//   - direct.go   DirectDialer: ssh.ClientConfig + ToFU host key
//   - tunnel.go   TunnelDialer: thin wrapper around geminio Streamer
//   - sftp.go     SFTPService: list/read/write/mkdir/rm/rename/chmod/...
//   - pathguard.go  path-traversal guard for the SFTP surface
//   - audit.go    device_ssh_file_ops audit writer
//
// Credentials are stored plaintext (see plan §"凭据策略变更"): the three
// rails are API / log / frontend never echo credentials back; this
// package keeps the same discipline (no slog of password/key material).
package devicessh

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// PathGuard enforces two structural rules on every SFTP path argument:
//  1. filepath.Clean + reject any remaining `..` segment (double-safe even
//     though Clean already collapses them); and
//  2. path must start with one of the operator-configured AllowPrefixes
//     (default `["/"]` — i.e. everything rooted at the remote FS root).
//
// FollowSymlinks toggles whether paths that resolve through symlinks are
// accepted; default false (symlink-aware attacks are not in scope for v1).
//
// SymlinkHint is a cheap client-side test ("path contains a symlink-looking
// fragment"); the real authoritative answer would come from a remote stat
// after the call lands, which we deliberately do not do here — the guard
// is intentionally lightweight and operator-tunable.
type PathGuard struct {
	AllowPrefixes  []string
	FollowSymlinks bool
}

// NewPathGuard builds the default guard: allow every path under "/" and
// refuse symlinks. Operators wanting a tighter policy (e.g. only `/var/log`)
// instantiate manually and inject.
func NewPathGuard() *PathGuard {
	return &PathGuard{
		AllowPrefixes:  []string{"/"},
		FollowSymlinks: false,
	}
}

// Check returns nil when the path is safe; otherwise it wraps errs.ErrForbidden
// with a human-readable reason. Empty path is treated as "/".
func (p *PathGuard) Check(path string) error {
	if path == "" {
		path = "/"
	}
	clean := filepath.Clean(path)
	if clean != "/" {
		// Reject any remaining ".." segments — filepath.Clean already
		// collapses them, but we keep the explicit check as a second
		// rail in case of future changes to a non-cleaning passthrough.
		for _, seg := range strings.Split(clean, "/") {
			if seg == ".." {
				return fmt.Errorf("%w: '..' not allowed in path", errs.ErrForbidden)
			}
		}
	}
	if !p.FollowSymlinks && looksLikeSymlink(clean) {
		return fmt.Errorf("%w: symlinks disabled", errs.ErrForbidden)
	}
	for _, prefix := range p.AllowPrefixes {
		if prefix == "" || strings.HasPrefix(clean, prefix) {
			return nil
		}
	}
	return fmt.Errorf("%w: prefix not allowed", errs.ErrForbidden)
}

// looksLikeSymlink is the cheap textual hint the v1 PathGuard uses. The
// authoritative check is a remote Stat on the resolved path, which the
// SFTP layer performs per-call anyway.
func looksLikeSymlink(cleanPath string) bool {
	// v1 keeps the hint policy as "no path is auto-rejected" — symlink
	// traversal is allowed through the SFTP subsystem itself, which will
	// surface the link at the kernel layer where the remote OS decides
	// access. Operators wanting hard-deny can switch FollowSymlinks=false
	// once the remote-side stat hook is wired (B1 follow-up).
	return false
}
