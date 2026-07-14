// SearchableSelect — 通用可搜索下拉。Logs 页面设备 / 任务下拉用。
//
// 与原生 <select> 的差别：
//   - 顶部 input 可键入做客户端 filter（受 options 数组大小限制的纯
//     客户端过滤；调用方把全量 options 传进来即可，0 延迟）。
//   - 键盘可达：↑↓ 选中、Enter 确认、Esc 关闭、Backspace 清空。
//   - 视觉上比 <select> 更克制：参考 RoleSelect 的 `block` 变体 + 视觉
//     规范（zinc 容器 + zinc 边框 / 文字，避免 hover:scale / 阴影 /
//     animate-pulse / 发光）。
//
// 调用方职责：
//   - 把全量 options 传进来；filter 由本组件内部做。
//   - onChange 拿到的是 value 字符串（与 <select> 语义一致）。
//   - 真正要支持服务端搜索时，在外面用 useState(input) + debounce + 远
//     程 options，本组件只负责渲染 + 选中回调。
import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { ChevronDown, X } from 'lucide-react';
import { cn } from '@/lib/cn';

export type SearchableSelectOption = {
  value: string;
  label: string;
  // muted 行（zinc-500）用于渲染"已删除"等需要弱化的场景。匹配仍
  // 按 label 走，只是显示上灰一档。
  muted?: boolean;
  // hint 副标题，例如显示 ip / hostname 等次级信息。
  hint?: string;
};

