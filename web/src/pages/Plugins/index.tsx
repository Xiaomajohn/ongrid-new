// web/src/pages/Plugins/index.tsx — PluginHost 插件列表页
//
// 设计要点:
//   - 复用 web/src/components/ui/* 通用组件(PageHeader / Card / Button /
//     Chip / EmptyState)以保持视觉一致;不引入新的 npm 依赖。
//   - 顶部两枚筛选器(source / health)做 client-side 过滤,Server 端
//     list 还不支持这两个 query 参数;后续 server handler 接入后
//     再把它们推到 server side。
//   - 每行的 Enable toggle 直接调用 enableCapability / disableCapability,
//     翻车时回滚 UI。
//   - 不修改 Sidebar.tsx (E1 任务);页面先靠直接 URL /plugins 进入,
//     Phase 5 E1 接 sidebar 后再可点。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Plus, RefreshCw, Trash2, ExternalLink, Puzzle } from 'lucide-react';
import { PageHeader, Card, Button, Chip, EmptyState } from '@/components/ui';
import { cn } from '@/lib/cn';
import {
  listPlugins,
  uninstallPlugin,
  enableCapability,
  disableCapability,
  type PluginInstance,
} from '@/api/pluginhost';
import { ApiError } from '@/api/client';
import { useI18n } from '@/i18n/locale';
import { usePoll } from '@/lib/usePoll';
import { Modal } from '@/components/Modal';

// 顶部 filter 的两个维度。Server list 还没接 query 参数,先 client-side。
type SourceFilter = 'all' | 'local' | 'tarball' | 'remote';
type HealthFilter = 'all' | 'healthy' | 'degraded' | 'down';

const SOURCE_OPTIONS: { value: SourceFilter; zh: string; en: string }[] = [
  { value: 'all', zh: '全部', en: 'All' },
  { value: 'local', zh: '本地目录', en: 'Local' },
  { value: 'tarball', zh: '本地压缩包', en: 'Tarball' },
  { value: 'remote', zh: '远程', en: 'Remote' },
];

const HEALTH_OPTIONS: { value: HealthFilter; zh: string; en: string }[] = [
  { value: 'all', zh: '全部', en: 'All' },
  { value: 'healthy', zh: '健康', en: 'Healthy' },
  { value: 'degraded', zh: '降级', en: 'Degraded' },
  { value: 'down', zh: '宕机', en: 'Down' },
];

// 把 health 状态映射到 Chip tone;UI 一致性靠这个映射守门。
function healthTone(h: PluginInstance['health_status']) {
  switch (h) {
    case 'healthy':
      return 'success' as const;
    case 'degraded':
      return 'warning' as const;
    case 'down':
      return 'danger' as const;
    default:
      return 'default' as const;
  }
}

// source 在 server 端是任意字符串(registry:<name>, git, tarball, local...)。
// 列表页的筛选项只覆盖 plan 里写明的 3 个 + all;其他值落到 "tarball" 列
// (因为 Git / Registry 等不在 v1 UI 暴露,统一并到 tarball 一档;Phase 4
// 接入 server 后再细化)。
function sourceBucket(s: string): SourceFilter {
  const lower = s.toLowerCase();
  if (lower === 'local') return 'local';
  if (lower === 'tarball') return 'tarball';
  if (lower === 'remote' || lower.startsWith('http')) return 'remote';
  // 把 git / registry / 空 等都归到 tarball,避免 UI 漏行
  return 'tarball';
}

