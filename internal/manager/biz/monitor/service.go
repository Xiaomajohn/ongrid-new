// Package monitor is the biz layer for user-managed Monitor panels.
// Operators create / edit / delete panels through the SPA; this package
// owns persistence (via store.Repo) and asynchronously mirrors every
// change into a single ongrid-managed Grafana dashboard.
//
// Sync is one-way (ongrid → Grafana). Edits made directly in Grafana are
// NOT pulled back; on the next mirror push from ongrid they get
// overwritten. This is intentional — it keeps the data model simple
// (one source of truth, no merge conflicts) and matches the operator
// mental model: "build it in ongrid, view it in Grafana".
//
// API contract (server/monitor/http.go):
//
//	GET    /v1/monitor/panels         — list, ordered by ordinal asc
//	POST   /v1/monitor/panels         — create
//	PATCH  /v1/monitor/panels/{id}    — update title / promql / legend / unit / type / ordinal
//	DELETE /v1/monitor/panels/{id}    — delete
//
// Sync invariant: API calls return 200 as soon as the row is persisted.
// Grafana mirroring is fire-and-forget on a background goroutine; a
// failed mirror is recorded in last_sync_error so the UI can surface
// "synced / failed" status without ever failing the operator action.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/monitor"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the narrow persistence contract this service depends on. The
// concrete implementation lives in internal/manager/data/monitor/store;
// the interface lets tests substitute an in-memory fake.
//
// ListFilter / Delete(hard) / Restore were added alongside the
// device_id binding + soft-delete parity work (2026-07). See
// model.Panel.DeviceID doc for context.
type Repo interface {
	List(ctx context.Context, f ListFilter) ([]*model.Panel, error)
	Get(ctx context.Context, id uint64) (*model.Panel, error)
	MaxOrdinal(ctx context.Context) (int, error)
	Create(ctx context.Context, p *model.Panel) (*model.Panel, error)
	Update(ctx context.Context, id uint64, fields map[string]any) (*model.Panel, error)
	SetSyncResult(ctx context.Context, id uint64, errMsg string) error
	Delete(ctx context.Context, id uint64, hard bool) error
	Restore(ctx context.Context, id uint64) error
}

// GrafanaSyncer is the narrow surface the service uses to mirror
// dashboards. *biz/grafana.Service satisfies it via the SyncMonitorPanels
// method added alongside this package. Optional: when nil, the service
// degrades to "persist only" with no sync — useful in tests and for
// deployments that disable Grafana entirely.
type GrafanaSyncer interface {
	SyncMonitorPanels(ctx context.Context, panels []*model.Panel) error
}

// Service is the biz-layer orchestrator.
type Service struct {
	repo    Repo
	syncer  GrafanaSyncer
	log     *slog.Logger
	syncTO  time.Duration
}

// New builds the service. syncer may be nil. log may be nil (defaults
// to slog.Default).
func New(repo Repo, syncer GrafanaSyncer, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:   repo,
		syncer: syncer,
		log:    log,
		// 30s is generous for a single Grafana POST /api/dashboards/db.
		// Going beyond that almost certainly means Grafana is down; we
		// don't want a slow upstream pinning a goroutine forever.
		syncTO: 30 * time.Second,
	}
}

// ListFilter narrows Service.List results. The same field set is
// exposed on the store.Repo for plumbing, but we keep a separate type
// here so the biz layer doesn't have to import the data layer.
//
// DeviceID, when non-nil, restricts the result to "panels that apply
// to this device": rows with device_id = f.DeviceID plus global rows
// (device_id IS NULL). The Logs page's per-device route passes the
// operator's selected device here, so the dropdown surfaces both the
// device's own panels and any shared/global ones the operator may
// want to attach to the log context. Callers that need the strictly
// device-bound slice should filter client-side (panel.device_id ==
// f.DeviceID).
//
// IncludeDeleted, when true, surfaces soft-deleted rows so the operator
// can revive them via POST /:id/restore. Default false keeps the
// default-scope behaviour (no soft-deleted rows visible).
type ListFilter struct {
	DeviceID       *uint64
	IncludeDeleted bool
}

// CreateInput is the wire-shape for POST /v1/monitor/panels. Ordinal is
// optional; empty falls back to max(ordinal)+1 so new panels land at
// the end. DeviceID is optional: nil = global panel; non-nil = bound
// to a specific device (Logs page's 监控 dropdown will surface it
// only when the operator picks that device).
type CreateInput struct {
	Title    string `json:"title"`
	Type     string `json:"type"`
	PromQL   string `json:"promql"`
	Legend   string `json:"legend"`
	Unit     string `json:"unit"`
	Ordinal  *int   `json:"ordinal,omitempty"`
	DeviceID *uint64 `json:"device_id,omitempty"`
}

