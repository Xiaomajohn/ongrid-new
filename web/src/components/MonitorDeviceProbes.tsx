// MonitorDeviceProbes.tsx — 监控设备详情页「探针」Tab。
//
// 原 HostDetail.tsx 的 ProbesTab 私有函数被抽出来到这里，让监控设备
// 详情页 (/hosts/:hostId/monitor) 可以直接复用。
//
// 行为：
//  - 列出该 device 下所有 edges（listEdges({device_id}））。
//  - 支持轮换密钥、整包升级、删除（受 canMutate 控制）。
//  - 每行可点详情跳到对应 EdgeDetailPage (/devices/:id)。
//
// 不在此组件内 fetch device / host 自己 — 由调用方传 deviceId 进来。
import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Plus, Trash2, RotateCw, Loader2, ExternalLink } from 'lucide-react';
import { StatusPill } from '@/components/StatusPill';
import { usePoll } from '@/lib/usePoll';
import { relativeTime } from '@/lib/format';
import {
  listEdges,
  deleteEdge,
  rotateSecret,
  upgradeEdgePackage,
  type Edge,
  type RotateSecretResponse,
} from '@/api/edges';
import { useI18n } from '@/i18n/locale';

type Props = {
  deviceId: number;
  deviceName: string;
  canMutate: boolean;
  // 软删除/解绑后调用 — 用于父页面刷新 device 自身 (角色 / 探针数)，
  // 而不是当前组件内部。本组件只刷新 edges 列表。
  onAfterChange?: () => void;
};

// 复用 HostDetail.tsx 的旧 listDeviceEdgesLocal 行为：优先 listEdges
// 客户端过滤失败时退化到全量 + 本地按 device_id 筛。逻辑保持一致，
// 避免 HostDetail.tsx 旧调用点出现回归。
async function listDeviceEdgesLocal(
  deviceId: string | number,
): Promise<Edge[]> {
  const r = await listEdges({ device_id: deviceId });
  return r.items ?? [];
}

export function MonitorDeviceProbes({
  deviceId,
  deviceName,
  canMutate,
  onAfterChange,
}: Props) {
  const { tr } = useI18n();
  const navigate = useNavigate();
  const [edges, setEdges] = useState<Edge[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    setLoading(true);
    setErr(null);
    try {
      try {
        const items = await listDeviceEdgesLocal(deviceId);
        setEdges(items);
      } catch {
        const r = await listEdges();
        const filtered = (r.items ?? []).filter(
          (e) => String(e.device_id ?? '') === String(deviceId),
        );
        setEdges(filtered);
      }
    } catch (e) {
      setErr((e as Error).message || tr('加载失败', 'Load failed'));
    } finally {
      setLoading(false);
    }
  }, [deviceId]);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  usePoll(refresh, 10_000, !!deviceId);

  async function onRotate(e: Edge) {
    if (
      !window.confirm(
        tr(
          `轮换 ${e.name} 密钥？旧密钥将立即失效。`,
          `Rotate ${e.name}'s secret? Old key stops immediately.`,
        ),
      )
    )
      return;
    try {
      const r: RotateSecretResponse = await rotateSecret(e.id);
      window.alert(
        tr(
          `新 secret_key（仅显示一次）：\n${r.secret_key}`,
          `New secret_key (shown once):\n${r.secret_key}`,
        ),
      );
      void refresh();
    } catch (err) {
      window.alert((err as Error).message || tr('轮换失败', 'Rotate failed'));
    }
  }

  async function onDelete(e: Edge) {
    if (
      !window.confirm(
        tr(
          `删除 ${e.name}？不可恢复。`,
          `Delete ${e.name}? Cannot be undone.`,
        ),
      )
    )
      return;
    try {
      await deleteEdge(e.id);
      void refresh();
      onAfterChange?.();
    } catch (err) {
      window.alert((err as Error).message || tr('删除失败', 'Delete failed'));
    }
  }

  async function onPackageUpgrade(e: Edge) {
    if (
      !window.confirm(
        tr(
          `整包升级 ${e.name}？agent 短暂重启；失败自动回滚。`,
          `Upgrade ${e.name} package? Agent restarts briefly; auto-rollback on failure.`,
        ),
      )
    )
      return;
    try {
      const resp = await upgradeEdgePackage(e.id);
      window.alert(
        resp.applied
          ? tr(
              `${e.name} → ${resp.version} 已 stage ${resp.manifest_files} 个文件`,
              `${e.name} → ${resp.version} staged ${resp.manifest_files} files`,
            )
          : tr(
              `${e.name} stage OK 但 apply 失败：${resp.apply_error ?? '未知'}`,
              `${e.name} staged but apply failed: ${resp.apply_error ?? 'unknown'}`,
            ),
      );
      void refresh();
    } catch (err) {
      window.alert((err as Error).message || tr('升级失败', 'Upgrade failed'));
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="text-sm text-zinc-300">
          {tr(
            `${deviceName || tr('该主机', 'this host')} 上 ${edges.length} 个探针`,
            `${edges.length} probe${edges.length === 1 ? '' : 's'} on ${deviceName || 'this host'}`,
          )}
        </div>
        {canMutate && (
          <button
            type="button"
            onClick={() => navigate('/devices')}
            className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-200 hover:bg-zinc-800"
            title={tr(
              '去监控设备列表签发新探针，安装到本主机后会自动绑定到此 device',
              'Issue a new probe from the monitor devices list; once installed on this host it will bind here',
            )}
          >
            <Plus size={12} /> {tr('添加探针', 'Add probe')}
          </button>
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
              <th className="px-4 py-2.5 text-left">
                {tr('最后心跳', 'Last heartbeat')}
              </th>
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
                  {tr('尚未安装探针', 'No probes installed yet')}
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
                      {e.access_key_id ? `${e.access_key_id.slice(0, 8)}…` : '—'}
                    </span>
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                    {e.agent_version || <span className="text-zinc-600">—</span>}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right">
                    <button
                      type="button"
                      onClick={() =>
                        navigate(`/devices/${encodeURIComponent(String(e.id))}`)
                      }
                      title={tr('打开监控探针详情', 'Open probe detail')}
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
                          aria-label={tr(
                            `删除 ${e.name}`,
                            `Delete ${e.name}`,
                          )}
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
