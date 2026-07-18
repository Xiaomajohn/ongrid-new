// HostDetail.tsx — 实体设备详情页（3 Tabs: basic / topology / meta）。
// 拓扑 tab 复用 DeviceTopology —— 见 components/DeviceTopology.tsx。
//
// 「指标」与「探针」两个 tab 已迁移到独立监控设备详情页
// /hosts/:hostId/monitor（@/pages/MonitorDeviceDetail），那是 host 视角
// 下不存在、专门承载「监控维度」内容的独立路由。HostDetail 页头部加
// 「监控设备」按钮把用户切到该路由。
//
// EdgeDetail.tsx 同样复用 DeviceTopology（见其 T-EdgeTopologyTab-3），
// 与主机详情保持展示口径一致。
import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ChevronLeft, ExternalLink, TerminalSquare, Activity } from 'lucide-react';
import { StatusPill } from '@/components/StatusPill';
import { cn } from '@/lib/cn';
import { relativeTime } from '@/lib/format';
import { getDevice, type Device } from '@/api/devices';
import { EDGE_ROLE_LABELS, EDGE_ROLE_LABELS_EN } from '@/api/edges';
import { DeviceTopology } from '@/components/DeviceTopology';
import { useI18n } from '@/i18n/locale';
import { usePermissions } from '@/store/me';

// HostDevice — Devices API 当前最小集 + 该页面用到的所有字段（hostname /
// ip_address / os / cpu_count / fingerprint 等）。 由其他 agent 落地后端
// 模型，这里 cast 即可。
type HostDevice = Device & {
  ip_address?: string;
  hostname?: string;
  description?: string;
  os?: string;
  os_version?: string;
  arch?: string;
  kernel?: string;
  cpu_count?: number;
  mem_total_bytes?: number;
  disk_total_bytes?: number;
  fingerprint?: string;
  created_by?: string;
  updated_by?: string;
};

type Tab = 'basic' | 'topology' | 'meta';

