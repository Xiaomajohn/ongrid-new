import { request } from './client';

export type EdgeStatus = 'online' | 'offline' | 'unknown';

// EdgeRole drives the sidebar 设备 sub-menu and AI prompt routing.
// Backend stores a bit field; the wire shape is an array of these names.
// One device can carry multiple roles (e.g. a hyper-converged box that's
// both server + storage).
export type EdgeRole = 'server' | 'storage' | 'network' | 'database';

export const EDGE_ROLES: EdgeRole[] = ['server', 'storage', 'network', 'database'];

export const EDGE_ROLE_LABELS: Record<EdgeRole, string> = {
  server: '服务器',
  storage: '存储',
  network: '网络设备',
  database: '数据库',
};

export const EDGE_ROLE_LABELS_EN: Record<EdgeRole, string> = {
  server: 'Server',
  storage: 'Storage',
  network: 'Network',
  database: 'Database',
};

export type Edge = {
  id: number;
  name: string;
  status: EdgeStatus;
  // roles is always present (empty array == 未分类). Coding-wise prefer
  // checking includes() rather than .length so the intent reads cleanly.
  roles: EdgeRole[];
  access_key_id: string;
  last_seen_at: string | null;
  created_at?: string;
  updated_at?: string;
  host_info?: Record<string, unknown> | string | null;
  // device_id is the Prom label key — every host metric (cpu/mem/disk) is
  // tagged with the linked Device.ID, not the edge's own id. The dashboard
  // sparkline + drilldowns key off this. Null means the edge hasn't been
  // linked to a Device row yet (newly created or the linker pass missed
  // it); the UI treats that as "no metrics yet".
  device_id?: number | null;
  // agent_version is the binary version the edge agent self-reported on
  // its most recent register_edge handshake. Empty string means the agent
  // declined to report (e.g. pre-introduction binary).
  agent_version?: string;
  // 任务名 — 由「新建 Edge」流程在 CreateEdgeModal 填入并直接写入
  // edge.task_name；「安装 Edge」弹窗不再二次询问。空字符串表示创建时未携
  // 带该字段；UI 渲染为空时显示 —。后端新增精准查询参数 name / hostname /
  // ip 的依据字段之一。
  task_name?: string;
  // 软删除时间戳，非空表示该 edge 已被删除。历史数据页面用来过滤。
  deleted_at?: string | null;
  // 确认删除标记（二次删除），非零表示已确认删除。include_deleted=true
  // 的查询会排除这些行；历史数据页用它过滤已确认的行。
  purge_marker?: number;
};

export type UpgradeAgentResponse = {
  staged_path: string;
  bytes: number;
};

export function upgradeEdgeAgent(id: number, url: string, sha256: string) {
  return request<UpgradeAgentResponse>('POST', `/edges/${id}/upgrade`, { url, sha256 });
}

// one-button upgrade. Server resolves the bundle (URL + sha)
// from its baked edge-bundles dir; admin doesn't supply anything.
// Optional body overrides arch / version for pinning scenarios.
export type UpgradePackageResponse = {
  version: string;
  staged_path: string;
  bytes: number;
  manifest_files: number;
  applied: boolean;
  apply_error?: string;
};

export function upgradeEdgePackage(id: number, opts?: { arch?: string; version?: string }) {
  return request<UpgradePackageResponse>('POST', `/edges/${id}/upgrade-package`, opts ?? {});
}

export type EdgeProcess = {
  pid: number;
  name: string;
  cmdline?: string;
  cpu_pct: number;
  mem_pct: number;
  user?: string;
};

export type EdgeProcessesResp = {
  items: EdgeProcess[];
  sampled_at: number;
};

// Reads top-N processes via tunnel RPC. sort_by = 'cpu' | 'mem'; default
// mem (Monitor panel's primary use case is "what's eating memory").
export function listEdgeProcesses(id: number, opts?: { topN?: number; sortBy?: 'cpu' | 'mem' }) {
  const q = new URLSearchParams();
  if (opts?.topN) q.set('top_n', String(opts.topN));
  if (opts?.sortBy) q.set('sort_by', opts.sortBy);
  const qs = q.toString();
  return request<EdgeProcessesResp>('GET', `/edges/${id}/processes${qs ? `?${qs}` : ''}`);
}

export type CreateEdgeResponse = {
  id: number;
  name: string;
  access_key_id: string;
  secret_key: string;
  created_at: string;
};

export type RotateSecretResponse = {
  secret_key: string;
};

export type MetricBucket = { avg: number; max: number };

export type MetricPoint = {
  ts: string;
  cpu: MetricBucket;
  mem: MetricBucket;
  load1: MetricBucket;
  load5: MetricBucket;
  load15: MetricBucket;
  net_rx_bps: number;
  net_tx_bps: number;
  disk_used_pct: MetricBucket;
};

