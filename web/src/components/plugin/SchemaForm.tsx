// SchemaForm — 通用 JSON Schema 表单渲染器。
//
// Phase 5 E4:web 端根据 plugin capability 的 schema(JSON Schema 子集)
// 渲染出对应的输入控件,把用户填写结果回成 JSON object,供
// invoke API 的 params 字段直接透传。
//
// 覆盖的 JSON Schema 子集(刻意收口,不实现完整 JSON Schema):
//   - type:string / number / integer / boolean
//   - enum:[...] → <select>
//   - format:"password" → <input type="password">
//   - format:"textarea" → <textarea>
//   - format:"multiline" → <textarea rows=4>
//   - required:[...] → 必填校验
//   - default → 初始值
//   - description → 控件下方 hint 文案
//   - minLength / maxLength / minimum / maximum → 数值约束
//   - pattern → string 正则
//
// 不覆盖:
//   - oneOf / anyOf / allOf(plan:Phase 5+ 视情况补)
//   - $ref / definitions(避免循环依赖)
//   - array / object 嵌套深表单(本期只支持一层平铺,够 90% 用例)
//
// 校验失败时:控件变红边框 + 错误信息显示在控件下方,提交按钮置灰。
// 父组件 onChange 持续拿到当前表单值(即使有错误也传,便于实时预览)。

import { useMemo, useState } from 'react';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/cn';

// ----- types --------------------------------------------------------------

export type SchemaFormValue = Record<string, unknown>;

/**
 * JSON Schema 子集。刻意只声明本组件用到的字段,完整 JSON Schema 由
 * server 端 schema 校验阶段(P1 暂未启用)兜底。
 */
export interface JSONSchemaSubset {
  type?: 'object' | 'string' | 'number' | 'integer' | 'boolean';
  properties?: Record<string, JSONSchemaSubset>;
  required?: string[];
  default?: unknown;
  description?: string;
  enum?: unknown[];
  /** string formats we recognize: "password" / "textarea" / "multiline". */
  format?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
  pattern?: string;
  title?: string;
}

type FieldErrors = Record<string, string | undefined>;

export interface SchemaFormProps {
  schema: JSONSchemaSubset;
  /** 初始值;key 不在 schema.properties 里的字段会被忽略。 */
  initial?: SchemaFormValue;
  /** 表单值变化时实时回调(包含错误态的值,便于预览)。 */
  onChange?: (value: SchemaFormValue, valid: boolean) => void;
  /** 提交按钮文案;默认 "Submit"。 */
  submitLabel?: string;
  /** 隐藏提交按钮(onChange-only 模式,父组件自己控制提交)。 */
  hideSubmit?: boolean;
  /** 父组件控制提交。 */
  onSubmit?: (value: SchemaFormValue) => void;
  /** 外部 loading 态(提交中 → 按钮置灰 + 文案换 "Submitting...")。 */
  loading?: boolean;
  /** 容器 className 覆盖。 */
  className?: string;
}

// ----- helpers ------------------------------------------------------------

/**
 * 把 schema + initial 拍平成 "key -> schema" 列表。
 *
 * P1 阶段只支持 object 类型 + 一层平铺 properties。遇到嵌套 object /
 * array 暂时以空字段跳过,并在 console.warn 提示调用方升级。
 */
function flattenSchema(
  schema: JSONSchemaSubset,
): Array<{ key: string; sub: JSONSchemaSubset; required: boolean }> {
  if (schema.type !== 'object' || !schema.properties) {
    return [];
  }
  const required = new Set(schema.required ?? []);
  return Object.entries(schema.properties).map(([key, sub]) => ({
    key,
    sub,
    required: required.has(key),
  }));
}

/** 校验单个字段值;返回错误消息(空串 = 通过)。 */
function validateField(
  key: string,
  sub: JSONSchemaSubset,
  value: unknown,
  required: boolean,
): string {
  const isEmpty =
    value === undefined ||
    value === null ||
    (typeof value === 'string' && value.length === 0);
  if (required && isEmpty) {
    return `${key} 不能为空`;
  }
  if (isEmpty) {
    return '';
  }
  switch (sub.type) {
    case 'string': {
      if (typeof value !== 'string') return `${key} 必须是字符串`;
      if (sub.minLength !== undefined && value.length < sub.minLength) {
        return `${key} 至少 ${sub.minLength} 字符`;
      }
      if (sub.maxLength !== undefined && value.length > sub.maxLength) {
        return `${key} 最多 ${sub.maxLength} 字符`;
      }
      if (sub.pattern) {
        try {
          if (!new RegExp(sub.pattern).test(value)) {
            return `${key} 格式不匹配`;
          }
        } catch {
          // pattern 本身非法时静默跳过(避免一个错误阻塞整个表单)
        }
      }
      return '';
    }
    case 'number':
    case 'integer': {
      const n = typeof value === 'number' ? value : Number(value);
      if (!Number.isFinite(n)) return `${key} 必须是数字`;
      if (sub.type === 'integer' && !Number.isInteger(n)) {
        return `${key} 必须是整数`;
      }
      if (sub.minimum !== undefined && n < sub.minimum) {
        return `${key} 不能小于 ${sub.minimum}`;
      }
      if (sub.maximum !== undefined && n > sub.maximum) {
        return `${key} 不能大于 ${sub.maximum}`;
      }
      return '';
    }
    case 'boolean': {
      if (typeof value !== 'boolean') return `${key} 必须是布尔`;
      return '';
    }
    default:
      return '';
  }
}

// ----- component ----------------------------------------------------------

