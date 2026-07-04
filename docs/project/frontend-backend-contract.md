# 前后端交互 — 鸟瞰

> 本文档描述 ongrid 的前后端交互契约：**前端 SPA 怎么调后端、API 客户端组织方式、鉴权与本地化机制、SSE 聊天流、RPC 桥**。聚焦"一次交互走过的路径"，不深入 React 组件实现。代码位置：`web/`、`internal/manager/server/`、`internal/edgeagent/`。

---

## 1. 总体鸟瞰

### 1.1 技术栈

| 维度 | 选型 | 关键依赖 |
| --- | --- | --- |
| 框架 | React 18 + TypeScript | `react`/`react-dom`/`react-router-dom` |
| 构建 | Vite 5（`vite.config.ts`） | HMR、本地开发服务、`@` 路径别名指向 `web/src` |
| 样式 | Tailwind CSS 3 + 自研 zinc 配色 | `tailwind.config.ts`、`postcss.config.js` |
| 状态管理 | Zustand（**9 个 store**，按职责分文件） | `web/src/store/<topic>.ts` |
| 国际化 | 自研 "inline 双语" 模式（**没有字典文件**） | `web/src/i18n/locale.ts` 单文件 |
| 数据获取 | 自研 `request<T>()` + fetch（**无 SWR / React Query**） | `web/src/api/client.ts` |
| 测试 | Vitest + React Testing Library + MSW | `web/src/test/{setup,msw-server}.ts` |
| Lint | ESLint + `@typescript-eslint` | `.eslintrc.cjs` |

> **轻栈哲学**：所有依赖只解决"必要"问题。状态用 zustand 而非 redux；i18n 用 inline 而非 i18next；数据获取用 fetch 包装而非 RQ 缓存——刻意保持栈薄、SPA 与后端通过纯 HTTP/SSE 通信而不是 SDK / RPC 生成物。

### 1.2 部署形态

```
┌─────────────────────────────────────────────────────┐
│                      浏览器                         │
│  ┌──────────────────────────────────────────────┐   │
│  │         ongrid SPA (web/dist/*)              │   │
│  │  Vite 静态构建 → nginx serve                 │   │
│  └──────────────────────────────────────────────┘   │
│         │   /api/v1/*   HTTP / SSE                 │
│         ▼                                            │
│  ┌──────────────────────────────────────────────┐   │
│  │   nginx 反向代理 (deploy/nginx/nginx.conf)   │   │
│  │     /api/v1/* → ongrid:8080                  │   │
│  └──────────────────────────────────────────────┘   │
│         │                                            │
│         ▼                                            │
│  ┌──────────────────────────────────────────────┐   │
│  │  ongrid 二进制 (cmd/ongrid/main.go)          │   │
│  │   chi 路由 + middleware + service + biz      │   │
│  └──────────────────────────────────────────────┘   │
│         │                                            │
│         │  frontier 反向隧道                          │
│         ▼                                            │
│  ┌──────────────────────────────────────────────┐   │
│  │       ongrid-edge 探针（每台主机）           │   │
│  └──────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
```

---

## 2. 前端目录结构（`web/src/`）

8 个一级目录，职责互不重叠：

| 目录 | 职责 | 备注 |
| --- | --- | --- |
| `api/` | **后端 API 客户端**（35 个 `.ts` 文件） | 所有 HTTP/SSE 调用出口；详见 §3 |
| `components/` | 复用 UI 组件与图表 | 含 5 个子目录；详见 §6 |
| `i18n/` | locale 入口 | 仅 `locale.ts` 一个文件；详见 §5 |
| `lib/` | 通用工具库（无业务） | 16 个工具文件：cn、format、promql、routes、toolSkill、usePoll 等 |
| `pages/` | 路由级页面 | 32 个顶层 + `settings/` 子目录；详见 §4 |
| `store/` | Zustand 全局状态 | 9 个 store，每个 store = 一个领域 |
| `styles/` | Tailwind 入口 + 全局覆盖 | `index.css` 含 `html.light` 主题切换 |
| `test/` | 测试基础设施 | `setup.ts`、`msw-server.ts`、`fixtures/marketplace.ts` |

顶层 3 个文件：`main.tsx`（React 根）、`App.tsx`（路由表）、`vite-env.d.ts`。

