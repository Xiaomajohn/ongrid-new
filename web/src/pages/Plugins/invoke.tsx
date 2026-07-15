// web/src/pages/Plugins/invoke.tsx — 试运行一个 plugin capability
//
// 设计要点:
//   - Phase 2 阶段 cap 的 JSON schema 解析未做(plan §14.2 T13 "前端联调"
//     时再做 form 生成);这里退化为 textarea 输 JSON,跟 SkillRun.tsx
//     的 inventory-only 分支行为一致。
//   - 左侧:JSON params textarea + "调用"按钮;右侧:pretty-print 结果 + latency_ms +
//     错误高亮。
//   - 复用 PageHeader / Card / Button,跟其他 page 保持视觉一致。
//   - useSearchParams 读 ?cap=<name>;useParams 拿 id。两者都需要。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router-dom';
import { ChevronLeft, Play, Loader2, AlertTriangle, Clock } from 'lucide-react';
import { PageHeader, Card, Button } from '@/components/ui';
import { cn } from '@/lib/cn';
import {
  getPlugin,
  invokeCapability,
  type PluginInstance,
  type InvokeResult,
} from '@/api/pluginhost';
import { ApiError } from '@/api/client';
import { tr as trInline, useI18n } from '@/i18n/locale';

const SAMPLE_PARAMS = `{
  "foo": "bar"
}`;

export default function PluginInvokePage() {
  const { tr } = useI18n();
  const params = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  const id = useMemo(() => {
    const n = parseInt(params.id ?? '', 10);
    return Number.isFinite(n) ? n : 0;
  }, [params.id]);
  const capName = searchParams.get('cap') ?? '';

  const [plugin, setPlugin] = useState<PluginInstance | null>(null);
  const [paramsText, setParamsText] = useState(SAMPLE_PARAMS);
  const [executing, setExecuting] = useState(false);
  const [latest, setLatest] = useState<InvokeResult | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);

  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    getPlugin(id)
      .then((p) => {
        if (!cancelled) setPlugin(p);
      })
      .catch((e) => {
        if (cancelled) return;
        setLoadErr(e instanceof ApiError ? e.message : (e as Error).message);
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  const onInvoke = useCallback(async () => {
    if (!id || !capName) return;
    let parsed: unknown = {};
    if (paramsText.trim().length > 0) {
      try {
        parsed = JSON.parse(paramsText);
      } catch (e) {
        setLatest({
          error: trInline(
            `参数 JSON 解析失败：${(e as Error).message}`,
            `Failed to parse params JSON: ${(e as Error).message}`,
          ),
          latency_ms: 0,
        });
        return;
      }
    }
    setExecuting(true);
    setLatest(null);
    try {
      const r = await invokeCapability(id, capName, parsed);
      setLatest(r);
    } catch (e) {
      setLatest({
        error: e instanceof ApiError ? e.message : (e as Error).message,
        latency_ms: 0,
      });
    } finally {
      setExecuting(false);
    }
  }, [id, capName, paramsText]);

  if (!id) {
    return (
      <main className="anim-fade flex flex-1 items-center justify-center text-sm text-zinc-500">
        {tr('无效的插件 ID', 'Invalid plugin ID')}
      </main>
    );
  }
  if (!capName) {
    return (
      <main className="anim-fade flex flex-1 flex-col overflow-hidden">
        <PageHeader
          leading={
            <Link
              to={`/plugins/${id}`}
              className="inline-flex items-center gap-1 text-zinc-400 hover:text-zinc-200"
            >
              <ChevronLeft size={12} />
              {tr('返回详情', 'Back to detail')}
            </Link>
          }
          title={tr('试运行', 'Invoke')}
        />
        <div className="flex-1 overflow-y-auto px-6 py-6">
          <Card>
            <div className="flex items-center gap-2 py-6 text-sm text-amber-300">
              <AlertTriangle size={14} />
              {tr(
                '请在 URL 上指定 cap 参数：/plugins/:id/invoke?cap=<name>',
                'Please specify cap in URL: /plugins/:id/invoke?cap=<name>',
              )}
            </div>
          </Card>
        </div>
      </main>
    );
  }

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <PageHeader
        leading={
          <Link
            to={`/plugins/${id}`}
            className="inline-flex items-center gap-1 text-zinc-400 hover:text-zinc-200"
          >
            <ChevronLeft size={12} />
            {tr('返回详情', 'Back to detail')}
          </Link>
        }
        title={
          <span className="flex items-baseline gap-2">
            <span className="text-zinc-100">{tr('试运行', 'Invoke')}:</span>
            <span className="font-mono text-zinc-100">{capName}</span>
            {plugin && (
              <span className="font-mono text-[11px] text-zinc-500">
                {plugin.pack_id} · {plugin.version}
              </span>
            )}
          </span>
        }
        subtitle={
          loadErr
            ? tr(`加载失败：${loadErr}`, `Load failed: ${loadErr}`)
            : plugin
              ? tr(
                  `${plugin.capabilities_count} 个能力 · source=${plugin.source}`,
                  `${plugin.capabilities_count} cap(s) · source=${plugin.source}`,
                )
              : tr('加载中…', 'Loading…')
        }
      />

      <div className="flex-1 overflow-y-auto px-6 py-6">
        <div className="grid gap-4 lg:grid-cols-2">
          {/* left: params */}
          <Card>
            <div className="mb-3 flex items-center justify-between">
              <div className="text-xs font-semibold uppercase tracking-wider text-zinc-400">
                {tr('参数（JSON）', 'Params (JSON)')}
              </div>
              <button
                type="button"
                onClick={() => setParamsText(SAMPLE_PARAMS)}
                className="text-[11px] text-zinc-500 hover:text-zinc-300"
                title={tr('重置示例', 'Reset to sample')}
              >
                {tr('重置示例', 'Reset to sample')}
              </button>
            </div>
            <textarea
              value={paramsText}
              onChange={(e) => setParamsText(e.target.value)}
              spellCheck={false}
              className="h-72 w-full resize-y rounded-md border border-zinc-800 bg-zinc-950/60 px-3 py-2 font-mono text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              placeholder="{}"
            />
            <div className="mt-3 flex items-center justify-end">
              <Button
                variant="primary"
                onClick={() => void onInvoke()}
                disabled={executing || !plugin}
                title={tr('试运行', 'Invoke')}
              >
                {executing ? <Loader2 size={12} className="animate-spin" /> : <Play size={12} />}
                {executing ? tr('执行中…', 'Running…') : tr('调用', 'Invoke')}
              </Button>
            </div>
          </Card>

          {/* right: result */}
          <Card className="flex min-h-[260px] flex-col">
            <div className="mb-3 flex items-center justify-between">
              <div className="text-xs font-semibold uppercase tracking-wider text-zinc-400">
                {tr('结果', 'Result')}
              </div>
              {latest && (
                <span className="inline-flex items-center gap-1 text-[11px] text-zinc-500">
                  <Clock size={11} />
                  {tr(`耗时 ${latest.latency_ms}ms`, `${latest.latency_ms}ms`)}
                </span>
              )}
            </div>
            <ResultPanel executing={executing} latest={latest} />
          </Card>
        </div>
      </div>
    </main>
  );
}

