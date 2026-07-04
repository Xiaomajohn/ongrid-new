// HostDetail.tsx — 实体设备详情页（5 Tabs: basic / metrics / probes / topology / meta）。
// 独立可用：MultiLinePanel / matrixToPanel / formatters 全部从 EdgeDetail.tsx 私有复制。
import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  Legend,
} from 'recharts';
import { ChevronLeft, Cpu, HardDrive, ArrowDownRight, ArrowUpRight, ExternalLink, TerminalSquare, Plus, Trash2, RotateCw, Loader2 } from 'lucide-react';
import { StatusPill } from '@/components/StatusPill';
import { cn } from '@/lib/cn';
import { relativeTime } from '@/lib/format';
import { usePoll } from '@/lib/usePoll';
import {
  getDevice,
  type Device,
  type DeviceRole as HostEdgeRole,
} from '@/api/devices';
import {
  listEdges,
  deleteEdge,
  rotateSecret,
  upgradeEdgePackage,
  EDGE_ROLE_LABELS,
  EDGE_ROLE_LABELS_EN,
  type Edge,
  type RotateSecretResponse,
} from '@/api/edges';
import { promQueryRange, type PromMatrixSeries } from '@/api/edges';
import { openMetricDrilldown } from '@/lib/drilldown';
import { NodeNeighbors } from '@/components/topology/NodeNeighbors';
import { useI18n } from '@/i18n/locale';
import { request } from '@/api/client';
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

type Tab = 'basic' | 'metrics' | 'probes' | 'topology' | 'meta';

const RANGE_MS = 6 * 60 * 60 * 1000;
const STEP = '1m';
const REFRESH_MS = 30_000;

const SERIES_COLORS = [
  '#60a5fa',
  '#34d399',
  '#f59e0b',
  '#a78bfa',
  '#f87171',
  '#22d3ee',
  '#fb7185',
  '#facc15',
];

// 指标 Tab 的 panel 元数据表：key 决定了 PromQL / panel 状态 / 隐藏集合。
// 让 4 个 MultiLinePanel 走 .map() 拼，避免在 JSX 里复制 4 遍。
type PanelMeta = {
  key: PanelKey;
  title: string;
  titleEn: string;
  subtitle: string;
  subtitleEn: string;
  icon: typeof Cpu;
  isPercent: boolean;
};
const PANEL_META: PanelMeta[] = [
  {
    key: 'cpu',
    title: 'CPU 利用率（按核）',
    titleEn: 'CPU utilization (per core)',
    subtitle: '最近 6 小时 · 1m 粒度 · 每条线 = 一个 cpu',
    subtitleEn: 'Last 6h · 1m step · one line per CPU',
    icon: Cpu,
    isPercent: true,
  },
  {
    key: 'disk',
    title: '磁盘使用率（按挂载点）',
    titleEn: 'Disk usage (per mountpoint)',
    subtitle: '最近 6 小时 · 1m 粒度 · 每条线 = 一个 mountpoint',
    subtitleEn: 'Last 6h · 1m step · one line per mountpoint',
    icon: HardDrive,
    isPercent: true,
  },
  {
    key: 'netRx',
    title: '网络入向（按设备）',
    titleEn: 'Network RX (per device)',
    subtitle: '最近 6 小时 · 1m 粒度 · 每条线 = 一个 device · bytes/s',
    subtitleEn: 'Last 6h · 1m step · one line per device · bytes/s',
    icon: ArrowDownRight,
    isPercent: false,
  },
  {
    key: 'netTx',
    title: '网络出向（按设备）',
    titleEn: 'Network TX (per device)',
    subtitle: '最近 6 小时 · 1m 粒度 · 每条线 = 一个 device · bytes/s',
    subtitleEn: 'Last 6h · 1m step · one line per device · bytes/s',
    icon: ArrowUpRight,
    isPercent: false,
  },
];

