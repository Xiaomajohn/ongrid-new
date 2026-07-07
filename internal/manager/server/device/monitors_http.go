// Package device — per-device monitor panel routes. The list endpoint
// is a thin convenience wrapper over biz/monitor.Service.List with a
// DeviceID filter injected; the underlying rows live in the
// monitor_panels table (see model/monitor). The route lives under
// /v1/devices/{id}/... so the SPA's Logs page can "拉这台设备关联的
// 监控" without having to know the monitor service's full surface.
package device

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	monitorbiz "github.com/ongridio/ongrid/internal/manager/biz/monitor"
	monitormodel "github.com/ongridio/ongrid/internal/manager/model/monitor"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// MonitorLookup is the narrow slice of biz/monitor.Service that the
// per-device endpoint needs. Kept as an interface (rather than importing
// the concrete *Service) so the device handler can be unit-tested with
// a stub.
type MonitorLookup interface {
	List(ctx context.Context, f monitorbiz.ListFilter) ([]*monitormodel.Panel, error)
}

// RegisterDeviceMonitors attaches the /v1/devices/{id}/monitors route
// to r. The route is a thin handler (listMonitorsForDevice) that
// delegates to the monitor biz service with DeviceID pinned to the path
// param. Use this from main.go right after the device handler and the
// monitor service are both constructed.
func RegisterDeviceMonitors(r chi.Router, monitor MonitorLookup) {
	r.Get("/v1/devices/{id}/monitors", listMonitorsForDevice(monitor))
}

// listMonitorsForDevice returns the panels scoped to the device in the
// path param: device-bound rows (device_id = id) plus global rows
// (device_id IS NULL) — see biz/monitor.Service.List for the exact
// semantics. ?include_deleted=true surfaces soft-deleted rows so the
// SPA's "显示已删除" toggle can pick them up (matches the device model
// semantics). Any-authed — viewing device-bound panels isn't
// privileged; the per-panel mutation endpoints stay admin-gated in
// server/monitor/http.go.
func listMonitorsForDevice(monitor MonitorLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := tenantctx.From(r.Context()); !ok {
			writeErr(w, errs.ErrUnauthorized)
			return
		}
		idRaw := chi.URLParam(r, "id")
		deviceID, err := strconv.ParseUint(idRaw, 10, 64)
		if err != nil || deviceID == 0 {
			writeErr(w, errorsJoinInvalid(err))
			return
		}
		f := monitorbiz.ListFilter{
			DeviceID:       &deviceID,
			IncludeDeleted: parseBoolQuery(r.URL.Query().Get("include_deleted")),
		}
		panels, err := monitor.List(r.Context(), f)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"panels": panels})
	}
}

// errorsJoinInvalid is a tiny shim that keeps this file's import list
// compact. We re-use the same "id parse failure" code path as the
// other handlers in this package without dragging in the errors
// package at the top level.
func errorsJoinInvalid(err error) error {
	if err == nil {
		return errs.ErrInvalid
	}
	return err
}

