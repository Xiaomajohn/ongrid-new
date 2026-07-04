package audit

import (
	"bytes"
	"fmt"
	"os"
	"path"
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
//   - auditd.audit_rules + fim exclude_paths : always inject
//     `${workDir}/audit/**` and `audit.jsonl/**` so FIM doesn't watch
//     its own state files (which would otherwise spam noise on every
//     rotate / checkpoint).
const auditbeatTemplate = `# Rendered by ongrid-edge audit plugin.
# DO NOT EDIT — regenerated from manager-pushed PluginConfig on every reconcile.

auditbeat:
  modules:
{{- range .Modules }}
  - module: {{ . }}
    {{- if eq . "fim" }}
    fim:
      modes:
        - realtime
      scan_at_start: true
      scan_rate_per_sec: "5 MiB"
      hash_types: [sha1]
      recursive: true
      exclude_paths:
{{- range $.Excludes }}
        - {{ . }}
{{- end }}
    {{- end }}
    {{- if eq . "auditd" }}
    auditd:
      socket_type: {{ $.AuditdSocketType }}
      resolve_ids: {{ $.AuditdResolveIDs }}
      failure_mode: {{ $.AuditdFailureMode }}
      backlog_limit: {{ $.AuditdBacklogLimit }}
      audit_rules: |
{{- range $.AuditdRules }}
        {{ . }}
{{- end }}
    {{- end }}
    {{- if eq . "system" }}
    system:
      state.period: {{ $.SystemStatePeriod }}
      login:
        enabled: {{ $.SystemLogin }}
      user:
        enabled: {{ $.SystemUser }}
      package:
        enabled: {{ $.SystemPackage }}
      process:
        enabled: {{ $.SystemProcess }}
      socket:
        enabled: {{ $.SystemSocket }}
    {{- end }}
{{- end }}

output.file:
  path: "{{ .OutputDir }}"
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

// render builds auditbeat.yml bytes from a PluginConfig.
//
// Two modes:
//
//	Mode 1 (raw_config) : spec["raw_config"] is a non-empty string.
//	                      We emit it verbatim so power users can ship
//	                      the full auditbeat native YAML (custom modules,
//	                      advanced processors, etc.). All structured
//	                      fields are ignored. The auto-injected
//	                      device_id / drop_fields / excludes are also
//	                      SKIPPED — the operator owns the entire YAML.
//	Mode 2 (structured) : otherwise. We render the template with the
//	                      structured fields + their defaults.
//
// Spec keys (Mode 2, all optional):
//
//	modules           : []string   — fim / auditd / system (default ["fim"])
//	fim_paths         : []string   — FIM watch list (env: ONGRID_EDGE_AUDIT_FIM_PATHS, comma-split)
//	output_file       : string     — filename prefix (default "audit.jsonl")
//	auditd_socket_type: unicast|multicast (default "unicast")
//	auditd_rules      : []string   — custom audit rules (default empty)
//	auditd_resolve_ids: bool       — resolve uid/gid → username/groupname (default true)
//	auditd_failure_mode: silent|log|warn (default "silent")
//	auditd_backlog_limit: int      — listen backlog (default 8192)
//	system_state_period: duration  — system.state scrape period (default "12h")
//	system_login / user / package / process / socket : bool (default true,true,true,true,false)
func render(cfg plugins.PluginConfig) ([]byte, error) {
	if cfg.EdgeID == 0 {
		return nil, fmt.Errorf("audit plugin: device_id required (set ONGRID_EDGE_ID)")
	}

	// Mode 1: raw passthrough.
	if raw := stringField(cfg.Spec, "raw_config"); raw != "" {
		return []byte(raw), nil
	}

	// Mode 2: structured. Build the data model with defaults applied.
	data := buildTemplateData(cfg)

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
type templateData struct {
	Modules            []string
	Excludes           []string
	AuditdSocketType   string
	AuditdResolveIDs   string
	AuditdFailureMode  string
	AuditdBacklogLimit int
	AuditdRules        []string
	SystemStatePeriod  string
	SystemLogin        string
	SystemUser         string
	SystemPackage      string
	SystemProcess      string
	SystemSocket       string
	OutputDir          string
	OutputFilename     string
	EdgeID             uint64
}

// buildTemplateData resolves defaults + env overrides for Mode 2.
func buildTemplateData(cfg plugins.PluginConfig) templateData {
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

	// Excludes: always inject our own state + audit.jsonl regardless of
	// operator-provided fim_paths so FIM never watches its own files.
	// The audit subdir + JSONL filename globs are stable (set by this
	// plugin's Name + output_file default), so FIM never loops back on
	// its own state even when the operator customises fim_paths.
	excludes := []string{
		auditWorkDirExclude(),
		auditOutputExclude(),
	}
	for _, p := range fimPaths {
		excludes = append(excludes, p)
	}

	outFile := stringField(cfg.Spec, "output_file")
	if outFile == "" {
		outFile = "audit.jsonl"
	}
	// OutputDir is rendered as ${WorkDir}/audit (relative to pluginWorkDir
	// the supervisor passes in via SubprocessPlugin.cmd.Dir). Keeping it
	// relative keeps the rendered config host-agnostic.
	outDir := Name

	return templateData{
		Modules:            modules,
		Excludes:           excludes,
		AuditdSocketType:   stringFieldOr(cfg.Spec, "auditd_socket_type", "unicast"),
		AuditdResolveIDs:   boolFieldStr(cfg.Spec, "auditd_resolve_ids", true),
		AuditdFailureMode:  stringFieldOr(cfg.Spec, "auditd_failure_mode", "silent"),
		AuditdBacklogLimit: intFieldOr(cfg.Spec, "auditd_backlog_limit", 8192),
		AuditdRules:        stringSliceField(cfg.Spec, "auditd_rules"),
		SystemStatePeriod:  stringFieldOr(cfg.Spec, "system_state_period", "12h"),
		SystemLogin:        boolFieldStr(cfg.Spec, "system_login", true),
		SystemUser:         boolFieldStr(cfg.Spec, "system_user", true),
		SystemPackage:      boolFieldStr(cfg.Spec, "system_package", true),
		SystemProcess:      boolFieldStr(cfg.Spec, "system_process", true),
		SystemSocket:       boolFieldStr(cfg.Spec, "system_socket", false),
		OutputDir:          outDir,
		OutputFilename:     outFile,
		EdgeID:             cfg.EdgeID,
	}
}

// auditWorkDirExclude returns the path glob the FIM module should skip
// to avoid watching the plugin's own state dir. Rendered into
// auditbeat.yml exclude_paths as a string. Uses path.Join so the
// YAML literal stays forward-slash on every host — auditbeat
// itself only runs on Linux, but the edge Go code is cross-compiled.
func auditWorkDirExclude() string {
	return path.Join("audit", "**")
}

// auditOutputExclude returns the path glob for the JSONL output file
// itself (and rotated siblings). auditbeat emits audit.jsonl, then
// rotates to audit.jsonl.1, audit.jsonl.2, … on every 10MB by default.
func auditOutputExclude() string {
	return "audit.jsonl*"
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

func boolFieldStr(spec map[string]interface{}, key string, def bool) string {
	raw, ok := spec[key]
	if !ok {
		if def {
			return "true"
		}
		return "false"
	}
	if b, ok := raw.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	if def {
		return "true"
	}
	return "false"
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