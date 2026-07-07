# 修复主机详情页终端连接 400：DeviceShell 未读 hostId 路由参数

## 问题

在「主机详情」页（`/hosts/:hostId`）点击「终端」按钮，浏览器对
`/api/v1/devices//shell-direct` 发起 GET 请求，得到 400 Bad Request。

现象：URL 中设备 id 段为空（双斜杠 `devices//shell-direct`）。

## 根因

`DeviceShellPage` 同时被两条路由复用：

- `/devices/:deviceId/shell[-direct]`（探针视角）
- `/hosts/:hostId/shell[-direct]`（实体设备视角，见 App.tsx:122-123）

两边的路径段都是 Prom label `device_id`，但 React Router 路径参数名不同。
旧实现 [DeviceShell.tsx:75](file:///root/builder/ongrid-new/web/src/pages/DeviceShell.tsx#L75)
只解构 `deviceId`：

```ts
const { deviceId = '' } = useParams<{ deviceId: string }>();
```

走到 hosts 路由时 `deviceId` 为 `undefined`，降级为空串；`openShellSocket` /
`probeShellPreflight`（[webshell.ts:41, 161](file:///root/builder/ongrid-new/web/src/api/webshell.ts#L41)）
拼出来的 URL 变成 `/api/v1/devices//shell-direct`，后端路由不匹配 → 400。

## 修复

[DeviceShell.tsx](file:///root/builder/ongrid-new/web/src/pages/DeviceShell.tsx) 改为同时读两个 param，按 `deviceId || hostId` 兜底：

```ts
const routeParams = useParams<{ deviceId?: string; hostId?: string }>();
const deviceId = routeParams.deviceId || routeParams.hostId || '';
```

这样：
- `/devices/:deviceId/shell*` 走 `deviceId`
- `/hosts/:hostId/shell*` 走 `hostId`
- 两条入口的 WS 握手 / preflight 都能拿到正确 id。

## 验证

- `pnpm tsc --noEmit`：DeviceShell / webshell 无新增类型错误。
- ESLint：仓库 hooks 顺序告警（24 errors / 3 warnings）改动前就存在，
  本次未引入新告警。
- 走查链路：HostDetail 按钮 → App 路由 → DeviceShell 解参 → webshell
  拼 URL → 后端 `/v1/devices/{id}/shell-direct`（manager/server/devicessh/http.go:136）
  现已能命中。
