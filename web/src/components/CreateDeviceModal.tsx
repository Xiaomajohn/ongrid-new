// CreateDeviceModal.tsx — modal for registering a new logical host + SSH
// credentials. Submits via POST /devices (api/devices.ts → createDevice).
//
// Plaintext-at-rest is called out in the body copy (per plan §SSH API
// contract) so operators are never surprised by where the password lands.
// We deliberately keep this self-contained instead of editing
// devices.ts's CreateDeviceInput on every tweak — the modal owns form
// ergonomics (validation, submit button state) and the api type only
// carries the wire shape.

import { useEffect, useState } from 'react';
import { Modal } from './Modal';
import { Button } from './ui/Button';
import { useI18n } from '@/i18n/locale';
import { createDevice, type CreateDeviceInput } from '@/api/devices';

type Props = {
  open: boolean;
  onClose(): void;
  onCreated?(): void;
};

export function CreateDeviceModal({ open, onClose, onCreated }: Props) {
  const { tr } = useI18n();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [hostname, setHostname] = useState('');
  const [sshHost, setSshHost] = useState('');
  const [sshPort, setSshPort] = useState('22');
  const [sshUser, setSshUser] = useState('root');
  const [authKind, setAuthKind] = useState<'password' | 'key'>('password');
  const [credential, setCredential] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      // Reset the form so reopening doesn't leak last invocation's
      // password back into the textarea.
      setName('');
      setDescription('');
      setHostname('');
      setSshHost('');
      setSshPort('22');
      setSshUser('root');
      setAuthKind('password');
      setCredential('');
      setErr(null);
      setSubmitting(false);
    }
  }, [open]);

  const submit = async () => {
    const trimmedName = name.trim();
    if (!trimmedName) {
      setErr(tr('请填写名称', 'Name is required'));
      return;
    }
    if (!sshHost.trim()) {
      setErr(tr('请填写 SSH 主机', 'SSH host is required'));
      return;
    }
    if (!sshUser.trim()) {
      setErr(tr('请填写 SSH 用户', 'SSH user is required'));
      return;
    }
    const port = Number(sshPort || '22');
    if (!Number.isFinite(port) || port < 1 || port > 65535) {
      setErr(tr('端口必须在 1-65535 之间', 'Port must be between 1 and 65535'));
      return;
    }
    if (!credential.trim()) {
      setErr(
        authKind === 'password'
          ? tr('请填写密码', 'Password is required')
          : tr('请填写私钥', 'Private key is required'),
      );
      return;
    }
    setSubmitting(true);
    setErr(null);
    const body: CreateDeviceInput = {
      name: trimmedName,
      description: description.trim() || undefined,
      hostname: hostname.trim() || undefined,
      ssh_host: sshHost.trim(),
      ssh_port: port,
      ssh_user: sshUser.trim(),
      ssh_auth_kind: authKind,
    };
    if (authKind === 'password') body.ssh_password = credential;
    else body.ssh_key = credential;
    try {
      await createDevice(body);
      onCreated?.();
      onClose();
    } catch (e) {
      setErr((e as Error).message || tr('创建失败', 'Create failed'));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={tr('添加设备', 'Add device')}
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={submitting}>
            {tr('取消', 'Cancel')}
          </Button>
          <Button variant="primary" onClick={submit} disabled={submitting}>
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
              onChange={(e) => setName(e.target.value)}
              placeholder={tr('例如 db-prod-01', 'e.g. db-prod-01')}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <Field label={tr('描述', 'Description')}>
            <input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={tr('可选', 'optional')}
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <Field label={tr('主机名', 'Hostname')}>
            <input
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
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
                onChange={(e) => setSshHost(e.target.value)}
                placeholder="10.0.0.1"
                className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              />
            </Field>
            <Field label={tr('端口', 'Port')}>
              <input
                inputMode="numeric"
                value={sshPort}
                onChange={(e) => setSshPort(e.target.value.replace(/[^0-9]/g, ''))}
                placeholder="22"
                className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
              />
            </Field>
          </div>
          <Field label={tr('用户', 'User')} required>
            <input
              value={sshUser}
              onChange={(e) => setSshUser(e.target.value)}
              placeholder="root"
              className="w-full rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
            />
          </Field>
          <Field label={tr('认证方式', 'Auth method')}>
            <div className="flex items-center gap-4 text-xs">
              <label className="flex cursor-pointer items-center gap-2">
                <input
                  type="radio"
                  name="auth-kind"
                  className="accent-zinc-300"
                  checked={authKind === 'password'}
                  onChange={() => {
                    setAuthKind('password');
                    setCredential('');
                  }}
                />
                <span>{tr('密码', 'Password')}</span>
              </label>
              <label className="flex cursor-pointer items-center gap-2">
                <input
                  type="radio"
                  name="auth-kind"
                  className="accent-zinc-300"
                  checked={authKind === 'key'}
                  onChange={() => {
                    setAuthKind('key');
                    setCredential('');
                  }}
                />
                <span>{tr('私钥', 'Private key')}</span>
              </label>
            </div>
          </Field>
          <Field
            label={authKind === 'password' ? tr('密码', 'Password') : tr('私钥 (PEM)', 'Private key (PEM)')}
            required
          >
            <textarea
              value={credential}
              onChange={(e) => setCredential(e.target.value)}
              rows={authKind === 'key' ? 6 : 2}
              placeholder={
                authKind === 'password'
                  ? tr('SSH 登录密码', 'SSH password')
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