### 2.1 `pages/` — 32 个路由级页面

`pages/` 顶层是平铺页面 + `settings/` 子目录（设置画板的二级页面）。

| 类别 | 文件 | 对应后端 BC |
| --- | --- | --- |
| 入口 / 框架 | `Login.tsx`、`Home.tsx`、`AdminLayout.tsx`、`SettingsLayout.tsx`、`DeviceShell.tsx`、`PageView.tsx`、`Pages.tsx`、`Tasks.tsx`、`Dashboard.tsx` | IAM / Frame |
| Agent / Chat | `Agents.tsx`、`ChatThread.tsx`、`SkillRun.tsx` | aiops / skill |
| 告警 / 调查 | `Alerts.tsx`、`IncidentDetail.tsx`、`AlertRules.tsx`、`ReportDetail.tsx` | alert / report |
| 边缘 / 设备 | `Edges.tsx`、`EdgeDetail.tsx`、`Topology.tsx`、`Knowledge.tsx`、`KnowledgeRepos.tsx` | edge / topology / knowledge |
| Workflow / Monitor / Traces | `FlowEditor.tsx`、`Flows.tsx`、`Monitor.tsx`、`Traces.tsx`、`Logs.tsx` | flow / monitor / traces / logs |
| 集成 / MCP | `Mcp.tsx` | mcp |
| 设置（16 子页面）| `settings/{About,Agent,AuditLog,Channels,Health,Integrations,LLM,Marketplace,Notifications,Orgs,Preferences,Secrets,Upgrade,Users,Webshell}.tsx` | 各对应 BC + systemhealth/systemupgrade |
| 测试 | `Agents.test.tsx`、`Knowledge.test.tsx`、`Skills.test.tsx` | 与被测组件并列 |

> **页面文件命名**：与后端 BC 名基本一一对应（alert → `Alerts.tsx`、flow → `Flows.tsx`），这是约定的"找页面"路径。

### 2.2 `components/` — 5 个组件域

| 子目录 | 内容 | 复用度 |
| --- | --- | --- |
| `ui/` | **原子组件库**：`Button`、`Card`、`Chip`、`EmptyState`、`PageHeader`、`RoleSelect`、`index.ts` | 全局高复用，新页面默认组合这些 |
| `monitor/` | 监控面板图表组件（PromQL 渲染、TimeSeries、Stat、Table） | 给 `Monitor.tsx`、`Alerts.tsx` 用 |
| `topology/` | 业务拓扑图组件（节点、关系、布局算法封装） | 给 `Topology.tsx` 用 |
| `marketplace/` | 技能市场卡片、详情侧栏 | 给 `settings/Marketplace.tsx` 用 |
| `icons/` | 图标库（lucide-react 二次封装） | 全局 |

### 2.3 `lib/` — 16 个工具

无状态、无业务依赖的纯工具集合，常被多个页面 import：

| 文件 | 作用 |
| --- | --- |
| `cn.ts` | `clsx + tailwind-merge` 合并 className |
| `format.ts` | 字节/时间/百分数格式化（i18n 感知） |
| `routes.ts` | 前端路由常量 + 路径生成器 |
| `promql.ts` | PromQL 解析与语法检查（带 `promql.test.ts`） |
| `grafanaVars.ts` | Grafana 模板变量展开 |
| `toolSkill.ts` | Tool / Skill 的差异 + 注册表读取 |
| `icon.ts` | 图标名 → 组件映射 |
| `drilldown.ts` | 下钻链接生成 |
| `events.ts` | 浏览器自定义事件（与 i18n 的 locale-change 同模式） |
| `usePoll.ts` | 通用 polling hook |
| `frontmatter.ts` | Markdown frontmatter 解析（带测试） |
| `rule_presets.ts` | 告警规则预置模板 |
| `paramDescEn.ts` | 接口参数字符串描述（中英） |
| `configDraftConfirmation.ts` | 设置草稿确认（防误关） |
| `*.test.ts` | 同名单元测试 |

### 2.4 `store/` — 9 个 Zustand store

每个 store 一文件，命名与内容对应清晰：

