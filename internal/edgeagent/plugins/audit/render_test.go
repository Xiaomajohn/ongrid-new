package audit

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ongridio/ongrid/internal/edgeagent/plugins"
)

// testWorkDir is the pluginDir (= <edgeWorkDir>/audit) value the
// updated render() requires. Renderings assert against this constant
// so a future WorkDir rename / path change trips a single place.
const testWorkDir = "/var/lib/ongrid-edge/plugins/audit"

func TestRenderHappyPath(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  42,
		Spec: map[string]interface{}{
			"modules": []interface{}{"fim", "auditd"},
		},
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)

	// 9.4.2 schema: modules rendered FLAT (no auditd: wrapper, no fim:
	// sub-key). "fim" in Spec is an alias for file_integrity.
	//
	// Note: we intentionally do NOT assert "- module: auditd" appears
	// here — the Spec asks for fim + auditd, so the auditd block IS in
	// the body, but the assertion is more usefully placed in
	// TestAuditdDefaults below where it's the explicit focus.
	wants := []string{
		// auto-injected Loki labels
		"ongrid.device_id: \"42\"",
		"ongrid.source: \"auditbeat\"",
		// absolute output path so audit.jsonl lands at pluginDir
		"output.file:",
		"path: \"" + testWorkDir + "\"",
		"filename: audit.jsonl",
		// auditd (flat, no socket_type in 9.x)
		"resolve_ids: true",
		"failure_mode: silent",
		"backlog_limit: 8192",
		"audit_rules: |",
		// file_integrity (Spec alias "fim" → file_integrity in YAML)
		"- module: file_integrity",
		"hash_types: [sha1]",
		// RE2 exclude_files (not exclude_paths; auditbeat 9.x dropped globs)
		"exclude_files:",
		// self-exclusion patterns: pluginDir-anchored + audit.jsonl RE2
		regexp.QuoteMeta(testWorkDir),
		`audit\.jsonl`,
		// host-identifying fields dropped for bounded cardinality
		"- host.hostname",
		"- host.os.kernel",
		"- host.architecture",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("rendered config missing %q\n--- body ---\n%s", w, body)
		}
	}

	// Negative assertions: things that MUST NOT appear in 9.4.2 schema.
	notWants := []string{
		"socket_type:",          // removed in 9.x
		"\nauditd:\n",           // no top-level auditd: wrapper
		"- module: fim\n",       // runtime name is file_integrity
		"exclude_paths:",        // renamed to exclude_files (RE2)
		"audit.jsonl*",          // glob syntax; 9.x uses RE2 `audit\.jsonl`
	}
	for _, w := range notWants {
		if strings.Contains(body, w) {
			t.Errorf("rendered config must NOT contain %q\n--- body ---\n%s", w, body)
		}
	}
}

func TestRenderDefaultModules(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	// Default modules = ["fim", "auditd"] — auditd was added to
	// defaults in 2026-07-18 so the on-event audit surface is broad
	// enough out of the box, but auditd_rules still defaults to []
	// so auditd emits no rules of its own. Operators fill the rules
	// in the UI to opt specific paths (e.g. /bin, /usr/bin) into the
	// execve watcher. The system module stays opted-out (default).
	for _, w := range []string{
		"- module: file_integrity", // Spec alias "fim"
		"- module: auditd",
		"resolve_ids: true",         // auditd defaults
		"audit_rules: |",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("default modules missing %q\n--- body ---\n%s", w, body)
		}
	}
	// system block stays absent — system is NOT in [fim, auditd].
	if strings.Contains(body, "- module: system") {
		t.Errorf("system module should NOT render when default modules=[fim, auditd]\n--- body ---\n%s", body)
	}
	// system datasets should not render either way (system opted out).
	if strings.Contains(body, "datasets:") {
		t.Errorf("system datasets should not render by default\n--- body ---\n%s", body)
	}
}

func TestRenderRawConfigPassthrough(t *testing.T) {
	const raw = "auditbeat:\n  modules:\n  - module: file_integrity\n    paths:\n        - /etc\n"
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  7,
		Spec: map[string]interface{}{
			"raw_config": raw,
		},
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if body != raw {
		t.Errorf("raw_config mode must passthrough verbatim\nexpected:\n%s\n\ngot:\n%s", raw, body)
	}
	// Mode 1 must NOT inject auto-fields.
	if strings.Contains(body, "ongrid.device_id") {
		t.Errorf("raw_config mode must not inject ongrid.device_id")
	}
}

func TestRenderRejectsMissingEdgeID(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true}
	if _, err := render(testWorkDir, cfg); err == nil {
		t.Errorf("render must reject missing edge_id")
	}
}

func TestRenderEnvOverrideFimPaths(t *testing.T) {
	t.Setenv("ONGRID_EDGE_AUDIT_FIM_PATHS", "/var/log,/srv")
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "/var/log") || !strings.Contains(body, "/srv") {
		t.Errorf("env-provided fim_paths must show up in paths:\n--- body ---\n%s", body)
	}
}

func TestRenderDropsHostFields(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	for _, field := range []string{"host.hostname", "host.os.kernel", "host.os.platform", "host.mac"} {
		if !strings.Contains(body, "- "+field) {
			t.Errorf("drop_fields must include %q\n--- body ---\n%s", field, body)
		}
	}
}

