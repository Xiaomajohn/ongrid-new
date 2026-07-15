// pluginhost.ts — 前端 HTTP client for the PluginHost subsystem.
//
// 设计要点:
//   - 复用项目已有的 fetch 包装 web/src/api/client.ts (BASE = '/api/v1'),
//     不引入 axios;PluginHost 服务端在 main.go wire-up 阶段把路由
//     mount 到 '/api/pluginhost/v1/*',前端走 '/pluginhost/...' 拼到
//     BASE 后 → 实际请求 '/api/v1/pluginhost/...'。本文件留一个
//     注释,等 Phase 4 D1 接入 server handler 时若路径变化,只改
//     BASE_PREFIX 即可。
//   - 所有字段 snake_case,跟后端 model/plugin_*.go 的 wire shape 对齐。
//   - request<T>() 已经处理了 401 自动 refresh + 错误转 ApiError,
//     调用方直接 try/catch 拿 ApiError.message 给 toast 用即可。
//
// 已知 server 端点(待 Phase 4 D1 接入后生效):
//   GET    /pluginhost/instances
//   GET    /pluginhost/instances/:id
//   GET    /pluginhost/instances/:id/capabilities
//   GET    /pluginhost/instances/:id/audits
//   POST   /pluginhost/instances         (install)
//   DELETE /pluginhost/instances/:id     (uninstall)
//   POST   /pluginhost/instances/:id/capabilities/:name/enable
//   POST   /pluginhost/instances/:id/capabilities/:name/disable
//   POST   /pluginhost/instances/:id/capabilities/:name/invoke
//   POST   /pluginhost/upload            (multipart/form-data, tarball)

import { request } from './client';

const BASE_PREFIX = '/pluginhost';

// ---------- wire types (mirror model/plugin_*.go) --------------------------

/** One installed plugin instance row (plugin_instances table). */
export interface PluginInstance {
  id: number;
  tenant_id: number;
  pack_id: string;
  version: string;
  /** source label, e.g. "local" / "tarball" / "git" / "registry:<name>". */
  source: string;
  install_path: string;
  manifest_sha256: string;
  enabled: boolean;
  health_status: 'healthy' | 'degraded' | 'down' | 'unknown';
  capabilities_count: number;
  created_at: string;
  updated_at: string;
}

/** One capability row under a plugin instance (plugin_capabilities table). */
export interface PluginCapability {
  id: number;
  plugin_instance_id: number;
  kind:
    | 'ai.tool'
    | 'notifier'
    | 'workflow.node'
    | 'skill.runner'
    | 'llm.provider'
    | 'embedding.provider'
    | 'alert.evaluator';
  name: string;
  /** safety class — drives UI badge tone + approval gate (mirrors skill class). */
  class: 'safe' | 'mutating' | 'dangerous';
  enabled: boolean;
}

/** One row from plugin_audits table; matches the audit API wire shape. */
export interface PluginAudit {
  id: number;
  occurred_at: string;
  action: string;
  actor: string;
  plugin_instance_id?: number;
  /** Free-form JSON; rendered as pretty-print in the UI. */
  details_json?: string;
}

/** Result envelope returned by POST .../capabilities/:name/invoke. */
export interface InvokeResult {
  result?: unknown;
  error?: string;
  latency_ms: number;
}

/** Spec for POST /pluginhost/instances (install). */
export interface InstallSpec {
  source: 'local' | 'tarball' | 'remote';
  /** Required when source=local. */
  path?: string;
  /** Required when source=remote (http(s) tarball URL). */
  url?: string;
}

// ---------- list ----------------------------------------------------------

type ListResp = {
  items?: PluginInstance[];
  /** Snake + Pascal fallback so we survive a json-tag flip on the server. */
  Items?: PluginInstance[];
};

function pickItems(raw: ListResp): PluginInstance[] {
  return raw.items ?? raw.Items ?? [];
}

/** GET /pluginhost/instances — list all installed plugins. */
export async function listPlugins(): Promise<PluginInstance[]> {
  const raw = await request<ListResp>('GET', `${BASE_PREFIX}/instances`);
  return pickItems(raw);
}