// 按 panel key 生成对应的 PromQL。与 refreshMetrics 里硬编码的 expr 保持
// 一致；drilldown 时复用同一台设备同一标签取值。
function panelExpr(key: PanelKey, deviceId: string): string {
  const sel = `device_id="${deviceId}"`;
  switch (key) {
    case 'cpu':
      return `100 * (1 - rate(node_cpu_seconds_total{${sel},mode="idle"}[5m]))`;
    case 'disk':
      return `100 * (1 - node_filesystem_avail_bytes{${sel},fstype=~"ext4|xfs|btrfs|zfs|ext3|ext2|f2fs",device=~"(/dev/)?(vd|sd|xvd)[a-z]+[0-9]*|(/dev/)?nvme[0-9]+n[0-9]+(p[0-9]+)?"} / node_filesystem_size_bytes{${sel},fstype=~"ext4|xfs|btrfs|zfs|ext3|ext2|f2fs",device=~"(/dev/)?(vd|sd|xvd)[a-z]+[0-9]*|(/dev/)?nvme[0-9]+n[0-9]+(p[0-9]+)?"})`;
    case 'netRx':
      return `rate(node_network_receive_bytes_total{${sel}}[5m])`;
    case 'netTx':
      return `rate(node_network_transmit_bytes_total{${sel}}[5m])`;
  }
}

type ChartRow = {
  ts: number;
  tsLabel: string;
} & Record<string, number | null | string>;

type SeriesDescriptor = {
  key: string;
  label: string;
  color: string;
};

type PanelData = {
  rows: ChartRow[];
  series: SeriesDescriptor[];
};

type PanelKey = 'cpu' | 'disk' | 'netRx' | 'netTx';

const EMPTY_PANEL: PanelData = { rows: [], series: [] };

// ----- local wrappers for non-existent API functions -----

async function listDeviceEdgesLocal(deviceId: string | number): Promise<Edge[]> {
  const r = await request<{ items?: Edge[]; total?: number }>(
    'GET',
    `/devices/${encodeURIComponent(String(deviceId))}/edges`,
  );
  return r.items ?? [];
}