| Store | 内容 | 持久化 | 读多写少 |
| --- | --- | --- | --- |
| `auth.ts` | `access_token`、`refresh_token`、`user`、`role` | localStorage | 几乎每个请求都读 |
| `me.ts` | 当前用户个人设置（默认 UI 语言、时区） | localStorage | 设置页 |
| `theme.ts` | `dark/light` 主题切换 | localStorage | 全局 |
| `mode.ts` | 全局 UI 模式（密度、动画偏好） | 否 | Layout |
| `ui.ts` | 全局 UI 状态（侧栏折叠、模态可见） | 否 | Layout |
| `chatSessions.ts` | 当前活跃 chat session id、tab 列表 | 否 | Chat 页 |
| `modelSelection.ts` | 当前 LLM provider/model 选择（per 用户） | localStorage | Chat / 设置 |
| `incidentBadge.ts` | 未读 / 待响应 incident 计数（顶部红点） | 否 | Header |
| `observability.ts` | 最近查看的故障 ID / 监控面板 ID | 否 | Drilldown |

> **绝不在 store 里放"派生数据"**：派生数据通过 selector / hook 实时计算（如活跃 incident 数 = `Incident.filter(in status=Open)`）；store 只放"用户偏好 / 跨页共享的会话状态 / 后台推送的瞬态缓存"。

---

## 3. API 客户端层（`web/src/api/`）

**35 个 `.ts` 文件**，每个对应一个后端 BC 或子域。**所有 HTTP / SSE 调用都从这里出发**，页面从不直接 fetch。

### 3.1 `client.ts` — 唯一通用客户端

`request<T>(method, path, body, opts)` 提供：

| 能力 | 实现 |
| --- | --- |
| **Base URL** | `BASE='/api/v1'`；绝对 URL 直通；相对路径自动补 `/` |
| **Accept-Language** | 每请求自动注入 `getLocale()` 返回值（zh-CN / en-US）；后端 LLM 端点据此切语言 |
| **Bearer Token** | 调 `getToken()` 取 `access_token`，注入 `Authorization`；`noAuth` 选项跳过（用于 `/auth/login` 自身） |
| **FormData** | 检测到 `FormData` 时不 JSON 序列化，让浏览器自动设 multipart + boundary |
| **JSON 解析** | `Content-Type: application/json` 自动 `res.json()`；否则 fallback 到 `res.text()` |
| **错误对象** | `ApiError(msg, status, code?, payload?)`：业务 `{code,message,data}` 包在 `data.code`，HTTP 错误体压平成 message |
| **401 → refresh** | 单飞保护：`refreshInFlight` 共享 Promise，N 个并发 401 只触发一次 `/auth/refresh`；成功后 retry 一次（`_retryingAfterRefresh` 防递归）；仅当 refresh 失败才登出 |
| **AbortSignal** | `opts.signal` 透传，Abort 后抛 `AbortError` |

> **无 SWR / 无 React Query**：所有缓存都靠 store + 组件本地 `useState/useEffect`；轮询通过 `lib/usePoll.ts`；SSE 走 `chat/streamMessage`，自带 reader。

### 3.2 35 个 API 文件按 BC 映射

