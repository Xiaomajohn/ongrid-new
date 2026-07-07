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
  // — ping 服务 5min 定时器写入的网络层可达性结果。与 edge agent
  // 推送的 online 解耦：operator 视角“机器在线” = reachable。
  // 首次启动后第一次跑 ping 之前，reachable 为 false；
  // last_reachable_at 为 null。SPA 的 Hosts 页面按 reachable 渲染状态列。
  reachable?: boolean;
  last_reachable_at?: string | null;
  created_at?: string;
  updated_at?: string;
  // — points at the row in topology.nodes that fronts this
  // device. Null until topology.Migrate's backfill has run.
  node_id?: number | null;
  // — soft-delete sentinel. null/missing ⇒ row is live; non-null ⇒ row
  // was soft-deleted. Only present in the response when the operator
  // passes `include_deleted=true` to listDevices (the default-scope
  // request still strips soft-deleted rows at the server, so the SPA
  // only ever sees this set when it explicitly asked for it).
  deleted_at?: string | null;
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
  // — SSH 凭据（内部系统，明文回显）。仅 `ssh_host` / `ssh_port` /
  // `ssh_user` 是上下文信息；`ssh_password` / `ssh_key` 是真凭据，
  // 渲染时需自建查看 / 复制 UI（本次未做 UI 组件，仅类型扩展对齐 API）。
  ssh_host?: string;
  ssh_port?: number;
  ssh_user?: string;
  ssh_auth_kind?: 'password' | 'key';
  ssh_password?: string;
  ssh_key?: string;
};

