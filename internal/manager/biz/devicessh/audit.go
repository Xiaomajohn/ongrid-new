package devicessh

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// AuditLogger appends a single row per SFTP / shell file op to the
// device_ssh_file_ops table. The writes are kicked off in their own
// goroutine so the SFTP hot path isn't blocked behind the DB; the
// downside is a brief window where a crashed manager loses the tail of
// recent audit rows — acceptable trade-off for a UI audit feed.
//
// v1 deliberately uses the unmodelled (Table(...).Create) path instead of
// a GORM model because (a) the columns are write-only here and read
// exclusively by a separate audit-list handler (A4 future work) and
// (b) introducing another model file would force a soft-delete debate
// that the audit table doesn't need.
type AuditLogger struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewAuditLogger wires the audit sink. log may be nil — in that case
// errors fall through to the standard logger via slog.Default().
func NewAuditLogger(db *gorm.DB, log *slog.Logger) *AuditLogger {
	if log == nil {
		log = slog.Default()
	}
	return &AuditLogger{db: db, log: log}
}

// errMsgCap is the column-length ceiling for the err_msg VARCHAR(512)
// that the migration script creates; we trim defensively so a panic
// stack trace doesn't blow the row write.
const errMsgCap = 500

// Log records one file op. All fields map 1:1 onto device_ssh_file_ops:
//
//	device_id, ongrid_user_id, op, path, path_new, size_bytes, status,
//	err_msg, started_at (set inside); finished_at / mode / client_ip
//	are populated by out-of-scope follow-ups.
//
// status classification:
//
//	nil / no error           → "ok"
//	errs.ErrForbidden        → "denied" (pathguard rejected)
//	errs.ErrInvalid          → "denied"
//	anything else            → "error"
func (a *AuditLogger) Log(
	ctx context.Context,
	deviceID, userID uint64,
	op, path, pathNew string,
	sizeBytes int64,
	err error,
) {
	if a == nil || a.db == nil {
		return
	}
	_ = ctx // ctx is implicit: the audit row inherits the request's tracing
	// by going through the same *gorm.DB. We pass ctx explicitly here only
	// so callers can wire their trace span into a downstream ctx-aware
	// writer should the table later switch to a slower path.

	status := "ok"
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
		if len(errMsg) > errMsgCap {
			errMsg = errMsg[:errMsgCap]
		}
		switch {
		case errors.Is(err, errs.ErrForbidden), errors.Is(err, errs.ErrInvalid):
			status = "denied"
		default:
			status = "error"
		}
	}
	startedAt := time.Now().UTC()

	// Async fan-out so the SFTP path isn't gated on the audit row.
	// Errors during the insert are logged but don't surface to the caller
	// — audit gaps are observable via the separate list endpoint.
	go func(deviceID, userID uint64, op, path, pathNew, status, errMsg string, sizeBytes int64, startedAt time.Time) {
		rec := map[string]any{
			"device_id":      deviceID,
			"ongrid_user_id": userID,
			"op":             op,
			"path":           path,
			"path_new":       pathNew,
			"size_bytes":     sizeBytes,
			"status":         status,
			"err_msg":        errMsg,
			"started_at":     startedAt,
		}
		if err := a.db.Table("device_ssh_file_ops").Create(rec).Error; err != nil {
			a.log.Error("devicessh audit insert failed",
				slog.String("op", op),
				slog.String("path", path),
				slog.String("err", err.Error()),
			)
		}
	}(deviceID, userID, op, path, pathNew, status, errMsg, sizeBytes, startedAt)
}
