// DeviceFiles.tsx — page hosting the SFTP file browser for a single
// device. The actual tree / upload / edit UI lives in
// components/FileBrowser.tsx so it can be reused (or wrapped in a modal)
// elsewhere later.

import { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ChevronLeft, HardDrive, Loader2 } from 'lucide-react';
import { getDevice, type Device } from '@/api/devices';
import { ApiError } from '@/api/client';
import { useI18n } from '@/i18n/locale';
import { FileBrowser } from '@/components/FileBrowser';
import { StatusPill } from '@/components/StatusPill';

export default function DeviceFilesPage() {
  const { tr } = useI18n();
  const navigate = useNavigate();
  const { id = '' } = useParams<{ id: string }>();
  const [device, setDevice] = useState<Device | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    setDevice(null);
    setErr(null);
    if (!id) return;
    getDevice(id)
      .then((d) => {
        if (cancelled) return;
        setDevice(d);
      })
      .catch((e) => {
        if (cancelled) return;
        setErr(e instanceof ApiError ? e.message : (e as Error).message);
      });
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (err) {
    return (
      <main className="anim-fade flex flex-1 flex-col overflow-hidden">
        <header className="app-header flex items-center justify-between border-b border-zinc-800 px-6 py-4">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => navigate('/hosts')}
              className="rounded-md p-1.5 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
              aria-label={tr('返回主机列表', 'Back to host list')}
            >
              <ChevronLeft size={16} />
            </button>
            <h1 className="text-base font-semibold text-zinc-100">{tr('文件', 'Files')}</h1>
          </div>
        </header>
        <div className="flex-1 overflow-y-auto px-6 py-6">
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
            {err}
          </div>
        </div>
      </main>
    );
  }

  return (
    <main className="anim-fade flex flex-1 flex-col overflow-hidden">
      <header className="app-header flex items-center justify-between border-b border-zinc-800 px-6 py-4">
        <div className="flex min-w-0 items-center gap-3">
          <button
            type="button"
            onClick={() => navigate(`/hosts/${id}`)}
            className="rounded-md p-1.5 text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
            aria-label={tr('返回设备详情', 'Back to device detail')}
          >
            <ChevronLeft size={16} />
          </button>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <HardDrive size={14} className="text-zinc-500" />
              <h1 className="truncate text-base font-semibold text-zinc-100">
                {device ? device.name || `host #${device.id}` : tr('加载中…', 'Loading…')}
              </h1>
              {device && <StatusPill status={device.online ? 'online' : 'offline'} />}
            </div>
            <div className="mt-0.5 truncate text-[11px] text-zinc-500">
              {tr('通过 SFTP 隧道访问设备文件系统', 'Access the device filesystem over the SFTP tunnel')}
            </div>
          </div>
        </div>
        <Link
          to={`/hosts/${id}`}
          className="inline-flex items-center gap-1.5 rounded-md border border-zinc-700 bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-200 hover:bg-zinc-800"
        >
          {tr('返回设备', 'Back to device')}
        </Link>
      </header>

      <div className="flex-1 overflow-y-auto px-6 py-5">
        {!device ? (
          <div className="flex items-center gap-2 text-xs text-zinc-500">
            <Loader2 size={12} className="animate-spin" />
            {tr('加载设备信息…', 'Loading device…')}
          </div>
        ) : (
          <FileBrowser deviceId={device.id} initialPath="/" />
        )}
      </div>
    </main>
  );
}