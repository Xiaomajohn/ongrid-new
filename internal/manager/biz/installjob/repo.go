package installjob

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the persistence contract for install_jobs / install_job_events.
//
// The interface is declared in the biz tier so callers (worker, future
// HTTP handler / cron) depend on the contract rather than the gorm
// concrete. The concrete *gormRepo is the only implementation today;
// split it to internal/manager/data/installjob when a non-gorm backend
// (e.g. a read-replica MySQL router) joins the party.
type Repo interface {
	// Create inserts a freshly-built job row (Status=queued). The
	// returned pointer carries the auto-assigned ID.
	Create(ctx context.Context, job *InstallJob) (*InstallJob, error)

	// Get returns the row by id; ErrNotFound otherwise. Soft-deleted
	// rows are excluded by default — pass IncludeDeleted=true to see
	// them (the worker rarely needs to).
	Get(ctx context.Context, id uint64) (*InstallJob, error)

	// ListByDevice returns recent jobs for a device, newest-first.
	// limit<=0 means "no limit".
	ListByDevice(ctx context.Context, deviceID uint64, limit int) ([]*InstallJob, error)

	// UpdateStatus flips the status column and — when the status
	// implies a timing milestone — stamps StartedAt / FinishedAt in
	// the same UPDATE so the row never lands in an inconsistent state.
	// exitCode may be nil (e.g. for the queued → running transition).
	UpdateStatus(ctx context.Context, id uint64, status Status, exitCode *int) error

	// AppendLog concatenates chunk onto log_output via SQL CONCAT so
	// the caller never has to read-modify-write the existing value.
	// Best-effort in the worker path: a failed UPDATE only costs us a
	// log line, never a failed install.
	AppendLog(ctx context.Context, id uint64, chunk string) error

	// ClearCredentialSnap wipes both PasswordSnap and KeySnap. Called
	// by the worker once the job settles (success / failed /
	// cancelled / timeout) so plaintext secrets don't linger beyond
	// the install window.
	ClearCredentialSnap(ctx context.Context, id uint64) error

	// AddEvent writes one entry to install_job_events. Used by the
	// worker to record state transitions and streamed log slices.
	AddEvent(ctx context.Context, jobID uint64, kind, payload string) error
}

// NewRepo returns the gorm-backed Repo. cmd/ongrid main.go wires this
// up with the shared *gorm.DB after AutoMigrate has run on the entity.
func NewRepo(db *gorm.DB) Repo { return &gormRepo{db: db} }

type gormRepo struct {
	db *gorm.DB
}

// Create inserts the row. We rely on the gorm Create hook for ID
// auto-fill; callers should not assume the input pointer's ID changes
// before this returns — we re-read the generated ID off the row.
func (r *gormRepo) Create(ctx context.Context, job *InstallJob) (*InstallJob, error) {
	if err := r.db.WithContext(ctx).Create(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

// Get loads one row by primary key. ErrRecordNotFound is translated to
// errs.ErrNotFound so callers across BCs share the sentinel.
func (r *gormRepo) Get(ctx context.Context, id uint64) (*InstallJob, error) {
	var j InstallJob
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&j).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &j, nil
}

// ListByDevice returns newest jobs first. limit<=0 disables the LIMIT
// clause. Soft-deleted rows are excluded (gorm soft_delete plugin
// transparently filters DeleteMarker != 0).
func (r *gormRepo) ListByDevice(ctx context.Context, deviceID uint64, limit int) ([]*InstallJob, error) {
	var jobs []*InstallJob
	q := r.db.WithContext(ctx).Where("device_id = ?", deviceID).Order("id DESC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

// UpdateStatus performs a single UPDATE so that status + timing
// columns never get observed in an inconsistent state. UpdatedAt is
// always refreshed.
func (r *gormRepo) UpdateStatus(ctx context.Context, id uint64, status Status, exitCode *int) error {
	now := time.Now().UTC()
	update := map[string]any{
		"status":     status,
		"updated_at": now,
	}
	if status == StatusRunning {
		update["started_at"] = now
	}
	if status == StatusSuccess || status == StatusFailed || status == StatusCancelled || status == StatusTimeout {
		update["finished_at"] = now
	}
	if exitCode != nil {
		update["exit_code"] = *exitCode
	}
	return r.db.WithContext(ctx).Model(&InstallJob{}).Where("id = ?", id).Updates(update).Error
}

// AppendLog uses SQL CONCAT so concurrent writer goroutines don't
// clobber each other's lines via read-modify-write races. The cost is
// one extra full-row write per chunk (no SELECT first); for the
// log-volume we expect in v1 that's a fine trade.
func (r *gormRepo) AppendLog(ctx context.Context, id uint64, chunk string) error {
	return r.db.WithContext(ctx).Model(&InstallJob{}).Where("id = ?", id).
		UpdateColumn("log_output", gorm.Expr("CONCAT(log_output, ?)", chunk)).Error
}

// ClearCredentialSnap wipes the secret fields in one statement.
func (r *gormRepo) ClearCredentialSnap(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Model(&InstallJob{}).Where("id = ?", id).Updates(map[string]any{
		"password_snap": "",
		"key_snap":      "",
	}).Error
}

// AddEvent inserts one row into install_job_events. We do not rely on
// the auto-fill of Ts because gorm's autoCreateTime fires on Create()
// even when the row already has a Ts set — so we deliberately leave
// Ts to its zero value and let autoCreateTime do the right thing.
// Gorm's Create call sets ts via the autoCreateTime tag, so passing a
// zero time is safe.
func (r *gormRepo) AddEvent(ctx context.Context, jobID uint64, kind, payload string) error {
	return r.db.WithContext(ctx).Create(&InstallJobEvent{
		InstallJobID: jobID,
		Kind:         kind,
		Payload:      payload,
	}).Error
}
