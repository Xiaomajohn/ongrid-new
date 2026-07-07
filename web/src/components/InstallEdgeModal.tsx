// InstallEdgeModal.tsx — one-button edge install launchpad.
//
// Two text areas (password override + PEM key override) are intentionally
// optional — the manager falls back to whatever was saved on the device
// row at creation time (POST /devices stores them per plan §SSH API
// contract). The override is for the case where DB credentials rotated
// out-of-band and the install still needs to work.
//
// On submit we POST /devices/{id}/install-edge → InstallEdgeResponse
// (job id). The caller closes this modal and opens InstallLogPanel with
// that id to tail the live log.

import { useEffect, useState } from 'react';
import { Modal } from './Modal';
import { Button } from './ui/Button';
import { useI18n } from '@/i18n/locale';
import { installEdge } from '@/api/devices';
import { createEdge, type CreateEdgeResponse } from '@/api/edges';
import { buildInstallCommand } from '@/lib/installCommand';
import type { Device } from '@/api/devices';

type Props = {
  open: boolean;
  device: Device | null;
  // Optional current edge status — we render a small "edge online" pill
  // when the device has a registered edge already so the operator sees
  // the "一键安装" / "重装" semantics at a glance.
  edgeOnline?: boolean;
  onClose(): void;
  onStarted(jobId: number): void;
};

export function InstallEdgeModal({ open, device, edgeOnline, onClose, onStarted }: Props) {
  const { tr } = useI18n();
  const [sshPass, setSshPass] = useState('');
  const [sshKeyPem, setSshKeyPem] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      setSshPass('');
      setSshKeyPem('');
      setErr(null);
      setSubmitting(false);
    }
  }, [open]);

  if (!device) return null;

  // 任务名（task_name）由「新建 Edge」流程在 CreateEdgeModal 填入 edge.task_name，
  // 本路径仅负责 create + install，不在安装弹窗里再次询问。
  const canSubmit = !submitting;

  const submit = async () => {
    if (!canSubmit) return;
    setSubmitting(true);
    setErr(null);
    try {
      // 前端拼安装命令，后端只执行不再拼。先调 createEdge 拿凭据，
      // 再用同一套 buildInstallCommand 生成命令，手动安装和自动安装共用同一份函数。
      // task_name 不再从安装弹窗传入：调用方若需任务名，请在「新建 Edge」
      // 时传 createEdge({ name, task_name })。
      const created: CreateEdgeResponse = await createEdge({
        name: device.name || '',
      });
      const { cmd } = buildInstallCommand({
        accessKey: created.access_key_id,
        secretKey: created.secret_key,
      });
      const body: {
        command: string;
        ssh_pass?: string;
        ssh_key_pem?: string;
      } = { command: cmd };
      if (sshPass.trim()) body.ssh_pass = sshPass;
      if (sshKeyPem.trim()) body.ssh_key_pem = sshKeyPem;
      // 用设备的 id 而不是新 edge 的 id：后端 POST /v1/devices/{id}/install-edge
      // 的 {id} 语义是 device_id（adapter 内部 deviceRepo.Get(ctx, deviceID)），
      // 传 edge.id 会让 deviceRepo 找不到对应设备 → ErrNotFound → 404。
      // 新 edge 的 access_key/secret 已经塞进 cmd 里随 SSH 命令带到目标机。
      const resp = await installEdge(device.id, body);
      onStarted(resp.install_job_id);
      onClose();
    } catch (e) {
      setErr((e as Error).message || tr('启动安装失败', 'Failed to start install'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={tr(`一键安装 · ${device.name || `host #${device.id}`}`, `Install edge · ${device.name || `host #${device.id}`}`)}
      size="md"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={submitting}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button variant="primary" onClick={submit} disabled={!canSubmit}>
            {submitting ? tr('启动中…', 'Starting…') : tr('开始安装', 'Start install')}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-xs text-zinc-300">
        <div className="flex items-center gap-2 text-[11px] text-zinc-500">
          <span>{tr('当前 Edge 状态', 'Current edge status')}:</span>
          <span
            className={
              edgeOnline
                ? 'inline-flex items-center rounded border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-emerald-300'
                : 'inline-flex items-center rounded border border-zinc-700 bg-zinc-800 px-1.5 py-0.5 text-zinc-400'
            }
          >
            {edgeOnline ? tr('在线', 'online') : tr('未安装 / 离线', 'not installed / offline')}
          </span>
        </div>

        {/* 任务名（task_name）由「新建 Edge」流程填入 edge.task_name，
            本一键安装路径不再二次询问；如需任务名，请在 Edges 页的「新建」
            入口填写。 */}
        <Field label={tr('SSH 密码（覆盖 DB；可选）', 'SSH password (override DB; optional)')}>
          <input
            type="password"
            autoComplete="off"
            value={sshPass}
            onChange={(e) => setSshPass(e.target.value)}
            placeholder={tr('留空则使用创建设备时保存的密码', 'Leave blank to use the password saved at device creation')}
            className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
          />
        </Field>

        <Field label={tr('SSH 私钥（覆盖 DB；可选）', 'SSH private key (override DB; optional)')}>
          <textarea
            value={sshKeyPem}
            onChange={(e) => setSshKeyPem(e.target.value)}
            rows={5}
            placeholder={tr('-----BEGIN OPENSSH PRIVATE KEY-----\n…\n留空则使用创建设备时保存的私钥', '-----BEGIN OPENSSH PRIVATE KEY-----\n…\nLeave blank to use the key saved at device creation')}
            className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 font-mono text-[11px] text-zinc-100 focus:border-zinc-600 focus:outline-none"
          />
        </Field>

        <p className="text-[11px] text-zinc-500">
          {tr(
            '提交后会创建一个异步安装任务，立即打开日志面板 tail 实时输出。可随时取消。',
            'Submitting creates an async install job and opens the live log panel. You can cancel any time.',
          )}
        </p>

        {err && (
          <div
            role="alert"
            className="rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-300"
          >
            {err}
          </div>
        )}
      </div>
    </Modal>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <div className="mb-1 text-[11px] text-zinc-500">{label}</div>
      {children}
    </label>
  );
}