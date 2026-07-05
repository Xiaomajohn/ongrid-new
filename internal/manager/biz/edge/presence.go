package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	edgemodel "github.com/ongridio/ongrid/internal/manager/model/edge"
)

// pollInterval is how often WaitOnline re-queries the DB. 500 ms is
// fine for an MVP install — it's cheap (single indexed SELECT) and
// gives the agent a sub-second window to phone home after the
// systemd unit starts.
const pollInterval = 500 * time.Millisecond

// EdgePresence is the worker's narrow contract for "is any edge
// online for this device?". Implemented by *DBEdgePresence.
type EdgePresence interface {
	// WaitOnline blocks until at least one non-deleted edge row for
	// the device reports status='online' (via the type=host junction),
	// or until timeout elapses. Returns nil on success, an error
	// wrapping context.DeadlineExceeded on timeout.
	WaitOnline(ctx context.Context, deviceID uint64, timeout time.Duration) error
}

// DBEdgePresence is the gorm-backed EdgePresence. The query joins
// edges → edge_devices on (edge_id, type=host) so a transient or
// mismatched-type row can't accidentally satisfy the wait.
type DBEdgePresence struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewDBEdgePresence wires the concrete. db must point at the same
// gorm.DB the manager migration ran against. log may be nil.
func NewDBEdgePresence(db *gorm.DB, log *slog.Logger) *DBEdgePresence {
	if log == nil {
		log = slog.Default()
	}
	return &DBEdgePresence{db: db, log: log.With(slog.String("comp", "edge-presence"))}
}

// WaitOnline polls every 500 ms (or sooner if ctx is cancelled) until
// any non-soft-deleted edge row linked to this device under
// type=host has status='online'. Returns:
//
//   - nil                       — edge is online
//   - context.DeadlineExceeded  — timeout elapsed before online
//   - ctx.Err()                 — caller cancelled the wait
//   - any underlying DB error   — surfaced verbatim
//
// Implementation notes:
//   - The query is bounded by LIMIT 1 so a host with N edges
//     (rare today, possible after multi-agent support) returns as
//     soon as the first online row is found; without the LIMIT a
//     busy host would scan all N rows every poll.
//   - We rely on the gorm soft_delete plugin to filter out
//     delete_marker != 0 rows by default; using the table name +
//     raw SQL bypasses the auto-WHERE the plugin adds to .Find on
//     tracked models. The explicit `AND e.delete_marker = 0` is
//     belt-and-braces in case a future query path goes through
//     Unscoped().
//   - We deliberately don't subscribe to the in-memory presence
//     tracker (the plugin-health map); an install just finished
//     and the DB is the source of truth for "did the agent register".
//     Reading the map would race against HandleRegister which hasn't
//     necessarily been called yet for this device.
func (p *DBEdgePresence) WaitOnline(ctx context.Context, deviceID uint64, timeout time.Duration) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("edge: DBEdgePresence not wired (nil db)")
	}
	if deviceID == 0 {
		return fmt.Errorf("edge: WaitOnline: deviceID is 0")
	}
	if timeout <= 0 {
		// Defensive — caller passed 0, treat as "no wait".
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Run the first probe immediately rather than waiting one tick —
	// a freshly-installed agent on a fast network is online by the
	// time we get here and we don't want to spend a free 500 ms.
	if err := p.pollOnce(waitCtx, deviceID); err == nil {
		return nil
	} else if errors.Is(err, errNotYetOnline) {
		// fall through to the tick loop.
	} else if waitCtx.Err() != nil {
		return waitCtx.Err()
	}
	// errNotYetOnline → keep polling.

	for {
		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		case <-ticker.C:
			if err := p.pollOnce(waitCtx, deviceID); err == nil {
				return nil
			} else if waitCtx.Err() != nil {
				return waitCtx.Err()
			}
			// errNotYetOnline / ErrNotFound → keep polling.
		}
	}
}

// errNotYetOnline is the internal sentinel returned by pollOnce when
// the DB has rows for the device but none are status=online yet.
// Distinct from errs.ErrNotFound (no rows at all) so callers can
// distinguish the two if they want, but WaitOnline collapses both
// to "keep polling".
var errNotYetOnline = errors.New("edge: no online edge yet")

// pollOnce runs the single-row SELECT and translates the result into
// a (nil / errNotYetOnline / errs.ErrNotFound / other-error) tuple.
func (p *DBEdgePresence) pollOnce(ctx context.Context, deviceID uint64) error {
	var row edgemodel.Edge
	res := p.db.WithContext(ctx).
		Table("edges AS e").
		Select("e.*").
		Joins("JOIN edge_devices ed ON ed.edge_id = e.id").
		Where("ed.device_id = ?", deviceID).
		Where("ed.type = ?", 1).
		Where("ed.delete_marker = 0").
		Where("e.delete_marker = 0").
		Where("e.status = ?", edgemodel.StatusOnline).
		Limit(1).
		Scan(&row)
	if res.Error != nil {
		return fmt.Errorf("edge: presence poll: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// Could be "no rows at all" or "rows exist but none online".
		// The two cases are observably identical to the worker
		// (both mean "wait more") so we collapse them. If a future
		// caller wants to distinguish, switch to a COUNT and
		// inspect.
		return errNotYetOnline
	}
	return nil
}