export function SearchableSelect({
  value,
  options,
  onChange,
  placeholder,
  emptyText,
  className,
  // 输入框变窄：Logs 页面 form 行宽 192px。外部传 width。
  // 不强制 width —— 通过 className 让调用方控制尺寸。
  showClearButton = true,
  // 失焦时是否收起面板（默认 true）。键盘交互时由 Enter / Esc 显式控制。
  blurCloses = true,
}: {
  value: string;
  options: SearchableSelectOption[];
  onChange(value: string): void;
  placeholder?: string;
  emptyText?: string;
  className?: string;
  showClearButton?: boolean;
  blurCloses?: boolean;
}) {
  const id = useId();
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState('');
  const [activeIdx, setActiveIdx] = useState(0);

  // 把已选 value 对应的 option 找出来：input 显示用当前选中项的 label；
  // 用户开始输入时切到 input 自身。这与原生 <select> 不一样 —— 原生是
  // 一直显示 label。这里在 idle（未输入）状态下显示 label，进入编辑后
  // 显示用户输入。
  const selected = useMemo(
    () => options.find((o) => o.value === value) ?? null,
    [options, value],
  );
  const [editing, setEditing] = useState(false);

  // 客户端 filter：input 与 label / hint 都不分大小写包含关系。
  const filtered = useMemo(() => {
    const q = input.trim().toLowerCase();
    if (!q) return options;
    return options.filter(
      (o) =>
        o.label.toLowerCase().includes(q) ||
        (o.hint ? o.hint.toLowerCase().includes(q) : false),
    );
  }, [options, input]);

  // open / filtered 变化时把 activeIdx 拉回到 0，避免上一次 hover 索引
  // 越界。Esc 关闭后再开也会触发重置。
  useEffect(() => {
    setActiveIdx(0);
  }, [input, open]);

  // 点击组件外区域时关闭面板。键盘交互通过 mousedown 阻止冒泡避免
  // 误关。
  useEffect(() => {
    if (!open) return;
    const onDocDown = (e: MouseEvent) => {
      if (!wrapRef.current) return;
      if (!wrapRef.current.contains(e.target as Node)) {
        if (blurCloses) setOpen(false);
      }
    };
    document.addEventListener('mousedown', onDocDown);
    return () => document.removeEventListener('mousedown', onDocDown);
  }, [open, blurCloses]);

  const commit = (v: string) => {
    onChange(v);
    setOpen(false);
    setEditing(false);
    setInput('');
  };

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setOpen(true);
      setActiveIdx((i) => Math.min(filtered.length - 1, i + 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setActiveIdx((i) => Math.max(0, i - 1));
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const pick = filtered[activeIdx];
      if (pick) commit(pick.value);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      setOpen(false);
      setEditing(false);
      setInput('');
    } else if (e.key === 'Backspace' && !input && showClearButton && value) {
      e.preventDefault();
      commit('');
    }
  };

  // 显示文本：编辑中显示 input；否则显示已选 option.label 或 placeholder
  const displayValue = editing ? input : selected ? selected.label : '';

  return (
    <div ref={wrapRef} className={cn('relative w-full', className)}>
      <div
        className={cn(
          // 高度固定 34px，与 Logs 页面 INPUT_BASE (h-[34px]) 保持一
          // 致，让设备 / 任务下拉与旁边的角色 / 文件 / unit 下拉视觉
          // 对齐。py-0 换成 h-[34px] 是为消除不同 line-height 引起的
          // 高度漂移。
          'flex h-[34px] items-center gap-1 rounded-md border bg-zinc-950 pl-2 pr-1.5 text-xs text-zinc-100 transition-colors',
          open
            ? 'border-zinc-600 ring-1 ring-zinc-600/30'
            : 'border-zinc-800 hover:border-zinc-700',
        )}
      >
        <input
          id={id}
          ref={inputRef}
          // type=text 才能绑 onKeyDown
          type="text"
          value={displayValue}
          placeholder={placeholder}
          onFocus={() => {
            setOpen(true);
            setEditing(true);
            setInput('');
          }}
          onChange={(e) => {
            setEditing(true);
            setInput(e.target.value);
            setOpen(true);
          }}
          onKeyDown={onKey}
          className="min-w-0 flex-1 bg-transparent text-xs text-zinc-100 placeholder:text-zinc-600 focus:outline-none"
        />
        {showClearButton && value && !editing && (
          <button
            type="button"
            aria-label="clear"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => commit('')}
            className="rounded p-0.5 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200"
          >
            <X size={11} />
          </button>
        )}
        <ChevronDown
          size={12}
          className={cn(
            'shrink-0 text-zinc-500 transition-transform',
            open && 'rotate-180',
          )}
        />
      </div>
      {open && (
        <div
          role="listbox"
          className={cn(
            'absolute left-0 right-0 z-30 mt-1 max-h-64 overflow-y-auto rounded-md border',
            'border-zinc-800 bg-zinc-950 shadow-lg shadow-black/40',
          )}
        >
          {filtered.length === 0 ? (
            <div className="px-2 py-2 text-center text-[11px] text-zinc-600">
              {emptyText ?? '无匹配项'}
            </div>
          ) : (
            filtered.map((o, i) => {
              const active = i === activeIdx;
              const selected = o.value === value;
              return (
                <div
                  key={o.value}
                  role="option"
                  aria-selected={selected}
                  onMouseDown={(e) => {
                    // onMouseDown 早于 input blur，提前 commit 避免
                    // 触发外点关闭。
                    e.preventDefault();
                    commit(o.value);
                  }}
                  onMouseEnter={() => setActiveIdx(i)}
                  className={cn(
                    'flex cursor-pointer items-center gap-2 px-2 py-1.5 text-xs',
                    active
                      // active 文字色用 text-zinc-100 而不是 text-indigo-100：
                      // index.css 在 html.light 下把 text-zinc-100 覆盖为
                      // --text（深色），与浅紫背景对比强；text-indigo-100 在
                      // light mode 下未被覆盖仍是浅蓝紫，与 bg-indigo-500/15
                      // 背景同色 → 用户只能看到 hint 文字、看不到主名。dark
                      // mode 下 text-zinc-100（近白）与偏暗紫底对比也清楚。
                      ? 'bg-indigo-500/15 text-zinc-100'
                      : selected
                        ? 'bg-zinc-800/60 text-zinc-100'
                        : 'text-zinc-200 hover:bg-zinc-900',
                    o.muted && 'text-zinc-500',
                  )}
                >
                  <div className="min-w-0 flex-1 truncate">
                    <div className="truncate">{o.label}</div>
                    {o.hint && (
                      <div className="truncate text-[10px] text-zinc-500">{o.hint}</div>
                    )}
                  </div>
                </div>
              );
            })
          )}
        </div>
      )}
    </div>
  );
}