export default function HostDetailPage() {
  const { tr } = useI18n();
  const navigate = useNavigate();
  const { canMutate } = usePermissions();
  const { hostId = '' } = useParams<{ hostId: string }>();

  const [device, setDevice] = useState<HostDevice | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  const [tab, setTab] = useState<Tab>('basic');

  // Fetch host once on mount / hostId change.
  useEffect(() => {
    if (!hostId) return;
    let cancelled = false;
    getDevice(hostId)
      .then((d) => {
        if (!cancelled) setDevice(d as HostDevice);
      })
      .catch((err) => {
        if (!cancelled)
          setLoadErr(
            (err as Error).message || tr('加载失败', 'Load failed'),
          );
      });
    return () => {
      cancelled = true;
    };
  }, [hostId]);

  // 设备页终端走 tunnel 通道：manager → device 关联的 online edge →
  // edge 本地 127.0.0.1:22 的 sshd。路径拼为 /shell，后端
  // devicessh.ShellHandler.handleTunnel 会迫使 Router.Pick 走
  // tunnel 分支，并由 device 表里已存的 ssh_user / ssh_password
  // （或 ssh_key）自动登录 —— 前端不需要弹 ConnectModal 收 OS 凭据。
  // tunnel 路径找不到 online edge 时后端返回 ErrTunnelNotImplemented
  // （503），前端 ConnectModal 兑底让用户改走 direct 输入凭据。
  const terminalHref = device?.id
    ? `/hosts/${encodeURIComponent(String(device.id))}/shell`
    : '#';
  // 只读账号不允许进入终端，与 Hosts.tsx 列表页 ShellButton 规则一致。
  const terminalEnabled = !!device?.id && canMutate;

  // 监控设备详情路由：同 hostId 下挂在 /monitor 子路径上，承载 host 上
  // 已装探针列表 + 设备指标。该路由由 MonitorDeviceDetail 渲染。
  const monitorHref = device?.id
    ? `/hosts/${encodeURIComponent(String(device.id))}/monitor`
    : '#';

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <header className="app-header flex items-center justify-between border-b border-zinc-800 px-6 py-4">
        <div className="flex min-w-0 items-center gap-3">
          <button
            type="button"
            onClick={() => navigate('/hosts')}
            aria-label={tr('返回主机列表', 'Back to host list')}
            className="rounded-md p-1.5 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
          >
            <ChevronLeft size={16} />
          </button>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <h1 className="truncate text-base font-semibold text-zinc-100">
                {device?.name || hostId}
              </h1>
              {device && (
                <StatusPill
                  status={device.reachable ? 'online' : 'offline'}
                />
              )}
              {/* 状态按 reachable 渲染：ping 服务 5min 周期写入的网络层
                  可达性，即 operator 视角"机器在线"。edge agent 推送
                  的 online 仍在 device 对象里，仅供内部诊断；UI 统一
                  走 reachable，与 Hosts.tsx 列表页一致。 */}
              {device && (device.roles ?? []).length > 0 && (
                <span className="inline-flex flex-wrap items-center gap-1">
                  {(device.roles ?? []).map((r) => (
                    <span
                      key={r}
                      className="rounded border border-zinc-700 bg-zinc-800 px-1.5 py-0.5 text-[11px] text-zinc-300"
                    >
                      {tr(
                        EDGE_ROLE_LABELS[
                          r as keyof typeof EDGE_ROLE_LABELS
                        ] ?? String(r),
                        EDGE_ROLE_LABELS_EN[
                          r as keyof typeof EDGE_ROLE_LABELS_EN
                        ] ?? String(r),
                      )}
                    </span>
                  ))}
                </span>
              )}
            </div>
            <div className="mt-0.5 truncate text-[11px] text-zinc-500">
              {device?.last_seen_at
                ? tr(
                    `最后心跳 ${relativeTime(device.last_seen_at)}`,
                    `Last seen ${relativeTime(device.last_seen_at)}`,
                  )
                : tr('设备 ID: ', 'Device ID: ') + hostId}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {/* 「监控设备」按钮把用户从主机视角切到监控设备视角：
              指标 / 已装探针 / 元数据 这块内容在 /hosts/:hostId/monitor。
              复用 indigo 主色使其与 HostDetail 头部灰底按钮（终端）
              形成视觉层级。 */}
          {device?.id ? (
            <Link
              to={monitorHref}
              title={tr(
                '查看该主机的设备指标与已装探针',
                'View this host\'s metrics and installed probes',
              )}
              className="inline-flex items-center gap-1.5 rounded-md bg-indigo-600 px-2.5 py-1.5 text-xs font-medium text-white hover:bg-indigo-500"
            >
              <Activity size={12} /> {tr('监控设备', 'Monitor')}
            </Link>
          ) : (
            <span
              title={tr('加载中…', 'Loading…')}
              className="inline-flex cursor-not-allowed items-center gap-1.5 rounded-md bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-600"
            >
              <Activity size={12} /> {tr('监控设备', 'Monitor')}
            </span>
          )}
          {terminalEnabled ? (
            <a
              href={terminalHref}
              target="_blank"
              rel="noopener noreferrer"
              title={tr('打开终端 (新标签页)', 'Open terminal (new tab)')}
              className="inline-flex items-center gap-1.5 rounded-md bg-zinc-100 px-2.5 py-1.5 text-xs font-medium text-zinc-900 hover:bg-white"
            >
              <TerminalSquare size={12} /> {tr('终端', 'Terminal')}
            </a>
          ) : (
            <span
              title={tr('设备未上线或只读账号', 'Device offline or read-only')}
              className="inline-flex cursor-not-allowed items-center gap-1.5 rounded-md bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-600"
            >
              <TerminalSquare size={12} /> {tr('终端', 'Terminal')}
            </span>
          )}
        </div>
      </header>

      <div className="flex items-center gap-1 border-b border-zinc-800 px-6">
        <TabBtn
          active={tab === 'basic'}
          onClick={() => setTab('basic')}
          label={tr('基本信息', 'Basic')}
        />
        <TabBtn
          active={tab === 'topology'}
          onClick={() => setTab('topology')}
          label={tr('拓扑', 'Topology')}
        />
        <TabBtn
          active={tab === 'meta'}
          onClick={() => setTab('meta')}
          label={tr('元数据', 'Metadata')}
        />
      </div>

      <div className="flex-1 overflow-y-auto px-6 py-5">
        {loadErr && (
          <div
            role="alert"
            className="mb-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300"
          >
            {loadErr}
          </div>
        )}

        {tab === 'basic' && <BasicTab device={device} />}

        {tab === 'topology' && device && (
          <DeviceTopology deviceId={device.id} />
        )}

        {tab === 'meta' && device && (
          <JsonCard
            title={tr('元数据', 'Metadata')}
            data={buildMeta(device)}
            empty={tr('加载中…', 'Loading…')}
          />
        )}
      </div>
    </main>
  );
}

