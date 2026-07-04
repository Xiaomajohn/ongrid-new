import { request } from './client';

export type DeviceRole = 'host' | 'discovered';

export type Device = {
  id: number;
  name: string;
  hostname?: string;
  description?: string;
  // Required post-分层改造: listDevices endpoint always returns a roles
  // array (empty == 未分类). Callers should use roles.includes(role)
  // rather than .length so the intent reads cleanly.
  roles: string[];
  scope: DeviceRole;
  online?: boolean;
  last_seen_at?: string | null;
  created_at?: string;
  updated_at?: string;
  // — points at the row in topology.nodes that fronts this
  // device. Null until topology.Migrate's backfill has run.
  node_id?: number | null;
  // — basic host facts. Present once the linked edge has reported its
  // host_info at least once; absent on rows only seeded via topology
  // discovery. Frontend treats undefined as "not yet known".
  os?: string;
  os_version?: string;
  arch?: string;
  kernel_version?: string;
  ip_address?: string;
  cpu_count?: number;
  mem_total_bytes?: number;
  disk_total_bytes?: number;
  // — last sample percentages cached on the device row (Prom query
  // results, refreshed by the metrics collector). Optional because
  // they only land after the first scrape cycle runs.
  cpu_usage_pct?: number;
  mem_usage_pct?: number;
  disk_usage_pct?: number;
};

export function listDevices(params?: {
  hostname?: string;
  name?: string;
  roles?: string;
  online?: boolean;
  limit?: number;
  offset?: number;
}) {
  const qs = params
    ? '?' +
      new URLSearchParams(
        Object.entries(params).filter(
          ([, v]) => v != null && v !== '',
        ) as [string, string][],
      ).toString()
    : '';
  return request<{ items: Device[]; total: number }>('GET', `/devices${qs}`);
}

export function getDevice(id: string | number) {
  return request<Device>('GET', `/devices/${encodeURIComponent(String(id))}`);
}

export function updateDevice(
  id: string | number,
  body: { name?: string; description?: string },
) {
  return request<void>(
    'PATCH',
    `/devices/${encodeURIComponent(String(id))}`,
    body,
  );
}

export function updateDeviceRoles(id: string | number, roles: string[]) {
  return request<void>(
    'PATCH',
    `/devices/${encodeURIComponent(String(id))}/roles`,
    { roles },
  );
}

export function deleteDevice(id: string | number) {
  return request<void>('DELETE', `/devices/${encodeURIComponent(String(id))}`);
}

export function listDeviceEdges(id: string | number) {
  return request<{
    items: { edge_id: number; device_id: number; type: string; created_at: string }[];
  }>('GET', `/devices/${encodeURIComponent(String(id))}/edges`);
}