| API 客户端文件 | 行数（约） | 后端路由前缀（推测 / 实际） | 后端 BC |
| --- | --- | --- | --- |
| `auth.ts` | 30 | `/auth/*` | IAM |
| `users.ts` | 100 | `/users/*` | IAM |
| `orgs.ts` | 93 | `/orgs/*`、`/memberships/*` | IAM |
| `agents.ts` | 139 | `/aiops/agents/*`、`/user_agents/*` | aiops |
| `aiops.ts` | 28 | `/aiops/*` 杂项 | aiops |
| `alerts.ts` | 516 | `/alerts/*`、`/alert-rules/*` | alert |
| `audit.ts` | 55 | `/audit-logs` | audit |
| `approvals.ts` | 44 | `/approvals/*` | approval |
| `chat.ts` | 358 | `/chat/sessions/*`、`/aiops/*` | aiops |
| `devices.ts` | 27 | `/devices/*` | device |
| `edges.ts` | 217 | `/edges/*`、`/devices/{id}/roles`、`/prom/query_range` | edge + metric |
| `flows.ts` | 193 | `/flows/*`、`/flow_runs/*` | flow |
| `grafana.ts` | 87 | grafana 端点（代理） | grafana |
| `imbridge.ts` | 77 | `/im/apps/*`、`/im/threads/*` | imbridge |
| `integrations.ts` | 96 | `/integration/*` | integration |
| `knowledge.ts` | 403 | `/knowledge/repos/*`、`/knowledge/docs/*` | knowledge |
| `logs.ts` | 52 | `/logs/query` | logquery |
| `marketplace.ts` | 303 | `/marketplace/*`、`/installed-skills/*` | marketplace |
| `mcp.ts` | 184 | `/mcp/servers/*` | mcp |
| `monitorPanels.ts` | 59 | `/monitor/panels/*` | monitor |
| `pages.ts` | 42 | `/pages/*`（CMS） | pages |
| `prom.ts` | 42 | `/prom/*` | metric + prom |
| `prometheus.ts` | 12 | `/prometheus/*` | prometheus |
| `reports.ts` | 219 | `/report-schedules/*`、`/reports/*` | report |
| `secrets.ts` | 51 | `/secrets/*` | secret |
| `settings.ts` | 115 | `/system-settings/*` | setting |
| `skills.ts` | 306 | `/skills/*` | skill |
| `systemHealth.ts` | 31 | `/healthz`、`/readyz`、`/metrics` | systemhealth |
| `systemUpgrade.ts` | 23 | `/system-upgrade/*` | systemupgrade |
| `tasks.ts` | 40 | `/tasks/*` | tasks |
| `topology.ts` | 226 | `/topology/nodes/*`、`/topology/relations/*` | topology |
| `traces.ts` | 112 | `/traces/query` | tracequery |
| `version.ts` | 9 | `/version` | — |
| `webshell.ts` | 181 | `/webshell/sessions/*` | webshell |

> **约定**：API 文件导出**纯函数**（`listAlerts()`, `createRule(payload)`），不导出 React hook —— 缓存、轮询由调用方页面用 `useEffect` + store 决定。SSE 例外：`streamMessage` 是 async generator 风格的回调式 API。

---

## 4. 路由契约：HTTP 前缀 → 后端 BC → SPA 客户端

下表是 `cmd/ongrid` 各 `server/<domain>/http.go` 实际注册的 URL 前缀与 SPA 客户端的对应：

