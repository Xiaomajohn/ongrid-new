# Agent B3 — PluginHost 前端骨架 Final Report

> 任务 T8(Phase 2,B3 子 agent):web 前端骨架 + api client。
> 工作目录:`f:\Code\Go\运维\ongrid-new`
> 时间:2026-07-14
> 不修改任何 A 老文件(`Sidebar.tsx` 留给 E1 Phase 5 接入)。

---

## 1. 文件清单(全部新增)

| # | 路径 | 行数 | 角色 |
|---|------|------|------|
| 1 | `web/src/api/pluginhost.ts` | 229 | 类型 + HTTP client(axios-free,走项目封装的 `request<T>`) |
| 2 | `web/src/pages/Plugins/index.tsx` | 474 | 列表页(`/plugins`) |
| 3 | `web/src/pages/Plugins/detail.tsx` | 511 | 详情页 + 4 tabs(`/plugins/:id`) |
| 4 | `web/src/pages/Plugins/invoke.tsx` | 278 | 试运行(`/plugins/:id/invoke?cap=`) |
| 5 | `web/src/pages/Plugins/install.tsx` | 328 | 安装(`/plugins/install`) |

总计 5 个文件,1820 行(包含注释和空行)。

## 2. TS 类型检查

```
$ cd f:\Code\Go\运维\ongrid-new\web
$ pnpm run typecheck
> ongrid-web@0.1.0 typecheck F:\Code\Go\运维\ongrid-new\web
> tsc -b --noEmit
exit 0
```

**通过**。过程中只遇到一次类型错误(`FilterGroup` 的 onChange 与
`Dispatch<SetStateAction<T>>` 的协变不兼容),通过加显式泛型实参 +
放宽 onChange 形参解决,无其它问题。

