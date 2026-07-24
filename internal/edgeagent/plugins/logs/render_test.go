package logs

import (
	"strings"
	"testing"

	"github.com/ongridio/ongrid/internal/edgeagent/plugins"
)

// testWorkDir is a stable path the audit-probe in render() used to
// see as missing across every logs renderer test. Since 2026-07-23 the
// audit output path is appended UNCONDITIONALLY (no Glob probe — see
// render.go docstring for the startup-race rationale), so every render
// call now includes the audit.jsonl glob regardless of whether the
// file exists yet. Existing assertions stay authoritative; new
// assertions pin the always-on audit tail.
const testWorkDir = "/tmp/ongrid-edge-test-plugins"

func TestRenderHappyPath(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled:  true,
		EdgeID:   42,
		Endpoint: "https://manager.example.com/loki/api/v1/push",
		AuthUser: "ak-edge42",
		AuthPass: "sk-secret",
		Spec: map[string]interface{}{
			"file_paths":      []interface{}{"/var/log/syslog", "/var/log/auth.log"},
			"journald_units":  []interface{}{"sshd", "ongrid-edge"},
			"extra_labels":    map[string]interface{}{"service": "edge", "env": "test"},
			"enable_journald": true,
		},
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)

	for _, want := range []string{
		"https://manager.example.com/loki/api/v1/push",
		"basic_auth:",
		"username: ak-edge42",
		"password: sk-secret",
		`device_id: "42"`,
		`service: "edge"`,
		`env: "test"`,
		"job_name: journald",
		"target_label:  'identifier'", // journald syslog_identifier → label for non-unit entries
		"job_name: 'file-var-log-syslog'",
		"job_name: 'file-var-log-auth-log'",
		"__path__:      '/var/log/syslog'",
		"__path__:      '/var/log/auth.log'",
		"ongrid-edge|sshd", // sorted lex; '-' isn't a regex meta so not escaped
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered config missing %q\n--- full body ---\n%s", want, body)
		}
	}
}

func TestRenderRejectsMissingEndpoint(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	if _, err := render(testWorkDir, cfg); err == nil {
		t.Errorf("render must reject missing endpoint")
	}
}

func TestRenderRejectsMissingEdgeID(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, Endpoint: "https://x/loki/api/v1/push"}
	if _, err := render(testWorkDir, cfg); err == nil {
		t.Errorf("render must reject missing edge_id")
	}
}

func TestRenderEnableJournaldFalse(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled:  true,
		EdgeID:   1,
		Endpoint: "https://x/loki/api/v1/push",
		Spec: map[string]interface{}{
			"enable_journald": false,
			"file_paths":      []interface{}{"/var/log/x.log"},
		},
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if strings.Contains(body, "job_name: journald") {
		t.Errorf("journald block should be omitted when enable_journald=false:\n%s", body)
	}
	if !strings.Contains(body, "/var/log/x.log") {
		t.Errorf("file path missing from rendered config")
	}
}

// TestRenderSingleClient: with default spec, the rendered config has
// exactly one client[] entry pointing at cfg.Endpoint.
func TestRenderSingleClient(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled:  true,
		EdgeID:   1,
		Endpoint: "https://manager.example.com/loki/api/v1/push",
		AuthUser: "ak",
		AuthPass: "sk",
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if strings.Count(body, "- url: ") != 1 {
		t.Errorf("expected exactly one clients[] entry, got body:\n%s", body)
	}
	if !strings.Contains(body, "https://manager.example.com/loki/api/v1/push") {
		t.Errorf("internal endpoint missing from rendered config")
	}
}

// TestRenderJournaldDefaultOn: journald is the default source. Default
// render (no enable_journald in spec) MUST emit the journald scrape
// block; explicit enable_journald=false removes it (the operator falls
// back to file/syslog tail).
func TestRenderJournaldDefaultOn(t *testing.T) {
	base := plugins.PluginConfig{
		Enabled:  true,
		EdgeID:   1,
		Endpoint: "https://x/loki/api/v1/push",
		Spec: map[string]interface{}{
			"file_paths": []interface{}{"/var/log/syslog"},
		},
	}
	out, err := render(testWorkDir, base)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(out), "job_name: journald") {
		t.Errorf("journald should be on by default (unset spec):\n%s", string(out))
	}

	base.Spec["enable_journald"] = false
	out, err = render(testWorkDir, base)
	if err != nil {
		t.Fatalf("render with journald off: %v", err)
	}
	if strings.Contains(string(out), "job_name: journald") {
		t.Errorf("enable_journald=false must remove the journald scrape block:\n%s", string(out))
	}
}

// TestRenderAlwaysIncludesAuditGlob pins the unconditional audit
// tail contract added 2026-07-23: every rendered promtail config
// MUST include the audit plugin's JSONL output path glob regardless
// of whether the file currently exists on disk. Previously render()
// did filepath.Glob(audit.OutputPath(...)) and dropped the path if
// the probe came back empty — that probe had a startup race against
// auditbeat's first write, so default-installed audit events silently
// failed to reach Loki.
func TestRenderAlwaysIncludesAuditGlob(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled:  true,
		EdgeID:   1,
		Endpoint: "https://x/loki/api/v1/push",
		// Spec intentionally empty — fresh-install scenario where
		// auditbeat has not yet produced its first JSONL on disk.
	}
	out, err := render(testWorkDir, cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	want := testWorkDir + "/audit/audit.jsonl-*.ndjson"
	if !strings.Contains(body, "__path__:      '"+want+"'") {
		t.Errorf("rendered config must include audit glob even when file absent:\n%s", body)
	}
}

func TestJobNameSafe(t *testing.T) {
	cases := map[string]string{
		"/var/log/syslog":      "var-log-syslog",
		"/opt/app/log/app.log": "opt-app-log-app-log",
		"alpha_beta":           "alpha-beta",
	}
	for in, want := range cases {
		if got := jobNameSafe(in); got != want {
			t.Errorf("jobNameSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
