# 2026-07-18 — Hosts/HostDetail「终端」按钮改走 tunnel 通道，tunnel 模式自动用 device 凭据登录

## 用户反馈

> 从监控设备页面点击终端，应该不走输入密码的，走 tunnel 通道才对。

「监控设备」即 Hosts 页（`/hosts`）和 HostDetail 页（`/hosts/:hostId`）。
两个页面的「终端」按钮之前跳 `/shell-direct`（直连 SSH，需要在 ConnectModal
里输 OS 用户 + 密码）。用户期望与 Edges 页（`/devices`）的 ShellButton 行为一致——
跳 `/shell`（tunnel 通道），不弹 ConnectModal 输密码。

## 现状回顾（改动前）

| 入口 | 当前 URL | transport | ConnectModal |
|---|---|---|---|
| Hosts.tsx 列表 ShellButton | `/hosts/{id}/shell-direct` | direct | 必须输密码 |
| HostDetail.tsx 头部「终端」 | `/hosts/{id}/shell-direct` | direct | 必须输密码 |
| Edges.tsx 列表 ShellButton | `/devices/{id}/shell` | tunnel | 也弹窗输密码（不必要） |

虽然 `/shell`（tunnel）已经在 App.tsx 路由表里支持，但：
1. 两个 host 视角入口根本没跳 `/shell`；
2. 即使跳了，DeviceShell.tsx 的 ConnectModal 也无条件弹 OS 凭据；
3. 后端 tunnel 路径（`biz/devicessh/tunnel.go:buildClientConfig`）会读 device
   表里已存的 `ssh_user / ssh_password`（或 `ssh_key`）登录，但前端 open
   frame 强制塞 `ssh_user / ssh_pass`（`webshell.ts:ShellOpenFrame`），覆盖
   了 device 表凭据，反而触发"必须输密码"的体验。

## 根因

- host 视角入口的 href 写死 `/shell-direct`（`Hosts.tsx:671` + `HostDetail.tsx:79-80`）；
- 前端 `ShellOpenFrame.ssh_user / ssh_pass` 写死成必填，没有「省略让后端用
  device 表凭据」的语义；
- `DeviceShell.tsx` 不区分 tunnel / direct 是否需要弹凭据。

## 修复（前端，无后端改动）

后端 `biz/devicessh/service.go:OpenShell` 早就支持「open frame 不带 ssh_user /
ssh_pass 时，直接用 device 表的 ssh_user + ssh_password（或 ssh_key）登录」
（override 语义），无需改后端。前端三处改动如下：

### 1. Hosts.tsx（[Hosts.tsx#L657-L671](file://f:\Code\Go\运维\ongrid-new\web\src\pages\Hosts.tsx#L657-L671)）

- ShellButton href：`/shell-direct` → `/shell`
- 注释更新：明确语义是「走 device → online edge → 本地 sshd 的 tunnel」，
  并说明 tunnel 找不到 online edge 时后端 503，ConnectModal 兜底。

### 2. HostDetail.tsx（[HostDetail.tsx#L74-L82](file://f:\Code\Go\运维\ongrid-new\web\src\pages\HostDetail.tsx#L74-L82)）

- `terminalHref`：`/shell-direct` → `/shell`
- 注释同步更新。

### 3. webshell.ts（[webshell.ts#L60-L74](file://f:\Code\Go\运维\ongrid-new\web\src\api\webshell.ts#L60-L74)）

- `ShellOpenFrame.ssh_user / ssh_pass` 从必填改为可选。
- 注释说明：tunnel 下省略让后端用 device 表凭据；direct 下必传。

### 4. DeviceShell.tsx（核心）