// ----- BasicTab -----
// 网格 2 列展示 host 关键字段。hostname / os / arch / kernel / ip / cpu /
// mem / disk / fingerprint / created / last_seen / node_id。
function BasicTab({ device }: { device: HostDevice | null }) {
  const { tr } = useI18n();
  if (!device) {
    return (
      <div className="rounded-lg border border-dashed border-zinc-800 bg-zinc-900/30 px-4 py-6 text-center text-xs text-zinc-500">
        {tr('加载中…', 'Loading…')}
      </div>
    );
  }
  const rows: Array<[string, string]> = [
    [tr('主机名', 'Hostname'), device.hostname || '—'],
    [
      tr('操作系统', 'OS'),
      [device.os, device.os_version].filter(Boolean).join(' ') || '—',
    ],
    [tr('架构', 'Arch'), device.arch || '—'],
    [tr('内核', 'Kernel'), device.kernel || '—'],
    [tr('IP 地址', 'IP address'), device.ip_address || '—'],
    [
      tr('CPU 核数', 'CPU cores'),
      typeof device.cpu_count === 'number'
        ? String(device.cpu_count)
        : '—',
    ],
    [
      tr('内存', 'Memory'),
      typeof device.mem_total_bytes === 'number'
        ? formatBytes(device.mem_total_bytes)
        : '—',
    ],
    [
      tr('磁盘', 'Disk'),
      typeof device.disk_total_bytes === 'number'
        ? formatBytes(device.disk_total_bytes)
        : '—',
    ],
    [
      tr('Fingerprint', 'Fingerprint'),
      device.fingerprint ? `${device.fingerprint.slice(0, 8)}…` : '—',
    ],
    [
      tr('创建时间', 'Created at'),
      device.created_at ? new Date(device.created_at).toLocaleString() : '—',
    ],
    [
      tr('最后心跳', 'Last seen'),
      device.last_seen_at ? relativeTime(device.last_seen_at) : '—',
    ],
  ];
  return (
    <div className="grid gap-3 md:grid-cols-2">
      {rows.map(([label, value]) => (
        <div
          key={label}
          className="rounded-lg border border-zinc-800/60 bg-zinc-900/40 px-4 py-3"
        >
          <div className="text-[11px] uppercase tracking-wider text-zinc-500">
            {label}
          </div>
          <div className="mt-1 break-all font-mono text-xs text-zinc-100">
            {value}
          </div>
        </div>
      ))}
      {device.node_id != null && (
        <div className="rounded-lg border border-zinc-800/60 bg-zinc-900/40 px-4 py-3">
          <div className="text-[11px] uppercase tracking-wider text-zinc-500">
            Node ID
          </div>
          <div className="mt-1">
            <Link
              to="/topology"
              className="inline-flex items-center gap-1 rounded border border-zinc-700 bg-zinc-900 px-2 py-0.5 font-mono text-xs text-zinc-300 hover:bg-zinc-800"
            >
              #{device.node_id} <ExternalLink size={10} />
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}

function TabBtn({
  active,
  onClick,
  label,
}: {
  active: boolean;
  onClick(): void;
  label: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        'border-b-2 px-3 py-2.5 text-sm transition-colors',
        active
          ? 'border-zinc-100 text-zinc-100'
          : 'border-transparent text-zinc-400 hover:text-zinc-200',
      )}
    >
      {label}
    </button>
  );
}

function JsonCard({
  title,
  data,
  empty,
}: {
  title: string;
  data: Record<string, unknown> | null;
  empty: string;
}) {
  return (
    <div className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
      <div className="mb-2 text-sm font-medium text-zinc-200">{title}</div>
      {data && Object.keys(data).length > 0 ? (
        <pre className="overflow-x-auto rounded-lg bg-zinc-950/60 p-3 text-xs leading-5 text-zinc-300">
          {JSON.stringify(data, null, 2)}
        </pre>
      ) : (
        <div className="rounded-lg border border-dashed border-zinc-800 bg-zinc-950/40 px-3 py-6 text-center text-xs text-zinc-500">
          {empty}
        </div>
      )}
    </div>
  );
}

function buildMeta(d: HostDevice): Record<string, unknown> {
  return {
    id: d.id,
    name: d.name,
    scope: d.scope,
    hostname: d.hostname,
    description: d.description,
    roles: d.roles,
    online: d.online,
    last_seen_at: d.last_seen_at,
    created_at: d.created_at,
    updated_at: d.updated_at,
    created_by: d.created_by,
    updated_by: d.updated_by,
    ip_address: d.ip_address,
    os: d.os,
    os_version: d.os_version,
    arch: d.arch,
    kernel: d.kernel,
    cpu_count: d.cpu_count,
    mem_total_bytes: d.mem_total_bytes,
    disk_total_bytes: d.disk_total_bytes,
    fingerprint: d.fingerprint,
    node_id: d.node_id,
  };
}

function formatBytes(b: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = Math.abs(b);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[i]}`;
}