| 后端 URL 前缀 | 后端 server 子目录 | SPA 客户端文件 | SPA 页面 |
| --- | --- | --- | --- |
| `/api/v1/auth/*` | `iam/server/auth/` | `auth.ts` | `Login.tsx` |
| `/api/v1/users`、`/api/v1/orgs`、`/api/v1/memberships`、`/api/v1/authz/*` | `iam/server/{user,org,membership,authz}/` | `users.ts`、`orgs.ts` | `settings/Users.tsx`、`settings/Orgs.tsx` |
| `/api/v1/edges/*`、`/api/v1/devices/*`、`/api/v1/devices/{id}/roles`、`/api/v1/integration/*` | `manager/server/{edge,device,integration}/` | `edges.ts`、`devices.ts`、`integrations.ts` | `Edges.tsx`、`EdgeDetail.tsx` |
| `/api/v1/host-metrics/*`、`/api/v1/prom/*` | `manager/server/metric/` | `prom.ts`、`edges.ts`（`promQueryRange`）、`monitorPanels.ts` | `Monitor.tsx` |
| `/api/v1/alerts/*`、`/api/v1/alert-rules/*`、`/api/v1/investigations/*` | `manager/server/alert/` | `alerts.ts` | `Alerts.tsx`、`AlertRules.tsx`、`IncidentDetail.tsx` |
| `/api/v1/notification-channels/*` | `manager/server/alert/`（channel 部分） | `alerts.ts`（`ChannelType`） | `settings/Channels.tsx`、`settings/Notifications.tsx` |
| `/api/v1/chat/sessions/*`、`/api/v1/aiops/*` | `manager/server/aiops/` | `chat.ts`、`agents.ts`、`aiops.ts` | `ChatThread.tsx`、`Agents.tsx` |
| `/api/v1/flows/*`、`/api/v1/flow-runs/*` | `manager/server/flow/` | `flows.ts` | `Flows.tsx`、`FlowEditor.tsx` |
| `/api/v1/report-schedules/*`、`/api/v1/reports/*` | `manager/server/report/` | `reports.ts` | `ReportDetail.tsx` |
| `/api/v1/topology/nodes/*`、`/api/v1/topology/relations/*` | `manager/server/topology/` | `topology.ts` | `Topology.tsx` |
| `/api/v1/knowledge/repos/*`、`/api/v1/knowledge/docs/*` | `manager/server/knowledge/` | `knowledge.ts` | `Knowledge.tsx`、`KnowledgeRepos.tsx` |
| `/api/v1/secrets/*` | `manager/server/secret/` | `secrets.ts` | `settings/Secrets.tsx` |
| `/api/v1/marketplace/*`、`/api/v1/installed-skills/*` | `manager/server/marketplace/` | `marketplace.ts`、`skills.ts` | `settings/Marketplace.tsx`、`Skills.tsx` |
| `/api/v1/mcp/servers/*` | `manager/server/mcp/` | `mcp.ts` | `Mcp.tsx` |
| `/api/v1/system-settings/*` | `manager/server/setting/` | `settings.ts` | `settings/LLM.tsx`、`settings/Agent.tsx` 等 |
| `/api/v1/audit-logs` | `manager/server/audit/` | `audit.ts` | `settings/AuditLog.tsx` |
| `/api/v1/approvals/*` | `manager/server/approval/` | `approvals.ts` | `Approvals.tsx` |
| `/api/v1/im/apps/*`、`/api/v1/im/threads/*` | `manager/server/imbridge/` | `imbridge.ts` | `settings/Integrations.tsx` |
| `/api/v1/webshell/sessions/*` | `manager/server/webshell/` | `webshell.ts` | `settings/Webshell.tsx` |
| `/api/v1/logs/query` | `manager/server/logs/` | `logs.ts` | `Logs.tsx` |
| `/api/v1/traces/query` | `manager/server/traces/` | `traces.ts` | `Traces.tsx` |
| `/api/v1/system-upgrade/*` | `manager/server/systemupgrade/` | `systemUpgrade.ts` | `settings/Upgrade.tsx` |
| `/healthz`、`/readyz`、`/metrics` | `manager/server/systemhealth/` | `systemHealth.ts`（可选） | `settings/Health.tsx` |
| `/api/v1/tasks/*` | `manager/server/tasks/` | `tasks.ts` | `Tasks.tsx` |
| `/api/v1/monitor/panels/*` | `manager/server/monitor/` | `monitorPanels.ts` | `Monitor.tsx` |
| `/api/v1/grafana/*` | `manager/server/grafana/`（代理） | `grafana.ts` | `Monitor.tsx` |
| `/api/v1/pages/*` | `manager/server/pages/` | `pages.ts` | `Pages.tsx` |
| `/version` | — | `version.ts` | `settings/About.tsx` |

> **约定**：所有路由前缀必须 `/api/v1/<domain>/...`，且 `<domain>` 与 `web/src/api/<domain>.ts` 文件名一一对应；变更路由前缀前应先改 `client.ts.BASE` 的设计文档再改代码。

---

## 5. i18n 机制（`web/src/i18n/locale.ts`）

**自研 "inline 双语" 模式**，只有 **一个 102 行文件**，无字典、无 i18next、无 message format。

### 5.1 核心 API

```typescript
getLocale(): 'zh-CN' | 'en-US'           // 同步读取；localStorage 优先，自动检测兜底
setLocale(l): void                       // 写入并派发 'ongrid-locale-change' 事件
tr(zh, en): string                       // 函数式翻译；非 React 路径用（模块级常量）
useI18n(): { locale, tr, toggleLocale }  // Hook；订阅 locale 变化并重渲染
```

### 5.2 自动检测规则

1. **localStorage 显式选择** 永远优先（语言切换器写过的值）。
2. **未设置时**：先看 timezone —— `Asia/Shanghai/Chongqing/Urumqi/Harbin/Hong_Kong/Macau` → zh-CN。
3. timezone 不在白名单：看浏览器语言（navigator.language）以 zh 开头 → zh-CN。
4. 都不命中 → en-US。

### 5.3 页面文案写法

```tsx
import { tr, useI18n } from '@/i18n/locale';

const label = tr('保存', 'Save');                  // 模块级

function MyComponent() {
  const { tr } = useI18n();                       // Hook 级（响应式）
  return <button>{tr('保存', 'Save')}</button>;
}
```

### 5.4 grep 自查