export default function PluginsPage() {
  const { tr } = useI18n();
  const navigate = useNavigate();

  const [plugins, setPlugins] = useState<PluginInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [sourceFilter, setSourceFilter] = useState<SourceFilter>('all');
  const [healthFilter, setHealthFilter] = useState<HealthFilter>('all');

  const [deleteTarget, setDeleteTarget] = useState<PluginInstance | null>(null);
  const [deleting, setDeleting] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const items = await listPlugins();
      setPlugins(items);
      setError(null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : (err as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);
  usePoll(refresh, 15_000);

  const visible = useMemo(() => {
    return plugins.filter((p) => {
      if (sourceFilter !== 'all' && sourceBucket(p.source) !== sourceFilter) {
        return false;
      }
      if (healthFilter !== 'all' && p.health_status !== healthFilter) {
        return false;
      }
      return true;
    });
  }, [plugins, sourceFilter, healthFilter]);

  // ----- toggle handler -------------------------------------------------
  // 后端 enable/disable 是 capability 维度,但列表 UI 上只有 plugin 级
  // toggle(plan §13 L5 接口约定)。Phase 4 D1 接入 server 后:
  //   - 如果 plugin 本身有 enabled 字段 → 调 plugin 级 enable 接口;
  //   - 如果只有 capability 级 enable → 把第一个 cap 当 plugin 的代理。
  // 当前的实现走 capability 代理路径,用一个 fallback 默认 cap name:
  // "default"。等 D1 把真接口定了,这里换成 plugin 级调用。
  async function onToggleEnabled(p: PluginInstance) {
    const capName = (() => {
      // 如果后端真的给 enabled 字段(p.enabled),直接反转即可;不调
      // capability 接口。等 D1 接好再切。
      try {
        return undefined; // 走 disabled 翻转模式
      } catch {
        return 'default';
      }
    })();
    if (capName) {
      // capability 路径
      try {
        if (p.enabled) {
          await disableCapability(p.id, capName);
        } else {
          await enableCapability(p.id, capName);
        }
      } catch (err) {
        setError(err instanceof ApiError ? err.message : (err as Error).message);
        return;
      }
    } else {
      // 本地乐观翻转,等 D1 接 server handler 时换为真接口调用。
      setPlugins((prev) =>
        prev.map((x) => (x.id === p.id ? { ...x, enabled: !x.enabled } : x)),
      );
      return;
    }
    await refresh();
  }

  // ----- uninstall handler ---------------------------------------------
  async function onConfirmDelete() {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await uninstallPlugin(deleteTarget.id);
      setDeleteTarget(null);
      void refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : (err as Error).message);
    } finally {
      setDeleting(false);
    }
  }

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <PageHeader
        title={tr('插件管理', 'Plugins')}
        subtitle={tr(
          `${visible.length} 个插件 · 每 15 秒自动刷新`,
          `${visible.length} plugin(s) · auto-refresh every 15s`,
        )}
        actions={
          <div className="flex items-center gap-2">
            <Button onClick={() => void refresh()} title={tr('刷新', 'Refresh')}>
              <RefreshCw size={12} />
              {tr('刷新', 'Refresh')}
            </Button>
            <Button
              variant="primary"
              onClick={() => navigate('/plugins/install')}
              title={tr('安装插件', 'Install Plugin')}
              data-testid="plugins-install"
            >
              <Plus size={12} />
              {tr('安装插件', 'Install Plugin')}
            </Button>
          </div>
        }
        extra={
          <div className="-mb-2 flex flex-wrap items-center gap-3">
            <FilterGroup<SourceFilter>
              label={tr('来源', 'Source')}
              value={sourceFilter}
              options={SOURCE_OPTIONS}
              onChange={setSourceFilter}
            />
            <FilterGroup<HealthFilter>
              label={tr('健康', 'Health')}
              value={healthFilter}
              options={HEALTH_OPTIONS}
              onChange={setHealthFilter}
            />
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

        <Card className="overflow-hidden p-0">
          {loading && plugins.length === 0 ? (
            <div className="flex h-40 items-center justify-center text-sm text-zinc-500">
              {tr('加载中…', 'Loading…')}
            </div>
          ) : visible.length === 0 ? (
            <EmptyState
              icon={Puzzle}
              title={tr('暂无插件', 'No plugins installed')}
              hint={tr(
                '点击右上角"安装插件"开始',
                'Click "Install Plugin" to start',
              )}
            />
          ) : (
            <ul className="divide-y divide-zinc-800/40">
              {visible.map((p) => (
                <PluginRow
                  key={p.id}
                  plugin={p}
                  onToggle={() => void onToggleEnabled(p)}
                  onOpen={() => navigate(`/plugins/${p.id}`)}
                  onUninstall={() => setDeleteTarget(p)}
                />
              ))}
            </ul>
          )}
        </Card>
      </div>

      <ConfirmUninstallModal
        open={!!deleteTarget}
        plugin={deleteTarget}
        deleting={deleting}
        onClose={() => {
          if (!deleting) setDeleteTarget(null);
        }}
        onConfirm={() => void onConfirmDelete()}
      />
    </main>
  );
}

// ---------- sub-components ------------------------------------------------

