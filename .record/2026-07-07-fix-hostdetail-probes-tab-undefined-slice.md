# 修复 HostDetail 探针 Tab 白屏：edgeLinkRow vs Edge 类型不匹配

## 现象
访问 `/hosts/{hostId}` 设备详情 → 点击「探针」Tab，整个面板白屏。
控制台抛：

```
Uncaught TypeError: Cannot read properties of undefined (reading 'slice')
    at HostDetail-DgZpuMnh.js:3:3456
    at Array.map (<anonymous>)
    at W (HostDetail-DgZpuMnh.js:3:2719)
    ...
```

## 根因
`web/src/pages/HostDetail.tsx` 中的 `listDeviceEdgesLocal` 包装函数直连
`GET /api/v1/devices/{id}/edges`，但该后端端点返回的是 **junction 视图** `edgeLinkRow`：

```go
// internal/manager/server/device/http.go
type edgeLinkRow struct {
    EdgeID    uint64    `json:"edge_id"`
    DeviceID  uint64    `json:"device_id"`
    Type      string    `json:"type"` // host | discovered
    CreatedAt time.Time `json:"created_at"`
}
```

只有这 4 个字段，**没有** `access_key_id / name / status / last_seen_at /
agent_version / roles` 等 Edge 完整字段。

前端却把响应 `items` 直接当 `Edge[]` 用并塞进 `<ProbesTab>` 表格，渲染到第 456
行 `e.access_key_id.slice(0, 8)` 时 `access_key_id` 是 `undefined`，于是读取
`.slice` 全白屏。React 报错后整个 tree 不挂载 → Tab 变空白。

## 错误调用链（按 README 全栈调用链规范）

- 层级 1 UI 事件：HostDetail.tsx `<TabBtn onClick={() => setTab('probes')} />`
- 层级 2 前端 API 请求：`listDeviceEdgesLocal(deviceId)` → `GET /v1/devices/{id}/edges`
- 层级 3 后端网关/路由：`internal/manager/server/device/http.go:69` `r.Get("/v1/devices/{id}/edges", h.listEdges)`
- 层级 4 后端业务：实际只取 `edge_devices` junction 表（h.links.ListEdgesForDevice），**不查 edges 表**
- 层级 5 数据层：`edgeLinkRow` 仅 4 字段，与前端的 `Edge` 类型严重不匹配

## 修复
按「前端局部自包含修复」原则，**仅改前端**，不复用错误的 `/devices/{id}/edges` 端点。

### 改动 1：`listDeviceEdgesLocal` 改用 `listEdges({ device_id })`
`/v1/edges` 后端本已支持 `device_id` 过滤（见 `internal/manager/server/edge/http.go:454`
与 `internal/manager/data/edge/store/edge.go:88` 的 `List` JOIN edge_devices），
返回完整 Edge 列表。后端无任何改动。

### 改动 2：`access_key_id.slice` 加可选链防御
`{e.access_key_id.slice(0, 8)}…` → `{e.access_key_id ? \`${e.access_key_id.slice(0, 8)}…\` : '—'}`
即便后端数据缺失字段也不再 throw，符合 "以跳过验证为耻" 准则。

### 改动 3：删除已不再被使用的 `import { request } from '@/api/client'`

## 文件
- `web/src/pages/HostDetail.tsx`
  - L54-64 注释 + 改写 `listDeviceEdgesLocal`
  - L458 表格 `<td>access_key_id` 渲染加可选链
  - L30 删除 dead import `request`

## 验证
- `npx tsc --noEmit` → HostDetail.tsx 0 错误（剩余错误为项目内第三方 @types 缺失，与本改动无关）
- `npx eslint src/pages/HostDetail.tsx` → 0 errors（2 个 pre-existing `useEffect`/`useCallback` dep warning，与本次无关）
- `npx vite build` → built in 31.59s，成功

## 注意
- 「探针」列表之所以列表页 `Edges.tsx` 正常、而 HostDetail 的 ProbesTab 炸：两边数据源不同——`Edges.tsx` 走的是 `GET /v1/edges`（完整 Edge），HostDetail 走的是 `GET /v1/devices/{id}/edges`（junction 视图）。
- 没有改 `/v1/devices/{id}/edges` 端点语义，因为它原本就是给"device ↔ edge 关联管理"用的轻量视图，没人用就先保留；后续若还要做"只查关联 ID" 场景可继续用，但不能 cast 成 Edge[]。
- `Edges.tsx:420` 的 `e.access_key_id.slice` 没有改，因为 `/v1/edges` 全口径列表里 `access_key_id` 是 DB `NOT NULL` 列（model.Edge.AccessKeyID `gorm:"not null"`），安全。HostDetail 这边做防御纯粹是因为绕过 junction 端点后我又补了一道防线，不再依赖 DB 列约束。
