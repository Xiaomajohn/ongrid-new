# 2026-07-06 — 主机管理：ping 探活 + 编辑主机

## 背景

用户反馈 Hosts 页面（web/src/pages/Hosts.tsx）三处体验问题：

1. **在线状态不准** — 状态列按 edge agent 推送的 `online` 渲染，但
   operator 关心的是"网络层能否 ping 通这台机器"，而不是"这台机器
   上是否有 agent 进程在跑"。两者可能不一致：agent 挂了但 host 在；
   host 关机了但 agent 还没回告。
2. **IP 显示空** — IP 列绑死显示 `device.ip_address`（edge 实际上报
   的），但用户填的 IP 存在 `device.ssh_host`。表里 IP 一直显示
   "—" 是因为早期还没有 ssh_host 概念。期望：显示创建设备时输入的
   IP。
3. **缺编辑按钮** — 操作栏没有"编辑主机"入口，operator 要改 ssh
   凭据 / IP / 端口 / 名称必须删了重建。

## 目标

- 加 `reachable` 字段，由后端 5min 定时 ping 写入；前端 Hosts 状态
  列按 reachable 渲染。
- IP 列改显示 `ssh_host`（添加时输入的 IP），兼容老数据回退到
  `ip_address`。
- 新增"编辑主机"对话框，支持改全部字段：name / description /
  hostname / ssh_host / ssh_port / ssh_user / ssh_auth_kind /
  ssh_password / ssh_key。

## 改动一览

### 后端

| 文件 | 改动 |
|------|------|
| `internal/manager/model/device/model.go` | Device 加 `Reachable bool` + `LastReachableAt *time.Time` 字段（gorm 自动 migrate 加列） |
| `internal/manager/biz/device/repo.go` | Repo interface 加 `UpdateReachability(ctx, id, reachable, at)` + `ListReachableTargets(ctx)` |
| `internal/manager/biz/device/repo.go` | `UpdateNameDescription` 扩参接受 `hostname` |
| `internal/manager/data/device/store/device.go` | 实现 `UpdateReachability`（reachable=false 时清 last_reachable_at 为 NULL）+ `ListReachableTargets`（只取 `id, ssh_host` 最小列，软删除自动过滤） |
| `internal/manager/data/device/store/device.go` | `UpdateNameDescription` 多 update `hostname` 列 |
| `internal/manager/biz/device/pinger.go` | **新文件**。`Pinger` 结构 + `Ping(ctx, host)` + `RunAll(ctx, targets)` 并发池。exec 调系统 `ping -c 1 -W <n>`（Linux 秒 / Darwin 毫秒）。2s timeout / 16 并发默认。 |
| `internal/manager/biz/device/usecase.go` | Usecase 加 `PingReachable(ctx, pinger)` + `UpdateReachability(ctx, id, ...)` |
| `internal/manager/biz/device/usecase.go` | `UpdateNameDescription` 改三参：name / description / hostname |
| `internal/manager/server/device/http.go` | `deviceItem` DTO 加 `Reachable` + `LastReachableAt` 字段；`devToItem` 同步映射 |
| `internal/manager/server/device/http.go` | `updateReq` 扩 6 个指针字段（hostname / ssh_host / ssh_port / ssh_user / ssh_auth_kind / ssh_password / ssh_key）。`update` handler 改走两段：展示字段（UpdateNameDescription）+ SSH 块（SetSSHCredentials，merge 语义：未提供的字段从当前行读回），最后回 200 + 最新 DTO |
| `internal/manager/server/edge/http_test.go` | `fakeDeviceRepo` 补 `UpdateReachability` / `ListReachableTargets` / `UpdateNameDescription` 新签名的 stub |
| `internal/manager/biz/edge/usecase_test.go` | `fakeDeviceRepo.UpdateNameDescription` 改三参 stub |
| `cmd/ongrid/main.go` | deviceUC 旁实例化 `reachPinger`；新增 5min 定时器（boot 跑一次，然后 5min tick 调 `deviceUC.PingReachable`） |
| `deploy/Dockerfile.ongrid` | runtime stage apt-get install 加 `iputils-ping`（自带 cap_net_raw+ep 文件属性，nonroot 也能 ping） |

### 前端

