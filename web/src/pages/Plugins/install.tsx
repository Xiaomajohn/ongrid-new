// web/src/pages/Plugins/install.tsx — 安装插件(上传 / 本地路径 / 远程 URL)
//
// 设计要点:
//   - 复用 PageHeader / Card / Button,跟其他 Plugins 页保持视觉一致。
//   - 三种 source 在同一个 page 上以三个 Card 块并列展示;不互斥,
//     操作员可分别尝试。
//   - 底部 manifest 预览区域:Phase 2 阶段 server 端 list 解析接口
//     还没接,这里展示 form 入参 + 提示文案,等 Phase 4 D1 接入后
//     改成 POST /pluginhost/instances/preview 之类的预解析接口。
//   - 不 import 任何其它 Plugins/* page(避免循环依赖);提交成功后
//     直接 navigate('/plugins/<id>')。
//   - 不修改 Sidebar.tsx (E1 任务)。

import { useCallback, useMemo, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ChevronLeft, Upload, Folder, Link as LinkIcon, Loader2 } from 'lucide-react';
import { PageHeader, Card, Button } from '@/components/ui';
import {
  installPlugin,
  uploadTarball,
  type InstallSpec,
} from '@/api/pluginhost';
import { ApiError } from '@/api/client';
import { useI18n } from '@/i18n/locale';

type Kind = 'tarball' | 'local' | 'remote';

