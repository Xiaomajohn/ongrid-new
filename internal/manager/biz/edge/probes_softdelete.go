package edge

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// hostEdgeDeviceType is the integer column value of
// devicemodel.EdgeDeviceRelationHost. Re-declared here as an untyped
// const so this file stays free of a model import (the edge biz
// package already imports the model for other purposes, but keeping
// the literal inline makes the SQL obviously the "host type" branch
// to readers without needing to chase the import).
const hostEdgeDeviceType = 1

// SSHBulkSoftDelete is the concrete DeviceSSHSoftDelete used by the
// installjob worker. The install flow re-issues a fresh edge credential
// AFTER a successful install; old "SSH probe" edges left over from a
// previous attempt would otherwise re-validate against stale creds and
// could let the operator think the box is online when in fact it's the
// previous edge row that's still around.
//
// The two UPDATEs run inside a single transaction so a partial wipe
// never leaves the edges / edge_devices tables inconsistent: either
// both sides flip their delete_marker together, or neither does.
type SSHBulkSoftDelete struct {
	db  *gorm.DB
	log *slog.Logger
}

// NewSSHBulkSoftDelete wires the concrete. db must point at the same
// gorm.DB the manager migration ran against (so the edges / edge_devices
// tables exist and the soft_delete plugin is wired). log may be nil.
func NewSSHBulkSoftDelete(db *gorm.DB, log *slog.Logger) *SSHBulkSoftDelete {
	if log == nil {
		log = slog.Default()
	}
	return &SSHBulkSoftDelete{db: db, log: log.With(slog.String("comp", "edge-ssh-bulk-softdelete"))}
}

// SoftDeleteOldProbes flips the delete_marker on:
//
//   - every edges row whose id appears in edge_devices for this device
//     under type=host (EdgeDeviceRelationHost = 1)
//   - every edge_devices row for this device under type=host
//
// Both flips happen in one transaction. Returns nil on a clean
// commit, the underlying DB error otherwise.
//
// Implementation notes:
//   - The soft_delete plugin's column is `delete_marker` and uses
//     UnixMilli timestamps as the "deleted" sentinel; zero means
//     active. We set it to time.Now().UnixMilli() so the value is
//     monotonic and sub-second distinguishable, matching the schema
//     declared on the model (`softDelete:milli`).
//   - The edges UPDATE references the edge_devices junction via a
//     subquery so we don't have to load ids into Go memory; one round
//     trip per call regardless of how many probes existed.
//   - We use tx.Exec with raw SQL (not Model().UpdateColumn) because
//     GORM's soft_delete plugin hooks .Updates / .Delete on tracked
//     models — bypassing the ORM lets us flip delete_marker without
//     triggering the plugin's auto-WHERE filter.
func (s *SSHBulkSoftDelete) SoftDeleteOldProbes(ctx context.Context, deviceID uint64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("edge: SSHBulkSoftDelete not wired (nil db)")
	}
	if deviceID == 0 {
		return fmt.Errorf("edge: SoftDeleteOldProbes: deviceID is 0")
	}

	marker := time.Now().UTC().UnixMilli()

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) Flip delete_marker on every edges row linked to this
		//    device via the type=host junction. Subquery so a single
		//    SQL round trip regardless of probe count.
		edgeRes := tx.Exec(`
			UPDATE edges
			SET delete_marker = ?
			WHERE delete_marker = 0
			  AND id IN (
				SELECT edge_id FROM edge_devices
				WHERE device_id = ? AND type = ? AND delete_marker = 0
			  )`, marker, deviceID, hostEdgeDeviceType)
		if edgeRes.Error != nil {
			return fmt.Errorf("soft-delete edges: %w", edgeRes.Error)
		}

		// 2) Flip delete_marker on the junction rows themselves so
		//    subsequent SELECTs (e.g. LookupEdgeForDevice) don't
		//    surface the ghost rows.
		linkRes := tx.Exec(`
			UPDATE edge_devices
			SET delete_marker = ?
			WHERE device_id = ? AND type = ? AND delete_marker = 0`,
			marker, deviceID, hostEdgeDeviceType)
		if linkRes.Error != nil {
			return fmt.Errorf("soft-delete edge_devices: %w", linkRes.Error)
		}

		s.log.Info("ssh bulk soft-delete committed",
			slog.Uint64("device_id", deviceID),
			slog.Int64("edges_flipped", edgeRes.RowsAffected),
			slog.Int64("links_flipped", linkRes.RowsAffected),
		)
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}