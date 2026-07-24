// Package audit is the edge-side `audit` plugin.
//
// It wraps an auditbeat subprocess (Elastic closed-source binary):
// ongrid-edge writes an auditbeat.yml derived from the manager-pushed
// PluginConfig, spawns auditbeat, and auditbeat writes JSONL events to
// <workDir>/audit/audit.jsonl. The logs plugin (promtail) tails that
// path via audit.OutputPath(workDir) as an unconditional __path__ glob
// (no on-disk probe, see logs/render.go) — no separate push channel
// for audit.
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
//   --path.home <dir>  : override auditbeat's default path.home. The tar.gz
//                         default for a Beats distribution is the directory
//                         the binary lives in (= binDir = LIB_DIR), which on
//                         this edge is root:root 0755; auditbeat then tries
//                         to mkdir ${LIB_DIR}/data + ${LIB_DIR}/logs as the
//                         ongrid-edge user and crashes with EROFS / EACCES
//                         ("failed to create data path ... read-only file
//                         system" / "permission denied"). Forcing path.home
//                         to pluginDir puts data/, logs/, and the relative
//                         output.file.path under a tree the edge agent's
//                         supervisor already chowns to ongrid-edge
//                         (see internal/edgeagent/plugins/subprocess.go
//                         Configure — os.MkdirAll(workDir, 0o755) under the
//                         effective uid ongrid-edge).
//
//                         Note: this MUST be `--path.home` (double dash).
//                         Beats uses kingpin as its CLI parser and the
//                         long form is registered as `--path.home`. With a
//                         single dash, kingpin drops the value into a
//                         positional-arg slot and surfaces
//                         `Error: unknown command "<pluginDir>" for
//                         "auditbeat"` — i.e. it tries to dispatch the
//                         path as a subcommand name and never sets
//                         path.home at all.
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
		ConfigRender: renderWith(pluginDir),
		Args: func(_ plugins.PluginConfig, configFile string) []string {
			return []string{
				"-c", configFile,
				"-e",
				"--strict.perms=false",
				// See New docstring: force auditbeat's path.home to pluginDir
				// so data/, logs/, and relative output paths land somewhere
				// the ongrid-edge user can actually write.
				//
				// MUST be `--path.home` (kingpin long form). With a single
				// dash, kingpin treats the next token as a subcommand and
				// we get `Error: unknown command "<pluginDir>" ...`.
				"--path.home", pluginDir,
			}
		},
		Log: log,
	})
}

// OutputPath returns the JSONL file glob the auditbeat subprocess
// writes to under default configuration. Exposed so the logs plugin
// (promtail) can include it as an unconditional file_path glob
// without operator configuration — see
// internal/edgeagent/plugins/logs/render.go for the consumer side.
// The logs renderer appends this glob on every render (no on-disk
// probe), so the default-enabled audit plugin's events are picked up
// in Loki without race conditions against auditbeat's first write.
//
// auditbeat 9.x's file output appends a daily suffix + .ndjson extension
// to whatever filename is set in auditbeat.yml: filename "audit.jsonl"
// produces files named `audit.jsonl-YYYYMMDD.ndjson` (and rotated
// siblings `audit.jsonl-YYYYMMDD-1.ndjson`, `audit.jsonl-YYYYMMDD-2.ndjson`,
// ...). The `-YYYYMMDD` portion is mandatory and not configurable —
// Beats 8.x dropped the old "filename only" behaviour. So we return a
// glob that matches every variant the writer will produce, and let
// promtail's `__path__` glob support handle rotation implicitly (one
// stable scrape job covers today + all rotated siblings).
//
// The glob is deliberately narrow (`-*.ndjson`, not just `*`):
//   - it requires the .ndjson extension auditbeat always uses
//   - it requires the leading dash + suffix that auditbeat always emits
//   - so operator-added files (e.g. an `audit.jsonl.snapshot` artifact
//     dropped by a custom exporter) won't accidentally get pulled into
//     the audit Loki stream with the wrong label.
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
	return path.Join(workDir, Name, "audit.jsonl-*.ndjson")
}