// ConfirmDeleteModal.tsx — type-the-name-to-confirm delete dialog for a
// device. Backend may not yet fill in `edges / recent_metrics / job_count`
// per the plan (§SSH/SFTP), so we render a static impact list rather than
// calling GET /devices/{id}?include=... and risking a 400.
//
// The match requirement (typed input must equal device.name) is the same
// pattern GitHub uses for destructive actions — a low-cost guard against
// misclicks when the operator is mid-task and only half-reading.

import { useEffect, useState } from 'react';
import { Modal } from './Modal';
import { Button } from './ui/Button';
import { useI18n } from '@/i18n/locale';
import type { Device } from '@/api/devices';

type Props = {
  open: boolean;
  device: Device | null;
  deleting?: boolean;
  onClose(): void;
  onConfirm(): void;
};

export function ConfirmDeleteModal({ open, device, deleting, onClose, onConfirm }: Props) {
  const { tr } = useI18n();
  const [confirmText, setConfirmText] = useState('');

  useEffect(() => {
    if (!open) setConfirmText('');
  }, [open]);

  if (!device) return null;
  const target = device.name || `host #${device.id}`;
  const matched = confirmText.trim() === (device.name ?? '').trim() && (device.name ?? '').trim() !== '';

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={tr(`删除设备 ${target}`, `Delete device ${target}`)}
      size="md"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={deleting}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button variant="danger" onClick={onConfirm} disabled={deleting || !matched}>
            {deleting ? tr('删除中…', 'Deleting…') : tr('确认删除', 'Confirm delete')}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-xs text-zinc-300">
        <p>{tr('此操作将一并清除:', 'This will also remove:')}</p>
        <ul className="ml-4 list-disc space-y-1 text-[11px] text-zinc-400">
          <li>{tr('该设备上所有 edge 探针的注册信息', 'All edge probes registered on this device')}</li>
          <li>{tr('该设备关联的 SSH 凭据（密码 / 私钥）', 'The SSH credentials (password / private key) saved for this device')}</li>
          <li>{tr('该设备相关的历史安装任务日志', 'Historical install job logs for this device')}</li>
          <li>{tr('该设备在 /topology 中的节点关联（如有）', 'The device\'s node association in /topology (if any)')}</li>
        </ul>
        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-[11px] text-red-300">
          {tr(
            '主机的 SSH 凭据将随设备一同清除。删除后无法恢复。',
            "The host's SSH credentials are removed with the device. This cannot be undone.",
          )}
        </div>
        <label className="block pt-1">
          <div className="mb-1 text-[11px] text-zinc-500">
            {tr(`输入设备名 "`, `Type the device name "`)}
            <span className="font-mono text-zinc-300">{device.name || `host #${device.id}`}</span>
            {tr(`" 以确认:`, `" to confirm:`)}
          </div>
          <input
            autoFocus
            value={confirmText}
            onChange={(e) => setConfirmText(e.target.value)}
            placeholder={device.name || `host #${device.id}`}
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