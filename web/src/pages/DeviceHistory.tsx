// DeviceHistory — 历史数据管理页面。展示被软删除的主机及其关联的
// edge 监控任务，让操作员可以回溯已删除的设备与任务信息。
import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { ArrowLeft, ChevronDown, ChevronRight, Server, Trash2 } from 'lucide-react';
import { Card, EmptyState, PageHeader } from '@/components/ui';
import { cn } from '@/lib/cn';
import { relativeTime } from '@/lib/format';
import { useI18n } from '@/i18n/locale';
import { listDevices, type Device } from '@/api/devices';
import { listEdges, type Edge } from '@/api/edges';

export default function DeviceHistoryPage() {
  const { tr } = useI18n();
  const [devices, setDevices] = useState<Device[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  // 展开行：显示该设备关联的已删除 edge
  const [expandedId, setExpandedId] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      const [devResp, edgeResp] = await Promise.all([
        listDevices({ include_deleted: true }),
        listEdges({ include_deleted: true }),
      ]);
      // 前端过滤：只保留已删除的行
      setDevices((devResp.items ?? []).filter((d) => !!d.deleted_at));
      setEdges((edgeResp.items ?? []).filter((e) => !!e.deleted_at));
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

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
        {err && (
          <div className="rounded-md border border-red-900/50 bg-red-950/30 px-3 py-2 text-xs text-red-400">
            {err}
          </div>
        )}

        {loading ? (
          <div className="py-16 text-center text-xs text-zinc-500">
            {tr('加载中…', 'Loading…')}
          </div>
        ) : devices.length === 0 && edges.length === 0 ? (
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
                {tr(`已删除主机（${devices.length}）`, `Deleted hosts (${devices.length})`)}
              </h2>
              {devices.length === 0 ? (
                <p className="text-xs text-zinc-500">{tr('无', 'None')}</p>
              ) : (
                <Card className="divide-y divide-zinc-800/60">
                  {devices.map((d) => {
                    const expanded = expandedId === d.id;
                    // 该设备关联的已删除 edge
                    const deletedEdges = edges.filter(
                      (e) => e.device_id === d.id,
                    );
                    return (
                      <div key={d.id}>
                        <button
                          type="button"
                          onClick={() => setExpandedId(expanded ? null : d.id)}
                          className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-zinc-800/30"
                        >
                          {deletedEdges.length > 0 ? (
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
                        {/* 展开：关联的已删除 edge 任务 */}
                        {expanded && deletedEdges.length > 0 && (
                          <div className="border-t border-zinc-800/40 bg-zinc-950/30 px-4 py-2 pl-10">
                            <div className="space-y-1">
                              {deletedEdges.map((e) => (
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

            {/* 已删除 Edge 监控任务（全局视角） */}
            <section>
              <h2 className="mb-3 flex items-center gap-2 text-sm font-medium text-zinc-200">
                <Trash2 size={14} className="text-zinc-500" />
                {tr(`已删除监控任务（${edges.length}）`, `Deleted monitoring tasks (${edges.length})`)}
              </h2>
              {edges.length === 0 ? (
                <p className="text-xs text-zinc-500">{tr('无', 'None')}</p>
              ) : (
                <Card className="divide-y divide-zinc-800/60">
                  {edges.map((e) => {
                    const dev = devices.find((d) => d.id === e.device_id);
                    return (
                      <div
                        key={e.id}
                        className={cn(
                          'flex items-center gap-3 px-4 py-3 text-xs',
                        )}
                      >
                        <span className="min-w-0 flex-1 truncate text-zinc-200">
                          {e.name || `edge #${e.id}`}
                        </span>
                        <span className="shrink-0 text-zinc-500">
                          {e.task_name || tr('无任务名', 'No task name')}
                        </span>
                        <span className="shrink-0 text-zinc-500">
                          {dev
                            ? dev.name || dev.hostname || dev.ip_address || `#${dev.id}`
                            : e.device_id
                              ? `device #${e.device_id}`
                              : tr('未关联设备', 'No device')}
                        </span>
                        <span className="shrink-0 text-red-400/80">
                          {e.deleted_at ? relativeTime(e.deleted_at) : '—'}
                        </span>
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
