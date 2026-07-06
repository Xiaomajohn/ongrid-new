// EditDeviceModal.tsx — 编辑主机的对话框。复用 CreateDeviceModal 的视
// 觉风格（Section / Field / Modal size=lg），但语义改 dirty-tracking：
// 打开时从传入的 device 预填表单，submit 时只把"用户改过"的字段加到
// PATCH body——未改动的字段保持原值。
//
// password / key 的处理是关键 UX 细节：留空 = "不动现有凭据"；非空 =
// "替换为新值"。切换 auth_kind radio 时清掉 credential 输入框，避免
// 误把 password 存到 ssh_key 字段。
//
// 后端走指针 = 可选 语义：未指定的字段保持不变，指定为空串 ("") 则视
// 为清空（仅对 ssh_password / ssh_key 有效）。前端不主动传空串——
// 留空=undefined；非空=实际值。这样和后端的 wire 契约对齐。

import { useEffect, useState } from 'react';
import { Modal } from './Modal';
import { Button } from './ui/Button';
import { useI18n } from '@/i18n/locale';
import { updateDevice, type Device, type UpdateDeviceInput } from '@/api/devices';

type Props = {
  device: Device;
  onClose(): void;
  onSaved?(): void;
};

// 记录"用户改过的字段"。在 submit 时 merge 到 PATCH body；空对象
// 仍然允许保存（后端会回 200 + 原 DTO），但 UI 上灰色"保存"按钮
// 暗示"没东西要改"。
type Dirty = Partial<Record<keyof UpdateDeviceInput, true>>;

