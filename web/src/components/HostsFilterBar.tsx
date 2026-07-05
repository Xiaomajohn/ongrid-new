// HostsFilterBar.tsx — filter row above the Hosts table.
//
// All filter state lives in the parent (Hosts.tsx) so the table can
// re-fetch on each change. The bar owns the search debounce (300ms)
// because parent rerenders fire on every keystroke otherwise and we'd
// burn an extra round trip per character.
//
// Backend currently accepts: name, hostname, roles, online. We mirror
// those as controls. "Edge 状态" is a UI-only concept today (we infer
// it client-side from edgesPerHost) so the bar shows it disabled until
// a backend filter exists; same for the per-device "已删除" toggle
// (uses include_deleted param we just added to devices.ts).

import { useEffect, useRef, useState } from 'react';
import { useI18n } from '@/i18n/locale';

export type HostsFilterValue = {
  name: string;
  role: string; // '' | 'server' | 'storage' | 'network' | 'database'
  online: 'all' | 'online' | 'offline';
  edge: 'all' | 'online' | 'offline';
  includeDeleted: boolean;
};

type Props = {
  value: HostsFilterValue;
  total: number;
  onChange(next: HostsFilterValue): void;
};

const SEARCH_DEBOUNCE_MS = 300;

export function HostsFilterBar({ value, total, onChange }: Props) {
  const { tr } = useI18n();
  // Search field mirrors `value.name` but lets us debounce independently
  // of the parent's other filter changes (which take effect immediately).
  const [search, setSearch] = useState(value.name);
  const debounceRef = useRef<number | null>(null);

  // When parent's name filter changes externally (e.g. user clears with
  // a button outside the bar), sync the local field too. Avoids the
  // "I clicked reset but the search box still has the old text" trap.
  useEffect(() => {
    setSearch(value.name);
  }, [value.name]);

  useEffect(() => {
    if (search === value.name) return;
    if (debounceRef.current) window.clearTimeout(debounceRef.current);
    debounceRef.current = window.setTimeout(() => {
      onChange({ ...value, name: search });
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      if (debounceRef.current) window.clearTimeout(debounceRef.current);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search]);

  return (
    <div className="mb-3 flex flex-wrap items-center gap-2 rounded-lg border border-zinc-800/60 bg-zinc-900/40 px-3 py-2 text-xs">
      <input
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        placeholder={tr('搜索名称 / 主机名', 'Search name / hostname')}
        className="w-56 rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-100 focus:border-zinc-600 focus:outline-none"
      />

      <Select
        value={value.role}
        onChange={(v) => onChange({ ...value, role: v })}
        options={[
          { value: '', label: tr('全部角色', 'All roles') },
          { value: 'server', label: tr('服务器', 'Server') },
          { value: 'storage', label: tr('存储', 'Storage') },
          { value: 'network', label: tr('网络设备', 'Network') },
          { value: 'database', label: tr('数据库', 'Database') },
        ]}
        placeholder={tr('角色', 'Role')}
      />

      <Select
        value={value.online}
        onChange={(v) => onChange({ ...value, online: v as HostsFilterValue['online'] })}
        options={[
          { value: 'all', label: tr('全部状态', 'All status') },
          { value: 'online', label: tr('在线', 'Online') },
          { value: 'offline', label: tr('离线', 'Offline') },
        ]}
        placeholder={tr('在线', 'Online')}
      />

      <Select
        value={value.edge}
        onChange={(v) => onChange({ ...value, edge: v as HostsFilterValue['edge'] })}
        options={[
          { value: 'all', label: tr('Edge 全部', 'Edge all') },
          { value: 'online', label: tr('Edge 在线', 'Edge online') },
          { value: 'offline', label: tr('Edge 离线', 'Edge offline') },
        ]}
        placeholder={tr('Edge 状态', 'Edge status')}
      />

      <label className="ml-2 inline-flex cursor-pointer items-center gap-1.5 text-zinc-300">
        <input
          type="checkbox"
          checked={value.includeDeleted}
          onChange={(e) => onChange({ ...value, includeDeleted: e.target.checked })}
          className="h-3.5 w-3.5 accent-zinc-300"
        />
        {tr('显示已删除', 'Show deleted')}
      </label>

      <div className="ml-auto text-[11px] text-zinc-500">
        {tr(`共 ${total} 台`, `${total} total`)}
      </div>
    </div>
  );
}

function Select({
  value,
  onChange,
  options,
  placeholder,
}: {
  value: string;
  onChange(v: string): void;
  options: Array<{ value: string; label: string }>;
  placeholder?: string;
}) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="rounded-md border border-zinc-800 bg-zinc-950 px-2 py-1.5 text-xs text-zinc-200 focus:border-zinc-600 focus:outline-none"
      aria-label={placeholder}
    >
      {options.map((o) => (
        <option key={o.value || '__all'} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}
