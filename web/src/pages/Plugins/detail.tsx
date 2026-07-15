// web/src/pages/Plugins/detail.tsx — 插件详情 + Capabilities / Audit / Config
//
// 设计要点:
//   - 复用 PageHeader + Card + Chip + Button + EmptyState,与列表页保持
//     视觉一致。
//   - Tab 是 client-side state,不动 URL(避免 router 配置改动);刷新页面
//     回到 Overview。
//   - Capabilities 行的 enable toggle 调 enable/disableCapability;翻转失败
//     时回滚 UI,跟列表页一致。
//   - "试运行" 跳到 /plugins/:id/invoke?cap=<name>,由 invoke.tsx 渲染。
//   - Config tab 暂 placeholder,等 Phase 4 接入 pluginhost server 后
//     再补 UI(plan §14.2 T13 "前端联调"阶段)。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ChevronLeft,
  RefreshCw,
  Trash2,
  Play,
  ExternalLink,
} from 'lucide-react';
import {
  PageHeader,
  Card,
  Button,
  Chip,
  EmptyState,
} from '@/components/ui';
import { cn } from '@/lib/cn';
import {
  getPlugin,
  listCapabilities,
  listAudits,
  uninstallPlugin,
  enableCapability,
  disableCapability,
  type PluginInstance,
  type PluginCapability,
  type PluginAudit,
} from '@/api/pluginhost';
import { ApiError } from '@/api/client';
import { useI18n } from '@/i18n/locale';
import { usePoll } from '@/lib/usePoll';
import { Modal } from '@/components/Modal';
import { relativeTime } from '@/lib/format';

type Tab = 'overview' | 'capabilities' | 'audit' | 'config';

const TABS: { value: Tab; zh: string; en: string }[] = [
  { value: 'overview', zh: '概览', en: 'Overview' },
  { value: 'capabilities', zh: '能力', en: 'Capabilities' },
  { value: 'audit', zh: '审计', en: 'Audit' },
  { value: 'config', zh: '配置', en: 'Config' },
];

function classTone(c: PluginCapability['class']): 'success' | 'warning' | 'danger' {
  // safe = emerald, mutating = amber, dangerous = red
  if (c === 'safe') return 'success';
  if (c === 'mutating') return 'warning';
  return 'danger';
}

