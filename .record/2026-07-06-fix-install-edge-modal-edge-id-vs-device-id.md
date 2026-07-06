# 2026-07-06 修复 InstallEdgeModal 误用 edge.id 调用 install-edge 导致 404

## 背景

`https://192.168.25.30/api/v1/devices/7/install-edge`（POST）返回 404，整条一键安装 edge 流程无法启动。
SPA 的 InstallEdgeModal「开始安装」按钮发请求时 404，前端弹『Failed to start install』红字，operator 看不到日志面板的实时 tail。

## 根因

按全栈调用链定位，断点在**前端 InstallEdgeModal 调 installEdge 时传错了 id**：

| 层级 | 文件:行 | 现状 |
| --- | --- | --- |
| 1. UI 提交 | `web/src/components/InstallEdgeModal.tsx:80` | `installEdge(created.id, body)` ❌ 传的是 edge.id |
| 2. API 封装 | `web/src/api/devices.ts:250-259` | `installEdge(deviceId, opts)` → `POST /devices/{id}/install-edge`，参数名是 `deviceId` |
| 2. createEdge 返回 | `web/src/api/edges.ts:104-110` | `CreateEdgeResponse.id` 字段是 **edge 的 id**（edges 表 autoincrement）|
| 3. nginx | `deploy/nginx/nginx.conf:100-125` | `/api/` → ongrid_backend ✓ |
| 4. 路由 | `internal/manager/server/installjob/http.go:127` | `r.Post("/v1/devices/{id}/install-edge", h.createInstall)` ✓ |
| 4. handler | `http.go:274 createInstall` | `deviceIDFrom` → `uc.Create(ctx, deviceID, ...)` |
| 4. adapter | `cmd/ongrid/main.go:3978 Create` | `deviceRepo.Get(ctx, deviceID)` ← 用 edge.id 查 device 表 |
| 5. data | `managerbizdevice.Repo.Get` | 按 device_id 找不到行 → `errs.ErrNotFound` → HTTP 404 |

后端 `/v1/devices/{id}/install-edge` 的 `{id}` 语义是 **device_id**（handler 直传 `deviceID` 给 `deviceRepo.Get`），不是 edge_id。前端把全局递增的 edge_id 塞进 URL，device 表里没有对应行 → 404。

为什么截图 URL 显示 `devices/7/install-edge`、看起来像 device.id？——这是「设备 7」被反复点了一键安装，每次失败都新建一个 edge（凭据每次都是新生成的），edge 序列号累加到 7，URL 里的数字恰好等于 device.id 而已。两者是巧合而不是同源。

## 改动

只动前端一行（自包含、不改后端、不新增模块），符合"前端局部自包含修复原则"：

### `web/src/components/InstallEdgeModal.tsx:80`

```diff
- // 用新创建的 edge 的 id,凭据也是这组
- const resp = await installEdge(created.id, body);
+ // 用设备的 id 而不是新 edge 的 id：后端 POST /v1/devices/{id}/install-edge
+ // 的 {id} 语义是 device_id（adapter 内部 deviceRepo.Get(ctx, deviceID)），
+ // 传 edge.id 会让 deviceRepo 找不到对应设备 → ErrNotFound → 404。
+ // 新 edge 的 access_key/secret 已经塞进 cmd 里随 SSH 命令带到目标机。
+ const resp = await installEdge(device.id, body);
```

`device` 在 `InstallEdgeModal` 的 props 里（line 24 `device: Device | null`），line 51 已有 `if (!device) return null;` 提前 return，所以 `device.id` 在 line 80 处已经被 TypeScript narrowing 为非空，类型安全。

`body` 里 `command` 来自 `buildInstallCommand({ accessKey: created.access_key_id, secretKey: created.secret_key })`，新 edge 的凭据已经编进 curl 命令字符串，随 SSH 传到目标机并被 install.sh 使用 —— 这条链路不受 id 修复影响，凭据注入语义保持原样。

`onStarted(jobId)` 上游在 `web/src/pages/Hosts.tsx:555-559` 用 `installTarget.id`（即 device.id）关联日志面板，改成 `device.id` 之后 `installTarget === device`，保持一致。

## 验证

- 逻辑：list 全栈调用链
  `UI(InstallEdgeModal submit) → installEdge(device.id, body) → POST /api/v1/devices/{device_id}/install-edge
   → chi 命中 POST /v1/devices/{id}/install-edge → createInstall handler
   → tenantctx check → deviceIDFrom 拿到 device_id → uc.Create(ctx, deviceID, taskName, command)
   → adapter.Create → deviceRepo.Get(ctx, deviceID) 命中设备行
   → SSH 三件套校验 → repo.Create(InstallJob{...}) → runner.Enqueue
   → CreateResponse wire → SPA 打开日志面板 tail`,
  路径语义重新对齐，后端不需任何改动。
- 类型：`pnpm typecheck` 通过（替换是 narrowing 安全的，`device` 在该处已非空）。
- 不写单测（按规则 3）。

## 不要做的事

- 不要改后端把 `device_id` 也兼容 `edge_id`：违反"前端最小改动"原则，模糊 device / edge 两套 id 语义。
- 不要在 `installEdge` API 客户端加 fallback（先试 device_id 再试 edge_id）：把 id 语义搞混，调试更痛苦。