function ResultPanel({
  executing,
  latest,
}: {
  executing: boolean;
  latest: InvokeResult | null;
}) {
  const { tr } = useI18n();
  if (executing) {
    return (
      <div className="flex h-32 items-center justify-center gap-2 text-sm text-zinc-400">
        <Loader2 size={14} className="animate-spin" />
        {tr('执行中…', 'Running…')}
      </div>
    );
  }
  if (!latest) {
    return (
      <div className="flex h-32 items-center justify-center text-xs text-zinc-500">
        {tr('尚未执行', 'Not run yet')}
      </div>
    );
  }
  if (latest.error) {
    return (
      <div className="rounded-md border border-red-500/40 bg-red-500/5 px-3 py-2 text-xs text-red-300">
        <div className="font-medium">{tr('执行失败', 'Run failed')}</div>
        <pre
          className={cn(
            'mt-1 whitespace-pre-wrap break-words font-mono text-[11px]',
          )}
        >
          {latest.error}
        </pre>
      </div>
    );
  }
  return (
    <div className="overflow-x-auto rounded-md border border-zinc-800 bg-zinc-950/60 px-3 py-2">
      <pre className="whitespace-pre-wrap break-words font-mono text-xs text-zinc-200">
        {formatResult(latest.result)}
      </pre>
    </div>
  );
}

function formatResult(result: unknown): string {
  if (result === undefined) return trInline('(无返回)', '(no return value)');
  if (typeof result === 'string') return result;
  try {
    return JSON.stringify(result, null, 2);
  } catch {
    return String(result);
  }
}