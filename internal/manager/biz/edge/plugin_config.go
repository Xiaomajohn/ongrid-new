package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

// PluginConfigRepo is the narrow persistence contract this biz layer
// needs. *sqlite.PluginConfigRepo satisfies it.
type PluginConfigRepo interface {
	ListByEdge(ctx context.Context, edgeID uint64) ([]*model.PluginConfig, error)
	Get(ctx context.Context, edgeID uint64, plugin string) (*model.PluginConfig, error)
	Upsert(ctx context.Context, in *model.PluginConfig) (*model.PluginConfig, error)
	Delete(ctx context.Context, edgeID uint64, plugin string) error
	CountByPlugin(ctx context.Context) (map[string]int64, error)
}

// EdgeReloadNotifier abstracts "tell this edge to re-fetch its plugin
// configs". Implemented by frontierbound.Client.PluginConfigsChanged.
type EdgeReloadNotifier interface {
	NotifyPluginConfigsChanged(ctx context.Context, edgeID uint64) error
}

// DatabaseMetricsSecretWriter writes a managed databasemetrics credential file
// on an edge. Implemented by the frontierbound client. The manager calls this
// during a UI save and then persists only the non-secret plugin spec.
type DatabaseMetricsSecretWriter interface {
	WriteDatabaseMetricsSecrets(ctx context.Context, edgeID uint64, reqs []tunnel.WriteDatabaseMetricsSecretRequest) error
}

// EndpointResolver returns the data plane endpoint a given plugin
// should push to. Implementation lives at the wiring site (cmd/ongrid)
// because it composes ONGRID_PUBLIC_URL + the per-plugin path AND
// consults system_settings (loki.url / tempo.url) so an admin edit in
// the Integrations UI re-targets edges automatically. Stubbed out as
// an interface so PluginConfigUC stays testable without env.
//
// ctx is threaded through so the resolver can hit the cached settings
// service without inventing a background context that ignores deadlines.
type EndpointResolver interface {
	Endpoint(ctx context.Context, plugin string) string
}

// EdgeLookup is the narrow persistence contract FetchForEdge needs to
// enrich the logs plugin spec with edge-specific labels (task_name +
// resolved device_id). *data/edge/store.Repo satisfies it. nil is
// allowed — when unwired the logs plugin falls back to the empty
// extra_labels map (legacy behavior: device_id sourced only from
// cfg.EdgeID, no task_name label).
type EdgeLookup interface {
	GetByID(ctx context.Context, id uint64) (*model.Edge, error)
}

// PluginConfigUC is the use-case for managing per-edge plugin configs.
//
// Two consumers:
//   - HTTP API (UI): list / set / delete via internal/manager/server/edge.
//   - Tunnel RPC (edge): FetchForEdge serves the wire snapshot when an
//     edge calls MethodGetPluginConfigs.
//
// On any mutating call, UC fires-and-forgets a reload notification to
// the affected edge so changes propagate within seconds, not within the
// edge's 60s safety-net poll window.
type PluginConfigUC struct {
	repo         PluginConfigRepo
	edgeLookup   EdgeLookup
	notifier     EdgeReloadNotifier
	secretWriter DatabaseMetricsSecretWriter
	resolver     EndpointResolver
	log          *slog.Logger
}

// NewPluginConfigUC builds the use-case. notifier may be nil during
// startup (before frontierbound is wired); calls become no-ops then.
// resolver MUST be non-nil — without it FetchForEdge can't tell the edge
// where to push. edgeLookup may be nil for tests / minimal boot — the
// logs plugin just won't get the device_id / task_name enrichment.
func NewPluginConfigUC(repo PluginConfigRepo, edgeLookup EdgeLookup, notifier EdgeReloadNotifier, resolver EndpointResolver, log *slog.Logger) *PluginConfigUC {
	if log == nil {
		log = slog.Default()
	}
	return &PluginConfigUC{repo: repo, edgeLookup: edgeLookup, notifier: notifier, resolver: resolver, log: log}
}