| 文件 | 改动 |
|------|------|
| `web/src/api/devices.ts` | Device 加 `reachable?` / `last_reachable_at?` 字段；`updateDevice` 改走 `UpdateDeviceInput`（9 字段指针类型），返回 `Device`（之前是 `void`） |
| `web/src/pages/Hosts.tsx` | IP 列改 `h.ssh_host || h.ip_address`；状态列改 `h.reachable ? 'online' : 'offline'`；操作栏加"编辑"按钮（用 lucide Pencil icon，仅 canMutate 显示）；新增 `editTarget` state + 渲染 `EditDeviceModal` |
| `web/src/components/EditDeviceModal.tsx` | **新文件**。复用 CreateDeviceModal 的 Section/Field/Modal 视觉风格，但语义改 dirty-tracking：只把用户改过的字段加到 PATCH body。password / key 留空 = "不动现有"，非空 = "替换"；切换 auth_kind radio 时清掉 credential 输入框 |

## 关键设计

### 1. 在线 = reachable（解耦 edge online）

原来 `online` 字段绑定 edge agent 推送的 presence。新增 `reachable`
由独立的 5min ping 定时器写入。两者语义不同：

- `online`（保留，不删）— "该 host 上有 edge agent 在跑"
- `reachable`（新）— "网络层 ping 该 host 通了"（operator 视角）

UI 默认按 `reachable` 渲染。`online` 字段保留在 DTO 里供内部
诊断 / 排障用（比如 agent 在跑但 host 死了，online=true &
reachable=false 是典型的"机器故障 agent 还在挣扎回告"信号）。

### 2. ICMP 实现选型：exec 系统 ping

manager 在 distroless-ish 容器里以 nonroot（uid 65532）运行，
没有 `CAP_NET_RAW`，raw ICMP socket 会被内核拒绝（`x/net/icmp`
直接 EACCES）。两个可行方案：

1. 给容器加 `cap_net_raw` capability — 改 deployment 拓扑，破坏
   "nonroot 跑 manager" 的安全边界。
2. exec 调系统 `ping` 命令 — iputils-ping 的 `/bin/ping` 二进制
   自带 `cap_net_raw+ep` 文件属性，nonroot 也能跑。只需要在
   Dockerfile 装 `iputils-ping` 包。

选 2。`pinger.go` 用 `os/exec` 调 `ping -c 1 -W <n>`，跨平台走
`runtime.GOOS` 切 -W 单位（Linux 秒 / Darwin 毫秒）。失败兜底
检测 stdout 里的 "100% packet loss" 字符串。

### 3. PATCH 语义：指针 = 可选

`updateReq` 字段全部 `*string` / `*int`：

- 字段为 `nil`（请求体里没出现）= "不改"
- 字段为 `*p`（含空串）= "写这个值"

`update` handler 把 SSH 块的非 null 字段 merge 到从 DB 读出的当前
行再调 `SetSSHCredentials`——避免"只改了 ssh_host 但 password 被
nil 推成空"的事故。

### 4. dirty-tracking 编辑对话框

后端 PATCH 走"指针 = 可选"语义，前端 EditDeviceModal 维护一份
`dirty: Set<field>`，submit 时只把改过的字段加到 body：

- 改过的字段才传 → 后端 merge 语义
- 没改过的字段不传 → 后端保持原值
- password / key 单独走：textarea 留空 = 不动；非空 = 替换

切换 auth_kind radio 时清 credential 输入框，避免把 password
存到 ssh_key 字段这种"已发生过的真实 bug"。

## 验证

- go build `./cmd/ongrid` 通过
- web tsc --noEmit 通过（前端类型对齐 Device / UpdateDeviceInput）
- hosts 表自动加列（gorm AutoMigrate 在 init 阶段跑一次）

## 影响面

- 数据库：自动加 `reachable` (bool, default false, idx) +
  `last_reachable_at` (timestamp) 两列。存量行的 reachable=false
  持续到第一次 5min ping 完成。
- API wire：`/v1/devices` list / get / patch 的响应 body 多两字
  段，向后兼容。
- 启动时间：5min timer 启动开销 < 1ms（goroutine + ticker）。
- Docker 镜像：+1.5MB（iputils-ping 包）。