- `tr\('` 计所有双语调用点 = 总翻译数。
- `tr\('[^']+', *''\)` 找出"漏英"的 zh 占位。

### 5.5 与后端联动

`client.ts` 在每个请求头注入 `Accept-Language: <getLocale()>`；后端 LLM 驱动端点（RCA worker、消息总结、未来 chat 帮助器）解析该 header 来决定输出语言。

---

## 6. 鉴权与会话契约

### 6.1 令牌形态

| 令牌 | 字段 | 寿命 | 存储 |
| --- | --- | --- | --- |
| `access_token`（JWT） | user_id、role、tenant_id | 短（分钟级） | `store/auth.ts` + localStorage |
| `refresh_token` | server-side 引用 | 长（天级） | 同上 |

### 6.2 请求流

```
request<T>
   ├─ Authorization: Bearer <access_token>
   ├─ 响应 401
   ├─ → refreshAccessToken()
   │   ├─ refreshInFlight 共享 Promise
   │   ├─ fetch POST /api/v1/auth/refresh
   │   └─ 拿到新 token → store.setSession
   ├─ 重试原请求一次（_retryingAfterRefresh = true）
   ├─ 重试仍 401 → 业务错误（如 casbin 拒绝）抛 ApiError
   └─ 仅当 refresh 失败才 useAuth.logout()
```

> **不是所有 401 都登出**：刷新成功后仍 401 通常是服务端权限问题（如路由 casbin policy 漏配）；这种情况必须把错误抛给用户而不是踢登录。

### 6.3 登录流

1. 用户提交 → `POST /api/v1/auth/login {email, password}`
2. 后端 casbin → 颁发 access + refresh。
3. 前端 store 写入；后续请求自动带 token。
4. RBAC 隐藏（前端）：`RoleSelect` 组件 + Layout 据 `store.auth.role` 隐藏菜单项，但**前端隐藏不是安全边界**，后端必须重检。

---

## 7. SSE 聊天流契约

SSE 是 ongrid 中最复杂的前后端契约，主要由 `web/src/api/chat.ts` 的 `streamMessage()` 消费，后端是 `manager/server/aiops/chat_stream.go`。

### 7.1 SSE 帧格式

服务端用 `text/event-stream` 推送、用 `\n\n` 分帧；前端按 `event:` + `data:` 字段组装事件对象。

| 事件 | 帧字段 | 触发时机 |
| --- | --- | --- |
| `assistant` | iteration、message_id、content、created_at、pending_tool_calls | 每个 LLM chunk / 每个完整 assistant 消息 |
| `tool_start` | tool_call_id、name、device_id?、status='pending'、started_at、duration_ms、arguments | agent 准备调 tool；含参数 |
| `tool_end` | 同上 + status in {success,error,timeout}、ended_at、result | tool 返回 |
| `approval_pending` | approval_id、tool_call_id?、command?、credentials? | `cloud_bash` 等同步阻塞工具等待人类决策 |
| `done` | final PostMessageResponse（session_id、assistant_message、tool_calls、usage、iterations） | 一个 turn 收尾 |
| `error` | error object | transport / parse 失败 |

### 7.2 前端消费

```ts
streamMessage(sessionId, content, {
  onAssistant:   e => appendAssistant(e),
  onToolStart:   e => renderToolCard(e),
  onToolEnd:     e => patchToolCard(e),
  onApprovalPending: e => renderApprovalCard(e),
  onDone:        r => finalize(r),
  onError:       err => toast(err),
});
```

> **Esc 取消**：`POST /chat/sessions/{id}/stop` 显式停 turn；turn 与请求 ctx 解耦（刷新不会杀），所以单关闭 SSE 不够，必须显式 stop。

---

## 8. WebSSH 与反向隧道 RPC 桥

### 8.1 WebSSH

- 入口：`web/src/api/webshell.ts` → `POST /api/v1/webshell/sessions` 创建会话 → WebSocket 升级到 `wss://.../webshell/ws?session_id=...`。
- SPA：`settings/Webshell.tsx` 提供 xterm.js 终端 + 录屏控件。
- 后端：`manager/server/webshell/` + `manager/biz/webshell/` 通过 `frontierbound` 把字节流转发到 Edge：`terminal/exec` 反向 RPC。
- 审计：`webshell_sessions` 表记每次会话的 operator / target / 时长（不存密码、不存字节流）。