// SetNotifier injects the notifier post-construction. cmd/ongrid wires
// the use-case before frontierbound is ready, then back-fills the
// notifier once the tunnel is alive.
func (uc *PluginConfigUC) SetNotifier(n EdgeReloadNotifier) { uc.notifier = n }

// SetDatabaseMetricsSecretWriter injects the edge-side credential writer once
// frontierbound is alive.
func (uc *PluginConfigUC) SetDatabaseMetricsSecretWriter(w DatabaseMetricsSecretWriter) {
	uc.secretWriter = w
}

// PluginRow is the UI/HTTP-friendly view of one plugin row.
type PluginRow struct {
	PluginName string                 `json:"plugin_name"`
	Enabled    bool                   `json:"enabled"`
	Spec       map[string]interface{} `json:"spec,omitempty"`
}

// pluginDefaultEnabled declares the on-by-default policy for fresh
// edges that don't yet have a row in edge_plugin_configs. Every
// subprocess + push path ships in the edge tarball (install-edge.sh
// drops the binaries into /usr/local/lib/ongrid-edge), so they're
// safe to auto-start on first connect. Without this every freshly
// installed edge shows up with empty Monitor panels and silent log /
// trace ingestion until an operator hand-clicks every toggle on
// /edges/{id}.
//
// Data path:
//   - hostmetrics — node_exporter subprocess exposing :9102/metrics
//   - procmetrics — process_exporter subprocess exposing :9256/metrics
//   - metrics — parent metrics pipeline whose sub-plugins push via
//     the tunnel's push_prom_samples RPC into cloud Prom's
//     remote_write. This is the universal path that works for any
//     edge (local or across the internet). It replaces the legacy
//     prometheus.yml host.docker.internal scrape, which only ever
//     worked for an edge co-resident with the manager.
//   - custommetrics / databasemetrics — operator configured metric
//     sub-plugins. They stay disabled until targets/sources are set.
//   - logs / traces — promtail / otelcol subprocesses pushing direct
//     to manager nginx via publicURL.
//
// Stay off:
//   - profiles — pyroscope agent isn't in the default install bundle.
//
// Explicit operator opt-out is preserved: Set writes a row with
// Enabled=false, which beats this default (the table lookup wins
// over the map fallback below).
var pluginDefaultEnabled = map[string]bool{
	model.PluginNameMetrics:     true,
	model.PluginNameHostMetrics: true,
	model.PluginNameProcMetrics: true,
	model.PluginNameLogs:        true,
	model.PluginNameTraces:      true,
	model.PluginNameAudit:       true,
}

// ListForUI returns every plugin config row for an edge, decoding the
// spec JSON for the UI. Plugins the system knows about but that have no
// row yet are filled in as Enabled=false / empty spec so the UI shows a
// stable list of toggles.
func (uc *PluginConfigUC) ListForUI(ctx context.Context, edgeID uint64) ([]PluginRow, error) {
	rows, err := uc.repo.ListByEdge(ctx, edgeID)
	if err != nil {
		return nil, err
	}
	have := map[string]*model.PluginConfig{}
	for _, r := range rows {
		have[r.PluginName] = r
	}
	knownPlugins := []string{
		model.PluginNameMetrics,
		model.PluginNameLogs,
		model.PluginNameAudit,
		model.PluginNameTraces,
		model.PluginNameProfiles,
		model.PluginNameHostMetrics,
		model.PluginNameProcMetrics,
		model.PluginNameCustomMetrics,
		model.PluginNameDatabaseMetrics,
	}
	out := make([]PluginRow, 0, len(knownPlugins))
	for _, name := range knownPlugins {
		row := PluginRow{PluginName: name, Enabled: pluginDefaultEnabled[name]}
		if r, ok := have[name]; ok {
			// Explicit DB row always wins — preserves operator opt-out.
			row.Enabled = r.Enabled
			row.Spec = decodeSpec(r.SpecJSON)
		}
		// audit plugin：spec 为空时填默认 spec，让 UI 能看到完整模板。
		// 操作员在 form/json 间修改后存回 DB，下次走 DB row 分支。
		if len(row.Spec) == 0 && name == model.PluginNameAudit {
			row.Spec = model.AuditDefaultSpec()
		}
		out = append(out, row)
	}
	return out, nil
}