export default function PluginDetailPage() {
  const { tr } = useI18n();
  const params = useParams<{ id: string }>();
  const navigate = useNavigate();
  const id = useMemo(() => {
    const n = parseInt(params.id ?? '', 10);
    return Number.isFinite(n) ? n : 0;
  }, [params.id]);

  const [plugin, setPlugin] = useState<PluginInstance | null>(null);
  const [caps, setCaps] = useState<PluginCapability[]>([]);
  const [audits, setAudits] = useState<PluginAudit[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [tab, setTab] = useState<Tab>('overview');
  const [confirmUninstall, setConfirmUninstall] = useState(false);
  const [uninstalling, setUninstalling] = useState(false);
  const [confirmText, setConfirmText] = useState('');

  const refresh = useCallback(async () => {
    if (!id) return;
    try {
      const [p, c, a] = await Promise.all([
        getPlugin(id),
        listCapabilities(id).catch(() => [] as PluginCapability[]),
        listAudits(id).catch(() => [] as PluginAudit[]),
      ]);
      setPlugin(p);
      setCaps(c);
      setAudits(a);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : (err as Error).message);
    } finally {
      setLoading(false);
    }
  }, [id]);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  usePoll(refresh, 15_000);

  // ----- capability toggle (本地乐观 + 失败回滚) ---------------------
  async function onToggleCap(c: PluginCapability, next: boolean) {
    setCaps((prev) => prev.map((x) => (x.id === c.id ? { ...x, enabled: next } : x)));
    try {
      if (next) {
        await enableCapability(id, c.name);
      } else {
        await disableCapability(id, c.name);
      }
    } catch (err) {
      // 回滚
      setCaps((prev) => prev.map((x) => (x.id === c.id ? { ...x, enabled: !next } : x)));
      setError(err instanceof ApiError ? err.message : (err as Error).message);
    }
  }

  // ----- uninstall --------------------------------------------------
  async function onConfirmUninstall() {
    if (!plugin) return;
    setUninstalling(true);
    try {
      await uninstallPlugin(plugin.id);
      navigate('/plugins');
    } catch (err) {
      setError(err instanceof ApiError ? err.message : (err as Error).message);
    } finally {
      setUninstalling(false);
      setConfirmUninstall(false);
    }
  }

  if (!id) {
    return (
      <main className="anim-fade flex flex-1 items-center justify-center text-sm text-zinc-500">
        {tr('无效的插件 ID', 'Invalid plugin ID')}
      </main>
    );
  }

  const matched =
    !!plugin && confirmText.trim() === plugin.pack_id.trim() && plugin.pack_id.trim() !== '';

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <PageHeader
        leading={
          <Link
            to="/plugins"
            className="inline-flex items-center gap-1 text-zinc-400 hover:text-zinc-200"
          >
            <ChevronLeft size={12} />
            {tr('返回插件列表', 'Back to Plugins')}
          </Link>
        }
        title={
          plugin ? (
            <span className="flex items-baseline gap-2">
              <span className="font-mono">{plugin.pack_id}</span>
              <span className="font-mono text-[11px] text-zinc-500">
                {plugin.version}
              </span>
              <Chip
                tone={
                  plugin.health_status === 'healthy'
                    ? 'success'
                    : plugin.health_status === 'degraded'
                      ? 'warning'
                      : plugin.health_status === 'down'
                        ? 'danger'
                        : 'default'
                }
              >
                {plugin.health_status}
              </Chip>
            </span>
          ) : (
            tr('加载中…', 'Loading…')
          )
        }
        subtitle={
          plugin
            ? tr(
                `安装路径 ${plugin.install_path}`,
                `Installed at ${plugin.install_path}`,
              )
            : undefined
        }
        actions={
          <div className="flex items-center gap-2">
            <Button onClick={() => void refresh()} title={tr('刷新', 'Refresh')}>
              <RefreshCw size={12} />
              {tr('刷新', 'Refresh')}
            </Button>
            <Button
              variant="danger"
              onClick={() => setConfirmUninstall(true)}
              title={tr('卸载插件', 'Uninstall plugin')}
              disabled={!plugin}
            >
              <Trash2 size={12} />
              {tr('卸载', 'Uninstall')}
            </Button>
          </div>
        }
      />

      <div className="flex-1 overflow-y-auto px-6 py-6">
        {error && (
          <div
            role="alert"
            className="mb-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300"
          >
            {error}
          </div>
        )}

        {/* tab strip */}
        <div className="mb-4 inline-flex overflow-hidden rounded-md border border-zinc-800 bg-zinc-900/60">
          {TABS.map((t) => (
            <button
              key={t.value}
              type="button"
              onClick={() => setTab(t.value)}
              className={cn(
                'px-3 py-1.5 text-xs transition-colors',
                tab === t.value
                  ? 'bg-zinc-800 text-zinc-100'
                  : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200',
              )}
            >
              {tr(t.zh, t.en)}
            </button>
          ))}
        </div>

        {loading && !plugin ? (
          <div className="flex h-40 items-center justify-center text-sm text-zinc-500">
            {tr('加载中…', 'Loading…')}
          </div>
        ) : tab === 'overview' ? (
          <OverviewTab plugin={plugin} />
        ) : tab === 'capabilities' ? (
          <CapabilitiesTab
            caps={caps}
            onToggle={onToggleCap}
            onInvoke={(name) => navigate(`/plugins/${id}/invoke?cap=${encodeURIComponent(name)}`)}
          />
        ) : tab === 'audit' ? (
          <AuditTab audits={audits} />
        ) : (
          <ConfigTab />
        )}
      </div>

      <Modal
        open={confirmUninstall}
        onClose={() => {
          if (!uninstalling) {
            setConfirmUninstall(false);
            setConfirmText('');
          }
        }}
        title={tr(`卸载插件 ${plugin?.pack_id ?? ''}`, `Uninstall plugin ${plugin?.pack_id ?? ''}`)}
        size="sm"
        footer={
          <>
            <Button
              variant="ghost"
              onClick={() => {
                setConfirmUninstall(false);
                setConfirmText('');
              }}
              disabled={uninstalling}
            >
              {tr('取消', 'Cancel')}
            </Button>
            <Button
              variant="danger"
              onClick={() => void onConfirmUninstall()}
              disabled={uninstalling || !matched}
            >
              {uninstalling ? tr('卸载中…', 'Uninstalling…') : tr('卸载', 'Uninstall')}
            </Button>
          </>
        }
      >
        <div className="space-y-3 text-xs text-zinc-300">
          <p>
            {tr(
              '卸载将停止该插件所有能力,删除本地文件及数据库记录。该操作不可恢复。',
              'Uninstalling stops all capabilities, removes local files and DB rows. This cannot be undone.',
            )}
          </p>
          <label className="block pt-1">
            <div className="mb-1 text-[11px] text-zinc-500">
              {tr('输入插件 ID "', 'Type the plugin ID "')}
              <span className="font-mono text-zinc-300">{plugin?.pack_id}</span>
              {tr('" 以确认:', '" to confirm:')}
            </div>
            <input
              autoFocus
              value={confirmText}
              onChange={(e) => setConfirmText(e.target.value)}
              placeholder={plugin?.pack_id ?? ''}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              onKeyDown={(e) => {
                if (e.key === 'Enter' && matched) void onConfirmUninstall();
              }}
            />
          </label>
        </div>
      </Modal>
    </main>
  );
}

