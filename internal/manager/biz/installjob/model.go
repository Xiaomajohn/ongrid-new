package installjob

import (
	"time"

	"gorm.io/plugin/soft_delete"
)

// Status is the lifecycle of a one-click install job. Stored as a
// short varchar on the row so list queries can filter with the
// idx_install_jobs_status index.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimeout   Status = "timeout"
)

// AuthKind discriminates the credential bundle stashed on the job so
// the worker knows which SSH path to take when reconnecting. Mirrors
// the device-level auth taxonomy.
const (
	AuthKindPassword = "password"
	AuthKindKey      = "key"
)

// InstallJob is one asynchronous one-click install attempt against a
// device. The job carries everything the worker needs to reach the
// host (host/port/user + credential snapshot) and the streaming log
// buffer it built up while running.
//
// Credential handling:
//   - PasswordSnap / KeySnap hold a *plaintext* copy of the secret at
//     submit time so the worker doesn't have to re-derive / re-decrypt
//     during execution.
//   - The worker MUST call Repo.ClearCredentialSnap once the job
//     settles (success / failure / timeout) so the secret does not
//     linger in MySQL/SQLite beyond the install window.
//   - Job.KeySnap is also used by redactSecretKey as the substr to
//     scrub from the streamed log before it lands in LogOutput.
//
// Lifecycle:
//   - Created with Status=queued from the HTTP / gRPC handler.
//   - Picked up by Runner → Worker.Execute → flips to running.
//   - Settles into one of success / failed / cancelled / timeout.
//   - Soft-deleted via DeleteMarker (gorm soft_delete plugin) — never
//     hard-deleted so the audit history survives restarts.
type InstallJob struct {
	ID       uint64  `gorm:"primaryKey;autoIncrement"`
	DeviceID uint64  `gorm:"not null;column:device_id;index:idx_install_jobs_device,priority:1"`
	EdgeID   *uint64 `gorm:"column:edge_id"`

	Status   Status `gorm:"size:16;not null;default:'queued';column:status;index:idx_install_jobs_status,priority:1"`
	AuthKind string `gorm:"size:16;not null;column:auth_kind"` // password|key

	// PasswordSnap / KeySnap are scoped to the worker lifetime; the
	// repo clears them once the job settles (success/failed/etc).
	PasswordSnap string `gorm:"size:255;column:password_snap"`
	KeySnap      string `gorm:"type:mediumtext;column:key_snap"`

	// Connection target — the device's current SSH endpoint at the
	// time the user hit "install".
	Host string `gorm:"size:255;not null;column:host"`
	Port int    `gorm:"not null;column:port"`
	User string `gorm:"size:64;not null;column:user"`

	// OptionsJSON holds the free-form install options (proxy,
	// custom CA, …) as opaque JSON; the wire shape is owned by the
	// HTTP handler / usecase layer.
	OptionsJSON string `gorm:"type:json;column:options_json"`

	// LogOutput accumulates the streamed install output. mediumtext
	// so even a long install (think heavy package downloads) does
	// not blow past varchar(255) limits.
	LogOutput string `gorm:"type:mediumtext;not null;column:log_output"`

	// StartedAt is set when the worker transitions queued→running.
	// FinishedAt is set when the job lands in a terminal state.
	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	ExitCode   *int       `gorm:"column:exit_code"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
	DeletedAt *time.Time `gorm:"index;column:deleted_at"`

	// DeleteMarker drives the gorm.io/plugin/soft_delete plugin.
	// Soft-delete by millisecond so sub-second double-submits stay
	// distinguishable if we ever need them; the unique soft-delete
	// column on (device_id) when applicable will be added by the
	// migration step in main.go.
	DeleteMarker soft_delete.DeletedAt `gorm:"column:delete_marker;not null;default:0;softDelete:milli,DeletedAtField:DeletedAt"`
}

// TableName pins the gorm table for the install_jobs entity.
func (InstallJob) TableName() string { return "install_jobs" }

// EventKind discriminates rows in install_job_events. Keeping the kind
// short keeps the indexed column cheap.
const (
	EventKindLog   = "log"   // streamed chunk (already redacted)
	EventKindState = "state" // state-machine transition
	EventKindError = "error" // failure detail
)

// InstallJobEvent is an append-only timeline of what happened during
// a job's lifetime: log chunks, state transitions, and error reasons.
// We keep it as a separate table so the install_jobs row stays light
// (long output doesn't bloat the metadata read path).
type InstallJobEvent struct {
	ID           uint64    `gorm:"primaryKey;autoIncrement"`
	InstallJobID uint64    `gorm:"not null;column:install_job_id;index:idx_install_job_events,priority:1"`
	Ts           time.Time `gorm:"not null;column:ts;autoCreateTime"`
	Kind         string    `gorm:"size:32;not null;column:kind"` // log|state|error
	Payload      string    `gorm:"type:text;not null;column:payload"`
}

// TableName pins the gorm table for the install_job_events entity.
func (InstallJobEvent) TableName() string { return "install_job_events" }