// SetInput is the mutation payload from the UI / API.
type SetInput struct {
	Enabled bool                   `json:"enabled"`
	Spec    map[string]interface{} `json:"spec,omitempty"`
}

// Set upserts one plugin config and (best-effort) notifies the edge to
// reload. Validates plugin name + spec marshallability.
func (uc *PluginConfigUC) Set(ctx context.Context, edgeID uint64, plugin string, in SetInput) (*PluginRow, error) {
	if edgeID == 0 {
		return nil, fmt.Errorf("%w: edge_id required", errs.ErrInvalid)
	}
	if !model.IsKnownPluginName(plugin) {
		return nil, fmt.Errorf("%w: unknown plugin %q", errs.ErrInvalid, plugin)
	}
	var databaseSecretReqs []tunnel.WriteDatabaseMetricsSecretRequest
	var previous *model.PluginConfig
	switch plugin {
	case model.PluginNameCustomMetrics:
		if err := validateCustomMetricsSpec(in.Spec); err != nil {
			return nil, err
		}
	case model.PluginNameDatabaseMetrics:
		spec, secretReqs, err := uc.prepareDatabaseMetricsSpec(in.Spec)
		if err != nil {
			return nil, err
		}
		in.Spec = spec
		databaseSecretReqs = secretReqs
		previous, err = uc.repo.Get(ctx, edgeID, plugin)
		if errors.Is(err, errs.ErrNotFound) {
			previous = nil
		} else if err != nil {
			return nil, fmt.Errorf("load previous %s config: %w", plugin, err)
		}
		if previous != nil {
			databaseSecretReqs = append(databaseSecretReqs, databaseMetricsSecretDeleteRequests(decodeSpec(previous.SpecJSON), in.Spec)...)
		}
	}
	specJSON := "{}"
	if in.Spec != nil {
		blob, err := json.Marshal(in.Spec)
		if err != nil {
			return nil, fmt.Errorf("%w: marshal spec: %v", errs.ErrInvalid, err)
		}
		specJSON = string(blob)
	}
	row, err := uc.repo.Upsert(ctx, &model.PluginConfig{
		EdgeID:     edgeID,
		PluginName: plugin,
		Enabled:    in.Enabled,
		SpecJSON:   specJSON,
	})
	if err != nil {
		return nil, err
	}
	if len(databaseSecretReqs) > 0 {
		if err := uc.writeDatabaseMetricsSecrets(ctx, edgeID, databaseSecretReqs); err != nil {
			if rollbackErr := uc.rollbackPluginConfig(ctx, edgeID, plugin, previous); rollbackErr != nil {
				return nil, errors.Join(err, fmt.Errorf("rollback plugin config: %w", rollbackErr))
			}
			return nil, err
		}
	}
	uc.notify(ctx, edgeID, plugin)
	return &PluginRow{PluginName: row.PluginName, Enabled: row.Enabled, Spec: decodeSpec(row.SpecJSON)}, nil
}

func (uc *PluginConfigUC) rollbackPluginConfig(ctx context.Context, edgeID uint64, plugin string, previous *model.PluginConfig) error {
	if previous != nil {
		_, err := uc.repo.Upsert(ctx, previous)
		return err
	}
	return uc.repo.Delete(ctx, edgeID, plugin)
}

