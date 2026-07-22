// DeviceHistory — 历史数据管理页面。展示被软删除的主机及其关联的
// edge 监控任务，让操作员可以回溯已删除的设备与任务信息，并支持
// “确认删除”（二次删除）：确认后数据不做物理删除，但日志页面
// 查询不到，本页面也不再展示。
//
// 数据流约定：
//   - 主机 / 监控页面删除 → 进入本页面（软删除，日志勾选“已删除”仍可查）
//   - 本页面点“确认删除” → purge_marker 置位 → 日志页面查询不到
//   - 主机未删除、仅监控任务删除的情况：任务会出现在“已删除监控任务”
//     区域，所属主机显示为正常（未删除）的设备名。
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowLeft,
  ChevronDown,
  ChevronRight,
  Server,
  Trash2,
  AlertTriangle,
} from 'lucide-react';
import { Card, EmptyState, PageHeader } from '@/components/ui';
import { cn } from '@/lib/cn';
import { relativeTime } from '@/lib/format';
import { useI18n } from '@/i18n/locale';
import { listDevices, confirmDeleteDevice, type Device } from '@/api/devices';
import { listEdges, confirmDeleteEdge, type Edge } from '@/api/edges';

export default function DeviceHistoryPage() {
  const { tr } = useI18n();
  // allDevices 包含未删除 + 已删除（未确认）的全部设备，用于给
  // “已删除监控任务”区域回填所属主机名（主机可能并未删除）。
  const [allDevices, setAllDevices] = useState<Device[]>([]);
  const [deletedDevices, setDeletedDevices] = useState<Device[]>([]);
  const [deletedEdges, setDeletedEdges] = useState<Edge[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  // 展开行：显示该设备关联的已删除 edge
  const [expandedId, setExpandedId] = useState<number | null>(null);
  // 正在执行确认删除的行 id（device / edge 各自记一个，避免重复点击）
  const [confirmingDevice, setConfirmingDevice] = useState<number | null>(null);
  const [confirmingEdge, setConfirmingEdge] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      const [devResp, edgeResp] = await Promise.all([
        listDevices({ include_deleted: true }),
        listEdges({ include_deleted: true }),
      ]);
      // 后端 include_deleted=true 已排除 purge_marker != 0 的行；这里
      // 再做一次客户端兜底过滤，防止旧后端返回脏数据。
      const devs = (devResp.items ?? []).filter((d) => !d.purge_marker);
      const edgs = (edgeResp.items ?? []).filter((e) => !e.purge_marker);
      setAllDevices(devs);
      setDeletedDevices(devs.filter((d) => !!d.deleted_at));
      setDeletedEdges(edgs.filter((e) => !!e.deleted_at));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // 确认删除设备：会同时把该设备关联的所有 edge 一并确认删除。
  const onConfirmDevice = useCallback(
    async (d: Device) => {
      const label = d.name || d.hostname || d.ip_address || `#${d.id}`;
      const ok = window.confirm(
        tr(
          `确认删除主机「${label}」？\n\n确认后该主机及其关联的所有监控任务将不再出现在日志查询中（数据不会物理删除，但无法再通过页面查询）。`,
          `Confirm deletion of host "${label}"?\n\nAfter confirmation this host and all its monitoring tasks will no longer appear in log queries (data is not physically deleted, but can no longer be queried from the UI).`,
        ),
      );
      if (!ok) return;
      setConfirmingDevice(d.id);
      try {
        await confirmDeleteDevice(d.id);
        await refresh();
      } catch (e) {
        setErr(e instanceof Error ? e.message : String(e));
      } finally {
        setConfirmingDevice(null);
      }
    },
    [refresh, tr],
  );

  // 确认删除单个监控任务（不影响所属主机）。
  const onConfirmEdge = useCallback(
    async (e: Edge) => {
      const label = e.name || `edge #${e.id}`;
      const ok = window.confirm(
        tr(
          `确认删除监控任务「${label}」？\n\n确认后该任务将不再出现在日志查询中（数据不会物理删除，但无法再通过页面查询）。`,
          `Confirm deletion of task "${label}"?\n\nAfter confirmation this task will no longer appear in log queries (data is not physically deleted, but can no longer be queried from the UI).`,
        ),
      );
      if (!ok) return;
      setConfirmingEdge(e.id);
      try {
        await confirmDeleteEdge(e.id);
        await refresh();
      } catch (err2) {
        setErr(err2 instanceof Error ? err2.message : String(err2));
      } finally {
        setConfirmingEdge(null);
      }
    },
    [refresh, tr],
  );

  const deviceLabel = useCallback(
    (id?: number | null) => {
      if (id == null) return tr('未关联设备', 'No device');
      const dev = allDevices.find((d) => d.id === id);
      if (!dev) return `device #${id}`;
      return dev.name || dev.hostname || dev.ip_address || `#${dev.id}`;
    },
    [allDevices, tr],
  );

  const empty = deletedDevices.length === 0 && deletedEdges.length === 0;

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <PageHeader
        title={tr('历史数据', 'History')}
        subtitle={tr('已删除的主机与监控任务', 'Deleted hosts and monitoring tasks')}
        actions={
          <Link
            to="/devices"
            className="inline-flex items-center gap-1 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
          >
            <ArrowLeft size={12} />
            {tr('返回设备列表', 'Back to devices')}
          </Link>
        }
      />

      <div className="flex-1 overflow-y-auto px-6 py-6 space-y-6">
        {/* 数据流说明 */}
        <div className="flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2.5 text-xs text-amber-200/90">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          <p className="leading-relaxed">
            {tr(
              '在主机 / 监控页面删除的设备与任务会出现在这里，日志页面勾选“查询已删除数据”仍可查到。点击“确认删除”后，数据不会物理删除，但日志页面将查询不到，本页面也不再展示。',
              'Devices and tasks deleted from the Hosts / Monitor pages appear here; the Logs page can still query them when "show deleted data" is enabled. After clicking "Confirm deletion", data is not physically removed, but becomes unqueryable from the Logs page and disappears from this page.',
            )}
          </p>
        </div>

        {err && (
          <div className="rounded-md border border-red-900/50 bg-red-950/30 px-3 py-2 text-xs text-red-400">
            {err}
          </div>
        )}

        {loading ? (
          <div className="py-16 text-center text-xs text-zinc-500">
            {tr('加载中…', 'Loading…')}
          </div>
        ) : empty ? (
          <EmptyState
            icon={Trash2}
            title={tr('暂无已删除的数据', 'No deleted data')}
            hint={tr('删除的主机和监控任务会出现在这里', 'Deleted hosts and tasks will appear here')}
            className="flex flex-col items-center gap-2 py-20 text-center"
          />
        ) : (
          <>
            {/* 已删除主机 */}
            <section>
              <h2 className="mb-3 flex items-center gap-2 text-sm font-medium text-zinc-200">
                <Server size={14} className="text-zinc-500" />
                {tr(`已删除主机（${deletedDevices.length}）`, `Deleted hosts (${deletedDevices.length})`)}
              </h2>
              {deletedDevices.length === 0 ? (
                <p className="text-xs text-zinc-500">{tr('无', 'None')}</p>
              ) : (
                <Card className="divide-y divide-zinc-800/60">
                  {deletedDevices.map((d) => {
                    const expanded = expandedId === d.id;
                    // 该设备关联的已删除 edge（含主机删除后一并软删的任务）
                    const relatedEdges = deletedEdges.filter((e) => e.device_id === d.id);
                    const busy = confirmingDevice === d.id;
                    return (
                      <div key={d.id}>
                        <div className="flex w-full items-center gap-3 px-4 py-3">
                          <button
                            type="button"
                            onClick={() => setExpandedId(expanded ? null : d.id)}
                            className="flex min-w-0 flex-1 items-center gap-3 text-left"
                            aria-expanded={expanded}
                          >
                            {relatedEdges.length > 0 ? (
                              expanded ? (
                                <ChevronDown size={12} className="shrink-0 text-zinc-500" />
                              ) : (
                                <ChevronRight size={12} className="shrink-0 text-zinc-500" />
                              )
                            ) : (
                              <span className="w-3" />
                            )}
                            <span className="min-w-0 flex-1 truncate text-xs text-zinc-200">
                              {d.name || d.hostname || d.ip_address || `#${d.id}`}
                            </span>
                            <span className="shrink-0 text-[11px] text-zinc-500">
                              {d.ip_address || '—'}
                            </span>
                            <span className="shrink-0 text-[11px] text-zinc-500">
                              {d.hostname || '—'}
                            </span>
                            <span className="shrink-0 text-[11px] text-red-400/80">
                              {d.deleted_at ? relativeTime(d.deleted_at) : '—'}
                            </span>
                          </button>
                          <button
                            type="button"
                            onClick={() => void onConfirmDevice(d)}
                            disabled={busy}
                            title={tr(
                              '确认删除后，该主机及其监控任务将不再出现在日志查询中（不物理删除）',
                              'After confirmation, this host and its tasks will no longer appear in log queries (not physically deleted)',
                            )}
                            className={cn(
                              'inline-flex shrink-0 items-center gap-1 rounded-md border px-2 py-1 text-[11px] transition',
                              busy
                                ? 'cursor-not-allowed border-zinc-800 bg-zinc-900 text-zinc-600'
                                : 'border-red-900/50 bg-red-950/30 text-red-300 hover:bg-red-900/40',
                            )}
                          >
                            <Trash2 size={11} />
                            {busy ? tr('处理中…', 'Working…') : tr('确认删除', 'Confirm deletion')}
                          </button>
                        </div>
                        {/* 展开：关联的已删除 edge 任务 */}
                        {expanded && relatedEdges.length > 0 && (
                          <div className="border-t border-zinc-800/40 bg-zinc-950/30 px-4 py-2 pl-10">
                            <div className="space-y-1">
                              {relatedEdges.map((e) => (
                                <div
                                  key={e.id}
                                  className="flex items-center gap-3 text-[11px] text-zinc-400"
                                >
                                  <span className="min-w-0 flex-1 truncate">
                                    {e.name || `edge #${e.id}`}
                                  </span>
                                  <span className="shrink-0 text-zinc-500">
                                    {e.task_name
                                      ? tr(`任务: ${e.task_name}`, `Task: ${e.task_name}`)
                                      : tr('无任务名', 'No task name')}
                                  </span>
                                  <span className="shrink-0 text-red-400/70">
                                    {e.deleted_at ? relativeTime(e.deleted_at) : '—'}
                                  </span>
                                </div>
                              ))}
                            </div>
                          </div>
                        )}
                      </div>
                    );
                  })}
                </Card>
              )}
            </section>

            {/* 已删除 Edge 监控任务（全局视角，含主机未删除仅任务删除的情况） */}
            <section>
              <h2 className="mb-3 flex items-center gap-2 text-sm font-medium text-zinc-200">
                <Trash2 size={14} className="text-zinc-500" />
                {tr(`已删除监控任务（${deletedEdges.length}）`, `Deleted monitoring tasks (${deletedEdges.length})`)}
              </h2>
              {deletedEdges.length === 0 ? (
                <p className="text-xs text-zinc-500">{tr('无', 'None')}</p>
              ) : (
                <Card className="divide-y divide-zinc-800/60">
                  {deletedEdges.map((e) => {
                    const busy = confirmingEdge === e.id;
                    // 所属主机可能并未删除——用 allDevices 回填真实名称。
                    const hostDeleted = !!allDevices.find(
                      (d) => d.id === e.device_id && !!d.deleted_at,
                    );
                    return (
                      <div key={e.id} className="flex items-center gap-3 px-4 py-3 text-xs">
                        <span className="min-w-0 flex-1 truncate text-zinc-200">
                          {e.name || `edge #${e.id}`}
                        </span>
                        <span className="shrink-0 text-zinc-500">
                          {e.task_name || tr('无任务名', 'No task name')}
                        </span>
                        <span className="shrink-0 text-zinc-500">
                          {deviceLabel(e.device_id)}
                          {e.device_id != null && !hostDeleted && (
                            <span className="ml-1 text-[10px] text-emerald-400/70">
                              {tr('（主机正常）', '(host active)')}
                            </span>
                          )}
                        </span>
                        <span className="shrink-0 text-red-400/80">
                          {e.deleted_at ? relativeTime(e.deleted_at) : '—'}
                        </span>
                        <button
                          type="button"
                          onClick={() => void onConfirmEdge(e)}
                          disabled={busy}
                          title={tr(
                            '确认删除后，该任务将不再出现在日志查询中（不物理删除）',
                            'After confirmation, this task will no longer appear in log queries (not physically deleted)',
                          )}
                          className={cn(
                            'inline-flex shrink-0 items-center gap-1 rounded-md border px-2 py-1 text-[11px] transition',
                            busy
                              ? 'cursor-not-allowed border-zinc-800 bg-zinc-900 text-zinc-600'
                              : 'border-red-900/50 bg-red-950/30 text-red-300 hover:bg-red-900/40',
                          )}
                        >
                          <Trash2 size={11} />
                          {busy ? tr('处理中…', 'Working…') : tr('确认删除', 'Confirm deletion')}
                        </button>
                      </div>
                    );
                  })}
                </Card>
              )}
            </section>
          </>
        )}
      </div>
    </main>
  );
}
