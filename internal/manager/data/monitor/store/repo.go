package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/monitor"
	model "github.com/ongridio/ongrid/internal/manager/model/monitor"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the GORM-backed persistence for monitor_panels. Each call uses
// a fresh session via WithContext so concurrent callers don't share
// transaction state.
type Repo struct {
	db *gorm.DB
}

// NewRepo builds the repo around an opened *gorm.DB.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// List returns panels ordered by (ordinal asc, id asc). Stable
// ordering ensures the SPA renders panels deterministically across
// refreshes. Filtering is opt-in: an empty ListFilter returns every
// live (non-soft-deleted) panel.
//
// When ListFilter.DeviceID is non-nil, the result includes both
// device-bound panels (device_id = f.DeviceID) and global panels
// (device_id IS NULL). Rationale: the Logs page's "监控" dropdown
// picks the operator's device first, then surfaces every panel that
// could apply to it — and a "global" panel is, by definition, the
// one that applies to all devices. Excluding globals from a
// device-scoped view left the dropdown effectively empty whenever
// the operator had only created unbound panels.
func (r *Repo) List(ctx context.Context, f biz.ListFilter) ([]*model.Panel, error) {
	tx := r.db.WithContext(ctx).Model(&model.Panel{})
	if f.DeviceID != nil {
		tx = tx.Where("device_id = ? OR device_id IS NULL", *f.DeviceID)
	}
	if f.IncludeDeleted {
		// Bypass the GORM soft-delete scope so soft-deleted rows surface.
		tx = tx.Unscoped()
	}
	var out []*model.Panel
	if err := tx.
		Order("ordinal asc").
		Order("id asc").
		Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns one panel by id. Missing rows surface as errs.ErrNotFound.
func (r *Repo) Get(ctx context.Context, id uint64) (*model.Panel, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: id required", errs.ErrInvalid)
	}
	var p model.Panel
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// MaxOrdinal returns the largest ordinal currently in the table, or 0
// when the table is empty. Used by Create to place new panels at the
// end without forcing the caller to read the list first.
func (r *Repo) MaxOrdinal(ctx context.Context) (int, error) {
	var n int
	row := r.db.WithContext(ctx).
		Model(&model.Panel{}).
		Select("COALESCE(MAX(ordinal), 0)").
		Row()
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Create inserts a new panel and returns the persisted row (with id +
// timestamps populated).
func (r *Repo) Create(ctx context.Context, p *model.Panel) (*model.Panel, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: panel required", errs.ErrInvalid)
	}
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return nil, err
	}
	return p, nil
}

// Update writes the named columns onto the row with the given id and
// returns the post-update row. Missing id surfaces as errs.ErrNotFound.
//
// `fields` is the column-name → new-value map; gorm only emits an UPDATE
// for the columns named, leaving everything else untouched.
func (r *Repo) Update(ctx context.Context, id uint64, fields map[string]any) (*model.Panel, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: id required", errs.ErrInvalid)
	}
	if len(fields) == 0 {
		return r.Get(ctx, id)
	}
	res := r.db.WithContext(ctx).
		Model(&model.Panel{}).
		Where("id = ?", id).
		Updates(fields)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		// Distinguish "row missing" from "no-op update". gorm's Updates
		// reports 0 rows for both, so we follow up with a Get to confirm.
		return r.Get(ctx, id)
	}
	return r.Get(ctx, id)
}

// SetSyncResult records the outcome of an asynchronous Grafana mirror
// attempt. errMsg = "" means the sync succeeded; lastSyncAt is updated
// regardless. Failures are non-fatal — the caller still returns 200 to
// the operator and the row stays usable.
func (r *Repo) SetSyncResult(ctx context.Context, id uint64, errMsg string) error {
	if id == 0 {
		return fmt.Errorf("%w: id required", errs.ErrInvalid)
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&model.Panel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"last_sync_error": errMsg,
			"last_sync_at":    &now,
		}).Error
}

// Delete removes the row with the given id. Missing row surfaces as
// errs.ErrNotFound.
//
// hard=false (default) → soft delete: GORM stamps deleted_at + bumps
// delete_marker, and the row is hidden from default-scope List calls.
// hard=true → physical delete: the row is gone from the table entirely.
// This matches the device model semantics: default behaviour is
// "operator can recover", `?hard=true` is the un-recoverable path used
// for GDPR-style "really scrub this" flows.
func (r *Repo) Delete(ctx context.Context, id uint64, hard bool) error {
	if id == 0 {
		return fmt.Errorf("%w: id required", errs.ErrInvalid)
	}
	q := r.db.WithContext(ctx)
	if hard {
		q = q.Unscoped()
	}
	res := q.Delete(&model.Panel{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Restore un-soft-deletes a previously soft-deleted panel. No-op when
// the row is already live (delete_marker == 0). Implemented via
// Unscoped so the WHERE doesn't filter out the row we're trying to
// revive. Missing id surfaces as errs.ErrNotFound so callers can tell
// "id never existed" from "id was already live" (idempotent case).
func (r *Repo) Restore(ctx context.Context, id uint64) error {
	if id == 0 {
		return fmt.Errorf("%w: id required", errs.ErrInvalid)
	}
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Unscoped().
		Model(&model.Panel{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"deleted_at":    nil,
			"delete_marker": 0,
			"updated_at":    now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}
