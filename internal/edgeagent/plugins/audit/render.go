package audit

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/template"

	"github.com/ongridio/ongrid/internal/edgeagent/plugins"
)

// auditbeatTemplate is the auditbeat YAML config we render per edge.
//
// Why we ship a hand-written template instead of using auditbeat's
// auto-discovery: the auto path requires writable data dirs that vary
// per host, and we need deterministic output to drop host-identifying
// fields (host.hostname / host.os.*) and inject ongrid.device_id for
// Loki label joins. The template is intentionally narrow — it covers
// the modules ops actually use (fim / auditd / system) and bails out
// for anything else via Mode 1 (raw_config passthrough).
//
// Fields auto-injected and NOT exposed to spec:
//   - ongrid.device_id       : stable Loki label for tenant queries
//   - drop_fields            : strip host.* ECS metadata to keep cardinality bounded
//   - file_integrity exclude_files + audit_rules : always inject
//     `audit.*` + `audit\.jsonl.*` so FIM doesn't watch / hash its own
//     state files (which would otherwise spam noise on every rotate /
//     checkpoint). exclude_files uses RE2 regex (auditbeat 9.4.2 dropped
//     glob-shaped exclude_paths in favour of RE2 patterns).
//
// Schema version (auditbeat 9.4.2, see auditbeat.reference.yml):
//   - module names are `file_integrity` (not `fim` — older docs called
//     it fim but the runtime uses file_integrity), `auditd`, `system`.
//   - module config is FLAT — no `fim:` / `auditd:` wrapper inside the
//     module block. options go directly under `- module: file_integrity`.
//   - file_integrity fields used: paths, recursive, scan_at_start,
//     scan_rate_per_sec, hash_types, exclude_files (RE2).
//   - auditd fields used: resolve_ids, failure_mode, backlog_limit,
//     rate_limit, audit_rules. 9.x dropped socket_type (the multicast
//     vs unicast split is gone); we still accept `auditd_socket_type`
//     in Spec for backward compat with old manager pushes but ignore it
//     in the rendered YAML.
//   - system uses `datasets:` list (package / host / login / process /
//     socket / user) plus `state.period:`; per-dataset toggles in
//     auditbeat 8.x (`system.login.enabled`) are gone in 9.x, so the
//     `system_login / user / package / process / socket` Spec booleans
//     are joined into a datasets list.
//
// Path-layout note (see also plugin.go New docstring):
//   - auditbeat is launched with `--path.home <pluginDir>` (double-dash;
//     kingpin long form — single dash is interpreted as a positional
//     subcommand and dispatch fails), so its path.data
//     (= ${path.home}/data), path.logs (= ${path.home}/logs),
//     and any relative output.file.path resolve under pluginDir — a
//     tree the edge agent's supervisor already chowns to ongrid-edge.
//   - output.file.path is therefore written as the ABSOLUTE pluginDir
//     (not a relative "audit" subdir) so the resulting audit.jsonl lands
//     at ${pluginDir}/audit.jsonl, byte-for-byte matching
//     audit.OutputPath(<edgeWorkDir>). That single-file path is what
//     the logs plugin (promtail) auto-tails via the same OutputPath
//     helper — keeping the writer-side and reader-side paths in lock-step
//     avoids the "audit plugin running but no audit events in Loki"
//     silent failure mode.
const auditbeatTemplate = `# Rendered by ongrid-edge audit plugin.
# DO NOT EDIT — regenerated from manager-pushed PluginConfig on every reconcile.

auditbeat:
  modules:
{{- range .Modules }}
{{- if eq . "fim" }}
  - module: file_integrity
    paths:
{{- range $.FimPaths }}
      - {{ . }}
{{- end }}
    recursive: {{ $.FimRecursive }}
    scan_at_start: {{ $.FimScanAtStart }}
    scan_rate_per_sec: {{ $.FimScanRatePerSec }}
    hash_types: [{{ $.FimHashTypes }}]
    exclude_files:
{{- range $.FimExcludeFiles }}
      - {{ . }}
{{- end }}
{{- end }}
{{- if eq . "auditd" }}
  - module: auditd
    resolve_ids: {{ $.AuditdResolveIDs }}
    failure_mode: {{ $.AuditdFailureMode }}
    backlog_limit: {{ $.AuditdBacklogLimit }}
    rate_limit: {{ $.AuditdRateLimit }}
    audit_rules: |
{{- range $.AuditdRules }}
      {{ . }}
{{- end }}
{{- end }}
{{- if eq . "system" }}
  - module: system
    datasets:
{{- range $.SystemDatasets }}
      - {{ . }}
{{- end }}
    state.period: {{ $.SystemStatePeriod }}
{{- end }}
{{- end }}

output.file:
  # Absolute pluginDir (NOT a relative "audit" subdir). See
  # auditbeatTemplate header note for the writer/reader contract with
  # audit.OutputPath / logs plugin promtail auto-tail.
  path: "{{ .WorkDir }}"
  filename: {{ .OutputFilename }}
  rotate_every_kb: 10000
  number_of_files: 7

processors:
  - add_fields:
      target: ''
      fields:
        ongrid.device_id: "{{ .EdgeID }}"
        ongrid.source: "auditbeat"
  - drop_fields:
      fields:
        - host.hostname
        - host.os.kernel
        - host.os.name
        - host.os.version
        - host.os.family
        - host.os.platform
        - host.architecture
        - host.mac
        - host.name
      ignore_missing: true
`

