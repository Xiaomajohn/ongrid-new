// Package audit is the edge-side `audit` plugin.
//
// It wraps an auditbeat subprocess (Elastic closed-source binary):
// ongrid-edge writes an auditbeat.yml derived from the manager-pushed
// PluginConfig, spawns auditbeat, and auditbeat writes JSONL events to
// <workDir>/audit/audit.jsonl. The logs plugin (promtail) auto-discovers
// that path via audit.OutputPath(workDir) and tails it into Loki — no
// separate push channel for audit.
//
// Plugin name "audit" matches the ongrid lowercase domain convention
// (hostmetrics / procmetrics / custommetrics). It is Linux-only because
// auditbeat depends on the Linux audit subsystem; on darwin edges the
// plugin stays disabled and supervisor reports StateCrashed until the
// operator drops in a custom build.
package audit

import (
	"log/slog"
	"path"

	"github.com/ongridio/ongrid/internal/edgeagent/plugins"
)

// Name is the plugin identifier and the directory key under
// <workDir>/plugins/. Exposed as a constant so the logs plugin (and
// tests) can reference the same string without typos.
const Name = "audit"

// New constructs the audit plugin. binDir is where ongrid-edge looks
// for the bundled auditbeat binary (typically /usr/local/lib/ongrid-edge);
// workDir is where rendered config + auditbeat positions + JSONL output
// + subprocess log live (typically /var/lib/ongrid-edge/plugins).
//
// The returned *plugins.SubprocessPlugin satisfies plugins.Plugin and is
// registered with the Supervisor by ongrid-edge main.
//
// Command-line flags:
//   -c <yml>           : load the rendered config (PluginConfig -> bytes)
//   -e                  : log to stderr (in addition to file — kept here so
//                         operators can `journalctl -u ongrid-edge` and see
//                         auditbeat startup errors without reading plugin log)
//   --strict.perms=false: skip auditbeat's strict config-file permission
//                         check; we run as root via systemd and 0600 is
//                         already tight, so the strict mode only causes
//                         spurious startup failures.
func New(binDir, workDir string, log *slog.Logger) plugins.Plugin {
	// filepath.Join would let the host OS dictate separators — audit
	// only runs on Linux where the auditbeat binary writes
	// forward-slash paths. Use plain path.Join so the contract between
	// audit (writer) and logs (reader) stays OS-agnostic at the Go
	// boundary.
	auditBin := path.Join(binDir, "auditbeat")
	pluginDir := path.Join(workDir, Name)
	return plugins.NewSubprocess(plugins.SubprocessOpts{
		Name:         Name,
		Binary:       auditBin,
		WorkDir:      pluginDir,
		ConfigFile:   path.Join(pluginDir, "auditbeat.yml"),
		ConfigRender: render,
		Args: func(_ plugins.PluginConfig, configFile string) []string {
			return []string{"-c", configFile, "-e", "--strict.perms=false"}
		},
		Log: log,
	})
}

// OutputPath returns the JSONL file path the auditbeat subprocess
// writes to under default configuration. Exposed so the logs plugin
// (promtail) can probe and auto-tail without operator configuration —
// see internal/edgeagent/plugins/logs/render.go for the consumer side.
//
// Uses path.Join (forward-slash) rather than filepath.Join so the
// contract between audit and logs plugins is OS-agnostic — the audit
// plugin only runs on Linux but the edge agent's Go code can be
// cross-compiled / tested on darwin / windows hosts.
//
// Stable across renderings: this is the contract between the audit
// plugin's renderer and the logs plugin's renderer. Renaming the file
// or moving it under a different directory is a breaking change and
// must update both sides in lock-step.
func OutputPath(workDir string) string {
	return path.Join(workDir, Name, "audit.jsonl")
}