export default function HostDetailPage() {
  const { tr } = useI18n();
  const navigate = useNavigate();
  const { canMutate } = usePermissions();
  const { hostId = '' } = useParams<{ hostId: string }>();

  const [device, setDevice] = useState<HostDevice | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  const [panels, setPanels] = useState<Record<PanelKey, PanelData>>({
    cpu: EMPTY_PANEL,
    disk: EMPTY_PANEL,
    netRx: EMPTY_PANEL,
    netTx: EMPTY_PANEL,
  });
  const [metricsErr, setMetricsErr] = useState<string | null>(null);
  const [promErr, setPromErr] = useState<string | null>(null);
  const [hidden, setHidden] = useState<Record<PanelKey, Set<string>>>({
    cpu: new Set(),
    disk: new Set(),
    netRx: new Set(),
    netTx: new Set(),
  });

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
          setLoadErr((err as Error).message || tr('加载失败', 'Load failed'));
      });
    return () => {
      cancelled = true;
    };
  }, [hostId]);

  const refreshMetrics = useCallback(async () => {
    if (!device) return;
    const deviceId = device.id;
    const to = new Date();
    const from = new Date(to.getTime() - RANGE_MS);
    const fromIso = from.toISOString();
    const toIso = to.toISOString();
    const labelSel = `device_id="${deviceId}"`;
    const labelFor: Record<PanelKey, string> = {
      cpu: 'cpu',
      disk: 'device',
      netRx: 'device',
      netTx: 'device',
    };
    const keys = Object.keys(labelFor) as PanelKey[];

    try {
      const results = await Promise.all(
        keys.map(async (k) => {
          const resp = await promQueryRange({
            expr: panelExpr(k, String(deviceId)),
            from: fromIso,
            to: toIso,
            step: STEP,
          });
          return [k, matrixToPanel(resp.matrix ?? [], labelFor[k], k)] as const;
        }),
      );
      const next = { ...panels };
      for (const [k, panel] of results) next[k] = panel;
      setPanels(next);
      setMetricsErr(null);
    } catch (err) {
      setMetricsErr((err as Error).message || tr('加载指标失败', 'Failed to load metrics'));
    }
  }, [device]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!device?.id) return;
    void refreshMetrics();
  }, [device?.id, refreshMetrics]);
  usePoll(refreshMetrics, REFRESH_MS, !!device?.id);

  const openDrilldown = useCallback(
    async (expr: string, title: string) => {
      try {
        await openMetricDrilldown({
          expr,
          rangeInput: '6h',
          stepInput: '1m',
          title,
          deviceId: device?.id,
        });
        setPromErr(null);
      } catch (err) {
        setPromErr((err as Error).message || tr('打开图表失败', 'Failed to open chart'));
      }
    },
    [device?.id],
  );

  const toggleSeries = (panel: PanelKey, key: string) => {
    setHidden((prev) => {
      const nextSet = new Set(prev[panel]);
      if (nextSet.has(key)) nextSet.delete(key);
      else nextSet.add(key);
      return { ...prev, [panel]: nextSet };
    });
  };

  const terminalHref = device?.id
    ? `/hosts/${encodeURIComponent(String(device.id))}/shell`
    : '#';
  const terminalEnabled = !!device?.online && canMutate;

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
              {device && <StatusPill status={device.online ? 'online' : 'offline'} />}
              {device && (device.roles ?? []).length > 0 && (
                <span className="inline-flex flex-wrap items-center gap-1">
                  {(device.roles ?? []).map((r) => (
                    <span
                      key={r}
                      className="rounded border border-zinc-700 bg-zinc-800 px-1.5 py-0.5 text-[11px] text-zinc-300"
                    >
                      {tr(
                        EDGE_ROLE_LABELS[r as keyof typeof EDGE_ROLE_LABELS] ?? String(r),
                        EDGE_ROLE_LABELS_EN[r as keyof typeof EDGE_ROLE_LABELS_EN] ?? String(r),
                      )}
                    </span>
                  ))}
                </span>
              )}
            </div>
            <div className="mt-0.5 truncate text-[11px] text-zinc-500">
              {device?.last_seen_at
                ? tr(`最后心跳 ${relativeTime(device.last_seen_at)}`, `Last seen ${relativeTime(device.last_seen_at)}`)
                : tr('设备 ID: ', 'Device ID: ') + hostId}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
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
        <TabBtn active={tab === 'basic'} onClick={() => setTab('basic')} label={tr('基本信息', 'Basic')} />
        <TabBtn active={tab === 'metrics'} onClick={() => setTab('metrics')} label={tr('指标', 'Metrics')} />
        <TabBtn active={tab === 'probes'} onClick={() => setTab('probes')} label={tr('探针', 'Probes')} />
        <TabBtn active={tab === 'topology'} onClick={() => setTab('topology')} label={tr('拓扑', 'Topology')} />
        <TabBtn active={tab === 'meta'} onClick={() => setTab('meta')} label={tr('元数据', 'Metadata')} />
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

        {tab === 'basic' && (
          <BasicTab device={device} />
        )}

        {tab === 'metrics' && (
          <div className="space-y-4">
            {metricsErr && (
              <div className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">
                {metricsErr}
              </div>
            )}
            {promErr && (
              <div className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">
                {promErr}
              </div>
            )}

            {PANEL_META.map((p) => (
              <MultiLinePanel
                key={p.key}
                title={tr(p.title, p.titleEn)}
                subtitle={tr(p.subtitle, p.subtitleEn)}
                icon={p.icon}
                panel={panels[p.key]}
                hidden={hidden[p.key]}
                onToggle={(k) => toggleSeries(p.key, k)}
                formatValue={p.isPercent ? (v) => `${v.toFixed(1)}%` : formatBytesPerSec}
                yDomain={p.isPercent ? [0, 100] : undefined}
                onOpenDrilldown={
                  device?.id
                    ? () => void openDrilldown(panelExpr(p.key, String(device.id)), p.titleEn)
                    : undefined
                }
              />
            ))}
          </div>
        )}

        {tab === 'probes' && device && (
          <ProbesTab device={device} onAfterChange={() => void getDevice(hostId).then((d) => setDevice(d as HostDevice))} canMutate={canMutate} />
        )}

        {tab === 'topology' && (
          <div className="space-y-3">
            {!device?.node_id ? (
              <div className="rounded-lg border border-dashed border-zinc-800 bg-zinc-900/30 px-4 py-6 text-center text-xs text-zinc-500">
                {tr(
                  '该主机尚未链到拓扑节点（node_id 为空）— 等下次后台 migrate 补齐',
                  'This host is not yet linked to a topology node (node_id is null) — the next backend migrate will backfill it',
                )}
              </div>
            ) : (
              <>
                <div className="text-xs text-zinc-500">
                  {tr(
                    '本主机所在的业务拓扑邻居。在 /topology 加成员 / 依赖关系后会出现在下方。',
                    'Business topology neighbours of this host. Wire member_of / depends_on edges in /topology and they will appear below.',
                  )}
                </div>
                <NodeNeighbors nodeID={device.node_id} />
              </>
            )}
          </div>
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
      typeof device.cpu_count === 'number' ? String(device.cpu_count) : '—',
    ],
    [
      tr('内存', 'Memory'),
      typeof device.mem_total_bytes === 'number' ? formatBytes(device.mem_total_bytes) : '—',
    ],
    [
      tr('磁盘', 'Disk'),
      typeof device.disk_total_bytes === 'number' ? formatBytes(device.disk_total_bytes) : '—',
    ],
    [
      tr('Fingerprint', 'Fingerprint'),
      device.fingerprint
        ? `${device.fingerprint.slice(0, 8)}…`
        : '—',
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
          <div className="mt-1 break-all font-mono text-xs text-zinc-100">{value}</div>
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

// ----- ProbesTab -----
// 列出该 device 下所有 edges（调 listEdges({device_id})），表格复用
// Edge.tsx 的列顺序：ID / 名称 / 状态 / 最后心跳 / Access Key / Agent / 操作。
function ProbesTab({
  device,
  onAfterChange,
  canMutate,
}: {
  device: HostDevice;
  onAfterChange(): void;
  canMutate: boolean;
}) {
  const { tr } = useI18n();
  const navigate = useNavigate();
  const [edges, setEdges] = useState<Edge[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!device?.id) return;
    setLoading(true);
    setErr(null);
    try {
      // 优先走专用端点；不命中时退化到 listEdges() 客户端过滤。
      try {
        const items = await listDeviceEdgesLocal(device.id);
        setEdges(items);
      } catch {
        const r = await listEdges();
        const filtered = (r.items ?? []).filter(
          (e) => String(e.device_id ?? '') === String(device.id),
        );
        setEdges(filtered);
      }
    } catch (e) {
      setErr((e as Error).message || tr('加载失败', 'Load failed'));
    } finally {
      setLoading(false);
    }
  }, [device?.id]);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  usePoll(refresh, 10_000);

  async function onRotate(e: Edge) {
    if (!confirm(tr(`轮换 ${e.name} 密钥？旧密钥将立即失效。`, `Rotate ${e.name}'s secret? Old key stops immediately.`))) return;
    try {
      const r: RotateSecretResponse = await rotateSecret(e.id);
      alert(tr(`新 secret_key（仅显示一次）：\n${r.secret_key}`, `New secret_key (shown once):\n${r.secret_key}`));
      void refresh();
    } catch (err) {
      alert((err as Error).message || tr('轮换失败', 'Rotate failed'));
    }
  }

  async function onDelete(e: Edge) {
    if (!confirm(tr(`删除 ${e.name}？不可恢复。`, `Delete ${e.name}? Cannot be undone.`))) return;
    try {
      await deleteEdge(e.id);
      void refresh();
      onAfterChange();
    } catch (err) {
      alert((err as Error).message || tr('删除失败', 'Delete failed'));
    }
  }

  async function onPackageUpgrade(e: Edge) {
    if (!confirm(tr(`整包升级 ${e.name}？agent 短暂重启；失败自动回滚。`, `Upgrade ${e.name} package? Agent restarts briefly; auto-rollback on failure.`))) return;
    try {
      const resp = await upgradeEdgePackage(e.id);
      alert(resp.applied
        ? tr(`${e.name} → ${resp.version} 已 stage ${resp.manifest_files} 个文件`, `${e.name} → ${resp.version} staged ${resp.manifest_files} files`)
        : tr(`${e.name} stage OK 但 apply 失败：${resp.apply_error ?? '未知'}`, `${e.name} staged but apply failed: ${resp.apply_error ?? 'unknown'}`));
      void refresh();
    } catch (err) {
      alert((err as Error).message || tr('升级失败', 'Upgrade failed'));
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="text-sm text-zinc-300">
          {tr(
            `${edges.length} 个探针`,
            `${edges.length} probe${edges.length === 1 ? '' : 's'}`,
          )}
        </div>
        {canMutate && (
          <Link
            to="/devices"
            className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-200 hover:bg-zinc-800"
            title={tr(
              '去设备页签发新探针，安装到本主机后会自动绑定到此 device',
              'Issue a new probe from the devices page; once installed on this host it will bind here',
            )}
          >
            <Plus size={12} /> {tr('添加探针', 'Add probe')}
          </Link>
        )}
      </div>

      {err && (
        <div className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {err}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-zinc-800/60 bg-zinc-900/40">
        <table className="w-full text-sm">
          <thead className="border-b border-zinc-800/60 bg-zinc-950/40 text-[11px] uppercase tracking-wider text-zinc-500">
            <tr>
              <th className="px-4 py-2.5 text-left">ID</th>
              <th className="px-4 py-2.5 text-left">{tr('名称', 'Name')}</th>
              <th className="px-4 py-2.5 text-left">{tr('状态', 'Status')}</th>
              <th className="px-4 py-2.5 text-left">{tr('最后心跳', 'Last heartbeat')}</th>
              <th className="px-4 py-2.5 text-left">Access Key</th>
              <th className="px-4 py-2.5 text-left">Agent</th>
              <th className="px-4 py-2.5 text-right">{tr('操作', 'Actions')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-zinc-800/40">
            {loading && edges.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-4 py-10 text-center text-zinc-500">
                  <Loader2 size={14} className="mr-2 inline animate-spin" />
                  {tr('加载中…', 'Loading…')}
                </td>
              </tr>
            ) : edges.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-4 py-10 text-center text-zinc-500">
                  {tr(
                    '尚未安装探针',
                    'No probes installed yet',
                  )}
                </td>
              </tr>
            ) : (
              edges.map((e) => (
                <tr key={e.id} className="hover:bg-zinc-900/40">
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                    {e.id}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-zinc-100">
                    {e.name || (
                      <span className="italic text-zinc-500">
                        {tr('（待主机上线）', '(waiting for host)')}
                      </span>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5">
                    <StatusPill status={e.status} />
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-zinc-400">
                    {e.last_seen_at ? relativeTime(e.last_seen_at) : '—'}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                    <span className="rounded bg-zinc-800/60 px-1.5 py-0.5">
                      {e.access_key_id.slice(0, 8)}…
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                    {e.agent_version || <span className="text-zinc-600">—</span>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right">
                    <button
                      type="button"
                      onClick={() => navigate(`/devices/${encodeURIComponent(String(e.id))}`)}
                      title={tr('打开探针详情', 'Open probe detail')}
                      className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
                    >
                      <ExternalLink size={14} />
                      <span>{tr('详情', 'Detail')}</span>
                    </button>
                    {canMutate && (
                      <>
                        <button
                          type="button"
                          onClick={() => void onRotate(e)}
                          title={tr('轮换密钥', 'Rotate secret')}
                          className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
                        >
                          <RotateCw size={14} />
                        </button>
                        <button
                          type="button"
                          onClick={() => void onPackageUpgrade(e)}
                          title={tr('整包升级', 'Upgrade package')}
                          className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
                        >
                          {tr('整包', 'Pkg')}
                        </button>
                        <button
                          type="button"
                          onClick={() => void onDelete(e)}
                          title={tr('删除', 'Delete')}
                          aria-label={tr(`删除 ${e.name}`, `Delete ${e.name}`)}
                          className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-red-300 hover:bg-red-500/10"
                        >
                          <Trash2 size={14} />
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

// ----- matrixToPanel (从 EdgeDetail 私有复制) -----
function matrixToPanel(
  matrix: PromMatrixSeries[],
  nameLabel: string,
  panelKey: PanelKey,
): PanelData {
  if (!matrix || matrix.length === 0) return EMPTY_PANEL;

  const filtered = matrix.filter((s) => {
    if (panelKey !== 'disk') return true;
    const fstype = s.metric.fstype ?? '';
    if (!fstype) return true;
    return !['tmpfs', 'devtmpfs', 'overlay', 'squashfs', 'autofs'].includes(fstype);
  });

  const series: SeriesDescriptor[] = filtered
    .map((s, idx) => {
      const labelVal = s.metric[nameLabel] ?? `series ${idx}`;
      const key = `${panelKey}_${labelVal}`;
      return { labelVal, key, raw: s };
    })
    .sort((a, b) => a.labelVal.localeCompare(b.labelVal))
    .map((entry, idx) => ({
      key: entry.key,
      label: entry.labelVal,
      color: SERIES_COLORS[idx % SERIES_COLORS.length],
    }));

  const valuesByKey = new Map<string, Map<number, number>>();
  for (const s of filtered) {
    const labelVal = s.metric[nameLabel] ?? '';
    const key = `${panelKey}_${labelVal}`;
    const m = new Map<number, number>();
    for (const [tsSec, vStr] of s.values) {
      const v = parseFloat(vStr);
      if (Number.isFinite(v)) m.set(tsSec, v);
    }
    if (!valuesByKey.has(key)) valuesByKey.set(key, m);
  }

  const tsSet = new Set<number>();
  for (const m of valuesByKey.values()) for (const ts of m.keys()) tsSet.add(ts);
  const tsSorted = Array.from(tsSet).sort((a, b) => a - b);

  const rows: ChartRow[] = tsSorted.map((tsSec): ChartRow => {
    const row: ChartRow = {
      ts: tsSec,
      tsLabel: formatTimeLabel(tsSec * 1000),
    };
    for (const desc of series) {
      const m = valuesByKey.get(desc.key);
      const v = m?.get(tsSec);
      row[desc.key] = typeof v === 'number' ? v : null;
    }
    return row;
  });

  return { rows, series };
}

// ----- MultiLinePanel (从 EdgeDetail 私有复制) -----
function MultiLinePanel({
  title,
  subtitle,
  icon: Icon,
  panel,
  hidden,
  onToggle,
  formatValue,
  yDomain,
  onOpenDrilldown,
}: {
  title: string;
  subtitle: string;
  icon: typeof Cpu;
  panel: PanelData;
  hidden: Set<string>;
  onToggle(key: string): void;
  formatValue(v: number): string;
  yDomain?: [number, number];
  onOpenDrilldown?(): void;
}) {
  const { tr } = useI18n();
  const visibleSeries = panel.series.filter((s) => !hidden.has(s.key));

  return (
    <section className="rounded-xl border border-zinc-800 bg-zinc-900/40 p-4">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="flex items-start gap-2">
          <span className="mt-0.5 rounded-md border border-zinc-800 bg-zinc-950/60 p-1.5 text-zinc-300">
            <Icon size={14} />
          </span>
          <div>
            <h2 className="text-sm font-medium text-zinc-100">{title}</h2>
            <p className="mt-1 text-[11px] text-zinc-500">{subtitle}</p>
          </div>
        </div>
        {onOpenDrilldown && (
          <button
            type="button"
            onClick={onOpenDrilldown}
            className="inline-flex shrink-0 items-center gap-1 rounded-md border border-zinc-700 px-2 py-1 text-[11px] text-zinc-300 transition-colors hover:border-zinc-500 hover:bg-zinc-800 hover:text-zinc-100"
          >
            <ExternalLink size={12} />
            <span>{tr('查看图表', 'View chart')}</span>
          </button>
        )}
      </div>

      {panel.series.length > 0 && (
        <div className="mb-3 flex flex-wrap gap-x-3 gap-y-1.5">
          {panel.series.map((s) => {
            const isHidden = hidden.has(s.key);
            return (
              <button
                key={s.key}
                type="button"
                onClick={() => onToggle(s.key)}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-[11px] transition-colors',
                  isHidden
                    ? 'text-zinc-600 hover:bg-zinc-900 hover:text-zinc-400'
                    : 'text-zinc-300 hover:bg-zinc-800/60',
                )}
              >
                <span
                  className="h-2 w-3 rounded-sm"
                  style={{ backgroundColor: isHidden ? '#3f3f46' : s.color }}
                />
                <span className={cn('font-mono', isHidden && 'line-through decoration-zinc-600')}>
                  {s.label}
                </span>
              </button>
            );
          })}
        </div>
      )}

      <div className="h-60 w-full">
        {panel.rows.length === 0 || panel.series.length === 0 ? (
          <div className="flex h-full items-center justify-center text-xs text-zinc-500">
            {tr('无数据', 'No data')}
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={panel.rows} margin={{ top: 4, right: 8, bottom: 0, left: -8 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="#27272a" vertical={false} />
              <XAxis
                dataKey="tsLabel"
                stroke="#52525b"
                tick={{ fontSize: 10 }}
                interval="preserveStartEnd"
                minTickGap={36}
              />
              <YAxis
                stroke="#52525b"
                tick={{ fontSize: 10 }}
                width={56}
                domain={yDomain ?? ['auto', 'auto']}
                tickFormatter={(v) => formatValue(v as number)}
              />
              <Tooltip
                contentStyle={{
                  background: '#0a0a0aee',
                  border: '1px solid #27272a',
                  borderRadius: 8,
                  fontSize: 12,
                  color: '#e4e4e7',
                  padding: '8px 10px',
                }}
                labelStyle={{ color: '#a1a1aa', marginBottom: 4 }}
                itemStyle={{ padding: '1px 0' }}
                formatter={(value, name) => {
                  const desc = panel.series.find((s) => s.key === String(name));
                  return [formatValue(value as number), desc?.label ?? String(name)];
                }}
              />
              <Legend
                wrapperStyle={{ fontSize: 10, color: '#a1a1aa', paddingTop: 8 }}
                iconType="plainline"
                formatter={(value) => {
                  const desc = panel.series.find((s) => s.key === String(value));
                  return desc?.label ?? String(value);
                }}
              />
              {visibleSeries.map((s) => (
                <Line
                  key={s.key}
                  type="linear"
                  dataKey={s.key}
                  stroke={s.color}
                  strokeWidth={1.4}
                  dot={false}
                  connectNulls={false}
                  isAnimationActive={false}
                />
              ))}
            </LineChart>
          </ResponsiveContainer>
        )}
      </div>
    </section>
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

function formatTimeLabel(ms: number): string {
  const date = new Date(ms);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function formatBytesPerSec(v: number): string {
  if (!Number.isFinite(v)) return '—';
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  let n = Math.abs(v);
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(n < 10 ? 2 : 1)} ${units[i]}`;
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
