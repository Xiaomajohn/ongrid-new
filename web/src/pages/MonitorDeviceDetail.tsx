// MonitorDeviceDetail.tsx — 监控设备详情页。
//
// 路由 /hosts/:hostId/monitor。HostDetail 页原本把「指标」「探针」
// tabs 与主机视角的 basic/topology/meta 混在同一页内，导致一台机器
// 的「主机视角」与「监控视角」语义混乱（旧 HostDetail 在同一个 URL
// 下承载两种视角的入口）。
//
// 现在拆分：
//   - /hosts/:hostId          主机详情（基本/拓扑/元数据）
//   - /hosts/:hostId/monitor  监控设备详情（指标/探针/元数据）
//
// 头部借用 HostDetail 的视觉骨架（设备名 + 状态 + 角色 + 副标题），
// 但「返回」与右上角按钮分别指向 host 与监控页，使其形成显式的视
// 角切换对，让运维一眼分得清当前所在的语义层。
import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ChevronLeft, Server } from 'lucide-react';
import { StatusPill } from '@/components/StatusPill';
import { cn } from '@/lib/cn';
import { relativeTime } from '@/lib/format';
import { getDevice, type Device } from '@/api/devices';
import { EDGE_ROLE_LABELS, EDGE_ROLE_LABELS_EN } from '@/api/edges';
import { DeviceMetricsPanels } from '@/components/DeviceMetricsPanels';
import { MonitorDeviceProbes } from '@/components/MonitorDeviceProbes';
import { useI18n } from '@/i18n/locale';
import { usePermissions } from '@/store/me';

// MonitorDevice — 该页面用到的字段集合。本页关注 host 已上报的指标
// (cpu/mem/disk)、已装探针列表、原 HostDetail 元数据。cast HostDevice
// 即可，HostDevice 是 HostDetail.tsx 的同等本地扩展（一致性优先）。
type MonitorDevice = Device & {
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
  reachable?: boolean;
  last_seen_at?: string;
  roles?: string[];
  name?: string;
  created_at?: string;
  updated_at?: string;
  online?: boolean;
  node_id?: number;
  scope?: string;
};

type Tab = 'metrics' | 'probes' | 'meta';

export default function MonitorDeviceDetailPage() {
  const { tr } = useI18n();
  const navigate = useNavigate();
  const { canMutate } = usePermissions();
  const { hostId = '' } = useParams<{ hostId: string }>();

  const [device, setDevice] = useState<MonitorDevice | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('metrics');

  useEffect(() => {
    if (!hostId) return;
    let cancelled = false;
    getDevice(hostId)
      .then((d) => {
        if (!cancelled) setDevice(d as MonitorDevice);
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

  // 探针删除后 device 视角的「探针数」会变，让父页面重新拉一次 device
  // 以保持一致性（虽然本页自身不展示设备数，但 MonitorDeviceProbes 提
  // 供 onAfterChange 钩子，便于以后接汇总）。
  const refreshDevice = () => {
    if (!hostId) return;
    getDevice(hostId)
      .then((d) => setDevice(d as MonitorDevice))
      .catch(() => {
        /* best effort — 保持旧 device */
      });
  };

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <header className="app-header flex items-center justify-between border-b border-zinc-800 px-6 py-4">
        <div className="flex min-w-0 items-center gap-3">
          <button
            type="button"
            onClick={() => navigate(`/hosts/${encodeURIComponent(hostId)}`)}
            aria-label={tr(
              '返回主机详情',
              'Back to host detail',
            )}
            title={tr('返回主机详情', 'Back to host detail')}
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
              {tr(
                '监控设备',
                'Monitor device',
              )}
              {device?.ip_address ? ` · ${device.ip_address}` : ''}
              {device?.last_seen_at
                ? ` · ${tr(
                    '最后心跳',
                    'Last seen',
                  )} ${relativeTime(device.last_seen_at)}`
                : ''}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {/* 顶部右上角放「主机详情」明确回主机视角 — 与头部返回按钮
              一起提供双向跳转，避免用户在两页之间来回找不到出口。 */}
          {device?.id != null && (
            <Link
              to={`/hosts/${encodeURIComponent(String(device.id))}`}
              title={tr(
                '返回主机详情（基本信息/拓扑/元数据）',
                'Back to host detail (basic / topology / metadata)',
              )}
              className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
            >
              <Server size={12} /> {tr('主机详情', 'Host')}
            </Link>
          )}
        </div>
      </header>

      <div className="flex items-center gap-1 border-b border-zinc-800 px-6">
        <TabBtn
          active={tab === 'metrics'}
          onClick={() => setTab('metrics')}
          label={tr('指标', 'Metrics')}
        />
        <TabBtn
          active={tab === 'probes'}
          onClick={() => setTab('probes')}
          label={tr('探针', 'Probes')}
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

        {tab === 'metrics' && device && (
          <DeviceMetricsPanels deviceId={String(device.id)} />
        )}

        {tab === 'probes' && device && (
          <MonitorDeviceProbes
            deviceId={device.id}
            deviceName={device.name || device.hostname || `#${device.id}`}
            canMutate={canMutate}
            onAfterChange={refreshDevice}
          />
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

function buildMeta(d: MonitorDevice): Record<string, unknown> {
  return {
    id: d.id,
    name: d.name,
    scope: d.scope,
    hostname: d.hostname,
    description: d.description,
    roles: d.roles,
    online: d.online,
    reachable: d.reachable,
    last_seen_at: d.last_seen_at,
    created_at: d.created_at,
    updated_at: d.updated_at,
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
