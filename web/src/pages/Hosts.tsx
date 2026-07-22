// Hosts.tsx — 实体设备列表页（仿 Edges.tsx 风格，但展示 Device 实体，而非 Edge 探针）。
//
// 设计要点：
//   - 拷贝 Edges.tsx 的 RoleChips / RolesEditorModal / ShellButton /
//     RowMenu 等所有子组件到这里，避免循环依赖（Edges.tsx 和 Hosts.tsx
//     互不引用）。
//   - Device 字段可能仍在演进中（hostname / ip_address / os 等字段由
//     其他 agent 落地后端 Device 模型）。我们用 HostDevice = Device & {…}
//     扩展本地类型并 cast API 响应，方便在不修改 devices.ts 的情况下
//     容忍字段缺失。
//   - 删除 / 更新角色等 wire 调用暂时封装成本地 async helper，等其他
//     agent 把对应函数加进 devices.ts 后可以原地换为命名导入。
//
// 探针签发入口说明：
//   - 主机列表页（/hosts）顶部不再有"添加探针"按钮，避免与 /devices
//     监控页面（Edges.tsx）的"新建"按钮重复。
//   - 探针签发统一走两条路径：
//     1) 一键安装 — Hosts 列表每行 InstallButton，自动签发 + 自动安装
//     2) /devices 监控页面 — "新建"按钮 + HostDetail.tsx ProbesTab 的
//        "添加探针"链接（跳到 /devices）签发后手动安装
//
// 不要做的事（plan 明确）：
//   - 不要修改 Edges.tsx / EdgeDetail.tsx / Sidebar.tsx / api/devices.ts
//     — 都被其他 agent 占用。
//   - 不要写单元测试。
import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  Plus,
  Trash2,
  ExternalLink,
  TerminalSquare,
  Folder,
  Power,
  Pencil,
  History,
} from 'lucide-react';
import { StatusPill } from '@/components/StatusPill';
import { Modal } from '@/components/Modal';
import { cn } from '@/lib/cn';
import { relativeTime } from '@/lib/format';
import { usePoll } from '@/lib/usePoll';
import {
  listDevices,
  deleteDevice,
  type Device,
} from '@/api/devices';
import {
  listEdges,
  EDGE_ROLES,
  EDGE_ROLE_LABELS,
  EDGE_ROLE_LABELS_EN,
  type Edge,
  type EdgeRole as HostEdgeRole,
} from '@/api/edges';
import { request } from '@/api/client';
import { usePermissions } from '@/store/me';
import { notifyDevicesChanged } from '@/lib/events';
import { useI18n } from '@/i18n/locale';
import { CreateDeviceModal } from '@/components/CreateDeviceModal';
import { EditDeviceModal } from '@/components/EditDeviceModal';
import { InstallEdgeModal } from '@/components/InstallEdgeModal';
import { ConfirmDeleteModal } from '@/components/ConfirmDeleteModal';
import {
  HostsFilterBar,
  type HostsFilterValue,
} from '@/components/HostsFilterBar';

// HostDevice — 真实在用的 device 字段集合。devices.ts 当前的 `Device` 只
// 暴露了最小集；其他字段由其他 agent 落地后端模型。这里本地扩展 + cast
// API 响应，做到不修改 devices.ts 的前提下能写出符合直觉的页面。
type HostDevice = Device & {
  ip_address?: string;
  description?: string;
  os?: string;
  os_version?: string;
  arch?: string;
  kernel?: string;
  cpu_count?: number;
  cpu_usage_pct?: number;
  mem_total_bytes?: number;
  mem_usage_pct?: number;
  disk_total_bytes?: number;
  disk_usage_pct?: number;
  fingerprint?: string;
  created_by?: string;
  // 软删除标记 — 当 include_deleted=true 时由后端填充
  deleted_at?: string | null;
};

// ----- local wrappers around API functions not yet exported from devices.ts -----
// 等其他 agent 把 updateDeviceRoles / listDeviceEdges 加进
// devices.ts 之后，这些 wrapper 换成命名 import 即可，调用方代码不动。