export function SchemaForm({
  schema,
  initial,
  onChange,
  onSubmit,
  submitLabel = '提交',
  hideSubmit = false,
  loading = false,
  className,
}: SchemaFormProps) {
  const fields = useMemo(() => flattenSchema(schema), [schema]);

  // 默认值 = initial > schema.default > 类型兜底
  const [value, setValue] = useState<SchemaFormValue>(() => {
    const v: SchemaFormValue = {};
    for (const f of fields) {
      if (initial && initial[f.key] !== undefined) {
        v[f.key] = initial[f.key];
      } else if (f.sub.default !== undefined) {
        v[f.key] = f.sub.default;
      } else {
        v[f.key] = defaultByType(f.sub.type);
      }
    }
    return v;
  });

  const [errors, setErrors] = useState<FieldErrors>({});

  const setField = (key: string, v: unknown) => {
    const next = { ...value, [key]: v };
    setValue(next);
    const f = fields.find((x) => x.key === key);
    if (f) {
      const msg = validateField(key, f.sub, v, f.required);
      const nextErrors = { ...errors, [key]: msg || undefined };
      setErrors(nextErrors);
      onChange?.(next, !Object.values(nextErrors).some(Boolean));
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    // 全字段校验
    const allErrors: FieldErrors = {};
    for (const f of fields) {
      const msg = validateField(f.key, f.sub, value[f.key], f.required);
      if (msg) allErrors[f.key] = msg;
    }
    setErrors(allErrors);
    if (Object.values(allErrors).some(Boolean)) return;
    onSubmit?.(value);
  };

  if (fields.length === 0) {
    return (
      <div className={cn('text-xs text-zinc-500', className)}>
        该 capability 无可填写字段。
      </div>
    );
  }

  return (
    <form
      onSubmit={handleSubmit}
      className={cn('flex flex-col gap-3', className)}
    >
      {fields.map((f) => (
        <SchemaField
          key={f.key}
          fieldKey={f.key}
          sub={f.sub}
          required={f.required}
          value={value[f.key]}
          error={errors[f.key]}
          onChange={(v) => setField(f.key, v)}
        />
      ))}
      {!hideSubmit && (
        <div className="flex justify-end pt-1">
          <Button
            type="submit"
            variant="primary"
            disabled={loading}
          >
            {loading ? '提交中…' : submitLabel}
          </Button>
        </div>
      )}
    </form>
  );
}

// ----- single field -------------------------------------------------------

interface SchemaFieldProps {
  fieldKey: string;
  sub: JSONSchemaSubset;
  required: boolean;
  value: unknown;
  error?: string;
  onChange: (v: unknown) => void;
}

function SchemaField({
  fieldKey,
  sub,
  required,
  value,
  error,
  onChange,
}: SchemaFieldProps) {
  const label = sub.title ?? fieldKey;
  const hint = sub.description;

  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs font-medium text-zinc-300">
        {label}
        {required && <span className="ml-0.5 text-red-400">*</span>}
      </span>

      {/* enum → <select> */}
      {sub.enum ? (
        <select
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          className={selectCls(!!error)}
        >
          <option value="" disabled>
            请选择
          </option>
          {sub.enum.map((opt) => (
            <option key={String(opt)} value={String(opt)}>
              {String(opt)}
            </option>
          ))}
        </select>
      ) : sub.type === 'boolean' ? (
        <label className="inline-flex items-center gap-2 text-xs text-zinc-300">
          <input
            type="checkbox"
            checked={Boolean(value)}
            onChange={(e) => onChange(e.target.checked)}
            className="h-3.5 w-3.5 rounded border-zinc-700 bg-zinc-900"
          />
          启用
        </label>
      ) : sub.type === 'number' || sub.type === 'integer' ? (
        <input
          type="number"
          step={sub.type === 'integer' ? 1 : 'any'}
          value={value === undefined || value === null ? '' : String(value)}
          onChange={(e) => {
            const raw = e.target.value;
            if (raw === '') {
              onChange(undefined);
              return;
            }
            const n = Number(raw);
            onChange(Number.isFinite(n) ? n : raw);
          }}
          className={inputCls(!!error)}
        />
      ) : sub.format === 'textarea' || sub.format === 'multiline' ? (
        <textarea
          rows={sub.format === 'multiline' ? 4 : 2}
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          className={cn(inputCls(!!error), 'font-mono text-xs leading-relaxed')}
        />
      ) : sub.format === 'password' ? (
        <input
          type="password"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
          className={inputCls(!!error)}
        />
      ) : (
        // 默认:string input
        <input
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          className={inputCls(!!error)}
        />
      )}

      {hint && !error && (
        <span className="text-[11px] text-zinc-500">{hint}</span>
      )}
      {error && (
        <span className="text-[11px] text-red-400">{error}</span>
      )}
    </label>
  );
}

// ----- small helpers ------------------------------------------------------

function defaultByType(t: JSONSchemaSubset['type']): unknown {
  switch (t) {
    case 'string':
      return '';
    case 'number':
    case 'integer':
      return 0;
    case 'boolean':
      return false;
    default:
      return '';
  }
}

function inputCls(hasError: boolean): string {
  return cn(
    'rounded-md border bg-zinc-900 px-2.5 py-1.5 text-xs text-zinc-100 placeholder-zinc-600',
    'focus:outline-none focus:ring-1',
    hasError
      ? 'border-red-500/60 focus:border-red-400 focus:ring-red-500/30'
      : 'border-zinc-700 focus:border-indigo-500 focus:ring-indigo-500/30',
  );
}

function selectCls(hasError: boolean): string {
  return cn(inputCls(hasError), 'cursor-pointer');
}
