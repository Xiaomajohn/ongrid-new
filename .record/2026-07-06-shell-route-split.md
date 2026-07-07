# 2026-07-06 WebSSH 终端两个入口分离（设备页直连 / 监控页 tunnel）

## 背景

WebSSH 之前只有一个 endpoint `/v1/devices/{id}/shell`，内部 `Router.Pick(...)` 由 edge online 状态决定走 tunnel 还是 direct。operator 反馈两个使用场景语义混乱：

- **设备页（Hosts / 设备详情）**：点「终端」→ 想登录的就是 **设备本身**，跟 edge 在不在线无关；走 manager → 设备的 IP 直连。但因为 `Pick` 看 edge 在不在线就 fallback 到 direct，结果意外发生时（比如设备没装 edge、edge 暂下线）会回退到直连，体验割裂。
- **监控页（Edges / 设备探测器列表）**：点「终端」→ 想通过已上线的 **edge** 走 tunnel 通道登录设备的 127.0.0.1。这是设计本意（不暴露 ssh_host 凭据到 manager）。

按 user 给的指示：「一个要通过传出设备的IP、用户、密码登录即可 / 一个走edge的tunel通道」，把这两个场景的入口**显式拆开**，避免运行时自动选路的歧义。

## 决策

**方案**（已与用户确认）：拆两个 endpoint + 锁 transport：

| 入口 | URL | 后端 handler | transport |
| --- | --- | --- | --- |
| 监控页 | `/v1/devices/{id}/shell` | `ShellHandler.handleTunnel` | 强制 tunnel（要求 edge online） |
| 设备页 | `/v1/devices/{id}/shell-direct` | `ShellHandler.handleDirect` | 强制 direct（manager → ssh_host:22） |

后端用 `RouteKind` enum 锁 transport 决策，不依赖边缘在线自动 fallback。前端 SPA 按 URL 路径决定传入哪个 route hint。

## 改动

### 后端

| 文件 | 改动 |
| --- | --- |
| `internal/manager/biz/devicessh/dialer.go` | 新增 `RouteKind` enum：`RouteKindAuto` / `RouteKindDirect` / `RouteKindTunnel`，附 `String()` 方法 |
| `internal/manager/biz/devicessh/router.go` | `Router.Pick` / `Router.MustConnect` 加 `route RouteKind` 参数；`RouteKindDirect` 走原 direct 分支，`RouteKindTunnel` 强制要求 online edge（否则 `ErrTunnelNotImplemented`），`RouteKindAuto` 保留原行为 |
| `internal/manager/biz/devicessh/service.go` | `ShellOpts` 加 `Route RouteKind`；`OpenShell` 透传给 `router.Pick` |
| `internal/manager/biz/devicessh/sftp.go` | `MustConnect` 调用补 `RouteKindAuto` 占位参数 |
| `internal/manager/biz/installjob/installer.go` | `MustConnect` 调用补 `RouteKindAuto`（install worker 永远要走 direct，但保持 enum 一致） |
| `internal/manager/server/devicessh/http.go` | `ShellOpts.Route` 字段（string，在 server 层自由映射）；`Register` 挂两个 endpoint；`handle` 内部化，新增 `handleTunnel` / `handleDirect` 入参钩 route string |
| `cmd/ongrid/main.go` | `devicesshShellAdapter` 透传 `Route` 字段；新增 `routeStringToKind` 把 server-string 映射回 biz enum |

后端编译 `go build ./...` ✓

### 前端

| 文件 | 改动 |
| --- | --- |
| `web/src/api/webshell.ts` | 新增 `ShellRoute = 'tunnel'\|'direct'`；`openShellSocket` / `probeShellPreflight` 加 `route` 参数，自动拼 `/shell` 或 `/shell-direct` 路径 |
| `web/src/App.tsx` | 加 `/devices/:deviceId/shell-direct` 和 `/hosts/:hostId/shell-direct` 两个路由，沿用 `DeviceShellPage` |
| `web/src/pages/DeviceShell.tsx` | `useLocation()` 读 pathname 决定 `shellRoute`（`/shell-direct` 结尾 → `direct`，否则 `tunnel`），传给 `openShellSocket` / `probeShellPreflight` |
| `web/src/pages/HostDetail.tsx` | `terminalHref` 拼 `/hosts/:id/shell-direct`；`terminalEnabled` 从依赖 `device?.online` 改为只检查 `device?.id && canMutate`（直连 SSH 与 Prom online 无关） |
| `web/src/pages/Edges.tsx` | 不动——已经指向 `/devices/:id/shell`，自动走默认 tunnel 路径 |

前端的几个文件 `tsc --noEmit` 不引入新错误（项目本就有的 `@types/xterm` / `@types/xyflow__react` 等缺失与本 PR 无关）。

## 安全与权限

- `RouteKindTunnel` 强制要求 edge online ——失败时返 `ErrTunnelNotImplemented`，HTTP mapping 给 503 / edge-offline。前端 shell-probe 收到这个状态时弹「edge 暂不可用，请稍后重试」红字。
- `RouteKindDirect` 不要求 edge 状态，但强制要求设备行有 `ssh_host` + `ssh_user` + (`ssh_password` 或 `ssh_key`)，缺一个返 `ErrSSHConfigMissing`（428 / precondition）。设备页 terminal 入口的安全性靠 manager 侧 RBAC（`canMutate`）守住，viewer 进不来；**不**把这条放到 http.go（host-level 鉴权已由 auth.Middleware 完成）。
- HostDetail 终端可用判断从「device.online」改成「device.id + canMutate」是有意：直连 SSH 路径与 Prom 上报解耦，operator 不必等设备 Prom online 也能 ssh 直连调试；离线设备的故障恢复路径更短。

## 不在范围内

- `TunnelDialer` 真正实现 ssh.Client（目前 `MustConnect` tunnel 分支还返 `ErrTunnelNotImplemented: pending B1`）。B1 落地后 `/shell` 直接受益无需再改本 PR 的代码。
- SFTP 路径没切，仍走 `RouteKindAuto`（不强制 transport），符合文件浏览「tunnel 优先，不行降级直连」的预期。
- 设备页终端要"不允许操作"（read-only 模式）的能力，view 层 canMutate 已守住；shell 内的 read-only 还需要后续 plumb 一层 cmd policy（独立路线），本 PR 不做。

## 验证

1. `go build ./...` 编译通过 ✓
2. `tsc --noEmit` 改动文件（DeviceShell / HostDetail / App / webshell）无新错 ✓（项目预存的 @types/xterm 等缺失未波及本 PR）
3. 后端 `devicessh` 包路径：`Router.Pick(ctx, dev, PurposeShell, RouteKindDirect)` 时只走 direct；`RouteKindTunnel` 只走 tunnel；其他传 `RouteKindAuto` 保留 fallback。
4. 前端 URL 行为：
   - `GET /hosts/:id` → 「终端」按钮 href 为 `/hosts/:id/shell-direct`，进入页面 `useLocation` 判定 `direct`
   - `GET /devices/:edgeId`（Edges 页）→ 「终端」按钮 href 维持 `/devices/:id/shell`，进入页面 `useLocation` 判定 `tunnel`
