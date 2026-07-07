// Package monitor holds the persistence entity for user-managed Monitor
// page panels. Operators create / edit / delete panels through the SPA;
// the rows are the source of truth and ongrid asynchronously mirrors
// them into a single Grafana dashboard so deep-links / "在 Grafana 中
// 打开" keep working.
//
// One-way sync: ongrid is the source of truth. Edits made in Grafana to
// the mirrored dashboard are NOT pulled back — operators wanting to keep
// changes must round-trip through the ongrid UI.
package monitor

import (
	"time"

	"gorm.io/plugin/soft_delete"
)

// PanelType enumerates the renderable panel shapes. The SPA's
// PromQLPanel uses the same identifiers; values map 1:1 onto Grafana
// panel types so the mirror dashboard renders identically.
const (
	PanelTypeTimeseries = "timeseries"
	PanelTypeStat       = "stat"
	PanelTypeGauge      = "gauge"
)

// Panel is one user-defined Monitor panel.
//
// Ordinal controls render order on the page. Newly created panels get
// max(ordinal)+1 so they land at the bottom; reordering is done via
// PATCH ordinal. LastSyncError records the most recent Grafana mirror
// failure (if any); empty string means the last sync succeeded or has
// not been attempted yet.
//
// DeviceID optionally binds a panel to a specific device. NULL = global
// panel visible to all devices; non-NULL = "only the operator viewing
// logs for this device sees this panel in the Logs page's 监控 dropdown".
// Soft-delete mirrors the device model: GORM scopes out rows where
// delete_marker != 0 by default; the service layer can pass
// include_deleted=true to opt in for restore flows.
type Panel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"                                json:"id"`
	Title     string    `gorm:"size:128;not null"                                       json:"title"`
	Type      string    `gorm:"size:32;not null;default:timeseries"                     json:"type"`
	PromQL    string    `gorm:"type:text;not null;column:promql"                        json:"promql"`
	Legend    string    `gorm:"size:255;not null;default:''"                            json:"legend"`
	Unit      string    `gorm:"size:32;not null;default:''"                             json:"unit"`
	Ordinal   int       `gorm:"not null;default:0;index"                                json:"ordinal"`
	// DeviceID 关联设备：NULL=全局 panel；非 NULL=仅在该设备的 Logs 页面“监控”下拉框可见。index 走 device_id 列。
	DeviceID *uint64 `gorm:"column:device_id;index"                                 json:"device_id,omitempty"`
	// 软删除字段，与 device model 同语义：DeletedAt 记录 GORM 软删除时间戳；
	// DeleteMarker 用 soft_delete.DeletedAt 按毫秒记录唯一值，避免重名 restore 冲突。
	DeletedAt    *time.Time            `gorm:"index;column:deleted_at"                          json:"deleted_at,omitempty"`
	DeleteMarker soft_delete.DeletedAt `gorm:"column:delete_marker;not null;default:0;softDelete:milli,DeletedAtField:DeletedAt" json:"-"`
	LastSyncError string `gorm:"size:512;not null;default:'';column:last_sync_error"     json:"last_sync_error,omitempty"`
	LastSyncAt    *time.Time `gorm:"column:last_sync_at"                                 json:"last_sync_at,omitempty"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"                                          json:"updated_at"`
	CreatedAt time.Time `gorm:"autoCreateTime"                                          json:"created_at"`
}

// TableName pins the SQL table so package renames don't accidentally
// branch the schema.
func (Panel) TableName() string { return "monitor_panels" }

// ValidPanelType returns true if t is one of the supported panel types.
// Used by the biz validator before persisting.
func ValidPanelType(t string) bool {
	switch t {
	case PanelTypeTimeseries, PanelTypeStat, PanelTypeGauge:
		return true
	}
	return false
}