export function listDevices(params?: {
  hostname?: string;
  name?: string;
  roles?: string;
  online?: boolean;
  limit?: number;
  offset?: number;
  // include_deleted = true → surface soft-deleted rows. Default false so the
  // list view stays clean; the "显示已删除" filter bar toggles it.
  include_deleted?: boolean;
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

export type CreateDeviceInput = {
  name: string;
  description?: string;
  hostname?: string;
  ssh_host: string;
  ssh_port: number;
  ssh_user: string;
  ssh_auth_kind: 'password' | 'key';
  ssh_password?: string;
  ssh_key?: string;
};

export type CreateDeviceResponse = {
  id: number;
  name: string;
  hostname?: string;
  description?: string;
  created_at?: string;
  // 内部系统不回显密文 — 同 Device 上 6 个 SSH 字段语义
  ssh_host?: string;
  ssh_port?: number;
  ssh_user?: string;
  ssh_auth_kind?: 'password' | 'key';
  ssh_password?: string;
  ssh_key?: string;
};

// createDevice — register a new logical host + SSH credentials. Backend
// persists the credential plaintext-at-rest (per plan §SSH API contract)
// so the install / SFTP flows can SSH into the box without re-prompting.
export function createDevice(input: CreateDeviceInput) {
  return request<CreateDeviceResponse>('POST', '/devices', input);
}

export function getDevice(id: string | number) {
  return request<Device>('GET', `/devices/${encodeURIComponent(String(id))}`);
}

// UpdateDeviceInput — 后端 PATCH /v1/devices/{id} 的 wire body。后端
// 走“指针 = 可选”语义：未指定的字段保持不变；指定为空串 ("") 则
// 视为“清空”（仅对 ssh_password / ssh_key 有效，name / ssh_host /
// ssh_user 的空串会被服务端拒掉）。前端“编辑主机”对话框根据 dirty
// 状态传部分字段，服务端 merge 后回 200 + 完整 DTO。
export type UpdateDeviceInput = {
  name?: string;
  description?: string;
  hostname?: string;
  ssh_host?: string;
  ssh_port?: number;
  ssh_user?: string;
  ssh_auth_kind?: 'password' | 'key';
  ssh_password?: string;
  ssh_key?: string;
};

export function updateDevice(id: string | number, body: UpdateDeviceInput) {
  return request<Device>(
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

// deleteDevice removes a device row.
//
// `hard` defaults to false on the wire (soft delete, the operator can
// revive it via restoreDevice). Pass `{ hard: true }` for the
// un-recoverable path — only the admin UI should ever call this.
export function deleteDevice(
  id: string | number,
  opts: { hard?: boolean } = {},
) {
  const qs = opts.hard ? '?hard=true' : '';
  return request<void>(
    'DELETE',
    `/devices/${encodeURIComponent(String(id))}${qs}`,
  );
}

// restoreDevice revives a soft-deleted device. Admin-only on the
// server. Returns the post-restore DTO so the SPA can swap it into the
// list without a follow-up GET.
export function restoreDevice(id: string | number) {
  return request<Device>(
    'POST',
    `/devices/${encodeURIComponent(String(id))}/restore`,
  );
}

export function listDeviceEdges(id: string | number) {
  return request<{
    items: { edge_id: number; device_id: number; type: string; created_at: string }[];
  }>('GET', `/devices/${encodeURIComponent(String(id))}/edges`);
}

// =============================================================================
// SSH credentials (per-device, plaintext-at-rest internal use only)
// =============================================================================

export interface SSHInfo {
  host: string;
  port: number;
  user: string;
  auth_kind: 'password' | 'key';
  has_password: boolean;
  has_key: boolean;
  edge_online: boolean;
  // 内部系统：密码 / 私钥随主结构明文回显（不做密文处理），前端按需渲染。
  // 空串表示该 auth_kind 未配置。
  password?: string;
  key?: string;
}

export function getDeviceSSHInfo(deviceId: string | number) {
  return request<SSHInfo>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/ssh-info`,
  );
}

export function putDeviceSSHCredentials(
  deviceId: string | number,
  kind: 'password' | 'key',
  value: string,
) {
  return request<void>(
    'PUT',
    `/devices/${encodeURIComponent(String(deviceId))}/ssh-credentials`,
    { kind, value },
  );
}

export function deleteDeviceSSHCredentials(
  deviceId: string | number,
  kind: 'password' | 'key',
) {
  return request<void>(
    'DELETE',
    `/devices/${encodeURIComponent(String(deviceId))}/ssh-credentials?kind=${encodeURIComponent(kind)}`,
  );
}

// =============================================================================
// One-button edge install job
// =============================================================================

export type InstallJobStatus =
  | 'queued'
  | 'running'
  | 'success'
  | 'failed'
  | 'cancelled'
  | 'timeout';

export interface InstallJob {
  id: number;
  device_id: number;
  edge_id: number | null;
  status: InstallJobStatus;
  host: string;
  port: number;
  user: string;
  log_output: string;
  started_at: string | null;
  finished_at: string | null;
  exit_code: number | null;
  created_at: string;
  updated_at: string;
}

export interface InstallEdgeOptions {
  // 任务名（可选）。任务名由「新建 Edge」流程（CreateEdgeModal）填入并
  // 直接写入 edge.task_name，本接口（POST /v1/devices/{id}/install-edge）
  // 不再二次询问。前端两个安装弹窗均不再要求用户填这一字段；留空、null
  // 或省略该字段都被后端允许，edge.task_name 保持原值（由 agent 上报时
  // 补填或手动 Edit Edge 补填）。
  task_name?: string;
  // 前端拼好的完整 curl 安装命令，后端不再自己拼装，直接 SSH 执行。
  command?: string;
  ssh_pass?: string;
  ssh_key_pem?: string;
  options?: Record<string, unknown>;
}

export interface InstallEdgeResponse {
  install_job_id: number;
  status: string;
}

export function installEdge(
  deviceId: string | number,
  opts: InstallEdgeOptions,
) {
  return request<InstallEdgeResponse>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/install-edge`,
    opts,
  );
}

export function getInstallJob(jobId: string | number) {
  return request<InstallJob>(
    'GET',
    `/install-jobs/${encodeURIComponent(String(jobId))}`,
  );
}

export function listInstallJobsByDevice(
  deviceId: string | number,
  limit = 20,
) {
  return request<{ items: InstallJob[] }>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/install-jobs?limit=${limit}`,
  );
}

// listInstallJobsByEdge 按监控设备 edge 维度拉最近一批安装任务。一台
// device 可以装多个 edge（不同 task_name），Hosts 页面仅能按 device 查；
// 监控设备页面（Edges.tsx）的「日志」按钮走这里。后端对应
// GET /v1/edges/{id}/install-jobs，默认 limit=20，与 listInstallJobsByDevice
// 对齐。Hosts 页面的日志入口被需求调整掉，本函数仅 Edges 页使用。
export function listInstallJobsByEdge(
  edgeId: string | number,
  limit = 20,
) {
  return request<{ items: InstallJob[] }>(
    'GET',
    `/edges/${encodeURIComponent(String(edgeId))}/install-jobs?limit=${limit}`,
  );
}

export function cancelInstallJob(jobId: string | number) {
  return request<void>(
    'POST',
    `/install-jobs/${encodeURIComponent(String(jobId))}:cancel`,
  );
}

// =============================================================================
// SFTP / filesystem operations (proxied via edgeagent tunnel)
// =============================================================================

export interface FSEntry {
  name: string;
  mode: number;
  size: number;
  mtime: number;
  is_dir: boolean;
}

export function fsList(deviceId: string | number, path: string) {
  return request<{ entries: FSEntry[] }>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/list?path=${encodeURIComponent(path)}`,
  );
}

export function fsStat(deviceId: string | number, path: string) {
  return request<FSEntry>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/stat?path=${encodeURIComponent(path)}`,
  );
}

export function fsMkdir(deviceId: string | number, path: string, mode?: number) {
  return request<void>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/mkdir`,
    { path, mode },
  );
}

// fsWrite — overwrite / create a text file on the remote via the SFTP
// tunnel. Used by FileBrowser's "edit" affordance to commit file content
// without having to round-trip through a download + edit + upload cycle.
// Backend caps the size (text-only, see plan §SFTP API contract).
export function fsWrite(deviceId: string | number, path: string, content: string) {
  return request<void>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/write`,
    { path, content },
  );
}

export function fsRmdir(deviceId: string | number, path: string) {
  return request<void>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/rmdir`,
    { path },
  );
}