> 注:任务文案里提到"用 axios",但项目并未引入 axios,且"不引入新依赖"
> 与之冲突。统一通过项目已有的 `web/src/api/client.ts` (`request<T>`,
> 内部 fetch + 自动 refresh + ApiError 转换) 走 `/api/v1/pluginhost/...`
> 路径,与其它 api/*.ts 文件(marketplace / audit / alerts / skills 等)
> 保持一致。

## 3. API 端点表

所有调用都通过 `request<T>(method, path, body?)`,前缀为 `/api/v1`(取自
`client.ts` BASE),path 加 `BASE_PREFIX = '/pluginhost'`,最终 wire URL:

| 页面调用 | Method | Path(在 /api/v1 之下) | Payload | Response |
|---|---|---|---|---|
| `listPlugins` | GET | `/pluginhost/instances` | — | `{items: PluginInstance[]}` (兼容 PascalCase) |
| `getPlugin(id)` | GET | `/pluginhost/instances/:id` | — | `PluginInstance` |
| `listCapabilities(id)` | GET | `/pluginhost/instances/:id/capabilities` | — | `{items: PluginCapability[]}` |
| `listAudits(id)` | GET | `/pluginhost/instances/:id/audits` | — | `{items: PluginAudit[]}` |
| `installPlugin(spec)` | POST | `/pluginhost/instances` | `{source, path?, url?}` | `{id: number}` |
| `uninstallPlugin(id)` | DELETE | `/pluginhost/instances/:id` | — | void |
| `enableCapability(id, name)` | POST | `/pluginhost/instances/:id/capabilities/:name/enable` | — | void |
| `disableCapability(id, name)` | POST | `/pluginhost/instances/:id/capabilities/:name/disable` | — | void |
| `invokeCapability(id, name, params)` | POST | `/pluginhost/instances/:id/capabilities/:name/invoke` | `{params}` | `{result?, error?, latency_ms}` |
| `uploadTarball(file)` | POST | `/pluginhost/upload` | `FormData(file)` | `{id: number}` |

> 任务文案给的是 `/api/pluginhost/v1/*`(后端 mount 路径前缀),但前端
> BASE 是 `/api/v1`,且 `request()` 内部已拼 `/api/v1${path}`,所以前端
> path 用 `/pluginhost/...` → 最终 wire URL `/api/v1/pluginhost/...`。
> 等 Phase 4 D1 接入 server handler 时若实际 mount 在 `/api/pluginhost/v1/*`,
> 把 client.ts 的 BASE 或本文件的 BASE_PREFIX 调成对应路径即可,函数
> 签名不动。

## 4. 类型导出(`web/src/api/pluginhost.ts`)

| 类型 | 用途 |
|---|---|
| `PluginInstance` | 一行 `plugin_instances` 表(列表 / 详情 / install 响应) |
| `PluginCapability` | 一行 `plugin_capabilities` 表(详情 Capabilities tab / invoke) |
| `PluginAudit` | 一行 `plugin_audits` 表(详情 Audit tab) |
| `InvokeResult` | invoke 接口响应 `{result, error, latency_ms}` |
| `InstallSpec` | install 接口入参 `{source, path?, url?}` |

字段全部 snake_case,跟后端 `model/plugin_*.go` 对齐。

## 5. 组件复用清单(`web/src/components/ui/`)

| 组件 | 使用页 | 用途 |
|---|---|---|
| `PageHeader` | 4/4 页 | 标题 + 返回链 + 副标题 + actions |
| `Card` | 4/4 页 | 容器(列表 / 详情 / invoke 输入 / install 三块) |
| `Button` | index / detail / install | 主操作 / 次操作 / 危险操作三色 |
| `Chip` | index / detail | health 状态 + class 安全等级 + kind |
| `EmptyState` | index / detail | 空态(列表空 / 无 caps / 无 audits) |

非 `ui/` 但复用:
- `@/components/Modal` — install 不需要,detail 的卸载 confirm modal
  直接用 Modal 自渲染(无 type-to-confirm 模板时复用更轻)。
- `@/lib/cn`、`@/lib/usePoll`、`@/lib/format`(relativeTime) — 通用工具。
- `request<T>` from `@/api/client` — HTTP client。
- `useI18n` / `tr` from `@/i18n/locale` — 双语文案。

**未引入任何新 npm 依赖**(任务硬约束)。

## 6. Phase 5 — Sidebar 接入 TODO(给 E1)

在 `web/src/components/Sidebar.tsx` 末尾追加一个 `SidebarNavItem`,建议
放在 `Agent` section(SidebarNavItem `Skills` 之后、`MCP` 之前)或单独
建一个 `Plugins` section,以下两种位置都合理,选一种:

**方案 A(推荐,放在 Skills 旁边)** — 在 Sidebar.tsx 现有第 419 行
`<SidebarNavItem to="/skills" icon={Wrench} ... />` 之后、第 420 行
`<SidebarNavItem to="/mcp" icon={Plug} label="MCP" />` 之前,插入:

```tsx
<SidebarNavItem to="/plugins" icon={Puzzle} label={tr('插件', 'Plugins')} />
```

并在文件顶部 `lucide-react` 的 import 块里(目前第 3-35 行)追加
`Puzzle`(插件的视觉 icon,project 已经在用 — 见 Skills.tsx)。

**方案 B(单独 section)** — 在文件第 423 行 `SectionLabel>{tr('知识库'...)}`
上方新增:
```tsx
<SectionLabel>{tr('扩展', 'Extensions')}</SectionLabel>
<NavSection>
  <SidebarNavItem to="/plugins" icon={Puzzle} label={tr('插件', 'Plugins')} />
</NavSection>
```

> 此外还需要在 `web/src/App.tsx` 的 `<Routes>` 里加 4 条路由
> (当前任务不做,E1 或 D1 接入时一起做):
>
> ```tsx
> const PluginsPage = lazy(() => import('@/pages/Plugins'));
> const PluginDetailPage = lazy(() => import('@/pages/Plugins/detail'));
> const PluginInvokePage = lazy(() => import('@/pages/Plugins/invoke'));
> const PluginInstallPage = lazy(() => import('@/pages/Plugins/install'));
> // ...
> <Route path="/plugins" element={<PluginsPage />} />
> <Route path="/plugins/install" element={<PluginInstallPage />} />
> <Route path="/plugins/:id" element={<PluginDetailPage />} />
> <Route path="/plugins/:id/invoke" element={<PluginInvokePage />} />
> ```
>
> 注意 App.tsx 现有约定是把 `Plugins/index.tsx` 作为 `PluginsPage`
> (`import` 路径写到目录),而 detail / invoke / install 在子文件
> 需要显式路径,见 Edges/EdgeDetail 那一对的写法。

## 7. Phase 4 server handler 接入 TODO(给 D1)

后端需在 `internal/pluginhost/server/routes.go` 注册以下路由
(前缀 `/api/pluginhost/v1` 由 main.go append 那 ~30 行统一 mount,
也可以挂到 `/api/v1/pluginhost` 与前端 BASE 对齐 — 选哪种 mount 路径
取决于 cmd/ongrid/main.go 的 wire-up 决策):

| Wire URL | Handler 函数建议命名 | 返回类型 |
|---|---|---|
| `GET /pluginhost/instances` | `ListInstances` | `{items: []model.PluginInstance}` |
| `GET /pluginhost/instances/:id` | `GetInstance` | `model.PluginInstance` |
| `GET /pluginhost/instances/:id/capabilities` | `ListCapabilities` | `{items: []model.PluginCapability}` |
| `GET /pluginhost/instances/:id/audits` | `ListAudits` | `{items: []model.PluginAudit}` |
| `POST /pluginhost/instances` | `Install` | `{id: number}` |
| `DELETE /pluginhost/instances/:id` | `Uninstall` | `204` |
| `POST /pluginhost/instances/:id/capabilities/:name/enable` | `EnableCap` | `204` |
| `POST /pluginhost/instances/:id/capabilities/:name/disable` | `DisableCap` | `204` |
| `POST /pluginhost/instances/:id/capabilities/:name/invoke` | `Invoke` | `pluginhost.InvokeResult` |
| `POST /pluginhost/upload` | `UploadTarball` (multipart) | `{id: number}` |

字段名必须 snake_case,与 `web/src/api/pluginhost.ts` 的 TS 类型一致;
如 model 没打 json tag,参考 `marketplace.ts` 的 `RawInstalledPack`
蛇 / 帕斯卡双兼容 fallback。

`Invoke` handler 内部要执行的逻辑:
1. 调用 `pluginhost.Invoke(ctx, pluginID, capName, InvokeRequest{Params: params})`
2. 把返回的 `InvokeResponse` 序列化成 `{result, error, latency_ms}`
3. 失败时 HTTP 200 + body 仍 `{error, latency_ms:0}`,或 HTTP 4xx —
   跟 team 定。本前端 ApiError 会把非 2xx 转 Error,前端 invoke.tsx
   的 onInvoke 已经两层都接(error 字段 + 异常)。

`UploadTarball` 字段名:`file`(不是 `archive` / `tarball`,前端
`uploadTarball` 用 `fd.append('file', file)`)。

## 8. 其它备注

- **enable toggle** 在列表页 (`index.tsx`) 当前是本地乐观翻转 —
  后端 model 在 Phase 1 的 `model.PluginInstance` 上有 `Enabled bool`
  字段,但 server handler 还没出,所以前端暂时走本地状态。Phase 4 D1
  接入后,在 `onToggleEnabled` 里改成调用 `enableCapability` /
  `disableCapability` 即可(已留好接入点,见 `index.tsx` 第 110-140 行)。
- **filters** (source / health) 是 client-side 的,因为 server list
  还没接 query 参数。Phase 4 D1 时改成 URL query 推到 server。
- **Audit tab** 显示完整 `details_json` 字段(原始字符串),不尝试
  二次 JSON.parse + 美化,因为 audit 来源可能是任意结构。等真实数据
  进来了再决定要不要美化。
- **Config tab** 当前是 placeholder,plan §14.2 T13"前端联调"阶段
  会做运行时配置 / 数据作用域 / 凭据绑定的 UI,等 D1 接 server 后
  再补(本任务不做)。
- **install page 的 manifest 预览** 当前只是 local draft,显示
  `{source, filename/size/path/url}`。Phase 4 D1 接入后改成调用
  真正的 manifest 预解析接口(POST `/pluginhost/instances/preview`
  或类似),展示 pack_id / version / capabilities[] / permissions。
- **未 import 任何尚未写的页面**(任务硬约束):install.tsx 仅 import
  pluginhost.ts;detail.tsx 只 import index 页面路由不通过 index.tsx
  而通过 router。✅
- **未写测试文件**(任务硬约束)。✅

---

## 9. 验证步骤(主 agent 接手时跑)

```bash
# 1. TS 类型检查
cd f:\Code\Go\运维\ongrid-new\web
pnpm run typecheck        # exit 0,已验证

# 2. 视觉冒烟(等 E1 接 Sidebar + D1 接 server handler 后跑)
# 打开 /plugins → 列表 → 安装 → 试运行 → 详情 tabs

# 3. Phase 5 联调(由 E1)改 Sidebar + App.tsx 路由后:
pnpm run typecheck && pnpm run build
```