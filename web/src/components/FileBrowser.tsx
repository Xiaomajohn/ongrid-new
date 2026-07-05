// FileBrowser.tsx — SFTP-style file tree proxied via the edge agent.
//
// Wire path: every action goes through one of the fsXxx helpers in
// api/devices.ts (fsList / fsRead / fsWrite / fsMkdir / fsRmdir / fsRm /
// fsRename / fsChmod / fsUpload / fsRead + fsDownloadURL for downloads).
// The browser itself is "dumb" — it owns navigation state and renders
// rows; the API surface is the integration boundary.
//
// We deliberately do NOT use the browser's native <a download> for
// downloads because it bypasses the auth header; instead we fetch via
// fsRead() (authed), wrap in a Blob, and trigger an Object URL.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  ChevronRight,
  File as FileIcon,
  Folder as FolderIcon,
  RefreshCw,
  Upload,
  FolderPlus,
  ClipboardPaste,
  Download,
  Eye,
  Edit3,
  Trash2,
  MoreVertical,
} from 'lucide-react';
import { cn } from '@/lib/cn';
import { useI18n } from '@/i18n/locale';
import { Modal } from './Modal';
import { Button } from './ui/Button';
import {
  fsChmod,
  fsList,
  fsMkdir,
  fsRead,
  fsRename,
  fsRm,
  fsRmdir,
  fsUpload,
  fsWrite,
  type FSEntry,
} from '@/api/devices';

type Props = {
  deviceId: string | number;
  initialPath?: string;
};

type Clipboard =
  | { kind: 'cut'; srcPath: string; isDir: boolean }
  | null;

// fsDownload is bundled here so FileBrowser doesn't import `api/client`
// directly; we already pull fsRead in, which encapsulates the auth
// header dance.
async function fsDownload(deviceId: string | number, path: string, name: string) {
  const blob = await fsRead(deviceId, path);
  const url = URL.createObjectURL(blob);
  try {
    const a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
  } finally {
    URL.revokeObjectURL(url);
  }
}

function joinPath(parent: string, child: string): string {
  if (!parent || parent === '/') return child.startsWith('/') ? child : `/${child}`;
  if (parent.endsWith('/')) return parent + child;
  return `${parent}/${child}`;
}

function basename(p: string): string {
  const cleaned = p.endsWith('/') ? p.slice(0, -1) : p;
  const i = cleaned.lastIndexOf('/');
  return i < 0 ? cleaned : cleaned.slice(i + 1);
}

function parentPath(p: string): string {
  const cleaned = p.endsWith('/') && p !== '/' ? p.slice(0, -1) : p;
  const i = cleaned.lastIndexOf('/');
  if (i <= 0) return '/';
  return cleaned.slice(0, i);
}

function formatMode(mode: number): string {
  // Render as 9-char rwxrwxrwx so operators can read at a glance.
  const perms = [
    [0o400, 'r'], [0o200, 'w'], [0o100, 'x'],
    [0o040, 'r'], [0o020, 'w'], [0o010, 'x'],
    [0o004, 'r'], [0o002, 'w'], [0o001, 'x'],
  ] as const;
  let s = '';
  for (const [bit, ch] of perms) s += mode & bit ? ch : '-';
  return s;
}