### 8.2 反向隧道 RPC 在前端的体现

前端**不直接调用** tunnel；它感知的是"command 走了 → 用户看到结果"的端到端体感：

| 前端动作 | 后端体现 | 隧道作用 |
| --- | --- | --- |
| 点 `runFlow` 调用 Flow | `POST /api/v1/flows/{id}/run` → 执行节点遇到 `tool` 节点 | bash / host_files / restart 节点通过 frontier RPC 下发到 Edge |
| 在 Chat 里 @ 设备 → 点工具 | agent 编排生成 tool call → manager 调 service | tool_call 的能力由 Edge 上的 plugin 实现；前端的 chat card 只是把 frontier RPC 包装成可视化卡片 |
| Settings → Integrations → 加一台 Edge | `POST /api/v1/integration/register` | Edge 用一次性 token 换取长期 access_key；之后该 Edge 的所有调用都走 tunnel |

---

## 9. 跨域与浏览器直接调用

| 调用 | 走的路径 | 备注 |
| --- | --- | --- |
| **browser → `/api/v1/*`** | 同源（nginx 反代） | 默认 |
| **browser → `/api/v1/auth/refresh`** | 同源 | refresh 不能跨域 |
| **browser → Grafana** | `/api/v1/grafana/*`（代理） | 由 manager 转发，注入匿名头；前端不再单独配 Grafana base URL |
| **browser → PromQL / Loki / Tempo** | 全部走 manager（`/api/v1/prom/*`、`/api/v1/logs/query`、`/api/v1/traces/query`） | 鉴权 / 租户隔离统一在 manager 一侧 |

> **没有 CORS 配置**：所有跨域都借 nginx 反代 + path rewrite；前端只配 `BASE='/api/v1'`。

---

## 10. 测试基础设施（`web/src/test/`）

| 文件 | 作用 |
| --- | --- |
| `setup.ts` | Vitest 全局 setup（jest-dom、cleanup） |
| `msw-server.ts` | Mock Service Worker server：模拟 35 个 API 客户端的 HTTP/SSE 响应 |
| `fixtures/marketplace.ts` | 共享 fixture（marketplace 数据） |

> MSW 让 `vitest` 在 DOM 环境拦截 `fetch`，组件测试不需要起后端 —— 这也是 `request<T>()` 用原生 fetch 的间接好处：完全可 mock。

---

## 11. 设计契约与约束（来自 `AGENTS.md`）

| 项目 | 要求 |
| --- | --- |
| UI 跟随多数页面 | 新页面照 `Alerts.tsx`/`Devices.tsx`/`Monitor.tsx` 模式做 |
| 复用组件 | 优先 `components/ui/{Button,Card,Chip,PageHeader,EmptyState}` |
| 配色 | zinc 主底色、indigo 主操作色、emerald/amber/red/sky 语义色 |
| 品牌紫 | 仅 logo / 品牌面，不做大面积按钮 |
| light/dark | 用纯 zinc 类，透明度变体在 `styles/index.css` 配 `html.light` 覆盖 |
| i18n | 必须走 `tr('中文','English')`；禁止同一字符串中英拼接 |
| 视觉验证 | 改动必须 chrome headless 截图实看再提交；light + dark 各一张 |

---

## 12. 跨文档引用

- 后端路由注册与中间件：[backend-data-flow.md §3](./backend-data-flow.md#3-http-请求生命周期一次-api-调用走过的链路)
- 后端 BC 名称与权限矩阵：本目录 §4 表
- 实体表与 API DTO 的字段对齐：[data-model.md](./data-model.md)
- 测试约定（`docs/test/e2e-catalog.md`）：E2E 同时跑前端 SPA → manager 真链路

---

## 13. 自查清单

- ✅ 列出技术栈与部署形态（nginx → ongrid → ongrid-edge）
- ✅ 描述 `web/src/` 8 个一级目录的职责
- ✅ 解释 `client.ts` 的 Base / Auth / Refresh / Locale 注入
- ✅ 把 35 个 `web/src/api/*` 文件映射到 27 个后端 server 子目录
- ✅ 给出 SSE 帧类型表（6 种 frame）
- ✅ 解释 i18n inline 模式 + 自动检测规则
- ✅ 说明 WebSSH / reverse-tunnel 在前端的间接体现