// renderWith returns a SubprocessOpts.ConfigRender-compatible closure
// that captures pluginDir. plugin.go wires this closure into the
// SubprocessPlugin; every reconcile re-invokes it with the latest
// PluginConfig. Splitting the closure builder out lets us add the
// pluginDir parameter to render() without changing SubprocessOpts
// (whose ConfigRender signature is fixed at func(PluginConfig)).
func renderWith(pluginDir string) func(plugins.PluginConfig) ([]byte, error) {
	return func(cfg plugins.PluginConfig) ([]byte, error) {
		return render(pluginDir, cfg)
	}
}

// render builds auditbeat.yml bytes from a PluginConfig and the plugin's
// workDir (= <workDir>/audit). workDir is captured at plugin
// construction time (see plugin.go New → renderWith) so each reconcile
// doesn't need to know it.
//
// Two modes:
//
//	Mode 1 (raw_config) : spec["raw_config"] is a non-empty string.
//	                      We emit it verbatim so power users can ship
//	                      the full auditbeat native YAML (custom modules,
//	                      advanced processors, etc.). All structured
//	                      fields are ignored. The auto-injected
//	                      device_id / drop_fields / excludes / absolute
//	                      output.file.path are also SKIPPED — the
//	                      operator owns the entire YAML and is responsible
//	                      for picking a writable path themselves.
//	Mode 2 (structured) : otherwise. We render the template with the
//	                      structured fields + their defaults.
//
// Spec keys (Mode 2, all optional):
//
//	modules           : []string   — "fim" (alias of file_integrity),
//	                                "auditd", "system" (default ["fim"]).
//	                                In rendered YAML, "fim" is emitted as
//	                                `module: file_integrity` (auditbeat 9.4.2
//	                                runtime name).
//	fim_paths         : []string   — file_integrity watch list (env:
//	                                ONGRID_EDGE_AUDIT_FIM_PATHS, comma-split)
//	fim_recursive     : bool       — scan subdirectories (default true)
//	fim_scan_at_start : bool       — emit baseline events at startup (default true)
//	fim_scan_rate_per_sec : string — startup scan throttle (default "5 MiB")
//	fim_hash_types    : string     — comma-separated auditbeat hash list (default "sha1")
//	output_file       : string     — filename prefix (default "audit.jsonl")
//	auditd_rules      : []string   — custom audit rules (default empty)
//	auditd_resolve_ids: bool       — resolve uid/gid → username/groupname (default true)
//	auditd_failure_mode: silent|log|warn (default "silent")
//	auditd_backlog_limit: int      — listen backlog (default 8192)
//	auditd_rate_limit : int        — kernel audit event rate limit (default 0=off)
//	auditd_socket_type: string     — **deprecated** for auditbeat 9.x (the
//	                                multicast/unicast split is gone); still
//	                                accepted in Spec for backward compat with
//	                                old manager pushes but is NOT emitted to
//	                                the rendered auditbeat.yml.
//	system_state_period : duration — system.state scrape period (default "12h")
//	system_login / user / package / process / socket : bool — joined into a
//	                                datasets: list (default true,true,true,true,false).
func render(workDir string, cfg plugins.PluginConfig) ([]byte, error) {
	if cfg.EdgeID == 0 {
		return nil, fmt.Errorf("audit plugin: device_id required (set ONGRID_EDGE_ID)")
	}

	// Mode 1: raw passthrough.
	if raw := stringField(cfg.Spec, "raw_config"); raw != "" {
		return []byte(raw), nil
	}

	// Mode 2: structured. Build the data model with defaults applied.
	data := buildTemplateData(workDir, cfg)

	tmpl, err := template.New("auditbeat").Funcs(template.FuncMap{
		"eq": func(a, b string) bool { return a == b },
	}).Parse(auditbeatTemplate)
	if err != nil {
		return nil, fmt.Errorf("parse auditbeat template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute auditbeat template: %w", err)
	}
	return buf.Bytes(), nil
}