// UpdateInput is the wire-shape for PATCH /v1/monitor/panels/{id}. Every
// field is optional; nil means "leave column alone". The pointer-field
// shape lets the wire distinguish "set to empty string" from "don't
// touch", which a plain string can't do. DeviceID is nil = "leave
// binding alone"; a non-nil pointer sets the column to that value
// (operators wanting to unbind a panel should DELETE + re-create it
// rather than rely on PATCH).
type UpdateInput struct {
	Title    *string `json:"title,omitempty"`
	Type     *string `json:"type,omitempty"`
	PromQL   *string `json:"promql,omitempty"`
	Legend   *string `json:"legend,omitempty"`
	Unit     *string `json:"unit,omitempty"`
	Ordinal  *int    `json:"ordinal,omitempty"`
	DeviceID *uint64 `json:"device_id,omitempty"`
}

// List returns panels matching f. Pass zero-value ListFilter to keep
// the legacy "all live panels" behaviour.
func (s *Service) List(ctx context.Context, f ListFilter) ([]*model.Panel, error) {
	return s.repo.List(ctx, f)
}

// Get returns one panel.
func (s *Service) Get(ctx context.Context, id uint64) (*model.Panel, error) {
	return s.repo.Get(ctx, id)
}

// Create persists the new panel and kicks off an async Grafana mirror.
// The mirror runs on a detached goroutine — it must not block the API.
func (s *Service) Create(ctx context.Context, in CreateInput) (*model.Panel, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.PromQL = strings.TrimSpace(in.PromQL)
	in.Type = strings.TrimSpace(in.Type)
	in.Legend = strings.TrimSpace(in.Legend)
	in.Unit = strings.TrimSpace(in.Unit)
	if in.Title == "" {
		return nil, fmt.Errorf("%w: title required", errs.ErrInvalid)
	}
	if in.PromQL == "" {
		return nil, fmt.Errorf("%w: promql required", errs.ErrInvalid)
	}
	if in.Type == "" {
		in.Type = model.PanelTypeTimeseries
	}
	if !model.ValidPanelType(in.Type) {
		return nil, fmt.Errorf("%w: invalid type %q", errs.ErrInvalid, in.Type)
	}

	ord := 0
	if in.Ordinal != nil {
		ord = *in.Ordinal
	} else {
		max, err := s.repo.MaxOrdinal(ctx)
		if err != nil {
			return nil, fmt.Errorf("max ordinal: %w", err)
		}
		ord = max + 1
	}

	row := &model.Panel{
		Title:    in.Title,
		Type:     in.Type,
		PromQL:   in.PromQL,
		Legend:   in.Legend,
		Unit:     in.Unit,
		Ordinal:  ord,
		DeviceID: in.DeviceID,
	}
	saved, err := s.repo.Create(ctx, row)
	if err != nil {
		return nil, err
	}
	s.kickSync("create", saved.ID)
	return saved, nil
}

// Update writes the changed columns and triggers an async Grafana
// mirror. Empty-update (no fields set) is a no-op aside from re-syncing
// the dashboard, which is harmless.
func (s *Service) Update(ctx context.Context, id uint64, in UpdateInput) (*model.Panel, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: id required", errs.ErrInvalid)
	}
	fields := map[string]any{}
	if in.Title != nil {
		t := strings.TrimSpace(*in.Title)
		if t == "" {
			return nil, fmt.Errorf("%w: title cannot be empty", errs.ErrInvalid)
		}
		fields["title"] = t
	}
	if in.Type != nil {
		t := strings.TrimSpace(*in.Type)
		if !model.ValidPanelType(t) {
			return nil, fmt.Errorf("%w: invalid type %q", errs.ErrInvalid, t)
		}
		fields["type"] = t
	}
	if in.PromQL != nil {
		q := strings.TrimSpace(*in.PromQL)
		if q == "" {
			return nil, fmt.Errorf("%w: promql cannot be empty", errs.ErrInvalid)
		}
		fields["promql"] = q
	}
	if in.Legend != nil {
		fields["legend"] = strings.TrimSpace(*in.Legend)
	}
	if in.Unit != nil {
		fields["unit"] = strings.TrimSpace(*in.Unit)
	}
	if in.Ordinal != nil {
		fields["ordinal"] = *in.Ordinal
	}
	if in.DeviceID != nil {
		// PATCH /v1/monitor/panels/{id} 不接受清空（见 UpdateInput 注释）。
		fields["device_id"] = *in.DeviceID
	}
	updated, err := s.repo.Update(ctx, id, fields)
	if err != nil {
		return nil, err
	}
	s.kickSync("update", id)
	return updated, nil
}

