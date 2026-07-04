package audit

import (
	"strings"
	"testing"

	"github.com/ongridio/ongrid/internal/edgeagent/plugins"
)

func TestRenderHappyPath(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  42,
		Spec: map[string]interface{}{
			"modules": []interface{}{"fim", "auditd"},
		},
	}
	out, err := render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)

	wants := []string{
		"ongrid.device_id: \"42\"",
		"ongrid.source: \"auditbeat\"",
		"output.file:",
		"path: \"audit\"",
		"filename: audit.jsonl",
		"auditd:",
		"socket_type: unicast",
		"resolve_ids: true",
		"failure_mode: silent",
		"backlog_limit: 8192",
		"- module: fim",
		"- module: auditd",
		"hash_types: [sha1]",
		"exclude_paths:",
		"audit/**",
		"audit.jsonl*",
		"- host.hostname",
		"- host.os.kernel",
		"- host.architecture",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("rendered config missing %q\n--- body ---\n%s", w, body)
		}
	}
}

func TestRenderDefaultModules(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "- module: fim") {
		t.Errorf("default modules should include fim, got body:\n%s", body)
	}
	// auditd block must NOT be present when only fim is requested.
	if strings.Contains(body, "auditd:") {
		t.Errorf("auditd block should not render when modules=[fim]\n--- body ---\n%s", body)
	}
}

func TestRenderRawConfigPassthrough(t *testing.T) {
	const raw = "auditbeat:\n  modules:\n  - module: file_integrity\n    file_integrity:\n      paths:\n        - /etc\n"
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  7,
		Spec: map[string]interface{}{
			"raw_config": raw,
		},
	}
	out, err := render(cfg)
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
	if _, err := render(cfg); err == nil {
		t.Errorf("render must reject missing edge_id")
	}
}

func TestRenderEnvOverrideFimPaths(t *testing.T) {
	t.Setenv("ONGRID_EDGE_AUDIT_FIM_PATHS", "/var/log,/srv")
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "/var/log") || !strings.Contains(body, "/srv") {
		t.Errorf("env-provided fim_paths must show up in exclude_paths\n--- body ---\n%s", body)
	}
}

func TestRenderDropsHostFields(t *testing.T) {
	cfg := plugins.PluginConfig{Enabled: true, EdgeID: 1}
	out, err := render(cfg)
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
	out, err := render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "audit.jsonl*") {
		t.Errorf("exclude_paths must contain audit.jsonl* to skip auditbeat's own output\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "audit/**") {
		t.Errorf("exclude_paths must contain audit/** to skip plugin workdir\n--- body ---\n%s", body)
	}
}

func TestOutputPath(t *testing.T) {
	got := OutputPath("/var/lib/ongrid-edge/plugins")
	want := "/var/lib/ongrid-edge/plugins/audit/audit.jsonl"
	if got != want {
		t.Errorf("OutputPath = %q, want %q", got, want)
	}
}

func TestAuditdDefaults(t *testing.T) {
	cfg := plugins.PluginConfig{
		Enabled: true,
		EdgeID:  1,
		Spec:    map[string]interface{}{"modules": []interface{}{"auditd"}},
	}
	out, err := render(cfg)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(out)
	for _, w := range []string{
		"socket_type: unicast",
		"resolve_ids: true",
		"failure_mode: silent",
		"backlog_limit: 8192",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("auditd defaults missing %q", w)
		}
	}
}