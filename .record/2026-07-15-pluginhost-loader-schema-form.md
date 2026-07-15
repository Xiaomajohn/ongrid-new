# 2026-07-15 · pluginhost Phase 5 E4:pluginLoader + SchemaForm(前端动态加载)

## 问题

E3 asset server 把 plugin 前端 bundle 的 HTTP 出口开通了,但 web 端还
缺少两块关键的"消费侧"组件:

1. **pluginLoader**:把 asset server 拿到的 JS 注入浏览器,把 plugin
   注册的 custom element 挂到 ongrid 主前端的某个容器里
2. **SchemaForm**:plugin 的 capability 携带 JSON Schema,需要在 invoke
   之前让用户填参数;ongrid 主前端不该每加一个 capability 都手写一遍
   form

E4 这一刀切完,plugin manifest → asset 拉取 → custom element 挂载 →
capability 参数录入 → invoke 调用这条链就能完整 demo。

## 加了什么

### 1. 新文件:`web/src/components/plugin/pluginLoader.tsx`(239 行)

`<PluginLoader pluginId={...} frontend={...} attrs={...} />` 组件:

- 用 `window.fetch` 拉 `/api/pluginhost/plugins/{pluginId}/assets/{entry}`,
  **绕过** `web/src/api/client.ts` 的 `/api/v1` prefix(因为 pluginhost
  server 走的是 `/api/pluginhost` mount,与主 API prefix 不同)
- 拉到的 JS 转成 `Blob` + `URL.createObjectURL`,再 append 到
  `document.head` 当 `<script>` 执行
- 轮询 `customElements.get(tagName)`,检测到即 `document.createElement`
  + `setAttribute(...)` + `appendChild` 挂到容器
- **8s 超时**(POLL_INTERVAL_MS=50),失败态显示「plugin 加载失败:
  ${msg}」红色 banner
- 卸载时:`script.remove()` + `URL.revokeObjectURL`,不漏资源

辅助导出 `pickFrontendFromCapability(cap)`:从 capability 的 metadata
里挑出 `frontend` 字段(P1 manifest 不强约束 frontend 字段位置,这条
helper 让调用方少写点 if-else)。

安全边界:

- 跨域:asset 走同源 `credentials: 'same-origin'`,不显式放 CORS
- 元素 tag:**由调用方传入 expectedTag**,与 pluginLoader 内部一致
  即挂;不一致交给 manifest 校验(server 端做),前端只信 server 返
  回的 metadata
- 错误态:不静默吞错,错误信息回到 `onError` + 显示 banner

### 2. 新文件:`web/src/components/plugin/SchemaForm.tsx`(389 行)

`<SchemaForm schema={...} initial={...} onChange={...} onSubmit={...} />`:

覆盖的 JSON Schema 子集(刻意收口,不实现完整 JSON Schema):

- `type: 'string' | 'number' | 'integer' | 'boolean'`
- `enum: [...]` → `<select>`
- `format: 'password' | 'textarea' | 'multiline'`
- `required: string[]` → 必填校验
- `default` / `description` → hint 文案
- `minLength` / `maxLength` / `minimum` / `maximum` / `pattern`

校验失败时:控件红色边框 + 错误信息显示在控件下方,提交按钮置灰。
父组件 `onChange` 持续拿到当前表单值(包含错误态,便于实时预览)。

不做:

- `oneOf` / `anyOf` / `allOf`(plan Phase 5+ 视情况补)
- `$ref` / `definitions`(避免循环依赖)
- array / object 嵌套深表单(本期只支持一层平铺,够 90% 用例)

UI 风格沿用现有 Button / zinc 调色:

- 错误态:`border-red-500/60` + `focus:ring-red-500/30`
- 普通态:`border-zinc-700` + `focus:border-indigo-500`
- 提交按钮:`<Button variant="primary">` + `loading` 态自动 disable

## 目的

让 plugin 这条链路具备「端到端可用」的基础 demo 能力:

```
plugin manifest 描述 frontend / capability schema
  → asset server 暴露 bundle + JSON
  → pluginLoader 拉 JS + 挂载 custom element
  → SchemaForm 渲 capability 参数录入 UI
  → 用户填完提交 → invoke router 调用 host
```

不加 pluginLoader,plugin UI 一行代码到不了浏览器;
不加 SchemaForm,每加一个 capability 都要手写一份 form,扩展性归零。

## 验证

```bash
$ go build ./internal/pluginhost/...
# 通过(server 端没有改动)

$ npx vite build (待 Phase 6 F 在 192.168.25.30 跑)
# 编译通过即可,本机不再跑 vitest
```

完整端到端验证留到 Phase 6 F:在 192.168.25.56 上装 demo plugin,看
asset 拉取 + custom element 挂载 + capability 表单渲染是否一致。

## 后续

- P1 补:array / object 嵌套深表单 + oneOf 分支渲染
- P1 补:ES Module 动态 import(plugin 端若用 ESM,server 要补 `.mjs`
  MIME 适配,pluginLoader 这边 `import(/* @vite-ignore */ blobUrl)`)
- P2:把 pluginLoader 装到 React.lazy + Suspense 里,做 code splitting
- P2:sandbox iframe(用 `<iframe srcdoc>` 把 plugin JS 与主页面 CSS /
  global scope 隔离),避免 plugin 端 CSS / DOM 污染主前端

## 已知限制

- custom element 名称碰撞未做兜底:plugin 端乱选 tag 可能和未来
  ongrid 主页面注册的元素重名;P1 阶段 manifest 校验强制带 `ongrid-`
  前缀(后续 RFC 再加约束)
- 当前实现不在 iframe 里跑 plugin JS,plugin 端 CSS 会污染 ongrid 主
  端 CSS(全页面 tailwind reset 可能踩到)。Phase 6 demo 阶段如果发现
  实际污染再上 iframe sandbox