// Delete removes the panel and kicks off an async Grafana mirror so the
// dashboard catches up. We sync from the post-delete list, which is why
// we re-read after the delete inside the goroutine.
//
// hard=false (default) → soft delete; the row stays in the table and
// can be revived via Restore. hard=true → physical delete (Unscoped).
// Soft delete is the operator-friendly default; hard is the
// un-recoverable path used when an operator explicitly wants the row
// gone (e.g. audit remediation).
func (s *Service) Delete(ctx context.Context, id uint64, hard bool) error {
	if err := s.repo.Delete(ctx, id, hard); err != nil {
		return err
	}
	s.kickSync("delete", id)
	return nil
}

// Restore un-soft-deletes a previously soft-deleted panel. Idempotent:
// calling Restore on a live row is a no-op (Restore's repo impl
// reports ErrNotFound when the id is gone, never on already-live).
// Sync is kicked after a successful restore so the Grafana mirror
// picks the row up again.
func (s *Service) Restore(ctx context.Context, id uint64) error {
	if err := s.repo.Restore(ctx, id); err != nil {
		return err
	}
	s.kickSync("restore", id)
	return nil
}

// SyncNow pushes the Grafana mirror synchronously: lists the current
// user-managed panels and hands them to the syncer (which prepends the
// core fleet panels). Used at boot so the ongrid-monitor dashboard exists
// — populated with at least the core panels — before the operator makes
// the first panel edit. Best-effort: returns the syncer error so the
// caller can log it; a boot-time Grafana hiccup is non-fatal (the next
// panel edit re-syncs).
func (s *Service) SyncNow(ctx context.Context) error {
	if s.syncer == nil {
		return nil
	}
	panels, err := s.repo.List(ctx, ListFilter{})
	if err != nil {
		return err
	}
	return s.syncer.SyncMonitorPanels(ctx, panels)
}

// kickSync starts the Grafana mirror on a detached goroutine. The
// goroutine carries its own context with a timeout so a stuck Grafana
// can't pin it forever.
//
// We pass `op` and `panelID` purely for logging; the actual sync re-
// reads the full panel list from the DB so the mirror always reflects
// the latest state, even when many edits happen in quick succession.
//
// Sync errors are persisted onto the affected panel's last_sync_error
// column (when panelID > 0) so the SPA can surface "Grafana mirror
// failed" without polling Grafana.
func (s *Service) kickSync(op string, panelID uint64) {
	if s.syncer == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.syncTO)
		defer cancel()
		panels, err := s.repo.List(ctx, ListFilter{})
		if err != nil {
			s.log.Warn("monitor sync: list panels failed",
				slog.String("op", op),
				slog.Any("err", err),
			)
			return
		}
		if err := s.syncer.SyncMonitorPanels(ctx, panels); err != nil {
			s.log.Warn("monitor sync: grafana mirror failed",
				slog.String("op", op),
				slog.Uint64("panel_id", panelID),
				slog.Any("err", err),
			)
			if panelID != 0 {
				if perr := s.repo.SetSyncResult(ctx, panelID, truncateErr(err)); perr != nil {
					s.log.Debug("monitor sync: persist err result failed", slog.Any("err", perr))
				}
			}
			return
		}
		// Successful sync — clear last_sync_error on every existing panel.
		// Cheap (one UPDATE per row, but the table is tiny) and gives the
		// UI a deterministic "all green" signal.
		for _, p := range panels {
			if p.LastSyncError == "" {
				continue
			}
			if perr := s.repo.SetSyncResult(ctx, p.ID, ""); perr != nil {
				s.log.Debug("monitor sync: clear err result failed",
					slog.Uint64("panel_id", p.ID),
					slog.Any("err", perr))
			}
		}
		s.log.Debug("monitor sync: grafana mirror ok",
			slog.String("op", op),
			slog.Int("panel_count", len(panels)),
		)
	}()
}

// truncateErr keeps last_sync_error within the column's 512-char limit.
func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 480 {
		s = s[:480] + "…"
	}
	return s
}

// Compile-time guard: we expect the standard sentinels to remain
// importable from this package's callers.
var _ = errors.Is