function formatSize(n: number): string {
  if (n === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let v = Math.abs(n);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[i]}`;
}

function formatMtime(ms: number): string {
  if (!ms) return '—';
  return new Date(ms * 1000).toLocaleString();
}

export function FileBrowser({ deviceId, initialPath = '/' }: Props) {
  const { tr } = useI18n();
  const [path, setPath] = useState<string>(initialPath);
  const [entries, setEntries] = useState<FSEntry[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [clipboard, setClipboard] = useState<Clipboard>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      const r = await fsList(deviceId, path);
      setEntries(r.entries ?? []);
    } catch (e) {
      setErr((e as Error).message || tr('加载失败', 'Load failed'));
      setEntries([]);
    } finally {
      setLoading(false);
    }
  }, [deviceId, path]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Path breadcrumb: split on "/" and synthesize segments.
  const crumbs = useMemo(() => {
    const parts = path.split('/').filter(Boolean);
    const out: Array<{ label: string; path: string }> = [{ label: '/', path: '/' }];
    let acc = '';
    for (const p of parts) {
      acc += '/' + p;
      out.push({ label: p, path: acc });
    }
    return out;
  }, [path]);

  // --- top-level actions ---

  const onUpload = useCallback(
    async (files: FileList | null) => {
      if (!files || files.length === 0) return;
      try {
        for (const f of Array.from(files)) {
          await fsUpload(deviceId, joinPath(path, f.name), f);
        }
        await refresh();
      } catch (e) {
        setErr((e as Error).message || tr('上传失败', 'Upload failed'));
      }
    },
    [deviceId, path, refresh],
  );

  const onMkdir = useCallback(async () => {
    const name = window.prompt(tr('新建文件夹名称', 'New folder name'));
    if (!name) return;
    try {
      await fsMkdir(deviceId, joinPath(path, name));
      await refresh();
    } catch (e) {
      setErr((e as Error).message || tr('创建失败', 'Create failed'));
    }
  }, [deviceId, path, refresh]);

  const onPaste = useCallback(async () => {
    if (!clipboard) return;
    try {
      const dst = joinPath(path, basename(clipboard.srcPath));
      if (clipboard.isDir) {
        await fsRename(deviceId, clipboard.srcPath, dst);
      } else {
        // For "cut + paste" of files we currently use fsRename. A real
        // move across filesystems would need copy + delete; backend
        // should resolve that. Until then, just call rename and surface
        // any error verbatim.
        await fsRename(deviceId, clipboard.srcPath, dst);
      }
      setClipboard(null);
      await refresh();
    } catch (e) {
      setErr((e as Error).message || tr('粘贴失败', 'Paste failed'));
    }
  }, [clipboard, deviceId, path, refresh]);

  return (
    <div className="flex flex-col gap-3">
      {/* Path + toolbar */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-zinc-800/60 bg-zinc-900/40 px-3 py-2 text-xs">
        <div className="flex flex-wrap items-center gap-1 font-mono">
          {crumbs.map((c, i) => (
            <span key={c.path} className="flex items-center gap-1">
              {i > 0 && <span className="text-zinc-600">/</span>}
              <button
                type="button"
                onClick={() => setPath(c.path)}
                className="rounded px-1 py-0.5 text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
              >
                {c.label}
              </button>
            </span>
          ))}
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-1.5">
          <button
            type="button"
            onClick={() => void refresh()}
            title={tr('刷新', 'Refresh')}
            className="inline-flex items-center gap-1 rounded-md border border-zinc-700 bg-zinc-900 px-2 py-1 text-zinc-300 hover:bg-zinc-800"
          >
            <RefreshCw size={12} /> {tr('刷新', 'Refresh')}
          </button>
          <button
            type="button"
            onClick={onMkdir}
            title={tr('新建文件夹', 'New folder')}
            className="inline-flex items-center gap-1 rounded-md border border-zinc-700 bg-zinc-900 px-2 py-1 text-zinc-300 hover:bg-zinc-800"
          >
            <FolderPlus size={12} /> {tr('新建文件夹', 'New folder')}
          </button>
          <button
            type="button"
            onClick={() => fileInputRef.current?.click()}
            title={tr('上传文件', 'Upload')}
            className="inline-flex items-center gap-1 rounded-md border border-zinc-700 bg-zinc-900 px-2 py-1 text-zinc-300 hover:bg-zinc-800"
          >
            <Upload size={12} /> {tr('上传', 'Upload')}
          </button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            hidden
            onChange={(e) => void onUpload(e.target.files)}
          />
          <button
            type="button"
            onClick={() => void onPaste()}
            disabled={!clipboard}
            title={tr('粘贴', 'Paste')}
            className={cn(
              'inline-flex items-center gap-1 rounded-md border border-zinc-700 bg-zinc-900 px-2 py-1 text-zinc-300 hover:bg-zinc-800',
              !clipboard && 'cursor-not-allowed opacity-40 hover:bg-zinc-900',
            )}
          >
            <ClipboardPaste size={12} /> {tr('粘贴', 'Paste')}
            {clipboard && <span className="font-mono text-[10px] text-zinc-500">{basename(clipboard.srcPath)}</span>}
          </button>
        </div>
      </div>

      {err && (
        <div
          role="alert"
          className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300"
        >
          {err}
        </div>
      )}

      {/* Listing table */}
      <div className="overflow-hidden rounded-xl border border-zinc-800/60 bg-zinc-900/40">
        <table className="w-full text-sm">
          <thead className="border-b border-zinc-800/60 bg-zinc-950/40 text-[11px] uppercase tracking-wider text-zinc-500">
            <tr>
              <th className="px-4 py-2.5 text-left">{tr('名称', 'Name')}</th>
              <th className="px-4 py-2.5 text-left">{tr('权限', 'Mode')}</th>
              <th className="px-4 py-2.5 text-right">{tr('大小', 'Size')}</th>
              <th className="px-4 py-2.5 text-left">{tr('修改时间', 'Modified')}</th>
              <th className="px-4 py-2.5 text-right">{tr('操作', 'Actions')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-zinc-800/40">
            {path !== '/' && (
              <tr
                key=".."
                className="cursor-pointer hover:bg-zinc-900/40"
                onDoubleClick={() => setPath(parentPath(path))}
              >
                <td className="px-4 py-2.5 font-mono text-zinc-400" colSpan={5}>
                  ..
                </td>
              </tr>
            )}
            {loading && entries.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-4 py-10 text-center text-zinc-500">
                  {tr('加载中…', 'Loading…')}
                </td>
              </tr>
            ) : entries.length === 0 ? (
              <tr>
                <td colSpan={5} className="px-4 py-10 text-center text-zinc-500">
                  {tr('空目录', 'Empty directory')}
                </td>
              </tr>
            ) : (
              entries.map((e) => (
                <Row
                  key={e.name}
                  entry={e}
                  currentPath={path}
                  canNavigate={(p) => setPath(p)}
                  onAfterChange={refresh}
                  onCut={(p) => setClipboard({ kind: 'cut', srcPath: p, isDir: e.is_dir })}
                  deviceId={deviceId}
                />
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Row({
  entry,
  currentPath,
  canNavigate,
  onAfterChange,
  onCut,
  deviceId,
}: {
  entry: FSEntry;
  currentPath: string;
  canNavigate(p: string): void;
  onAfterChange(): Promise<void> | void;
  onCut(p: string): void;
  deviceId: string | number;
}) {
  const { tr } = useI18n();
  const fullPath = joinPath(currentPath, entry.name);
  const [menuOpen, setMenuOpen] = useState(false);
  const [readOpen, setReadOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);

  return (
    <tr
      className="hover:bg-zinc-900/40"
      onDoubleClick={() => entry.is_dir && canNavigate(fullPath)}
    >
      <td className="whitespace-nowrap px-4 py-2.5">
        <button
          type="button"
          onClick={() => entry.is_dir && canNavigate(fullPath)}
          className="inline-flex items-center gap-2 text-left text-zinc-100 hover:text-zinc-50"
          title={entry.is_dir ? tr('双击进入', 'Double-click to open') : entry.name}
        >
          {entry.is_dir ? (
            <FolderIcon size={14} className="text-amber-300" />
          ) : (
            <FileIcon size={14} className="text-zinc-500" />
          )}
          <span className={cn(entry.is_dir ? 'font-medium' : 'font-mono text-xs')}>
            {entry.name}
          </span>
          {entry.is_dir && <ChevronRight size={11} className="text-zinc-600" />}
        </button>
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
        {formatMode(entry.mode)}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right font-mono text-xs text-zinc-400">
        {entry.is_dir ? '—' : formatSize(entry.size)}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
        {formatMtime(entry.mtime)}
      </td>
      <td className="whitespace-nowrap px-4 py-2.5 text-right">
        {!entry.is_dir && (
          <>
            <button
              type="button"
              onClick={() => setReadOpen(true)}
              title={tr('查看', 'View')}
              className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
            >
              <Eye size={12} /> {tr('查看', 'View')}
            </button>
            <button
              type="button"
              onClick={() => setEditOpen(true)}
              title={tr('编辑', 'Edit')}
              className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
            >
              <Edit3 size={12} /> {tr('编辑', 'Edit')}
            </button>
            <button
              type="button"
              onClick={() => void fsDownload(deviceId, fullPath, entry.name)}
              title={tr('下载', 'Download')}
              className="mr-1 inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs text-zinc-300 hover:bg-zinc-800 hover:text-zinc-100"
            >
              <Download size={12} /> {tr('下载', 'Download')}
            </button>
          </>
        )}
        <div className="relative inline-block">
          <button
            type="button"
            onClick={() => setMenuOpen((v) => !v)}
            title={tr('更多', 'More')}
            className="inline-flex items-center rounded-md px-1.5 py-1 text-xs text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
          >
            <MoreVertical size={12} />
          </button>
          {menuOpen && (
            <div
              className="absolute right-0 z-10 mt-1 w-40 rounded-md border border-zinc-700 bg-zinc-900 py-1 text-xs shadow-lg"
              onMouseLeave={() => setMenuOpen(false)}
            >
              <button
                type="button"
                onClick={async () => {
                  setMenuOpen(false);
                  const next = window.prompt(tr('重命名为', 'Rename to'), entry.name);
                  if (!next || next === entry.name) return;
                  try {
                    await fsRename(deviceId, fullPath, joinPath(currentPath, next));
                    await onAfterChange();
                  } catch (e) {
                    alert((e as Error).message);
                  }
                }}
                className="block w-full px-3 py-1.5 text-left text-zinc-200 hover:bg-zinc-800"
              >
                {tr('重命名', 'Rename')}
              </button>
              <button
                type="button"
                onClick={async () => {
                  setMenuOpen(false);
                  const m = window.prompt(
                    tr(
                      'chmod（八进制 0-7777）',
                      'chmod (octal 0-7777)',
                    ),
                    String(entry.mode & 0o7777),
                  );
                  if (m == null) return;
                  const num = parseInt(m, 8);
                  if (!Number.isFinite(num)) return;
                  try {
                    await fsChmod(deviceId, fullPath, num);
                    await onAfterChange();
                  } catch (e) {
                    alert((e as Error).message);
                  }
                }}
                className="block w-full px-3 py-1.5 text-left text-zinc-200 hover:bg-zinc-800"
              >
                {tr('chmod…', 'chmod…')}
              </button>
              <button
                type="button"
                onClick={() => {
                  setMenuOpen(false);
                  onCut(fullPath);
                }}
                className="block w-full px-3 py-1.5 text-left text-zinc-200 hover:bg-zinc-800"
              >
                {tr('剪切', 'Cut')}
              </button>
              <button
                type="button"
                onClick={async () => {
                  setMenuOpen(false);
                  if (
                    !window.confirm(
                      tr(
                        `确认删除 ${entry.is_dir ? '文件夹' : '文件'} "${entry.name}"？`,
                        `Delete ${entry.is_dir ? 'folder' : 'file'} "${entry.name}"?`,
                      ),
                    )
                  )
                    return;
                  try {
                    if (entry.is_dir) await fsRmdir(deviceId, fullPath);
                    else await fsRm(deviceId, fullPath);
                    await onAfterChange();
                  } catch (e) {
                    alert((e as Error).message);
                  }
                }}
                className="block w-full px-3 py-1.5 text-left text-red-300 hover:bg-red-500/10"
              >
                {tr('删除', 'Delete')}
              </button>
            </div>
          )}
        </div>
      </td>
      {readOpen && (
        <ReadModal
          deviceId={deviceId}
          path={fullPath}
          name={entry.name}
          onClose={() => setReadOpen(false)}
        />
      )}
      {editOpen && (
        <WriteModal
          deviceId={deviceId}
          path={fullPath}
          name={entry.name}
          onClose={() => setEditOpen(false)}
          onSaved={() => void onAfterChange()}
        />
      )}
    </tr>
  );
}

function ReadModal({
  deviceId,
  path,
  name,
  onClose,
}: {
  deviceId: string | number;
  path: string;
  name: string;
  onClose(): void;
}) {
  const { tr } = useI18n();
  const [content, setContent] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    fsRead(deviceId, path)
      .then(async (blob) => {
        if (cancelled) return;
        const text = await blob.text();
        if (cancelled) return;
        setContent(text);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr((e as Error).message || tr('读取失败', 'Read failed'));
      });
    return () => {
      cancelled = true;
    };
  }, [deviceId, path]);

  return (
    <Modal
      open
      onClose={onClose}
      title={`${tr('查看', 'View')} · ${name}`}
      size="lg"
      footer={
        <Button variant="ghost" onClick={onClose}>
          {tr('关闭', 'Close')}
        </Button>
      }
    >
      {err ? (
        <div className="rounded border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {err}
        </div>
      ) : (
        <pre className="max-h-[60vh] overflow-auto rounded border border-zinc-800 bg-zinc-950 px-3 py-2 font-mono text-[11px] whitespace-pre-wrap break-all text-zinc-100">
          {content ?? tr('加载中…', 'Loading…')}
        </pre>
      )}
    </Modal>
  );
}

function WriteModal({
  deviceId,
  path,
  name,
  onClose,
  onSaved,
}: {
  deviceId: string | number;
  path: string;
  name: string;
  onClose(): void;
  onSaved(): void;
}) {
  const { tr } = useI18n();
  const [content, setContent] = useState<string | null>(null);
  const [draft, setDraft] = useState<string>('');
  const [err, setErr] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    fsRead(deviceId, path)
      .then(async (blob) => {
        if (cancelled) return;
        const text = await blob.text();
        if (cancelled) return;
        setContent(text);
        setDraft(text);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr((e as Error).message || tr('读取失败', 'Read failed'));
      });
    return () => {
      cancelled = true;
    };
  }, [deviceId, path]);

  const save = async () => {
    setSaving(true);
    setErr(null);
    try {
      await fsWrite(deviceId, path, draft);
      onSaved();
      onClose();
    } catch (e) {
      setErr((e as Error).message || tr('保存失败', 'Save failed'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={`${tr('编辑', 'Edit')} · ${name}`}
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button variant="primary" onClick={save} disabled={saving || content === null}>
            {saving ? tr('保存中…', 'Saving…') : tr('保存', 'Save')}
          </Button>
        </>
      }
    >
      {err ? (
        <div className="rounded border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          {err}
        </div>
      ) : (
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          spellCheck={false}
          rows={20}
          className="w-full rounded border border-zinc-800 bg-zinc-950 px-3 py-2 font-mono text-[11px] text-zinc-100 focus:border-zinc-600 focus:outline-none"
        />
      )}
    </Modal>
  );
}