- 新增 `getDevice(deviceId)` 调用，结果存 `deviceRow` state（[DeviceShell.tsx#L178-L186](file://f:\Code\Go\运维\ongrid-new\web\src\pages\DeviceShell.tsx#L178-L186)）。
- 新增 auto-connect effect（[DeviceShell.tsx#L402-L423](file://f:\Code\Go\运维\ongrid-new\web\src\pages\DeviceShell.tsx#L402-L423)）：
  - 条件：`shellRoute === 'tunnel' && deviceRowLoaded && conn.kind === 'idle' && deviceRow.ssh_user 非空`
  - 触发：`autoConnectTriedRef` 防 React 18 strict-mode 双触发；点「重连」时
    reset，让用户主动重试也能跳过弹窗。
  - 动作：`setModalOpen(false)` + `openConnection({user: deviceRow.ssh_user, password: '', port: 22})`。
- 修改 `openConnection.ws.onopen` 内的 open frame 构造（[DeviceShell.tsx#L314-L337](file://f:\Code\Go\运维\ongrid-new\web\src\pages\DeviceShell.tsx#L314-L337)）：
  - tunnel 模式下省略 `ssh_user / ssh_pass`（让后端用 device 表凭据）；
  - direct 模式仍必传这两个字段。
- `ConnectModal` 增加 `tunnelMode` / `defaultUser` 两个可选 props：
  - `submit()` 校验放宽：tunnel 模式下 user / password 允许为空
    （[DeviceShell.tsx#L622-L645](file://f:\Code\Go\运维\ongrid-new\web\src\pages\DeviceShell.tsx#L622-L645)）；
  - tunnel 模式下 `deviceRow.ssh_user` 优先于 localStorage 预填表单
    （[DeviceShell.tsx#L599-L617](file://f:\Code\Go\运维\ongrid-new\web\src\pages\DeviceShell.tsx#L599-L617)）；
  - password 输入框 placeholder 在 tunnel + 有默认凭据时改为「留空使用设备默认凭据」
    （[DeviceShell.tsx#L688-L712](file://f:\Code\Go\运维\ongrid-new\web\src\pages\DeviceShell.tsx#L688-L712)）；
  - 密码框下方加一行小字提示：留空 = 用设备默认凭据（`${defaultUser}`），填入 = 覆盖。
- `handleReconnect` 增加 `autoConnectTriedRef.current = false`，让「重连」按钮
  也能重新触发 tunnel 自动连接路径。

## 行为变化（operator 视角）

- Hosts 列表点「终端」→ 浏览器跳 `/hosts/{id}/shell` → DeviceShell mount →
  拉 device 详情 → 有 ssh_user → 后端 tunnel 通道自动登录 → 直接进入 shell，
  无 ConnectModal。
- HostDetail 头部点「终端」→ 同上。
- Edges 列表点「终端」→ 之前也跳 `/devices/{id}/shell`，但要输密码；现在
  也享受自动登录（同样的 device.ssh_user 来源）。
- Direct 入口（`/shell-direct`）行为不变，仍是 ConnectModal 收 OS 凭据——可
  作为「设备没装 edge / edge 不在线」时的手动 fallback。

## 兜底路径（未走 tunnel 时的失败可见性）

- device 表无 `ssh_user`：auto-connect effect 不触发，ConnectModal 正常弹出，
  用户可手输 OS 凭据重连（此时仍走 tunnel / `/shell`，后端会因为 device 没
  edge 或凭据不全返回 503，ConnectModal 的 ws.onclose 路径会写出错误）。
- device 有 `ssh_user` 但没装 edge / edge 不在线：tunnel 走到后端
  `Router.Pick` 的 `RouteKindTunnel` 分支返回 `ErrTunnelNotImplemented`
  （503），ConnectModal 弹出，前端 `explainPreflight` 显示「设备离线」。

## 关联文件

- 前端：
  - `web/src/pages/Hosts.tsx`
  - `web/src/pages/HostDetail.tsx`
  - `web/src/pages/DeviceShell.tsx`
  - `web/src/api/webshell.ts`
- 后端（参考，未改）：
  - `internal/manager/server/devicessh/http.go:ShellHandler.handleTunnel`
  - `internal/manager/biz/devicessh/service.go:OpenShell`（override 逻辑）
  - `internal/manager/biz/devicessh/tunnel.go:buildClientConfig`
  - `internal/manager/biz/devicessh/router.go:Router.Pick`（tunnel 分支）

## 验证

- `cd web && npm run typecheck`：通过，无 TS 错误。
- `npx eslint <4 个改动文件>`：本次新增代码无新增 lint 错误；原有的
  DeviceShell.tsx `react-hooks/rules-of-hooks` 警告（line 91 `if (!canMutate) return`
  之后的所有 hooks 都是 conditional）是历史问题，与本次改动无关。
- 后端无改动，不需要重新打包 ongrid server。

## 不动项

- 没有改后端 `/shell` / `/shell-direct` 的 transport 语义；
- 没有动 device 表的 SSH 凭据存储格式；
- 没有动 Router 选路决策；
- 没有动 AGENTS.md 里的时钟管理硬规则。