export function EditDeviceModal({ device, onClose, onSaved }: Props) {
  const { tr } = useI18n();

  // ----- 表单状态（全部用 string 表示，submit 时转 wire 类型） -----
  const [name, setName] = useState(device.name ?? '');
  const [description, setDescription] = useState(device.description ?? '');
  const [hostname, setHostname] = useState(device.hostname ?? '');
  const [sshHost, setSshHost] = useState(device.ssh_host ?? '');
  const [sshPort, setSshPort] = useState(String(device.ssh_port || 22));
  const [sshUser, setSshUser] = useState(device.ssh_user ?? '');
  const [authKind, setAuthKind] = useState<'password' | 'key'>(
    (device.ssh_auth_kind === 'key' ? 'key' : 'password'),
  );
  // credential 单独存；空字符串 = "保留现有凭据"；非空 = 替换。
  // 打开时不预填（不能把密文回显到 textarea 让别人看到）。
  const [credential, setCredential] = useState('');

  // ----- dirty 跟踪 -----
  // 用 useState + setState 标记哪些字段被改过。比 useRef 更直观：
  // dirty 触发 re-render 影响"保存"按钮的 enabled 状态。
  const [dirty, setDirty] = useState<Dirty>({});
  const mark = (k: keyof UpdateDeviceInput) =>
    setDirty((d) => (d[k] ? d : { ...d, [k]: true }));

  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // 切换 device（行被点开、refetch 触发时）时重置表单 + dirty。
  useEffect(() => {
    setName(device.name ?? '');
    setDescription(device.description ?? '');
    setHostname(device.hostname ?? '');
    setSshHost(device.ssh_host ?? '');
    setSshPort(String(device.ssh_port || 22));
    setSshUser(device.ssh_user ?? '');
    setAuthKind(device.ssh_auth_kind === 'key' ? 'key' : 'password');
    setCredential('');
    setDirty({});
    setErr(null);
    setSubmitting(false);
  }, [device]);

  const submit = async () => {
    // 校验：name / ssh_host / ssh_user 一旦 dirty 就不允许空串
    // （后端会拒掉）。
    if (dirty.name !== undefined && !name.trim()) {
      setErr(tr('名称不能为空', 'Name is required'));
      return;
    }
    if (dirty.ssh_host !== undefined && !sshHost.trim()) {
      setErr(tr('SSH 主机不能为空', 'SSH host is required'));
      return;
    }
    if (dirty.ssh_user !== undefined && !sshUser.trim()) {
      setErr(tr('SSH 用户不能为空', 'SSH user is required'));
      return;
    }
    if (dirty.ssh_port !== undefined) {
      const port = Number(sshPort || '22');
      if (!Number.isFinite(port) || port < 1 || port > 65535) {
        setErr(tr('端口必须在 1-65535 之间', 'Port must be between 1 and 65535'));
        return;
      }
    }
    setSubmitting(true);
    setErr(null);

    const body: UpdateDeviceInput = {};
    if (dirty.name) body.name = name.trim();
    if (dirty.description) body.description = description.trim();
    if (dirty.hostname) body.hostname = hostname.trim();
    if (dirty.ssh_host) body.ssh_host = sshHost.trim();
    if (dirty.ssh_port) body.ssh_port = Number(sshPort || '22');
    if (dirty.ssh_user) body.ssh_user = sshUser.trim();
    if (dirty.ssh_auth_kind) body.ssh_auth_kind = authKind;
    // password / key 走单独路径：dirty 由"输入框非空"派生
    if (credential.trim()) {
      if (authKind === 'password') body.ssh_password = credential;
      else body.ssh_key = credential;
    }
    try {
      await updateDevice(device.id, body);
      onSaved?.();
      onClose();
    } catch (e) {
      setErr((e as Error).message || tr('保存失败', 'Save failed'));
    } finally {
      setSubmitting(false);
    }
  };

  // 没有任何 dirty 字段时禁用保存按钮。
  const dirtyCount = Object.keys(dirty).length + (credential.trim() ? 1 : 0);
  const canSave = dirtyCount > 0 && !submitting;

  return (
    <Modal
      open
      onClose={onClose}
      title={tr(`编辑设备 · ${device.name || `#${device.id}`}`, `Edit device · ${device.name || `#${device.id}`}`)}
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={submitting}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button variant="primary" onClick={submit} disabled={!canSave}>
            {submitting ? tr('保存中…', 'Saving…') : tr('保存', 'Save')}
          </Button>
        </>
      }
    >
      <div className="space-y-4 text-xs text-zinc-300">
        <Section title={tr('基本信息', 'Basics')}>
          <Field label={tr('名称', 'Name')} required>
            <input
              autoFocus
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                mark('name');
              }}
              placeholder={tr('例如 db-prod-01', 'e.g. db-prod-01')}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <Field label={tr('描述', 'Description')}>
            <input
              value={description}
              onChange={(e) => {
                setDescription(e.target.value);
                mark('description');
              }}
              placeholder={tr('可选', 'optional')}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <Field label={tr('主机名', 'Hostname')}>
            <input
              value={hostname}
              onChange={(e) => {
                setHostname(e.target.value);
                mark('hostname');
              }}
              placeholder={tr('可选 — 留空则在 edge 上线时自动填入', 'optional — auto-fill when the edge comes online')}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
        </Section>

        <Section title={tr('SSH 连接', 'SSH connection')}>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-[1fr_120px]">
            <Field label={tr('主机', 'Host')} required>
              <input
                value={sshHost}
                onChange={(e) => {
                  setSshHost(e.target.value);
                  mark('ssh_host');
                }}
                placeholder="10.0.0.1"
                className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              />
            </Field>
            <Field label={tr('端口', 'Port')}>
              <input
                inputMode="numeric"
                value={sshPort}
                onChange={(e) => {
                  setSshPort(e.target.value.replace(/[^0-9]/g, ''));
                  mark('ssh_port');
                }}
                placeholder="22"
                className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              />
            </Field>
          </div>
          <Field label={tr('用户', 'User')} required>
            <input
              value={sshUser}
              onChange={(e) => {
                setSshUser(e.target.value);
                mark('ssh_user');
              }}
              placeholder="root"
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <Field label={tr('认证方式', 'Auth method')}>
            <div className="flex items-center gap-4 text-xs">
              <label className="flex cursor-pointer items-center gap-2">
                <input
                  type="radio"
                  name="edit-auth-kind"
                  className="accent-zinc-300"
                  checked={authKind === 'password'}
                  onChange={() => {
                    setAuthKind('password');
                    setCredential('');
                    mark('ssh_auth_kind');
                  }}
                />
                <span>{tr('密码', 'Password')}</span>
              </label>
              <label className="flex cursor-pointer items-center gap-2">
                <input
                  type="radio"
                  name="edit-auth-kind"
                  className="accent-zinc-300"
                  checked={authKind === 'key'}
                  onChange={() => {
                    setAuthKind('key');
                    setCredential('');
                    mark('ssh_auth_kind');
                  }}
                />
                <span>{tr('私钥', 'Private key')}</span>
              </label>
            </div>
          </Field>
          <Field
            label={
              authKind === 'password'
                ? tr('密码（留空保持现有）', 'Password (leave blank to keep current)')
                : tr('私钥（留空保持现有）', 'Private key (leave blank to keep current)')
            }
          >
            <textarea
              value={credential}
              onChange={(e) => setCredential(e.target.value)}
              rows={authKind === 'key' ? 6 : 2}
              placeholder={
                authKind === 'password'
                  ? tr('新密码', 'new password')
                  : tr('-----BEGIN OPENSSH PRIVATE KEY-----\n…', '-----BEGIN OPENSSH PRIVATE KEY-----\n…')
              }
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 font-mono text-[11px] text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <div className="rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-[11px] text-amber-300">
            {tr(
              '凭据将以明文保存在数据库中。仅在受信环境使用。',
              'Credentials are stored in the database in plaintext. Use only in trusted environments.',
            )}
          </div>
        </Section>

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

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-[11px] font-medium uppercase tracking-wider text-zinc-500">
        {title}
      </h3>
      <div className="space-y-2">{children}</div>
    </section>
  );
}

function Field({
  label,
  required,
  children,
}: {
  label: string;
  required?: boolean;
  children: React.ReactNode;
}) {
  return (
    <label className="block">
      <div className="mb-1 text-[11px] text-zinc-500">
        {label}
        {required && <span className="ml-0.5 text-red-400">*</span>}
      </div>
      {children}
    </label>
  );
}