// ---------- one -----------------------------------------------------------

/** GET /pluginhost/instances/:id — fetch one plugin. */
export async function getPlugin(id: number): Promise<PluginInstance> {
  return request<PluginInstance>('GET', `${BASE_PREFIX}/instances/${id}`);
}

// ---------- capabilities --------------------------------------------------

type CapsResp = {
  items?: PluginCapability[];
  Items?: PluginCapability[];
};

function pickCaps(raw: CapsResp): PluginCapability[] {
  return raw.items ?? raw.Items ?? [];
}

/** GET /pluginhost/instances/:id/capabilities — list caps of one plugin. */
export async function listCapabilities(id: number): Promise<PluginCapability[]> {
  const raw = await request<CapsResp>(
    'GET',
    `${BASE_PREFIX}/instances/${id}/capabilities`,
  );
  return pickCaps(raw);
}

// ---------- audits --------------------------------------------------------

type AuditsResp = {
  items?: PluginAudit[];
  Items?: PluginAudit[];
};

function pickAudits(raw: AuditsResp): PluginAudit[] {
  return raw.items ?? raw.Items ?? [];
}

/** GET /pluginhost/instances/:id/audits — list audit rows for one plugin. */
export async function listAudits(id: number): Promise<PluginAudit[]> {
  const raw = await request<AuditsResp>(
    'GET',
    `${BASE_PREFIX}/instances/${id}/audits`,
  );
  return pickAudits(raw);
}

// ---------- install / uninstall -------------------------------------------

type InstallResp = {
  /** Server returns the new plugin instance id. Accept either snake or
   *  PascalCase for the same reason as above. */
  id?: number;
  ID?: number;
};

function pickId(raw: InstallResp): number {
  const id = raw.id ?? raw.ID;
  if (typeof id !== 'number') {
    throw new Error('install response missing id');
  }
  return id;
}

/** POST /pluginhost/instances — install a plugin from local path or remote
 *  URL. For tarball uploads, prefer uploadTarball() (multipart). */
export async function installPlugin(spec: InstallSpec): Promise<{ id: number }> {
  const raw = await request<InstallResp>('POST', `${BASE_PREFIX}/instances`, spec);
  return { id: pickId(raw) };
}

/** DELETE /pluginhost/instances/:id — uninstall a plugin. */
export async function uninstallPlugin(id: number): Promise<void> {
  await request<void>('DELETE', `${BASE_PREFIX}/instances/${id}`);
}

// ---------- capability lifecycle ------------------------------------------

/** POST .../capabilities/:name/enable — flip one capability to enabled. */
export async function enableCapability(id: number, name: string): Promise<void> {
  await request<void>(
    'POST',
    `${BASE_PREFIX}/instances/${id}/capabilities/${encodeURIComponent(name)}/enable`,
  );
}

/** POST .../capabilities/:name/disable — flip one capability to disabled. */
export async function disableCapability(id: number, name: string): Promise<void> {
  await request<void>(
    'POST',
    `${BASE_PREFIX}/instances/${id}/capabilities/${encodeURIComponent(name)}/disable`,
  );
}

/** POST .../capabilities/:name/invoke — invoke one capability.
 *  Server returns { result, error, latency_ms }. */
export async function invokeCapability(
  id: number,
  name: string,
  params: unknown,
): Promise<InvokeResult> {
  return request<InvokeResult>(
    'POST',
    `${BASE_PREFIX}/instances/${id}/capabilities/${encodeURIComponent(name)}/invoke`,
    { params },
  );
}

// ---------- tarball upload ------------------------------------------------

/** POST /pluginhost/upload — multipart tarball upload. Server unpacks and
 *  installs like a local-dir install. */
export async function uploadTarball(file: File): Promise<{ id: number }> {
  const fd = new FormData();
  fd.append('file', file);
  const raw = await request<InstallResp>('POST', `${BASE_PREFIX}/upload`, fd);
  return { id: pickId(raw) };
}