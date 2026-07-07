// EdgeInstallModal.tsx — Edges 页面操作栏「安装」入口。
//
// 行为：
//   - 手动安装：展示 buildInstallCommand 生成的 curl 一行命令，operator
//     可复制到目标主机执行。命令需要 secretKey；secretKey 仅在创建 edge
//     时一次性回显，调用方从 sessionStorage 取出注入到 props.secretKey。
//     刷新后 secretKey 丢失时弹窗退化为提示「去轮换密钥」+ 不展示命令。
//   - 一键安装：复用 InstallEdgeModal 的逻辑 —— POST /devices/{id}/
//     install-edge，后端用 DB 中保存的设备 SSH 凭据（创建 device 时录入）
//     直接 SSH 到目标主机跑 curl pipe。device 信息（用户名/端口/host）
//     从 edge.device_id 关联的 Device 行读出展示给 operator。
//
// 复用策略：和 InstallEdgeModal 共用 buildInstallCommand + installEdge +
// Device.ssh_* 字段；本组件额外提供「手动安装命令展示」section。

import { useEffect, useState } from 'react';
import { Check, Copy, Power } from 'lucide-react';
import { Modal } from './Modal';
import { Button } from './ui/Button';
import { cn } from '@/lib/cn';
import { useI18n } from '@/i18n/locale';
import { buildInstallCommand } from '@/lib/installCommand';
import { getDevice, installEdge, type Device } from '@/api/devices';
import type { Edge } from '@/api/edges';
import { usePermissions } from '@/store/me';

type Props = {
  open: boolean;
  edge: Edge | null;
  // 创建时一次性回显的 secret_key；由调用方从 sessionStorage 取回注入。
  // 缺失（刷新后）时手动安装 section 退化为提示文案，不展示命令。
  secretKey?: string;
  onClose(): void;
  // 一键安装启动后回调；调用方负责打开 InstallLogPanel tail 日志。
  onStarted(jobId: number): void;
};