// templateData is the closed set of fields the auditbeatTemplate
// references. Keeping it as a struct (not map[string]any) makes the
// template contract explicit and forces the compiler to flag
// renames.
//
// WorkDir is pluginDir (= <edgeWorkDir>/audit). It's injected into the
// rendered YAML as the absolute output.file.path so the JSONL lands at
// ${pluginDir}/audit.jsonl, byte-for-byte matching audit.OutputPath().
//
// Field naming convention (auditbeat 9.4.2 schema):
//   - Fim* / Auditd* / System* groups mirror the auditbeat module
//     options rendered by auditbeatTemplate.
//   - FimExcludeFiles uses RE2 regex patterns (not globs), in line with
//     auditbeat 9.4.2 reference.yml — glob patterns are silently
//     ignored since 9.x dropped exclude_paths in favour of RE2.
type templateData struct {
	Modules []string

	// file_integrity (auditbeat module name; we accept "fim" in Spec
	// as an alias for backward compat with auditbeat 7.x docs that
	// called it fim).
	FimPaths          []string
	FimRecursive      bool
	FimScanAtStart    bool
	FimScanRatePerSec string
	FimHashTypes      string
	FimExcludeFiles   []string

	// auditd
	AuditdResolveIDs   bool
	AuditdFailureMode  string
	AuditdBacklogLimit int
	AuditdRateLimit    int
	AuditdRules        []string

	// system
	SystemDatasets     []string
	SystemStatePeriod  string

	WorkDir        string
	OutputFilename string
	EdgeID         uint64
}

// buildTemplateData resolves defaults + env overrides for Mode 2.
// workDir is pluginDir and is used as the absolute output.file.path so
// audit.jsonl lands in a directory the edge agent's supervisor already
// chowns to ongrid-edge (see plugin.go New docstring for the wider
// rationale; raw_config Mode 1 doesn't reach this code path).
func buildTemplateData(workDir string, cfg plugins.PluginConfig) templateData {
	// "fim" is kept as a Spec-side alias for "file_integrity" so old
	// manager-side audit probe configs (which named the module fim
	// per auditbeat 7.x docs) keep working. The template renders
	// `- module: file_integrity` directly — see auditbeatTemplate.
	modules := stringSliceField(cfg.Spec, "modules")
	if len(modules) == 0 {
		modules = []string{"fim"}
	}

	// FIM paths: spec > env > default.
	fimPaths := stringSliceField(cfg.Spec, "fim_paths")
	if len(fimPaths) == 0 {
		if env := os.Getenv("ONGRID_EDGE_AUDIT_FIM_PATHS"); env != "" {
			for _, p := range strings.Split(env, ",") {
				if p = strings.TrimSpace(p); p != "" {
					fimPaths = append(fimPaths, p)
				}
			}
		}
	}
	if len(fimPaths) == 0 {
		fimPaths = []string{"/opt", "/tmp", "/mnt/data", "/root/x1"}
	}

	// FIM exclude_files: RE2 regex patterns (auditbeat 9.4.2 dropped
	// the old exclude_paths glob syntax). We always inject two patterns
	// so FIM never watches / hashes its own state:
	//   - audit/.+         : everything under the per-plugin audit subdir
	//   - audit\.jsonl.*   : the JSONL output + rotated siblings
	// Both patterns anchor on simple glob → regex rewrites; they're
	// anchored to "audit" relative to pluginDir so they apply regardless
	// of which fim_paths the operator picks.
	fimExclude := []string{
		auditWorkDirExclude(workDir),
		auditOutputExclude(),
	}

	outFile := stringField(cfg.Spec, "output_file")
	if outFile == "" {
		outFile = "audit.jsonl"
	}

	// system datasets: join the 5 boolean toggles (auditbeat 8.x had
	// per-dataset .enabled: bool; auditbeat 9.x collapsed them into a
	// single datasets: list). Each true flag becomes the corresponding
	// dataset name. Order matches reference.yml so the rendered YAML
	// stays diff-friendly.
	var sysDatasets []string
	if boolFieldOr(cfg.Spec, "system_login", true) {
		sysDatasets = append(sysDatasets, "login")
	}
	if boolFieldOr(cfg.Spec, "system_package", true) {
		sysDatasets = append(sysDatasets, "package")
	}
	if boolFieldOr(cfg.Spec, "system_user", true) {
		sysDatasets = append(sysDatasets, "user")
	}
	if boolFieldOr(cfg.Spec, "system_process", true) {
		sysDatasets = append(sysDatasets, "process")
	}
	if boolFieldOr(cfg.Spec, "system_socket", false) {
		sysDatasets = append(sysDatasets, "socket")
	}
	if len(sysDatasets) == 0 {
		sysDatasets = []string{"login", "package"}
	}

	return templateData{
		Modules: modules,

		FimPaths:          fimPaths,
		FimRecursive:      boolFieldOr(cfg.Spec, "fim_recursive", true),
		FimScanAtStart:    boolFieldOr(cfg.Spec, "fim_scan_at_start", true),
		FimScanRatePerSec: stringFieldOr(cfg.Spec, "fim_scan_rate_per_sec", "5 MiB"),
		FimHashTypes:      stringFieldOr(cfg.Spec, "fim_hash_types", "sha1"),
		FimExcludeFiles:   fimExclude,

		AuditdResolveIDs:   boolFieldOr(cfg.Spec, "auditd_resolve_ids", true),
		AuditdFailureMode:  stringFieldOr(cfg.Spec, "auditd_failure_mode", "silent"),
		AuditdBacklogLimit: intFieldOr(cfg.Spec, "auditd_backlog_limit", 8192),
		AuditdRateLimit:    intFieldOr(cfg.Spec, "auditd_rate_limit", 0),
		AuditdRules:        stringSliceField(cfg.Spec, "auditd_rules"),

		SystemDatasets:    sysDatasets,
		SystemStatePeriod: stringFieldOr(cfg.Spec, "system_state_period", "12h"),

		WorkDir:        workDir,
		OutputFilename: outFile,
		EdgeID:         cfg.EdgeID,
	}
}

