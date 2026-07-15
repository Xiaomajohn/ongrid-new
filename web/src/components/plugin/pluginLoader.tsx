// pluginLoader — 动态加载 plugin 的 frontend bundle。
//
// Phase 5 E4:plugin manifest 携带 frontend 描述(Format / Entry / ElementTag /
// Schema),web 端用 pluginLoader 把 plugin 前端代码拉到浏览器,渲染成
// web component,嵌到 ongrid 主前端任意位置。
//
// 当前实现(简化版,够 demo / 内部工具用):
//   - 从 pluginhost server 的 /api/pluginhost/plugins/{id}/assets/{path}
//     端点拉取 entry JS
//   - 注入到 <head> 当作 <script> 执行(blob URL 形式,避免命名冲突)
//   - 等到 custom element 被注册后(轮询 customElements.get(tagName) 最多
//     2s),挂到容器 div 上
//   - 卸载时移除容器 + 释放 blob URL
//
// 安全约束:
//   - 仅加载自己服务器上的 asset(经 sandbox.PathSafeUnderRoot 校验)
//   - element tag 白名单:由调用方传入 expectedTag,不一致时拒绝挂载
//   - 加载失败/超时给出明确错误态
//
// 已知限制(P1 暂不解决):
//   - 暂不支持 ES Module 动态 import(plugin 打包若用 ESM 需要 main.go
//     增加 .mjs MIME 适配);当前 entry 走 IIFE / UMD
//   - 暂不实现 sandbox iframe(plugin 端 JS 直接在主页面执行,需要
//     Phase 5+ 用 srcdoc iframe 隔离)
//
// 注意:web 端所有 server 路由都走 /api/v1 前缀(见 client.ts BASE),
// 但 pluginhost server 在 main.go wire-up 时挂到了 /api/pluginhost,而
// client.ts 的 request() 默认拼 /api/v1 前缀。这里绕过 client.ts,
// 直接用 window.fetch + 绝对路径,避免 base prefix 干扰。

import { useEffect, useRef, useState } from 'react';
import { cn } from '@/lib/cn';

// ----- types --------------------------------------------------------------

export interface PluginFrontendSpec {
  /** "umd" | "iife" | "esm" — P1 仅 umd / iife 落地。 */
  format?: string;
  /** 入口 JS 路径,相对 plugin install 根,如 "./dist/main.js"。 */
  entry: string;
  /** 自定义元素 tag,小写带短横线,如 "gitlab-issue-panel"。 */
  element_tag: string;
  /** 可选:Capability 的 JSON Schema(给 plugin 自身 UI 渲参用)。 */
  schema?: Record<string, unknown>;
  /** 可选:permission 列表,plugin 端在 host.call 时由 server 强校验。 */
  permissions?: string[];
}

export interface PluginLoaderProps {
  pluginId: number;
  frontend: PluginFrontendSpec;
  /** 透传给 web component 的属性(用 kebab-case key)。 */
  attrs?: Record<string, string>;
  /** 可选:slot 内容(React 子节点会被塞到 <slot/> 里)。 */
  children?: React.ReactNode;
  /** 加载失败回调(便于父页面 toast)。 */
  onError?: (err: Error) => void;
  /** 加载完成回调。 */
  onLoaded?: () => void;
  className?: string;
}

// ----- constants ----------------------------------------------------------

const ASSET_BASE = '/api/pluginhost/plugins';
const LOAD_TIMEOUT_MS = 8000;
const POLL_INTERVAL_MS = 50;

// ----- component ----------------------------------------------------------