func TestRenderExcludesOwnState(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	// exclude_files (RE2, not exclude_paths/globs) must include:
	//   1. pluginDir-anchored regex so FIM never watches the audit
	//      subdir (which lives UNDER pluginDir).
	//   2. audit.jsonl pattern (RE2-escaped, not glob `*`).
	if !strings.Contains(body, "exclude_files:") {
		t.Fatalf("rendered config missing exclude_files:\n%s", body)
	}
	if !strings.Contains(body, regexp.QuoteMeta(testWorkDir)) {
		t.Errorf("exclude_files must contain QuoteMeta(pluginDir) anchor\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, `audit\.jsonl`) {
		t.Errorf("exclude_files must contain RE2 `audit\\.jsonl` (not glob `audit.jsonl*`)\n--- body ---\n%s", body)
	}
	if strings.Contains(body, "audit.jsonl*") {
		t.Errorf("exclude_files must NOT contain glob `audit.jsonl*` (9.x dropped globs)\n--- body ---\n%s", body)
	}
}

func TestOutputPath(t *testing.T) {
	got := OutputPath("/var/lib/ongrid-edge/plugins")
	// auditbeat 9.x's file output auto-appends `-YYYYMMDD.ndjson` to
	// the configured filename, so the contract surface for the logs
	// (promtail) plugin is a glob, not a single file path. Promtail's
	// `__path__` natively supports globs and rotates between matched
	// files transparently.
	want := "/var/lib/ongrid-edge/plugins/audit/audit.jsonl-*.ndjson"
	if got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

// TestAuditOutputExclude pins the exclude_files RE2 pattern against
// the actual filenames auditbeat 9.4.2 produces. If a future auditbeat
// release changes the suffix scheme (e.g. switches to `auditbeat-`
// prefix or drops the `.ndjson` extension), this test trips loudly
// rather than letting the FIM module silently start hashing its own
// JSONL output (which would spam events every 10MB rotate).
func TestAuditOutputExclude(t *testing.T) {
	pat, err := regexp.Compile(auditOutputExclude())
	if err != nil {
		t.Fatalf("auditOutputExclude does not compile: %v", err)
	}
	// Must match: every filename auditbeat 9.4.2 actually produces.
	mustMatch := []string{
		"/var/lib/ongrid-edge/plugins/audit/audit.jsonl",
		"/var/lib/ongrid-edge/plugins/audit/audit.jsonl-20260712.ndjson",
		"/var/lib/ongrid-edge/plugins/audit/audit.jsonl-20260712-1.ndjson",
		"/var/lib/ongrid-edge/plugins/audit/audit.jsonl-20260712-2.ndjson",
		"/mnt/data/tools-temp/ongrid-edge/var/lib/ongrid-edge/plugins/audit/audit.jsonl-20260712.ndjson",
	}
	for _, name := range mustMatch {
		if !pat.MatchString(name) {
			t.Errorf("auditOutputExclude must match %q", name)
		}
	}
	// Must NOT match: things FIM should keep watching.
	mustNotMatch := []string{
		"/opt/audit-summary.json",  // unrelated audit-named file
		"/var/lib/ongrid-edge/plugins/audit/audit.jsonl.snapshot", // operator export
		"/var/log/audit.log",       // different filename entirely
	}
	for _, name := range mustNotMatch {
		if pat.MatchString(name) {
			t.Errorf("auditOutputExclude must NOT match %q", name)
		}
	}
}

func TestAuditdDefaults(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  1,
		Spec:    map[string]interface{}{"modules": []interface{}{"auditd"}},
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	// 9.4.2 auditd block: flat (no auditd: wrapper), no socket_type.
	for _, w := range []string{
		"- module: auditd",
		"resolve_ids: true",
		"failure_mode: silent",
		"backlog_limit: 8192",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("auditd defaults missing %q\n--- body ---\n%s", w, body)
		}
	}
	// 9.x removed socket_type (multicast/unicast split gone).
	if strings.Contains(body, "socket_type:") {
		t.Errorf("auditd block must NOT contain socket_type in 9.x schema\n--- body ---\n%s", body)
	}
}

func TestRenderSystemDatasets(t *testing.T) {
	// Spec asks for system module + a couple of dataset toggles —
	// render() should join the bool fields into a datasets: list and
	// emit state.period:.
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  1,
		Spec: map[string]interface{}{
			"modules":         []interface{}{"system"},
			"system_login":    true,
			"system_package":  true,
			"system_user":     false,
			"system_process":  true,
			"system_socket":   false,
			"system_state_period": "1h",
		},
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	for _, w := range []string{
		"- module: system",
		"datasets:",
		"  - login",
		"  - package",
		"  - process",
		"state.period: 1h",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("system block missing %q\n--- body ---\n%s", w, body)
		}
	}
	for _, not := range []string{
		"  - user",
		"  - socket",
	} {
		if strings.Contains(body, not) {
			t.Errorf("system datasets must NOT include %q when toggled off\n--- body ---\n%s", not, body)
		}
	}
}