// auditWorkDirExclude returns the RE2 regex (anchored to pluginDir)
// that excludes the FIM module from watching / hashing files inside
// the audit plugin's own state subdir. Anchored to pluginDir so it
// applies even if the operator happens to set fim_paths to a parent
// like /mnt/data (which would otherwise drag the entire ongrid-edge
// state tree into the FIM result set).
//
// format: '^' + regexp.QuoteMeta(<pluginDir>) + '/.*'
func auditWorkDirExclude(pluginDir string) string {
	return "^" + regexp.QuoteMeta(pluginDir) + "/.*"
}

// auditOutputExclude returns the RE2 regex matching the JSONL output
// files auditbeat writes under pluginDir.
//
// auditbeat 9.x file output appends `-YYYYMMDD.ndjson` to whatever
// `filename:` is set in auditbeat.yml — there is no knob to suppress
// it (Beats 8.x dropped the old "filename only" behaviour). So the
// on-disk files are named e.g. `audit.jsonl-20260712.ndjson` with
// rotated siblings `audit.jsonl-20260712-1.ndjson`,
// `audit.jsonl-20260712-2.ndjson`, ...
//
// Format breakdown:
//
//	^.*/audit\.jsonl          base filename, anchored at any parent dir
//	(                        capture group (matches either branch)
//	  -\d{8}                 day-rollover suffix (-YYYYMMDD)
//	  (-\d+)?                optional rotation index (-1, -2, ...)
//	  \.ndjson               auditbeat's mandatory extension
//	|                        OR (rotated-pre-beats-8.x fallback)
//	  (\.\d+)?               legacy numbered-sibling suffix
//	)$
//
// The legacy branch keeps the prior `(.\d+)?` behaviour so a downgrade
// to an 8.x auditbeat (which wrote `audit.jsonl`, `audit.jsonl.1`,
// `audit.jsonl.99`) still self-excludes. The new branch covers 9.x.
func auditOutputExclude() string {
	return `^.*/audit\.jsonl(-\d{8}(-\d+)?\.ndjson|(\.\d+)?)$`
}

// ---- spec field helpers (tolerate JSON-decoded shapes) ----

func stringField(spec map[string]interface{}, key string) string {
	if v, ok := spec[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func stringFieldOr(spec map[string]interface{}, key, def string) string {
	if v := stringField(spec, key); v != "" {
		return v
	}
	return def
}

func stringSliceField(spec map[string]interface{}, key string) []string {
	raw, ok := spec[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func boolFieldOr(spec map[string]interface{}, key string, def bool) bool {
	raw, ok := spec[key]
	if !ok {
		return def
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	return def
}

func intFieldOr(spec map[string]interface{}, key string, def int) int {
	raw, ok := spec[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return def
}