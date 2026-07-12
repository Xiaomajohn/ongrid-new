package edge

import (
	"context"
	"testing"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

// fakeEdgeLookup satisfies the EdgeLookup interface for tests that
// exercise the logs-plugin extra_labels enrichment in FetchForEdge.
type fakeEdgeLookup struct {
	rows map[uint64]*model.Edge
}

func (f *fakeEdgeLookup) GetByID(_ context.Context, id uint64) (*model.Edge, error) {
	if e, ok := f.rows[id]; ok {
		cp := *e
		return &cp, nil
	}
	return nil, errs.ErrNotFound
}

// ptrU64 is a tiny helper to take the address of a uint64 literal —
// *uint64 fields on Edge model need it.
func ptrU64(v uint64) *uint64 { return &v }

// TestFetchForEdgeLogsPluginInjectsDeviceIDAndTaskName covers the
// common path: edge has both DeviceID (junction-resolved) and
// TaskName. The resulting logs spec.extra_labels MUST carry both,
// and the operator's pre-existing extra_labels (e.g. environment
// tag) MUST survive the merge.
func TestFetchForEdgeLogsPluginInjectsDeviceIDAndTaskName(t *testing.T) {
	repo := newFakePluginConfigRepo()
	look := &fakeEdgeLookup{rows: map[uint64]*model.Edge{
		42: {ID: 42, TaskName: "rack-A-2026Q3", DeviceID: ptrU64(7)},
	}}
	// Operator pre-set an "env" label. Make sure mergeLogsExtraLabels
	// preserves it.
	repo.rows[model.PluginNameLogs] = &model.PluginConfig{
		EdgeID:     42,
		PluginName: model.PluginNameLogs,
		Enabled:    true,
		SpecJSON:   `{"extra_labels":{"env":"prod"}}`,
	}
	uc := NewPluginConfigUC(repo, look, nil, fakeEndpointResolver{}, nil)

	snap, err := uc.FetchForEdge(context.Background(), 42)
	if err != nil {
		t.Fatalf("FetchForEdge: %v", err)
	}
	cfg := snap.Configs[model.PluginNameLogs]
	extra, ok := cfg.Spec["extra_labels"].(map[string]interface{})
	if !ok {
		t.Fatalf("logs spec.extra_labels missing or wrong type: %#v", cfg.Spec)
	}
	if got, want := extra["device_id"], "7"; got != want {
		t.Errorf("device_id = %v, want %q (junction-resolved id)", got, want)
	}
	if got, want := extra["task_name"], "rack-A-2026Q3"; got != want {
		t.Errorf("task_name = %v, want %q", got, want)
	}
	if got, want := extra["env"], "prod"; got != want {
		t.Errorf("operator env label = %v, want %q (must survive merge)", got, want)
	}
}

// TestFetchForEdgeLogsPluginFallsBackToEdgeID covers the edge that
// has no host device linked yet (Edge.DeviceID == nil). We expect
// the numeric edgeID to substitute so logs still arrive, and the
// operator's chip will see a "device_id" that doesn't match any
// device row — a clear sign to re-link the edge.
func TestFetchForEdgeLogsPluginFallsBackToEdgeID(t *testing.T) {
	repo := newFakePluginConfigRepo()
	look := &fakeEdgeLookup{rows: map[uint64]*model.Edge{
		42: {ID: 42, TaskName: "standalone"},
	}}
	uc := NewPluginConfigUC(repo, look, nil, fakeEndpointResolver{}, nil)
	snap, err := uc.FetchForEdge(context.Background(), 42)
	if err != nil {
		t.Fatalf("FetchForEdge: %v", err)
	}
	extra := snap.Configs[model.PluginNameLogs].Spec["extra_labels"].(map[string]interface{})
	if got, want := extra["device_id"], "42"; got != want {
		t.Errorf("device_id = %v, want %q (edgeID fallback)", got, want)
	}
	if got, want := extra["task_name"], "standalone"; got != want {
		t.Errorf("task_name = %v, want %q", got, want)
	}
}

// TestFetchForEdgeLogsPluginEmptyTaskNameStillEmitted covers the
// edge where TaskName was never set (legacy / manual-create). The
// empty string MUST still be present in extra_labels so the
// downstream Loki indexer creates the label column with the empty
// value rather than skipping it entirely (which would make
// `{task_name=""}` queries silently empty).
func TestFetchForEdgeLogsPluginEmptyTaskNameStillEmitted(t *testing.T) {
	repo := newFakePluginConfigRepo()
	look := &fakeEdgeLookup{rows: map[uint64]*model.Edge{
		1: {ID: 1, DeviceID: ptrU64(1)},
	}}
	uc := NewPluginConfigUC(repo, look, nil, fakeEndpointResolver{}, nil)
	snap, err := uc.FetchForEdge(context.Background(), 1)
	if err != nil {
		t.Fatalf("FetchForEdge: %v", err)
	}
	extra := snap.Configs[model.PluginNameLogs].Spec["extra_labels"].(map[string]interface{})
	v, ok := extra["task_name"]
	if !ok {
		t.Fatalf("task_name key missing; want present-but-empty for legacy rows")
	}
	if s, _ := v.(string); s != "" {
		t.Errorf("task_name = %q, want empty string", s)
	}
}

// TestFetchForEdgeLogsPluginNoEdgeLookupUnwired covers the
// minimal-boot / test path: edgeLookup is nil. The logs plugin spec
// should still be returned (no error), but without the
// device_id / task_name enrichment — the promtail template will fall
// back to cfg.EdgeID for device_id and omit task_name.
func TestFetchForEdgeLogsPluginNoEdgeLookupUnwired(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, nil, fakeEndpointResolver{}, nil)
	snap, err := uc.FetchForEdge(context.Background(), 99)
	if err != nil {
		t.Fatalf("FetchForEdge: %v", err)
	}
	spec := snap.Configs[model.PluginNameLogs].Spec
	if v, ok := spec["extra_labels"]; ok {
		t.Errorf("extra_labels must be absent when edgeLookup is nil, got %#v", v)
	}
}

// TestFetchForEdgeLogsPluginNonLogsPluginUntouched asserts the
// enrichment is scoped to the logs plugin only — metrics / traces /
// etc. must not grow an extra_labels block.
func TestFetchForEdgeLogsPluginNonLogsPluginUntouched(t *testing.T) {
	repo := newFakePluginConfigRepo()
	look := &fakeEdgeLookup{rows: map[uint64]*model.Edge{
		3: {ID: 3, TaskName: "t", DeviceID: ptrU64(3)},
	}}
	uc := NewPluginConfigUC(repo, look, nil, fakeEndpointResolver{}, nil)
	snap, err := uc.FetchForEdge(context.Background(), 3)
	if err != nil {
		t.Fatalf("FetchForEdge: %v", err)
	}
	for _, name := range []string{
		model.PluginNameMetrics,
		model.PluginNameHostMetrics,
		model.PluginNameTraces,
		model.PluginNameAudit,
	} {
		cfg := snap.Configs[name]
		if v, ok := cfg.Spec["extra_labels"]; ok {
			t.Errorf("%s spec.extra_labels = %#v, want absent", name, v)
		}
	}
}