// ---------- tab: overview --------------------------------------------------

function OverviewTab({ plugin }: { plugin: PluginInstance | null }) {
  const { tr } = useI18n();
  if (!plugin) {
    return (
      <EmptyState title={tr('未找到插件', 'Plugin not found')} />
    );
  }
  const rows: Array<[string, string]> = [
    [tr('版本', 'Version'), plugin.version],
    [tr('来源', 'Source'), plugin.source],
    [tr('安装路径', 'Install path'), plugin.install_path],
    [tr('Manifest SHA-256', 'Manifest SHA-256'), plugin.manifest_sha256],
    [tr('健康状态', 'Health'), plugin.health_status],
    [tr('能力数量', 'Capabilities'), String(plugin.capabilities_count)],
    [tr('创建时间', 'Created at'), plugin.created_at],
    [tr('更新时间', 'Updated at'), plugin.updated_at],
  ];
  return (
    <Card>
      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {rows.map(([k, v]) => (
          <div key={k} className="min-w-0">
            <dt className="text-[11px] uppercase tracking-wider text-zinc-500">{k}</dt>
            <dd className="mt-0.5 break-all font-mono text-xs text-zinc-200">{v}</dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

// ---------- tab: capabilities ---------------------------------------------

function CapabilitiesTab({
  caps,
  onToggle,
  onInvoke,
}: {
  caps: PluginCapability[];
  onToggle(c: PluginCapability, next: boolean): void;
  onInvoke(name: string): void;
}) {
  const { tr } = useI18n();
  if (caps.length === 0) {
    return (
      <EmptyState
        title={tr('暂无能力', 'No capabilities')}
        hint={tr(
          '该插件未声明任何能力,或 server 端 list 接口尚未就绪',
          'Plugin declares no capabilities, or the server list endpoint is not yet ready',
        )}
      />
    );
  }
  return (
    <Card className="overflow-hidden p-0">
      <ul className="divide-y divide-zinc-800/40">
        {caps.map((c) => (
          <li
            key={c.id}
            className="flex flex-wrap items-center gap-3 px-4 py-3"
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline gap-2">
                <span className="font-mono text-sm text-zinc-100">{c.name}</span>
                <Chip tone="default">{c.kind}</Chip>
                <Chip tone={classTone(c.class)}>
                  {tr(
                    c.class === 'safe'
                      ? '安全'
                      : c.class === 'mutating'
                        ? '变更'
                        : '危险',
                    c.class,
                  )}
                </Chip>
              </div>
              <div className="mt-0.5 text-[11px] text-zinc-500">
                #{c.id} · plugin_instance_id={c.plugin_instance_id}
              </div>
            </div>
            <label
              className="flex shrink-0 items-center gap-2 text-[11px] text-zinc-400"
              title={
                c.enabled ? tr('点击停用', 'Click to disable') : tr('点击启用', 'Click to enable')
              }
            >
              <span className="hidden sm:inline">
                {c.enabled ? tr('已启用', 'Enabled') : tr('已停用', 'Disabled')}
              </span>
              <input
                type="checkbox"
                checked={c.enabled}
                onChange={(e) => onToggle(c, e.target.checked)}
                className="h-3.5 w-3.5 cursor-pointer accent-indigo-500"
                aria-label={tr(`切换 ${c.name}`, `Toggle ${c.name}`)}
              />
            </label>
            <Button
              variant="ghost"
              onClick={() => onInvoke(c.name)}
              title={tr('试运行此能力', 'Invoke this capability')}
            >
              <Play size={12} />
              {tr('试运行', 'Invoke')}
            </Button>
          </li>
        ))}
      </ul>
    </Card>
  );
}

// ---------- tab: audit -----------------------------------------------------

function AuditTab({ audits }: { audits: PluginAudit[] }) {
  const { tr } = useI18n();
  if (audits.length === 0) {
    return (
      <EmptyState
        title={tr('暂无审计记录', 'No audit entries')}
        hint={tr(
          '该插件尚未产生 install / uninstall / enable / invoke 等审计事件',
          'No install / uninstall / enable / invoke events have been recorded for this plugin yet',
        )}
      />
    );
  }
  return (
    <Card className="overflow-hidden p-0">
      <table className="w-full text-sm">
        <thead className="border-b border-zinc-800/60 bg-zinc-950/40 text-[11px] uppercase tracking-wider text-zinc-500">
          <tr>
            <th className="px-4 py-2 text-left">{tr('时间', 'When')}</th>
            <th className="px-4 py-2 text-left">{tr('动作', 'Action')}</th>
            <th className="px-4 py-2 text-left">{tr('操作者', 'Actor')}</th>
            <th className="px-4 py-2 text-left">{tr('详情', 'Details')}</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-800/40">
          {audits.map((a) => (
            <tr key={a.id} className="text-xs">
              <td className="whitespace-nowrap px-4 py-2 text-zinc-400">
                {relativeTime(a.occurred_at)}
              </td>
              <td className="whitespace-nowrap px-4 py-2 font-mono text-zinc-200">
                {a.action}
              </td>
              <td className="whitespace-nowrap px-4 py-2 text-zinc-400">
                {a.actor}
              </td>
              <td className="px-4 py-2 font-mono text-[11px] text-zinc-500">
                {a.details_json ? (
                  <code className="break-all">{a.details_json}</code>
                ) : (
                  '—'
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}

// ---------- tab: config (placeholder) -------------------------------------

function ConfigTab() {
  const { tr } = useI18n();
  return (
    <Card>
      <div className="flex flex-col items-center gap-2 py-10 text-center">
        <ExternalLink size={20} className="text-zinc-600" />
        <div className="text-sm text-zinc-300">
          {tr('配置编辑暂未启用', 'Config editor not yet enabled')}
        </div>
        <div className="text-xs text-zinc-500">
          {tr(
            'Phase 4 server handler 接入后将开放:运行时配置 / 数据作用域 / 凭据绑定',
            'Phase 4 (after server handler integration) will expose: runtime config / data scopes / credential bindings',
          )}
        </div>
      </div>
    </Card>
  );
}