async function updateDeviceRolesLocal(
  deviceId: string | number,
  roles: string[],
): Promise<void> {
  return request<void>(
    'PATCH',
    `/devices/${encodeURIComponent(String(deviceId))}/roles`,
    { roles },
  );
}

async function listDeviceEdgesLocal(
  deviceId: string | number,
): Promise<Edge[]> {
  const r = await request<{ items?: Edge[]; total?: number }>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/edges`,
  );
  return r.items ?? [];
}

const INITIAL_FILTER: HostsFilterValue = {
  name: '',
  role: '',
  online: 'all',
  edge: 'all',
  includeDeleted: false,
};

export default function HostsPage() {
  const navigate = useNavigate();
  const { tr } = useI18n();
  const { canMutate } = usePermissions();

  const [filter, setFilter] = useState<HostsFilterValue>(INITIAL_FILTER);
  const [hosts, setHosts] = useState<HostDevice[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // 每行 host 的探针数（key=host.id），mount 时拉一次，之后刷新时不重拉
  // — 单独缓存避免每次 refresh 都打 N+1 个接口。
  const [edgesPerHost, setEdgesPerHost] = useState<Record<number, number>>({});
  // 每行 host 的"是否有在线 edge"（用于一键安装按钮的 disabled 规则）。
  const [edgeOnlinePerHost, setEdgeOnlinePerHost] = useState<Record<number, boolean>>({});

  const [createDeviceOpen, setCreateDeviceOpen] = useState(false);
  const [rolesEditTarget, setRolesEditTarget] = useState<HostDevice | null>(null);

  // Install modal state. 仅保留「一键安装」触发入口；安装日志入口
  // 已迁移到监控设备页面（Edges.tsx），按 edge 而不是 device 查日志。
  // 一台 device 装多个 edge 时，device 维度无法区分谁的安装属于谁，
  // 所以 Hosts 页面不再承载日志按钮。
  const [installTarget, setInstallTarget] = useState<HostDevice | null>(null);

  // Confirm-delete state.
  const [deleteTarget, setDeleteTarget] = useState<HostDevice | null>(null);
  const [deleting, setDeleting] = useState(false);

  // Edit-device state。点击“编辑”打开 EditDeviceModal，编辑后通过
  // updateDevice PATCH 到后端。模态关掉 + refresh 拿最新一行回填。
  const [editTarget, setEditTarget] = useState<HostDevice | null>(null);

  const refresh = useCallback(async () => {
    try {
      const r = await listDevices({
        name: filter.name || undefined,
        roles: filter.role || undefined,
        online:
          filter.online === 'all'
            ? undefined
            : filter.online === 'online',
        include_deleted: filter.includeDeleted || undefined,
      });
      const items = (r.items ?? []) as HostDevice[];
      setHosts(items);
      setError(null);
      // 后台拉探针数（best effort，失败就保持旧值 / 默认 0）
      void listEdges().then((edgesResp) => {
        const map: Record<number, number> = {};
        const onlineMap: Record<number, boolean> = {};
        for (const e of edgesResp.items ?? []) {
          if (e.device_id != null) {
            map[e.device_id] = (map[e.device_id] ?? 0) + 1;
            if (e.status === 'online') onlineMap[e.device_id] = true;
          }
        }
        setEdgesPerHost(map);
        setEdgeOnlinePerHost(onlineMap);
      });
    } catch (err) {
      setError((err as Error).message || tr('加载失败', 'Load failed'));
    } finally {
      setLoading(false);
    }
  }, [filter]);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  usePoll(refresh, 10_000);

  // Client-side "在线 / 离线" filter on the freshly fetched list. The
  // backend's `online` query is a bool — using the dropdown we
  // additionally constrain by edge status if a non-`all` value is set.
  const visibleHosts = (() => {
    if (filter.online === 'all' && filter.edge === 'all' && !filter.role) return hosts;
    return hosts.filter((h) => {
      if (filter.role && !(h.roles ?? []).includes(filter.role)) return false;
      if (filter.online !== 'all') {
        const want = filter.online === 'online';
        if (!!h.online !== want) return false;
      }
      if (filter.edge !== 'all') {
        const edgeOn = !!edgeOnlinePerHost[h.id];
        const want = filter.edge === 'online';
        if (edgeOn !== want) return false;
      }
      return true;
    });
  })();

  // 安装日志入口已迁到监控设备页面（Edges.tsx），这里不再维护每个
  // device 的最近一次 install job 缓存——一台 device 可能装多个 edge，
  // 按 device 维度拿“最近一次”会混在多个 edge 之间失去归属语义。

  // ----- 创建 / 删除 handlers -----

  async function onDeleteHost(h: HostDevice) {
    setDeleting(true);
    try {
      await deleteDevice(h.id);
      setDeleteTarget(null);
      void refresh();
      notifyDevicesChanged();
    } catch (err) {
      alert((err as Error).message || tr('删除失败', 'Delete failed'));
    } finally {
      setDeleting(false);
    }
  }

  return (
    <>
      <main className="anim-fade flex flex-1 flex-col overflow-hidden">
        <header className="app-header flex items-center justify-between border-b border-zinc-800/60 px-6 py-4">
          <div>
            <h1 className="text-base font-semibold text-zinc-100">
              {tr('主机', 'Hosts')}
            </h1>
            <p className="mt-0.5 text-xs text-zinc-500">
              {tr(
                `${visibleHosts.length} 台主机 · 每 10 秒自动刷新`,
                `${visibleHosts.length} host(s) · auto-refresh every 10s`,
              )}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Link
              to="/edges/shell-sessions"
              className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
              title={tr('WebSSH 会话审计 / 活跃会话', 'WebSSH session audit / active sessions')}
            >
              <TerminalSquare size={12} /> {tr('WebSSH 会话', 'WebSSH sessions')}
            </Link>
            <Link
              to="/devices/history"
              className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
              title={tr('已删除的主机与监控任务', 'Deleted hosts and monitoring tasks')}
            >
              <History size={12} /> {tr('历史数据', 'History')}
            </Link>
            {canMutate && (
              <button
                type="button"
                onClick={() => setCreateDeviceOpen(true)}
                aria-label={tr('添加设备', 'Add device')}
                data-testid="add-device"
                className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-200 hover:bg-zinc-800"
              >
                <Plus size={12} /> {tr('添加设备', 'Add device')}
              </button>
            )}
          </div>
        </header>

        <div className="flex-1 overflow-y-auto px-6 py-6">
          {error && (
            <div
              role="alert"
              className="mb-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300"
            >
              {error}
            </div>
          )}

          <HostsFilterBar
            value={filter}
            total={visibleHosts.length}
            onChange={setFilter}
          />

          <div className="overflow-hidden rounded-xl border border-zinc-800/60 bg-zinc-900/40">
            <table className="w-full text-sm">
              <thead className="border-b border-zinc-800/60 bg-zinc-950/40 text-[11px] uppercase tracking-wider text-zinc-500">
                <tr>
                  <th className="px-4 py-2.5 text-left">ID</th>
                  <th className="px-4 py-2.5 text-left">{tr('名称', 'Name')}</th>
                  <th className="px-4 py-2.5 text-left">{tr('主机名', 'Hostname')}</th>
                  <th className="px-4 py-2.5 text-left">IP</th>
                  <th className="px-4 py-2.5 text-left">{tr('角色', 'Roles')}</th>
                  <th className="px-4 py-2.5 text-left">{tr('状态', 'Status')}</th>
                  <th className="px-4 py-2.5 text-left">{tr('探针数', 'Probes')}</th>
                  <th className="px-4 py-2.5 text-left">{tr('最后心跳', 'Last heartbeat')}</th>
                  {filter.includeDeleted && (
                    <th className="px-4 py-2.5 text-left">{tr('已删除', 'Deleted')}</th>
                  )}
                  <th className="px-4 py-2.5 text-right">{tr('操作', 'Actions')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/40">
                {loading && hosts.length === 0 ? (
                  <tr>
                    <td colSpan={filter.includeDeleted ? 10 : 9} className="px-4 py-10 text-center text-zinc-500">
                      {tr('加载中…', 'Loading…')}
                    </td>
                  </tr>
                ) : visibleHosts.length === 0 ? (
                  <tr>
                    <td colSpan={filter.includeDeleted ? 10 : 9} className="px-4 py-10 text-center text-zinc-500">
                      {tr(
                        '暂无主机。点击右上角"添加设备"创建一台。',
                        'No hosts yet. Click "Add device" in the top right to create one.',
                      )}
                    </td>
                  </tr>
                ) : (
                  visibleHosts.map((h) => (
                    <tr
                      key={h.id}
                      className={cn(
                        'cursor-pointer transition-colors hover:bg-zinc-900/40',
                        h.deleted_at && 'opacity-60',
                      )}
                      onClick={() => navigate(`/hosts/${encodeURIComponent(String(h.id))}`)}
                    >
                      <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                        {h.id}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-zinc-100">
                        {h.name || (
                          <span className="italic text-zinc-500">{tr('（未命名）', '(unnamed)')}</span>
                        )}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-zinc-400">
                        {h.hostname || '—'}
                      </td>
                      {/* IP 列优先级：用户填的 ssh_host（创建设备时输入的）→
                          edge 实际上报的 ip_address。ssh_host 是 operator
                          写下的 IP，是真“添加时输入的 IP”，但容错上要
                          兼容老数据（只有 ip_address 没有 ssh_host）。
                          都为空时落 "—"。 */}
                      <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                        {h.ssh_host || h.ip_address || '—'}
                      </td>
                      <td
                        className={cn(
                          'whitespace-nowrap px-4 py-2.5',
                          canMutate && !h.deleted_at && 'cursor-pointer',
                        )}
                        title={canMutate ? tr('点击分配角色', 'Click to assign roles') : undefined}
                        onClick={(ev) => {
                          if (!canMutate || h.deleted_at) return;
                          ev.stopPropagation();
                          setRolesEditTarget(h);
                        }}
                      >
                        <HostRoleChips roles={(h.roles ?? []) as HostEdgeRole[]} />
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5">
                        {/* 状态列按 reachable 渲染：ping 服务的 5min
                            定时结果，operator 视角 = "网络层是否能
                            通"。edge agent 推送的 online 还在 device
                            对象里（h.online），但只用于内部诊断；UI
                            默认按 reachable 走。新装机器在第一次 ping
                            之前 reachable 为 false，显示离线，这是一
                            致语义。 */}
                        <StatusPill status={h.reachable ? 'online' : 'offline'} />
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-zinc-400">
                        {edgesPerHost[h.id] ?? 0}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2.5 text-zinc-400">
                        {h.last_seen_at ? relativeTime(h.last_seen_at) : '—'}
                      </td>
                      {filter.includeDeleted && (
                        <td className="whitespace-nowrap px-4 py-2.5 text-xs text-red-400">
                          {h.deleted_at ? relativeTime(h.deleted_at) : '—'}
                        </td>
                      )}
                      <td
                        className="whitespace-nowrap px-4 py-2.5 text-right"
                        onClick={(ev) => ev.stopPropagation()}
                      >
                        <ShellButton device={h} canMutate={canMutate} />
                        <InstallButton
                          device={h}
                          edgeOnline={!!edgeOnlinePerHost[h.id]}
                          canMutate={canMutate && !h.deleted_at}
                          onClick={() => setInstallTarget(h)}
                        />
                        <FilesButton
                          deviceId={h.id}
                          disabled={!!h.deleted_at}
                          onClick={() => navigate(`/devices/${encodeURIComponent(String(h.id))}/files`)}
                        />
                        {canMutate && !h.deleted_at && (
                          <button
                            type="button"
                            onClick={() => setEditTarget(h)}
                            title={tr('编辑主机', 'Edit host')}
                            aria-label={tr(`编辑 ${h.name || h.id}`, `Edit ${h.name || h.id}`)}
                            data-testid={`edit-host-${h.id}`}
                            className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
                          >
                            <Pencil size={14} />
                            <span>{tr('编辑', 'Edit')}</span>
                          </button>
                        )}
                        <button
                          type="button"
                          onClick={() => navigate(`/hosts/${encodeURIComponent(String(h.id))}`)}
                          title={tr('打开详情', 'Open detail')}
                          className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
                        >
                          <ExternalLink size={14} />
                          <span>{tr('详情', 'Detail')}</span>
                        </button>
                        {canMutate && !h.deleted_at && (
                          <button
                            type="button"
                            onClick={() => setDeleteTarget(h)}
                            title={tr('删除主机', 'Delete host')}
                            aria-label={tr(`删除 ${h.name || h.id}`, `Delete ${h.name || h.id}`)}
                            className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-red-300 hover:bg-red-500/10"
                          >
                            <Trash2 size={14} />
                          </button>
                        )}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      </main>

      <CreateDeviceModal
        open={createDeviceOpen}
        onClose={() => setCreateDeviceOpen(false)}
        onCreated={() => {
          setCreateDeviceOpen(false);
          void refresh();
          notifyDevicesChanged();
        }}
      />

      {editTarget && (
        <EditDeviceModal
          device={editTarget}
          onClose={() => setEditTarget(null)}
          onSaved={() => {
            setEditTarget(null);
            void refresh();
            notifyDevicesChanged();
          }}
        />
      )}

      {rolesEditTarget && (
        <HostRolesEditorModal
          device={rolesEditTarget}
          onClose={() => setRolesEditTarget(null)}
          onSaved={() => {
            setRolesEditTarget(null);
            void refresh();
            notifyDevicesChanged();
          }}
        />
      )}

      <InstallEdgeModal
        open={!!installTarget}
        device={installTarget}
        edgeOnline={installTarget ? !!edgeOnlinePerHost[installTarget.id] : false}
        onClose={() => setInstallTarget(null)}
        onStarted={() => {
          // 一键安装启动后日志入口已迁到监控设备页面（Edges.tsx），
          // 这里不再开 InstallLogPanel；操作员从 /edges 找对应 edge 的
          // 「日志」按钮查看。
          void refresh();
        }}
      />

      <ConfirmDeleteModal
        open={!!deleteTarget}
        device={deleteTarget}
        deleting={deleting}
        onClose={() => {
          if (!deleting) setDeleteTarget(null);
        }}
        onConfirm={() => {
          if (deleteTarget) void onDeleteHost(deleteTarget);
        }}
      />
    </>
  );
}

// ----- HostRoleChips -----
// 与 Edges.tsx 的 RoleChips 同语义（按 host 视角），私有复制一份避免
// 跨文件 imports 后 Hosts.tsx 也能独立编译。
function HostRoleChips({ roles }: { roles: HostEdgeRole[] }) {
  const { tr } = useI18n();
  if (roles.length === 0) {
    return (
      <span className="inline-flex items-center gap-1 rounded border border-dashed border-zinc-600 px-1.5 py-0.5 text-[11px] text-zinc-400 hover:border-accent hover:text-accent">
        <Plus size={11} />
        {tr('分配角色', 'Assign roles')}
      </span>
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      {roles.map((r) => (
        <span
          key={r}
          className={cn(
            'inline-flex items-center rounded border px-1.5 py-0.5 text-[11px]',
            HOST_ROLE_CHIP_CLASS[r] ?? 'border-zinc-700 bg-zinc-800 text-zinc-300',
          )}
        >
          {tr(EDGE_ROLE_LABELS[r] ?? String(r), EDGE_ROLE_LABELS_EN[r] ?? String(r))}
        </span>
      ))}
      <span
        className="inline-flex items-center rounded border border-dashed border-zinc-700 px-1 py-0.5 text-[11px] text-zinc-500 hover:border-accent hover:text-accent"
        aria-label={tr('编辑角色', 'Edit roles')}
      >
        <Plus size={10} />
      </span>
    </span>
  );
}

const HOST_ROLE_CHIP_CLASS: Record<HostEdgeRole, string> = {
  server: 'border-sky-500/30    bg-sky-500/10    text-sky-300',
  storage: 'border-violet-500/30 bg-violet-500/10 text-violet-300',
  network: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300',
  database: 'border-amber-500/30  bg-amber-500/10  text-amber-300',
};

// ----- HostRolesEditorModal -----
// 复刻 Edges.tsx 的 RolesEditorModal — 我们写的是 device 角色，但因为
// backend 已经在 /devices/{id}/roles 接受 roles 数组（device/edge split
// 后，host 角色就住在这里），直接复用 setEdgeRoles 的 wire path 是合
// 法的；不过我们用本地 wrapper 保持单一入口便于以后拆分。
function HostRolesEditorModal({
  device,
  onClose,
  onSaved,
}: {
  device: HostDevice;
  onClose(): void;
  onSaved(): void;
}) {
  const { tr } = useI18n();
  const [selected, setSelected] = useState<Set<HostEdgeRole>>(
    new Set((device.roles ?? []) as HostEdgeRole[]),
  );
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const toggle = (r: HostEdgeRole) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(r)) next.delete(r);
      else next.add(r);
      return next;
    });
  };

  const submit = async () => {
    setSubmitting(true);
    setErr(null);
    try {
      const out = (EDGE_ROLES as readonly HostEdgeRole[]).filter((r) =>
        selected.has(r),
      );
      await updateDeviceRolesLocal(device.id, out as string[]);
      onSaved();
    } catch (e) {
      setErr((e as Error).message || tr('保存失败', 'Save failed'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={tr(`分配角色 · ${device.name || `#${device.id}`}`, `Assign roles · ${device.name || `#${device.id}`}`)}
      size="sm"
      footer={
        <>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-zinc-700 bg-zinc-900 px-3 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
          >
            {tr('取消', 'Cancel')}
          </button>
          <button
            type="button"
            onClick={submit}
            disabled={submitting}
            className="rounded-md bg-zinc-100 px-3 py-1.5 text-xs font-medium text-zinc-900 hover:bg-white disabled:opacity-50"
          >
            {submitting ? tr('保存中…', 'Saving…') : tr('保存', 'Save')}
          </button>
        </>
      }
    >
      <div className="space-y-2">
        <p className="text-xs text-zinc-500">
          {tr(
            '一台主机可同时承担多个角色（例：超融合一体机 = 服务器 + 存储）。不勾选 = 未分类。',
            'A host can hold multiple roles (e.g. a hyper-converged box = server + storage). Leave empty for uncategorized.',
          )}
        </p>
        <div className="space-y-1">
          {(EDGE_ROLES as readonly HostEdgeRole[]).map((r) => (
            <label
              key={r}
              className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm text-zinc-200 hover:bg-zinc-800/60"
            >
              <input
                type="checkbox"
                checked={selected.has(r)}
                onChange={() => toggle(r)}
                className="h-3.5 w-3.5 accent-zinc-300"
              />
              <span
                className={cn(
                  'inline-flex items-center rounded border px-1.5 py-0.5 text-[11px]',
                  HOST_ROLE_CHIP_CLASS[r],
                )}
              >
                {tr(EDGE_ROLE_LABELS[r], EDGE_ROLE_LABELS_EN[r])}
              </span>
            </label>
          ))}
        </div>
        {err && <div className="text-xs text-red-400">{err}</div>}
      </div>
    </Modal>
  );
}

// ----- ShellButton (host 视角) -----
// Host 页不需要 Edge 列表的旋转 / 删除；只要一个面向 host.id 的终端
// 入口。语义与 HostDetail 头部按钮一致：走 device → online edge →
// device 本地 sshd 的 tunnel 通道（不走 manager → 设备 IP 的直连
// SSH）。路径走 /shell，后端 devicessh.ShellHandler.handleTunnel
// 会迫使 Router.Pick 走 tunnel 分支，并由 device 表里已存的
// ssh_user / ssh_password（或 ssh_key）自动登录，不需要前端再弹
// ConnectModal 收 OS 凭据。判断能否进入只看账号权限和 device.id
// 是否注入；不依赖 edge agent 上报的 device.online —— tunnel
// dialer 找不到 online edge 时由后端返回 ErrTunnelNotImplemented
// （503），前端 ConnectModal 兜底让用户改走 direct 输入凭据。
function ShellButton({ device, canMutate }: { device: HostDevice; canMutate: boolean }) {
  const { tr } = useI18n();
  const noId = !device?.id;
  const disabled = !canMutate || noId;
  const reason = !canMutate
    ? tr('只读账号不能进入终端', 'Viewer accounts cannot open the terminal')
    : noId
      ? tr('设备 ID 缺失', 'Device id missing')
      : '';
  const href = `/hosts/${encodeURIComponent(String(device.id))}/shell`;
  if (disabled) {
    return (
      <span
        title={reason}
        aria-label={reason}
        className="mr-1 inline-flex cursor-not-allowed items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-600"
      >
        <TerminalSquare size={14} />
        <span>{tr('终端', 'Terminal')}</span>
      </span>
    );
  }
  return (
    <a
      href={href}
      title={tr(`打开 ${device.name || device.hostname || `host #${device.id}`} 终端`, `Open terminal`)}
      aria-label={tr('终端', 'Terminal')}
      className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
    >
      <TerminalSquare size={14} />
      <span>{tr('终端', 'Terminal')}</span>
    </a>
  );
}

// ----- InstallButton (host 视角) -----
// 一键安装按钮：edge 在线时禁用（已经有了），否则打开 InstallEdgeModal。
function InstallButton({
  device,
  edgeOnline,
  canMutate,
  onClick,
}: {
  device: HostDevice;
  edgeOnline: boolean;
  canMutate: boolean;
  onClick(): void;
}) {
  const { tr } = useI18n();
  const disabled = !canMutate || edgeOnline;
  const reason = !canMutate
    ? tr('只读账号不能安装', 'Viewer accounts cannot install')
    : edgeOnline
      ? tr('设备已有 edge', 'Device already has an edge')
      : '';
  if (disabled) {
    return (
      <span
        title={reason}
        aria-label={reason}
        className="mr-1 inline-flex cursor-not-allowed items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-600"
      >
        <Power size={14} />
        <span>{tr('一键安装', 'Install')}</span>
      </span>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      title={tr(`一键安装 edge 到 ${device.name || device.hostname || `host #${device.id}`}`, `Install edge on ${device.name || `host #${device.id}`}`)}
      className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
    >
      <Power size={14} />
      <span>{tr('一键安装', 'Install')}</span>
    </button>
  );
}

// ----- FilesButton -----
// SFTP 文件入口：路由到 /devices/:id/files。
function FilesButton({
  deviceId,
  disabled,
  onClick,
}: {
  deviceId: number;
  disabled?: boolean;
  onClick(): void;
}) {
  const { tr } = useI18n();
  const reason = disabled
    ? tr('已删除的设备不可访问', 'Deleted device cannot be browsed')
    : '';
  if (disabled) {
    return (
      <span
        title={reason}
        aria-label={reason}
        className="mr-1 inline-flex cursor-not-allowed items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-600"
      >
        <Folder size={14} />
        <span>{tr('文件', 'Files')}</span>
      </span>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      title={tr(`打开文件浏览器 (#${deviceId})`, `Open file browser (#${deviceId})`)}
      className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
    >
      <Folder size={14} />
      <span>{tr('文件', 'Files')}</span>
    </button>
  );
}

// re-export nothing — HostsPage is the default export above.