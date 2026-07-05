// InstallLogPanel.tsx — live-tail log viewer for a one-button edge
// install job. Renders inside the shared <Modal> (same overlay +
// esc-to-close behaviour as the rest of the SPA) and polls
// GET /install-jobs/:id every second while the job is in a non-terminal
// state. Polling stops automatically when the job lands in success /
// failed / cancelled / timeout — the operator can still keep the panel
// open to read the captured log, but no extra requests fly out.
//
// Auto-scroll: scrolls to the bottom whenever the log_output changes
// unless the user has scrolled up to read earlier lines. We measure
// that with a small buffer (50px) so the tail still sticks to the
// bottom during normal reads without feeling jumpy.
//
// The actual cancellation button is wired to POST :cancel on the job.

import { useCallback, useEffect, useRef, useState } from 'react';
import { Modal } from './Modal';
import { usePoll } from '@/lib/usePoll';
import { useI18n } from '@/i18n/locale';
import { Button } from './ui/Button';
import {
  cancelInstallJob,
  getInstallJob,
  type InstallJob,
  type InstallJobStatus,
} from '@/api/installjob';

type Props = {
  jobId: number;
  open: boolean;
  onClose: () => void;
};

// `usePoll` only knows how to skip ticks; it doesn't expose a stop
// signal back to the caller. We turn it on / off via the `enabled`
// flag, which is the same pattern ReportDetail.tsx uses. interval=0
// also disables, but the explicit boolean reads more clearly here
// because we toggle it from inside an effect.
const POLL_MS = 1000;

// Terminal statuses freeze the panel: polling stops and the Cancel
// button hides (since you can only cancel a still-running job).
const TERMINAL: InstallJobStatus[] = ['success', 'failed', 'cancelled', 'timeout'];

function isTerminal(status: InstallJobStatus): boolean {
  return TERMINAL.includes(status);
}

export function InstallLogPanel({ jobId, open, onClose }: Props) {
  const { tr } = useI18n();
  const [job, setJob] = useState<InstallJob | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const preRef = useRef<HTMLPreElement>(null);
  // The user manually scrolled up to inspect earlier log lines — in
  // that mode we don't yank the viewport back to the bottom on every
  // update.
  const userScrolledUp = useRef(false);

  const refresh = useCallback(async () => {
    try {
      const j = await getInstallJob(jobId);
      setJob(j);
      setLoadError(null);
    } catch (e) {
      // Keep the last good log visible — only surface the load
      // failure as a header note. The next tick may recover
      // (transient 5xx, brief tunnel drop) and we don't want to
      // blank the whole panel.
      setLoadError((e as Error).message || String(e));
    }
  }, [jobId]);

  // Kick off a refresh whenever the panel is (re-)opened or the job
  // id changes — otherwise the operator might see a stale terminal
  // row left over from the previous open.
  useEffect(() => {
    if (!open) return;
    setJob(null);
    setLoadError(null);
    userScrolledUp.current = false;
    void refresh();
  }, [open, jobId, refresh]);

  usePoll(refresh, POLL_MS, open && !!job && !isTerminal(job.status));

  // Auto-scroll the log to the bottom on new content unless the
  // operator scrolled up to read earlier lines. We attach the scroll
  // listener once on mount and read the element via the ref.
  useEffect(() => {
    const el = preRef.current;
    if (!el) return;
    const onScroll = () => {
      // Within 50px of the bottom counts as "still following the tail".
      userScrolledUp.current = el.scrollTop + el.clientHeight < el.scrollHeight - 50;
    };
    el.addEventListener('scroll', onScroll);
    return () => el.removeEventListener('scroll', onScroll);
  }, []);

  useEffect(() => {
    if (userScrolledUp.current) return;
    const el = preRef.current;
    if (!el) return;
    // queueMicrotask avoids the "scrollHeight hasn't updated yet"
    // race when React just committed a new log_output value.
    queueMicrotask(() => {
      el.scrollTop = el.scrollHeight;
    });
  }, [job?.log_output]);

  const handleCancel = useCallback(async () => {
    if (!window.confirm(tr('确认取消安装？', 'Cancel installation?'))) return;
    try {
      await cancelInstallJob(jobId);
      // Optimistic — the next poll will reconcile status to 'cancelled'.
      await refresh();
    } catch (e) {
      window.alert((e as Error).message || String(e));
    }
  }, [jobId, refresh, tr]);

  const running = job?.status === 'running' || job?.status === 'queued';

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`${tr('一键安装日志', 'Install log')} · job #${jobId}`}
      size="lg"
      footer={
        <>
          {running && (
            <Button variant="danger" onClick={handleCancel}>
              {tr('取消安装', 'Cancel')}
            </Button>
          )}
          <Button variant="ghost" onClick={onClose}>
            {tr('关闭', 'Close')}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-xs text-zinc-400">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span>
            {tr('状态', 'Status')}: <StatusBadge status={job?.status} />
          </span>
          {job?.started_at && (
            <span>
              {tr('开始', 'Started')}:{' '}
              {new Date(job.started_at).toLocaleTimeString()}
            </span>
          )}
          {job?.finished_at && (
            <span>
              {tr('结束', 'Finished')}:{' '}
              {new Date(job.finished_at).toLocaleTimeString()}
            </span>
          )}
          {job?.exit_code != null && (
            <span>
              {tr('退出码', 'Exit code')}: {job.exit_code}
            </span>
          )}
        </div>
        {loadError && (
          <div className="rounded border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-amber-300">
            {tr('刷新失败', 'Refresh failed')}: {loadError}
          </div>
        )}
        <pre
          ref={preRef}
          className="h-[60vh] overflow-y-auto rounded border border-zinc-800 bg-zinc-950 px-4 py-3 font-mono text-xs whitespace-pre-wrap break-all text-zinc-100"
        >
          {job?.log_output || tr('暂无日志', 'No log yet')}
        </pre>
      </div>
    </Modal>
  );
}

function StatusBadge({ status }: { status: InstallJobStatus | undefined }) {
  const { tr } = useI18n();
  if (!status) {
    return <span className="text-zinc-500">● {tr('加载中', 'Loading')}</span>;
  }
  const cls = {
    queued: 'text-zinc-500',
    running: 'text-sky-400 animate-pulse',
    success: 'text-emerald-400',
    failed: 'text-red-400',
    cancelled: 'text-zinc-400',
    timeout: 'text-amber-400',
  }[status];
  return <span className={cls}>● {status}</span>;
}