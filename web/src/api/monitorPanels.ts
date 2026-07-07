import { request } from './client';

// MonitorPanel mirrors the wire shape of the manager-side
// monitor_panels row (see internal/manager/model/monitor/model.go).
//
// The Monitor page reads the list and renders each panel via
// PromQLPanel — the wire shape is intentionally a superset of
// GrafanaPanel (id + type + title + targets[].expr + fieldConfig.unit)
// so the existing renderer just works.
export type MonitorPanelType = 'timeseries' | 'stat' | 'gauge';

export type MonitorPanel = {
  id: number;
  title: string;
  type: MonitorPanelType;
  promql: string;
  legend: string;
  unit: string;
  ordinal: number;
  last_sync_error?: string;
  last_sync_at?: string;
  created_at: string;
  updated_at: string;
  // — optional pointer at the device this panel is scoped to. Null/missing
  // ⇒ the panel is "global" (not bound to a single device). The Logs page
  // surfaces these via /v1/devices/{id}/monitors so the operator can pick
  // "panel #5 on host web-server-01" without leaving the log context.
  // Only present in the response when the server side decided to populate
  // the column — older rows predate the soft-delete / device-binding split
  // and read back as null.
  device_id?: number | null;
  // — soft-delete sentinel. null/missing ⇒ row is live; non-null ⇒ row
  // was soft-deleted. Only present in the response when the operator
  // passes `include_deleted=true` to listMonitorPanels or
  // listDeviceMonitors (default scope still strips soft-deleted rows at
  // the server, so the SPA only sees this set when it explicitly asked).
  deleted_at?: string | null;
};

export type MonitorPanelInput = {
  title: string;
  type: MonitorPanelType;
  promql: string;
  legend?: string;
  unit?: string;
  ordinal?: number;
  // Bind this panel to a single device (Logs page's "监控" dropdown is
  // the main consumer). Null / undefined ⇒ global panel. Server stores
  // it as NULL when omitted, so legacy callers don't have to set it.
  device_id?: number | null;
};

export type MonitorPanelPatch = Partial<MonitorPanelInput>;

type ListResp = { panels: MonitorPanel[] };

// listMonitorPanels fetches panels ordered by ordinal. Returns [] when
// the table is empty (fresh install).
//
// `opts.device_id` filters by the binding column (added 2026-05).
// NULL values (global panels) ARE included in the response by default
// — the Logs page's per-device dropdown expects "panels that apply
// to this device", which by definition is "device-bound OR global".
// Callers that need the strictly device-bound slice should filter
// client-side (p.device_id === opts.device_id).
//
// `opts.include_deleted = true` surfaces soft-deleted rows so the
// operator's "显示已删除" toggle can render them in the dropdown.
export async function listMonitorPanels(
  opts: {
    device_id?: number;
    include_deleted?: boolean;
  } = {},
): Promise<MonitorPanel[]> {
  const qs = (() => {
    const u = new URLSearchParams();
    if (opts.device_id != null) u.set('device_id', String(opts.device_id));
    if (opts.include_deleted) u.set('include_deleted', 'true');
    const s = u.toString();
    return s ? `?${s}` : '';
  })();
  const resp = await request<ListResp>('GET', `/monitor/panels${qs}`);
  return resp.panels ?? [];
}

// listDeviceMonitors — narrow listing scoped to a single device. Hits
// the dedicated /v1/devices/{id}/monitors route that the manager-side
// exposes (see server/device/monitors_http.go) — it's a thin wrapper
// around biz/monitor.Service.List with DeviceID pinned to the path
// param, so the SPA doesn't have to know the monitor service's full
// surface.
export async function listDeviceMonitors(
  deviceId: number | string,
  opts: { include_deleted?: boolean } = {},
): Promise<MonitorPanel[]> {
  const qs = opts.include_deleted ? '?include_deleted=true' : '';
  const resp = await request<ListResp>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/monitors${qs}`,
  );
  return resp.panels ?? [];
}

export async function createMonitorPanel(input: MonitorPanelInput): Promise<MonitorPanel> {
  return request<MonitorPanel>('POST', '/monitor/panels', input);
}

export async function updateMonitorPanel(
  id: number,
  patch: MonitorPanelPatch,
): Promise<MonitorPanel> {
  return request<MonitorPanel>('PATCH', `/monitor/panels/${id}`, patch);
}

// deleteMonitorPanel removes a panel row.
//
// `hard` defaults to false on the wire (soft delete; the operator can
// revive it via restoreMonitorPanel). Pass `{ hard: true }` for the
// un-recoverable path — only the admin UI should ever call this.
export function deleteMonitorPanel(
  id: number,
  opts: { hard?: boolean } = {},
): Promise<void> {
  const qs = opts.hard ? '?hard=true' : '';
  return request<void>('DELETE', `/monitor/panels/${id}${qs}`);
}

// restoreMonitorPanel revives a soft-deleted panel. Admin-only on the
// server. Returns the post-restore DTO so the SPA can swap it into the
// list without a follow-up GET.
export function restoreMonitorPanel(id: number): Promise<MonitorPanel> {
  return request<MonitorPanel>('POST', `/monitor/panels/${id}/restore`);
}