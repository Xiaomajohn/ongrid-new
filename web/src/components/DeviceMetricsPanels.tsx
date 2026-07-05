// DeviceMetricsPanels.tsx — the four 6h/1m CPU/Disk/Net panels, factored
// out of HostDetail.tsx so EdgeDetail.tsx (per T-EdgeMetricsTab-1) can
// reuse the exact same charts keyed off the linked device_id.
//
// PromQL labels key off `device_id="<id>"` — same pattern HostDetail
// already uses — so no matter which page renders this, the underlying
// queries are identical and the wire shape doesn't have to learn a new
// label dimension.

import { useCallback, useEffect, useState } from 'react';
import {
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { Cpu, HardDrive, ArrowDownRight, ArrowUpRight, ExternalLink } from 'lucide-react';
import { cn } from '@/lib/cn';
import { usePoll } from '@/lib/usePoll';
import { useI18n } from '@/i18n/locale';
import { promQueryRange, type PromMatrixSeries } from '@/api/edges';
import { openMetricDrilldown } from '@/lib/drilldown';

type Props = {
  deviceId: string;
};

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

type PanelKey = 'cpu' | 'disk' | 'netRx' | 'netTx';

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

const EMPTY_PANEL: PanelData = { rows: [], series: [] };

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

export function DeviceMetricsPanels({ deviceId }: Props) {
  const { tr } = useI18n();
  const [panels, setPanels] = useState<Record<PanelKey, PanelData>>({
    cpu: EMPTY_PANEL,
    disk: EMPTY_PANEL,
    netRx: EMPTY_PANEL,
    netTx: EMPTY_PANEL,
  });
  const [hidden, setHidden] = useState<Record<PanelKey, Set<string>>>({
    cpu: new Set(),
    disk: new Set(),
    netRx: new Set(),
    netTx: new Set(),
  });
  const [err, setErr] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    const to = new Date();
    const from = new Date(to.getTime() - RANGE_MS);
    const fromIso = from.toISOString();
    const toIso = to.toISOString();
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
            expr: panelExpr(k, deviceId),
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
      setErr(null);
    } catch (e) {
      setErr((e as Error).message || tr('加载指标失败', 'Failed to load metrics'));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  usePoll(refresh, REFRESH_MS, !!deviceId);

  const openDrilldown = useCallback(
    async (expr: string, title: string) => {
      try {
        await openMetricDrilldown({
          expr,
          rangeInput: '6h',
          stepInput: '1m',
          title,
          deviceId: Number(deviceId),
        });
      } catch {
        /* swallow — openMetricDrilldown surfaces its own toast */
      }
    },
    [deviceId],
  );

  const toggleSeries = (panel: PanelKey, key: string) => {
    setHidden((prev) => {
      const nextSet = new Set(prev[panel]);
      if (nextSet.has(key)) nextSet.delete(key);
      else nextSet.add(key);
      return { ...prev, [panel]: nextSet };
    });
  };

  return (
    <div className="space-y-4">
      {err && (
        <div className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {err}
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
          onOpenDrilldown={() => void openDrilldown(panelExpr(p.key, deviceId), p.titleEn)}
        />
      ))}
    </div>
  );
}

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
  onOpenDrilldown(): void;
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
        <button
          type="button"
          onClick={onOpenDrilldown}
          className="inline-flex shrink-0 items-center gap-1 rounded-md border border-zinc-700 px-2 py-1 text-[11px] text-zinc-300 transition-colors hover:border-zinc-500 hover:bg-zinc-800 hover:text-zinc-100"
        >
          <ExternalLink size={12} />
          <span>{tr('查看图表', 'View chart')}</span>
        </button>
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