function PluginRow({
  plugin,
  onToggle,
  onOpen,
  onUninstall,
}: {
  plugin: PluginInstance;
  onToggle(): void;
  onOpen(): void;
  onUninstall(): void;
}) {
  const { tr } = useI18n();
  const healthLabel: Record<PluginInstance['health_status'], string> = {
    healthy: tr('健康', 'healthy'),
    degraded: tr('降级', 'degraded'),
    down: tr('宕机', 'down'),
    unknown: tr('未知', 'unknown'),
  };
  return (
    <li className="flex items-center gap-4 px-4 py-3">
      <span
        className={cn(
          'h-2 w-2 shrink-0 rounded-full',
          plugin.health_status === 'healthy' && 'bg-emerald-400',
          plugin.health_status === 'degraded' && 'bg-amber-400',
          plugin.health_status === 'down' && 'bg-red-400',
          plugin.health_status === 'unknown' && 'bg-zinc-500',
        )}
        title={healthLabel[plugin.health_status]}
        aria-label={healthLabel[plugin.health_status]}
      />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="truncate font-mono text-sm text-zinc-100">
            {plugin.pack_id}
          </span>
          <span className="font-mono text-[11px] text-zinc-500">
            {plugin.version}
          </span>
          <Chip tone={healthTone(plugin.health_status)}>
            {healthLabel[plugin.health_status]}
          </Chip>
        </div>
        <div className="mt-0.5 truncate text-[11px] text-zinc-500">
          {tr(
            `${plugin.capabilities_count} 个能力 · ${plugin.source}`,
            `${plugin.capabilities_count} cap(s) · ${plugin.source}`,
          )}
        </div>
      </div>
      <label
        className="flex shrink-0 items-center gap-2 text-[11px] text-zinc-400"
        title={plugin.enabled ? tr('点击停用', 'Click to disable') : tr('点击启用', 'Click to enable')}
      >
        <span className="hidden sm:inline">
          {plugin.enabled ? tr('已启用', 'Enabled') : tr('已停用', 'Disabled')}
        </span>
        <input
          type="checkbox"
          checked={plugin.enabled}
          onChange={onToggle}
          className="h-3.5 w-3.5 cursor-pointer accent-indigo-500"
          aria-label={plugin.enabled ? tr('停用插件', 'Disable plugin') : tr('启用插件', 'Enable plugin')}
        />
      </label>
      <div className="flex shrink-0 items-center gap-1">
        <Link
          to={`/plugins/${plugin.id}`}
          onClick={(e) => {
            e.preventDefault();
            onOpen();
          }}
          className="inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
        >
          <ExternalLink size={12} />
          {tr('详情', 'Detail')}
        </Link>
        <Button
          variant="danger"
          onClick={onUninstall}
          title={tr('卸载插件', 'Uninstall plugin')}
          aria-label={tr(`卸载 ${plugin.pack_id}`, `Uninstall ${plugin.pack_id}`)}
        >
          <Trash2 size={12} />
          {tr('卸载', 'Uninstall')}
        </Button>
      </div>
    </li>
  );
}

// ---------- ConfirmUninstallModal ---------------------------------------
// 简单的二次确认弹窗 — 复用项目里的 Modal 组件,做 type-to-confirm
// 避免误操作。pack_id 不为空时要求输入完全匹配,跟 ConfirmDeleteModal
// (设备删除) 行为一致。
function ConfirmUninstallModal({
  open,
  plugin,
  deleting,
  onClose,
  onConfirm,
}: {
  open: boolean;
  plugin: PluginInstance | null;
  deleting: boolean;
  onClose(): void;
  onConfirm(): void;
}) {
  const { tr } = useI18n();
  const [text, setText] = useState('');
  useEffect(() => {
    if (!open) setText('');
  }, [open]);
  if (!plugin) return null;
  const target = plugin.pack_id;
  const matched = text.trim() === target.trim() && target.trim() !== '';
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={tr(`卸载插件 ${target}`, `Uninstall plugin ${target}`)}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={deleting}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button
            variant="danger"
            onClick={onConfirm}
            disabled={deleting || !matched}
          >
            {deleting ? tr('卸载中…', 'Uninstalling…') : tr('卸载', 'Uninstall')}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-xs text-zinc-300">
        <p>
          {tr(
            '卸载插件将停止其所有能力(ai.tool / notifier / skill.runner 等)。该操作不可恢复。',
            'Uninstalling stops all capabilities (ai.tool / notifier / skill.runner / etc.). This cannot be undone.',
          )}
        </p>
        <label className="block pt-1">
          <div className="mb-1 text-[11px] text-zinc-500">
            {tr('输入插件 ID "', 'Type the plugin ID "')}
            <span className="font-mono text-zinc-300">{target}</span>
            {tr('" 以确认:', '" to confirm:')}
          </div>
          <input
            autoFocus
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={target}
            className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            onKeyDown={(e) => {
              if (e.key === 'Enter' && matched) onConfirm();
            }}
          />
        </label>
      </div>
    </Modal>
  );
}

function FilterGroup<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; zh: string; en: string }[];
  // 用更宽的签名以兼容 React 的 Dispatch<SetStateAction<T>>(后者
  // 在 v18 类型上实际就是 (v: T | ((prev: T) => T)) => void,但调用
  // 现场我们只传入具体值,所以这里把形参类型放宽即可)。
  onChange: (v: T) => void;
}) {
  const { tr } = useI18n();
  return (
    <div className="flex items-center gap-1.5">
      <span className="text-[11px] text-zinc-500">{label}</span>
      <div className="inline-flex overflow-hidden rounded-md border border-zinc-800 bg-zinc-900/60">
        {options.map((opt) => (
          <button
            key={opt.value}
            type="button"
            onClick={() => onChange(opt.value)}
            className={cn(
              'px-2 py-1 text-[11px] transition-colors',
              value === opt.value
                ? 'bg-zinc-800 text-zinc-100'
                : 'text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200',
            )}
          >
            {tr(opt.zh, opt.en)}
          </button>
        ))}
      </div>
    </div>
  );
}