// FetchForEdge is the tunnel-RPC view: returns the wire snapshot the
// edge supervisor consumes. Includes every known plugin (disabled
// ones surface so supervisor can stop them if they were running).
// Endpoint is filled in from EndpointResolver — single source of
// truth.
//
// Default-enable policy is owned by pluginDefaultEnabled (see above):
// freshly installed edges auto-start hostmetrics / procmetrics / logs
// / traces on first connect so Monitor panels and log/trace ingestion
// just work. Any explicit DB row (operator opt-out via UI) beats the
// default — table lookup wins.
func (uc *PluginConfigUC) FetchForEdge(ctx context.Context, edgeID uint64) (*WireSnapshot, error) {
	rows, err := uc.repo.ListByEdge(ctx, edgeID)
	if err != nil {
		return nil, err
	}
	have := map[string]*model.PluginConfig{}
	for _, r := range rows {
		have[r.PluginName] = r
	}

	knownPlugins := []string{
		model.PluginNameMetrics,
		model.PluginNameLogs,
		model.PluginNameAudit,
		model.PluginNameTraces,
		model.PluginNameProfiles,
		model.PluginNameHostMetrics,
		model.PluginNameProcMetrics,
		model.PluginNameCustomMetrics,
		model.PluginNameDatabaseMetrics,
	}
	out := &WireSnapshot{EdgeID: edgeID, Configs: make(map[string]WireConfig, len(knownPlugins))}
	enabledNames := make([]string, 0, len(knownPlugins))
	// logsExtra is the labels we want stamped onto every Loki stream
	// coming off this edge. Computed once per fetch (not per plugin
	// loop) so we hit edgeRepo at most one time. nil when edgeLookup
	// is unwired or the row is missing — the logs plugin then falls
	// back to the empty-extra-labels legacy behavior.
	logsExtra := uc.buildLogsExtraLabels(ctx, edgeID)
	for _, name := range knownPlugins {
		cfg := WireConfig{
			Endpoint: uc.resolver.Endpoint(ctx, name),
			Enabled:  pluginDefaultEnabled[name],
		}
		if r, ok := have[name]; ok {
			// Explicit row wins. This preserves opt-out: an operator
			// who turns hostmetrics off via the UI lands a row with
			// Enabled=false and the default does not override it.
			cfg.Enabled = r.Enabled
			cfg.Spec = decodeSpec(r.SpecJSON)
		}
		// audit plugin：spec 为空时填默认 spec（跟 ListForUI 行为一致，
		// 确保 edge 端 fetch 到的 spec 跟 UI 看到的模板相同）。
		if len(cfg.Spec) == 0 && name == model.PluginNameAudit {
			cfg.Spec = model.AuditDefaultSpec()
		}
		if name == model.PluginNameLogs && logsExtra != nil {
			cfg.Spec = uc.mergeLogsExtraLabels(cfg.Spec, logsExtra)
		}
		if cfg.Enabled {
			enabledNames = append(enabledNames, name)
		}
		out.Configs[name] = cfg
	}
	uc.log.Info("FetchForEdge",
		slog.Uint64("edge_id", edgeID),
		slog.Int("rows", len(rows)),
		slog.Int("configs_out", len(out.Configs)),
		slog.Any("enabled", enabledNames))
	return out, nil
}

// CountByPlugin proxies to the repo (UI Integrations cards).
func (uc *PluginConfigUC) CountByPlugin(ctx context.Context) (map[string]int64, error) {
	return uc.repo.CountByPlugin(ctx)
}