export function fsRm(deviceId: string | number, path: string) {
  return request<void>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/rm`,
    { path },
  );
}

export function fsRename(
  deviceId: string | number,
  oldPath: string,
  newPath: string,
) {
  return request<void>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/rename`,
    { old: oldPath, new: newPath },
  );
}

export function fsChmod(deviceId: string | number, path: string, mode: number) {
  return request<void>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/chmod`,
    { path, mode },
  );
}

export interface FSUploadResponse {
  size_bytes: number;
  mtime: number;
}

export function fsUpload(
  deviceId: string | number,
  path: string,
  file: File,
) {
  const form = new FormData();
  form.append('file', file);
  form.append('path', path);
  return request<FSUploadResponse>(
    'POST',
    `/devices/${encodeURIComponent(String(deviceId))}/fs/upload`,
    form,
  );
}

// fsDownloadURL returns the API path for fetching a file's raw bytes
// (the caller is responsible for adding the auth header via fetch / a
// helper that already has the bearer token, e.g. via the global fetch
// wrapper that uses `request`).
export function fsDownloadURL(deviceId: string | number, path: string): string {
  // NB: The request() helper prepends BASE='/api/v1' so the literal path
  // goes through. For a direct <a href> download the caller needs the
  // absolute URL — consumers should typically go through fetch with
  // auth, or open this in a new tab once they have a signed URL.
  return `/devices/${encodeURIComponent(String(deviceId))}/fs/download?path=${encodeURIComponent(path)}`;
}

// fsRead returns the raw bytes (Blob) for a file. Goes via fetch directly
// because the API returns binary, not JSON. Reuses the bearer-token helper
// from the auth store so the read picks up 401 refreshes the same way the
// rest of the SPA does.
export async function fsRead(
  deviceId: string | number,
  path: string,
): Promise<Blob> {
  const { getToken } = await import('@/store/auth');
  const token = getToken();
  const res = await fetch(`/api/v1${fsDownloadURL(deviceId, path)}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) {
    throw new Error(`fsRead HTTP ${res.status}`);
  }
  return res.blob();
}