export type MetricsResponse = {
  resolution: string;
  from: string;
  to: string;
  points: MetricPoint[];
};

// listEdges — 后端 listEdges 支持以下精准查询参数：
//   - roles       ：逗号分隔的角色列表（来自 Sidebar ?roles= URL）
//   - device_id   ：单设备过滤（一般不通过 UI 触发，预留）
//   - name        ：edge.name 模糊匹配
//   - hostname    ：从 device.hostname / edge.host_info 回填后模糊匹配
//   - ip          ：从 device.ip_address / edge.host_info 回填后模糊匹配
// 三个字符串参数都为空时省略 URL key，避免多余 query string。
export function listEdges(params?: {
  roles?: string;
  device_id?: number | string;
  name?: string;
  hostname?: string;
  ip?: string;
  include_deleted?: boolean;
}) {
  const usp = new URLSearchParams();
  if (params?.roles) usp.set('roles', params.roles);
  if (params?.device_id != null) usp.set('device_id', String(params.device_id));
  if (params?.name) usp.set('name', params.name);
  if (params?.hostname) usp.set('hostname', params.hostname);
  if (params?.ip) usp.set('ip', params.ip);
  if (params?.include_deleted) usp.set('include_deleted', 'true');
  const qs = usp.toString();
  return request<{ items: Edge[]; total: number }>('GET', `/edges${qs ? `?${qs}` : ''}`);
}

export function getEdge(id: string | number) {
  return request<Edge>('GET', `/edges/${encodeURIComponent(String(id))}`);
}

export function createEdge(input: {
  name: string;
  // 所属设备 ID（可选）。传了就创建时直接写 edge.device_id + edge_devices
  // 关联，不再等 agent register 指纹 upsert。0 / undefined = 不关联，
  // 保留 fingerprint 兑底路径。
  device_id?: number;
  // 任务名（可选）。直接写 edge.task_name，不再依赖 install 流程的
  // BindEdgeFromAccessKey 补写。空串等同于不传。
  task_name?: string;
}) {
  return request<CreateEdgeResponse>('POST', '/edges', {
    name: input.name,
    device_id: input.device_id,
    task_name: input.task_name,
  });
}

export function deleteEdge(id: string | number) {
  return request<void>('DELETE', `/edges/${encodeURIComponent(String(id))}`);
}

// confirmDeleteEdge 确认删除（二次删除）：将 edge 的 purge_marker 置为
// 非零值。行不做物理删除，但日志页面查询不到该任务，历史数据页不再展示。
export function confirmDeleteEdge(id: string | number) {
  return request<void>('POST', `/edges/${encodeURIComponent(String(id))}/confirm-delete`);
}

export function rotateSecret(id: string | number) {
  return request<RotateSecretResponse>(
    'POST',
    `/edges/${encodeURIComponent(String(id))}/rotate-secret`,
  );
}

// setEdgeRoles replaces the host device's role bit set. Pass [] to
// clear all. The roles live on the linked device row, not the edge,
// post device/edge split — so the API target is /v1/devices/{device_id}.
// The edge.device_id pointer must already be set (i.e. the edge has
// reported its host_info at least once); otherwise this rejects.
export function setEdgeRoles(deviceId: number | string, roles: EdgeRole[]) {
  return request<void>(
    'PATCH',
    `/devices/${encodeURIComponent(String(deviceId))}/roles`,
    { roles },
  );
}

export function getMetrics(
  id: string | number,
  params: { from: string; to: string; resolution?: string },
) {
  const qs = new URLSearchParams({
    from: params.from,
    to: params.to,
    resolution: params.resolution ?? 'raw',
  }).toString();
  return request<MetricsResponse>(
    'GET',
    `/edges/${encodeURIComponent(String(id))}/metrics?${qs}`,
  );
}

// Generic auth'd PromQL passthrough — backend wraps prom /api/v1/query_range,
// enforces 30s timeout and 4 KB expr cap. The matrix is shipped through
// untouched (each entry: {metric:{labels...}, values:[[ts, "value"], ...]})
// so callers can pivot on whatever label dimension the panel cares about
// (cpu / mountpoint / device / ...).
export type PromMatrixSample = [number, string];
export type PromMatrixSeries = {
  metric: Record<string, string>;
  values: PromMatrixSample[];
};
export type PromRangeResp = {
  resolution: string;
  from: string;
  to: string;
  matrix: PromMatrixSeries[];
};

export function promQueryRange(input: {
  expr: string;
  from: string;
  to: string;
  step: string;
}): Promise<PromRangeResp> {
  const qs = new URLSearchParams({
    expr: input.expr,
    start: input.from,
    end: input.to,
    step: input.step,
  }).toString();
  return request<PromRangeResp>('GET', `/metrics/query_range?${qs}`);
}