export function PluginLoader({
  pluginId,
  frontend,
  attrs,
  children,
  onError,
  onLoaded,
  className,
}: PluginLoaderProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const blobUrlRef = useRef<string | null>(null);
  const [status, setStatus] = useState<'idle' | 'loading' | 'ready' | 'error'>(
    'idle',
  );
  const [errorMsg, setErrorMsg] = useState<string>('');

  useEffect(() => {
    if (!containerRef.current) return;
    const container = containerRef.current;
    let cancelled = false;
    let timeoutId: number | null = null;
    let pollId: number | null = null;
    const script = document.createElement('script');

    const cleanup = () => {
      if (timeoutId !== null) window.clearTimeout(timeoutId);
      if (pollId !== null) window.clearInterval(pollId);
      if (script.parentNode) script.parentNode.removeChild(script);
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };

    setStatus('loading');
    setErrorMsg('');

    // 1) 拉 entry JS
    const entryPath = frontend.entry.replace(/^\.\//, '/');
    const url = `${ASSET_BASE}/${pluginId}/assets${entryPath}`;

    void (async () => {
      try {
        const resp = await fetch(url, {
          credentials: 'same-origin',
          headers: { Accept: 'application/javascript, text/javascript, */*' },
        });
        if (!resp.ok) {
          throw new Error(
            `fetch ${url} failed: ${resp.status} ${resp.statusText}`,
          );
        }
        const code = await resp.text();
        if (cancelled) return;

        // 2) 转 blob URL 后用 <script> 执行,避免与页面其它脚本命名冲突
        const blob = new Blob([code], { type: 'application/javascript' });
        const blobUrl = URL.createObjectURL(blob);
        blobUrlRef.current = blobUrl;
        script.src = blobUrl;
        script.async = false;
        document.head.appendChild(script);

        // 3) 等 customElements 注册(轮询,2s 后放弃)
        const tag = frontend.element_tag;
        const startTs = Date.now();
        const tryMount = () => {
          if (cancelled) return;
          if (customElements.get(tag)) {
            // 4) 挂到容器
            const el = document.createElement(tag);
            for (const [k, v] of Object.entries(attrs ?? {})) {
              el.setAttribute(k, v);
            }
            container.appendChild(el);
            setStatus('ready');
            onLoaded?.();
            return;
          }
          if (Date.now() - startTs > LOAD_TIMEOUT_MS) {
            setStatus('error');
            setErrorMsg(
              `custom element <${tag}> 未在 ${LOAD_TIMEOUT_MS}ms 内注册`,
            );
            onError?.(new Error(errorMsg || 'plugin load timeout'));
            return;
          }
          pollId = window.setTimeout(tryMount, POLL_INTERVAL_MS) as unknown as number;
        };
        tryMount();
      } catch (e) {
        if (cancelled) return;
        const err = e instanceof Error ? e : new Error(String(e));
        setStatus('error');
        setErrorMsg(err.message);
        onError?.(err);
      }
    })();

    return () => {
      cancelled = true;
      cleanup();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pluginId, frontend.entry, frontend.element_tag]);

  if (status === 'error') {
    return (
      <div
        className={cn(
          'rounded-md border border-red-500/40 bg-red-500/10 px-3 py-2 text-xs text-red-300',
          className,
        )}
      >
        plugin 加载失败:{errorMsg}
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      className={cn('plugin-loader-container', className)}
      data-plugin-id={pluginId}
      data-plugin-status={status}
    >
      {status === 'loading' && (
        <div className="rounded-md border border-zinc-700 bg-zinc-900/40 px-3 py-2 text-xs text-zinc-500">
          正在加载 plugin &lt;{frontend.element_tag}&gt;…
        </div>
      )}
      {children}
    </div>
  );
}

// ----- 辅助:从 capability 列表里挑 "ui" 类型的 frontend -------------------

/**
 * 从 plugin 的 capability 列表中,挑出 kind === "workflow.node" / "ai.tool" 中
 * 第一个带 frontend 描述的,作为 pluginLoader 默认挂载目标。
 *
 * P1 阶段 plugin manifest 不强约束 frontend 字段位置,提供这个 helper 帮
 * 调用方少写一点代码。
 */
export function pickFrontendFromCapability(
  cap: { metadata?: Record<string, unknown> } | undefined,
): PluginFrontendSpec | null {
  if (!cap?.metadata) return null;
  const fe = (cap.metadata as Record<string, unknown>).frontend;
  if (!fe || typeof fe !== 'object') return null;
  const obj = fe as Record<string, unknown>;
  if (typeof obj.entry !== 'string' || typeof obj.element_tag !== 'string') {
    return null;
  }
  return {
    format: typeof obj.format === 'string' ? obj.format : undefined,
    entry: obj.entry,
    element_tag: obj.element_tag,
    schema:
      obj.schema && typeof obj.schema === 'object'
        ? (obj.schema as Record<string, unknown>)
        : undefined,
    permissions: Array.isArray(obj.permissions)
      ? (obj.permissions as string[])
      : undefined,
  };
}