export default function PluginInstallPage() {
  const { tr } = useI18n();
  const navigate = useNavigate();

  // 每个 source 各自的表单 + busy + error state;互不干扰。
  const [file, setFile] = useState<File | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadErr, setUploadErr] = useState<string | null>(null);

  const [localPath, setLocalPath] = useState('');
  const [installingLocal, setInstallingLocal] = useState(false);
  const [localErr, setLocalErr] = useState<string | null>(null);

  const [remoteUrl, setRemoteUrl] = useState('');
  const [installingRemote, setInstallingRemote] = useState(false);
  const [remoteErr, setRemoteErr] = useState<string | null>(null);

  // 顶部"选中的 kind",驱动底部 manifest preview 区域。
  const [active, setActive] = useState<Kind>('tarball');

  // ---------- handlers ---------------------------------------------------

  const onUpload = useCallback(async () => {
    if (!file) {
      setUploadErr(tr('请选择 tarball 文件', 'Pick a tarball file first'));
      return;
    }
    setUploading(true);
    setUploadErr(null);
    try {
      const r = await uploadTarball(file);
      navigate(`/plugins/${r.id}`);
    } catch (e) {
      setUploadErr(e instanceof ApiError ? e.message : (e as Error).message);
    } finally {
      setUploading(false);
    }
  }, [file, navigate, tr]);

  const onInstallLocal = useCallback(async () => {
    const v = localPath.trim();
    if (!v) {
      setLocalErr(tr('请输入本地路径', 'Enter a local path'));
      return;
    }
    setInstallingLocal(true);
    setLocalErr(null);
    const spec: InstallSpec = { source: 'local', path: v };
    try {
      const r = await installPlugin(spec);
      navigate(`/plugins/${r.id}`);
    } catch (e) {
      setLocalErr(e instanceof ApiError ? e.message : (e as Error).message);
    } finally {
      setInstallingLocal(false);
    }
  }, [localPath, navigate, tr]);

  const onInstallRemote = useCallback(async () => {
    const v = remoteUrl.trim();
    if (!v) {
      setRemoteErr(tr('请输入 URL', 'Enter a URL'));
      return;
    }
    if (!/^https?:\/\//i.test(v)) {
      setRemoteErr(
        tr('URL 必须以 http:// 或 https:// 开头', 'URL must start with http:// or https://'),
      );
      return;
    }
    setInstallingRemote(true);
    setRemoteErr(null);
    const spec: InstallSpec = { source: 'remote', url: v };
    try {
      const r = await installPlugin(spec);
      navigate(`/plugins/${r.id}`);
    } catch (e) {
      setRemoteErr(e instanceof ApiError ? e.message : (e as Error).message);
    } finally {
      setInstallingRemote(false);
    }
  }, [remoteUrl, navigate, tr]);

  // ---------- manifest preview 草稿 -------------------------------------
  // Phase 2 阶段没有 preview 接口。这里用 active kind 拼一段描述给操作
  // 员反馈"我刚才在装什么";server 接入后(plan §14.2 T13)换成
  // 真实 manifest 解析结果。
  const previewDraft = useMemo(() => {
    if (active === 'tarball') {
      return file
        ? {
            source: 'tarball',
            filename: file.name,
            size_bytes: file.size,
          }
        : { source: 'tarball', filename: null, size_bytes: 0 };
    }
    if (active === 'local') {
      return { source: 'local', path: localPath.trim() || null };
    }
    return { source: 'remote', url: remoteUrl.trim() || null };
  }, [active, file, localPath, remoteUrl]);

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
        title={tr('安装插件', 'Install Plugin')}
        subtitle={tr(
          '三种方式任选其一;提交成功后跳转到新插件的详情页。',
          'Pick one of the three methods; on success you will land on the new plugin\'s detail page.',
        )}
      />

      <div className="flex-1 overflow-y-auto px-6 py-6">
        <div className="grid gap-4 lg:grid-cols-3">
          {/* 1. upload tarball */}
          <Card
            className={active === 'tarball' ? 'ring-1 ring-indigo-500/40' : ''}
            onClick={() => setActive('tarball')}
          >
            <Header
              icon={<Upload size={14} />}
              title={tr('上传 tarball', 'Upload tarball')}
              hint={tr(
                '支持 .tar / .tar.gz / .zip — 服务器会解压后按 local-dir 流程安装',
                'Supports .tar / .tar.gz / .zip — server unpacks and installs like a local-dir install',
              )}
            />
            <input
              type="file"
              accept=".tar,.gz,.tgz,.zip,application/zip,application/x-gzip"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
              className="mt-3 block w-full text-xs text-zinc-300 file:mr-3 file:rounded-md file:border-0 file:bg-zinc-800 file:px-3 file:py-1.5 file:text-xs file:text-zinc-100 hover:file:bg-zinc-700"
              data-testid="plugins-upload-file"
            />
            {file && (
              <div className="mt-2 truncate text-[11px] text-zinc-500">
                {file.name} ({Math.round(file.size / 1024)} KB)
              </div>
            )}
            {uploadErr && (
              <div className="mt-2 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1.5 text-[11px] text-red-300">
                {uploadErr}
              </div>
            )}
            <div className="mt-4 flex items-center justify-end">
              <Button
                variant="primary"
                onClick={() => void onUpload()}
                disabled={uploading || !file}
                data-testid="plugins-upload-submit"
              >
                {uploading ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Upload size={12} />
                )}
                {uploading ? tr('上传中…', 'Uploading…') : tr('上传', 'Upload')}
              </Button>
            </div>
          </Card>

          {/* 2. local path */}
          <Card
            className={active === 'local' ? 'ring-1 ring-indigo-500/40' : ''}
            onClick={() => setActive('local')}
          >
            <Header
              icon={<Folder size={14} />}
              title={tr('本地目录路径', 'Local directory path')}
              hint={tr(
                'manager 容器能直接访问的绝对路径(如 /var/lib/ongrid-pluginhost/staging/<name>)',
                'Absolute path reachable from the manager container (e.g. /var/lib/ongrid-pluginhost/staging/<name>)',
              )}
            />
            <input
              type="text"
              value={localPath}
              onChange={(e) => setLocalPath(e.target.value)}
              placeholder="/var/lib/ongrid-pluginhost/staging/my-plugin"
              className="mt-3 w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 font-mono text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              data-testid="plugins-local-path"
            />
            {localErr && (
              <div className="mt-2 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1.5 text-[11px] text-red-300">
                {localErr}
              </div>
            )}
            <div className="mt-4 flex items-center justify-end">
              <Button
                variant="primary"
                onClick={() => void onInstallLocal()}
                disabled={installingLocal || !localPath.trim()}
                data-testid="plugins-local-submit"
              >
                {installingLocal ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Folder size={12} />
                )}
                {installingLocal ? tr('安装中…', 'Installing…') : tr('安装', 'Install')}
              </Button>
            </div>
          </Card>

          {/* 3. remote URL */}
          <Card
            className={active === 'remote' ? 'ring-1 ring-indigo-500/40' : ''}
            onClick={() => setActive('remote')}
          >
            <Header
              icon={<LinkIcon size={14} />}
              title={tr('远程 URL', 'Remote URL')}
              hint={tr(
                'manager 容器能下载到的 http(s) tarball 链接',
                'An http(s) tarball URL reachable from the manager container',
              )}
            />
            <input
              type="text"
              value={remoteUrl}
              onChange={(e) => setRemoteUrl(e.target.value)}
              placeholder="https://example.com/plugins/foo-0.1.0.tar.gz"
              className="mt-3 w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 font-mono text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              data-testid="plugins-remote-url"
            />
            {remoteErr && (
              <div className="mt-2 rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1.5 text-[11px] text-red-300">
                {remoteErr}
              </div>
            )}
            <div className="mt-4 flex items-center justify-end">
              <Button
                variant="primary"
                onClick={() => void onInstallRemote()}
                disabled={installingRemote || !remoteUrl.trim()}
                data-testid="plugins-remote-submit"
              >
                {installingRemote ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <LinkIcon size={12} />
                )}
                {installingRemote
                  ? tr('下载并安装中…', 'Downloading…')
                  : tr('下载并安装', 'Download & Install')}
              </Button>
            </div>
          </Card>
        </div>

        {/* manifest preview */}
        <Card className="mt-4">
          <div className="mb-2 flex items-center justify-between">
            <div className="text-xs font-semibold uppercase tracking-wider text-zinc-400">
              {tr('Manifest 预览', 'Manifest preview')}
            </div>
            <span className="text-[10px] text-zinc-500">
              {tr(
                'Phase 2 预览为本地草稿;Phase 4 server 接入后将展示真实 manifest 解析结果',
                'Phase 2 preview is a local draft; Phase 4 server integration will render the real parsed manifest',
              )}
            </span>
          </div>
          <pre className="overflow-x-auto rounded-md border border-zinc-800 bg-zinc-950/60 px-3 py-2 font-mono text-[11px] text-zinc-300">
            {JSON.stringify(previewDraft, null, 2)}
          </pre>
        </Card>
      </div>
    </main>
  );
}

function Header({
  icon,
  title,
  hint,
}: {
  icon: React.ReactNode;
  title: string;
  hint?: string;
}) {
  return (
    <div>
      <div className="flex items-center gap-2 text-sm font-medium text-zinc-100">
        {icon}
        {title}
      </div>
      {hint && <div className="mt-1 text-[11px] text-zinc-500">{hint}</div>}
    </div>
  );
}