export function EdgeInstallModal({ open, edge, secretKey, onClose, onStarted }: Props) {
  const { tr } = useI18n();
  const { canMutate } = usePermissions();
  const [device, setDevice] = useState<Device | null>(null);
  const [loadingDevice, setLoadingDevice] = useState(false);
  const [deviceLoadFailed, setDeviceLoadFailed] = useState(false);
  const [sshPass, setSshPass] = useState('');
  const [sshKeyPem, setSshKeyPem] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  // Reset on close — stale state from a previous open must not leak.
  useEffect(() => {
    if (!open) {
      setSshPass('');
      setSshKeyPem('');
      setErr(null);
      setSubmitting(false);
      setCopied(false);
      setDevice(null);
      setDeviceLoadFailed(false);
    }
  }, [open]);

  // 拉 edge 关联的 device 详情，用于渲染 SSH 连接信息和调用 installEdge。
  useEffect(() => {
    if (!open || !edge?.device_id) {
      setDevice(null);
      setDeviceLoadFailed(false);
      return;
    }
    let cancelled = false;
    setLoadingDevice(true);
    setDeviceLoadFailed(false);
    getDevice(edge.device_id)
      .then((d) => {
        if (cancelled) return;
        setDevice(d as Device);
      })
      .catch(() => {
        if (cancelled) return;
        setDevice(null);
        setDeviceLoadFailed(true);
      })
      .finally(() => {
        if (!cancelled) setLoadingDevice(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, edge?.device_id]);

  if (!edge) return null;

  // 手动安装命令：只在有 secretKey 时生成；否则展示「去轮换密钥」提示。
  const installCmd =
    edge.access_key_id && secretKey
      ? buildInstallCommand({ accessKey: edge.access_key_id, secretKey }).cmd
      : '';

  const deviceMissing = !edge.device_id;
  // 任务名（task_name）由「新建 Edge」流程在 CreateEdgeModal 直接写入
  // edge.task_name，本弹窗安装时不再二次询问；edge 上未设 task_name 也
  // 照常可装，后续可在 Edges 行内或编辑弹窗补填。
  const canOneClick =
    !!edge.device_id && !submitting && canMutate;

  const handleOneClickInstall = async () => {
    if (!canOneClick || !edge.device_id) return;
    setSubmitting(true);
    setErr(null);
    try {
      // 一键安装走 InstallEdgeModal 的同款 wire body：后端用 device
      // 表里的 ssh_* 凭据直接 SSH 到目标机跑 install command。
      // command 字段仍然带上（如果本地有 secretKey）：当 DB 凭据和当前
      // access_key/secret_key 不一致（比如刚轮换了）后端会用最新凭据。
      // 不再携带 task_name：任务名由 CreateEdgeModal 统一收口。
      const body: {
        command: string;
        ssh_pass?: string;
        ssh_key_pem?: string;
      } = {
        command: installCmd || '',
      };
      if (sshPass.trim()) body.ssh_pass = sshPass;
      if (sshKeyPem.trim()) body.ssh_key_pem = sshKeyPem;
      const resp = await installEdge(edge.device_id, body);
      onStarted(resp.install_job_id);
      onClose();
    } catch (e) {
      setErr((e as Error).message || tr('启动安装失败', 'Failed to start install'));
    } finally {
      setSubmitting(false);
    }
  };

  const copyCmd = () => {
    if (!installCmd) return;
    navigator.clipboard
      .writeText(installCmd)
      .then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      })
      .catch(() => {
        /* noop — 剪贴板权限被拒时让用户手动复制 */
      });
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={tr(
        `安装 · ${edge.name || `edge #${edge.id}`}`,
        `Install · ${edge.name || `edge #${edge.id}`}`,
      )}
      size="md"
      footer={
        <Button variant="ghost" onClick={onClose} disabled={submitting}>
          {tr('关闭', 'Close')}
        </Button>
      }
    >
      <div className="space-y-4 text-xs text-zinc-300">
        {/* 所属设备：连接凭据概要 */}
        <section>
          <h3 className="mb-1 text-[11px] font-medium uppercase tracking-wide text-zinc-400">
            {tr('所属设备', 'Host device')}
          </h3>
          {device ? (
            <div className="space-y-0.5 rounded-md border border-zinc-800 bg-zinc-950/40 px-3 py-2 text-zinc-300">
              <div>{device.name || `host #${device.id}`}</div>
              <div className="font-mono text-[11px] text-zinc-500">
                {device.ssh_user}@{device.ssh_host || device.ip_address || '—'}:{device.ssh_port || 22}
              </div>
              <div className="text-[11px] text-zinc-500">
                {tr(
                  `认证方式：${device.ssh_auth_kind === 'key' ? '私钥' : '密码'}`,
                  `Auth: ${device.ssh_auth_kind === 'key' ? 'private key' : 'password'}`,
                )}
              </div>
            </div>
          ) : loadingDevice ? (
            <div className="text-zinc-500">{tr('加载中…', 'Loading…')}</div>
          ) : deviceMissing ? (
            <div className="text-amber-300/90">
              {tr(
                '该 edge 未关联所属设备，请到「编辑」分配后再安装。',
                'This edge is not linked to a host device. Assign one via "Edit" before installing.',
              )}
            </div>
          ) : (
            <div className="text-red-300">
              {tr('设备信息加载失败', 'Failed to load device info')}
              {deviceLoadFailed && (
                <span className="ml-1 text-zinc-500">(#{edge.device_id})</span>
              )}
            </div>
          )}
        </section>

        {/* 手动安装 */}
        <section>
          <h3 className="mb-1 text-[11px] font-medium uppercase tracking-wide text-zinc-400">
            {tr('手动安装', 'Manual install')}
          </h3>
          {installCmd ? (
            <>
              <div className="mb-1 flex items-center justify-between">
                <span className="text-[11px] text-zinc-500">
                  {tr('复制以下命令到目标主机执行', 'Run the following command on the target host')}
                </span>
                <button
                  type="button"
                  onClick={copyCmd}
                  className={cn(
                    'inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs',
                    copied
                      ? 'bg-emerald-500/15 text-emerald-300'
                      : 'bg-zinc-800 text-zinc-300 hover:bg-zinc-700',
                  )}
                >
                  {copied ? <Check size={12} /> : <Copy size={12} />}
                  {copied ? tr('已复制', 'Copied') : tr('复制', 'Copy')}
                </button>
              </div>
              <pre className="overflow-x-auto whitespace-pre-wrap break-all rounded-lg border border-zinc-800 bg-zinc-950/60 px-3 py-2 font-mono text-[11px] leading-relaxed text-zinc-200">
                {installCmd}
              </pre>
            </>
          ) : (
            <p className="text-[11px] text-zinc-500">
              {tr(
                '暂无可用安装命令：secret_key 仅在创建时一次性回显，请到行菜单「轮换密钥」生成新密钥后再次打开本弹窗。',
                'No install command available — the secret_key is only shown once at creation. Rotate the key via the row menu, then reopen this dialog.',
              )}
            </p>
          )}
        </section>

        {/* 一键安装 */}
        <section>
          <h3 className="mb-1 text-[11px] font-medium uppercase tracking-wide text-zinc-400">
            {tr('一键安装', 'One-click install')}
          </h3>
          <p className="mb-2 text-[11px] text-zinc-500">
            {tr(
              '使用所属设备的 SSH 凭据自动在主机上安装 edge。提交后会创建异步安装任务，可实时查看日志。',
              'Install the edge on the host device using its saved SSH credentials. An async install job is created with a live log.',
            )}
          </p>
          {/* 任务名（task_name）改在「新建 Edge」时填写并写入 edge.task_name，安装弹窗不再二次询问。 */}
          <Field
            label={tr('SSH 密码（覆盖 DB；可选）', 'SSH password (override DB; optional)')}
          >
            <input
              type="password"
              autoComplete="off"
              value={sshPass}
              onChange={(e) => setSshPass(e.target.value)}
              placeholder={tr('留空则使用设备保存的密码', 'Leave blank to use the password saved on the device')}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <div className="mt-2">
            <Field
              label={tr('SSH 私钥（覆盖 DB；可选）', 'SSH private key (override DB; optional)')}
            >
              <textarea
                value={sshKeyPem}
                onChange={(e) => setSshKeyPem(e.target.value)}
                rows={4}
                placeholder={tr(
                  '-----BEGIN OPENSSH PRIVATE KEY-----\n…\n留空使用设备保存的私钥',
                  '-----BEGIN OPENSSH PRIVATE KEY-----\n…\nLeave blank to use the key saved on the device',
                )}
                className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 font-mono text-[11px] text-zinc-100 focus:border-zinc-600 focus:outline-none"
              />
            </Field>
          </div>
          <Button
            variant="primary"
            onClick={handleOneClickInstall}
            disabled={!canOneClick}
            className="mt-2"
          >
            <Power size={12} className="mr-1" />
            {submitting ? tr('启动中…', 'Starting…') : tr('开始安装', 'Start install')}
          </Button>
        </section>

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