// buildLogsExtraLabels computes the labels stamped onto every Loki
// stream for this edge. The two fields we ALWAYS inject when we have
// the row:
//   - device_id: source of truth for the promtail external_label, so
//     querying Logs by `device_id="<X>"` matches what the manager-side
//     ingest pipeline stamps on metrics. Edge rows without a host
//     device (DeviceID == nil, e.g. mid-register) fall back to the
//     numeric edgeID so logs still arrive — operator will see and can
//     manually re-link.
//   - task_name: the install campaign / task identifier operators
//     group edges by. Empty when unset, which means "no task filter"
//     on the Loki side — preserved exactly so Loki treats it as a
//     distinct (but empty) stream label value.
//
// Returns nil when edgeLookup is unwired OR GetByID fails — the caller
// then takes the legacy path (no enrichment, just cfg.EdgeID for the
// device_id template fallback). We deliberately do NOT return a partial
// map: a missing edge row is a strong signal something is broken and
// should be visible in logs, not silently masked.
func (uc *PluginConfigUC) buildLogsExtraLabels(ctx context.Context, edgeID uint64) map[string]interface{} {
	if uc.edgeLookup == nil {
		return nil
	}
	edge, err := uc.edgeLookup.GetByID(ctx, edgeID)
	if err != nil {
		uc.log.Warn("plugin_config: edgeLookup.GetByID failed; logs plugin will use legacy extra_labels",
			slog.Uint64("edge_id", edgeID), slog.Any("err", err))
		return nil
	}
	deviceID := edgeID
	if edge.DeviceID != nil {
		deviceID = *edge.DeviceID
	}
	return map[string]interface{}{
		"device_id": strconv.FormatUint(deviceID, 10),
		"task_name": edge.TaskName,
	}
}

// mergeLogsExtraLabels returns a copy of spec with the manager-derived
// logs labels merged in under "extra_labels". The merge rule:
//   - any pre-existing extra_labels[k] NOT in {device_id, task_name}
//     is preserved (operator-added labels win through).
//   - any pre-existing extra_labels[k] in {device_id, task_name} is
//     overwritten with the manager-derived value — the manager is
//     authoritative for these two because they are tied to the
//     edge_devices / edges tables, not to operator wiring.
//
// Always returns a new map so we never mutate an operator-saved spec
// by reference (the same map may be served across multiple edges if
// the SQLite row is shared in test fixtures, and the test harness
// catches accidental mutation).
func (uc *PluginConfigUC) mergeLogsExtraLabels(spec map[string]interface{}, labels map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(spec)+2)
	for k, v := range spec {
		out[k] = v
	}
	existing := map[string]interface{}{}
	if raw, ok := out["extra_labels"]; ok {
		if m, ok := raw.(map[string]interface{}); ok {
			existing = m
		}
	}
	merged := make(map[string]interface{}, len(existing)+len(labels))
	for k, v := range existing {
		if k == "device_id" || k == "task_name" {
			continue
		}
		merged[k] = v
	}
	for k, v := range labels {
		merged[k] = v
	}
	out["extra_labels"] = merged
	return out
}

// notify fires the reload signal to the edge without blocking the
// caller. Errors are logged only — the edge's 60s safety net catches
// missed pushes anyway.
func (uc *PluginConfigUC) notify(ctx context.Context, edgeID uint64, plugin string) {
	if uc.notifier == nil {
		uc.log.Debug("notifier not wired; skipping push", slog.Uint64("edge_id", edgeID))
		return
	}
	if err := uc.notifier.NotifyPluginConfigsChanged(ctx, edgeID); err != nil {
		uc.log.Warn("plugin config reload push failed",
			slog.Uint64("edge_id", edgeID),
			slog.String("plugin", plugin),
			slog.Any("err", err))
	}
}

// WireSnapshot is what the edge sees on a get_plugin_configs RPC.
// Endpoint is server-derived; auth_user/auth_pass are filled in by the
// edge from its own access_key/secret_key (already in env), so secrets
// never traverse the wire on this RPC.
type WireSnapshot struct {
	EdgeID  uint64                `json:"edge_id"`
	Configs map[string]WireConfig `json:"configs"`
}

// WireConfig is one plugin's config as the edge sees it.
type WireConfig struct {
	Enabled  bool                   `json:"enabled"`
	Endpoint string                 `json:"endpoint,omitempty"`
	Spec     map[string]interface{} `json:"spec,omitempty"`
}

func decodeSpec(raw string) map[string]interface{} {
	if raw == "" {
		return nil
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
