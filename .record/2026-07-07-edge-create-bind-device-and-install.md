# 2026-07-07 edge 创建关联所属设备 + 行内安装入口

## 背景

Edges 页（`/devices`）的「新建」弹窗只有一个 `name` 字段，新建出来的 edge
和具体主机设备没有强关联 —— agent register 阶段才通过 fingerprint upsert
兜底关联，且安装是另一条「主机页 → 一键安装」独立链路。

需求：在创建 edge 时直接选「所属设备 + 任务名」并落库；保存后行的操作栏
出现「安装」按钮，点击后弹窗提供「手动安装（curl 一行）+ 一键安装（复用
所属设备的 SSH 凭据）」两种方式，让 operator 不再必须绕去主机页。

## 改动

### 后端（Go）

- `internal/manager/biz/edge/usecase.go`
  - 新增 `createOptions` + `CreateOption` func-options 类型，对外暴露
    `WithDeviceID` / `WithTaskName` 两个 option。
  - `Usecase.Create` 签名改为 `func(ctx, name, createdBy, opts ...CreateOption)`。
  - `WithDeviceID` 在 create 后立即调 `repo.SetDeviceID` + `links.Link`
    写 edge_devices M:N 关联；失败仅 warn（不阻塞 edge 创建）。
  - `WithTaskName` 直接把 `task_name` 写入 `model.Edge.TaskName` 字段。
  - 原 `uc.Create(ctx, name, uid)` 调用方（15 处测试 + service 透传）
    全部不需要改 —— variadic + 默认零值兼容。

- `internal/manager/service/edge/service.go`
  - `Service.Create` 透传 `opts ...biz.CreateOption` 到 usecase。

- `internal/manager/server/edge/http.go`
  - `createReq` 加 `DeviceID uint64` + `TaskName string` 两个 JSON 字段。
  - `Handler.createEdge` 根据 `req.DeviceID > 0` / `req.TaskName != ""`
    构造对应 option 透传给 `svc.Create`。
  - `EdgeService` 接口 `Create` 方法同步加 variadic 签名。

- `internal/manager/server/edge/http_test.go`
  - `fakeSvc.Create` 加 `_ ...biz.CreateOption` 满足新接口。

### 前端（TypeScript + React）

- `web/src/api/edges.ts`
  - `createEdge(input)` 扩展为 `{ name; device_id?; task_name? }`。

- `web/src/pages/Edges.tsx`
  - mount 时调 `listDevices({ limit: 1000 })` 拉一次全量设备存到
    `deviceOptions` state 供 CreateEdgeModal 下拉。
  - `onCreate(name, deviceID, taskName)` 把新字段透传给后端；同步把
    `secret_key` 暂存到 sessionStorage（键名 `ongrid.edge.secret_key.{id}`），
    让行内「安装」弹窗的「手动安装」section 也能拼出完整 curl 命令。
  - `onRotate` 同样把新 secret 写回 sessionStorage（覆盖旧值）。
  - `CreateEdgeModal` 加「所属设备」select + 「任务名」input 两个字段；
    任务名 trim 后空串等于不传，所属设备选「不选择」等同于不关联。
  - 行操作栏新增 `InstallButton`（Power icon + 「安装」label），点击
    打开新的 `EdgeInstallModal`。
  - 引入 `<EdgeInstallModal>` + `<InstallLogPanel>`：一键安装启动后
    切到日志面板 tail 输出。

- `web/src/components/EdgeInstallModal.tsx`（新建，323 行）
  - 三大 section：
    1. **所属设备** —— 拉 `getDevice(edge.device_id)` 展示设备名 +
       `ssh_user@ssh_host:ssh_port` + 认证方式，让 operator 一眼确认
       连接凭据。
    2. **手动安装** —— 读 `props.secretKey`（来自 sessionStorage），
       用 `buildInstallCommand` 拼 curl 一行；可复制。无 secretKey 时
       退化为「请到行菜单「轮换密钥」后重试」提示。
    3. **一键安装** —— 复用 `InstallEdgeModal` 的 wire body：POST
       `/devices/{id}/install-edge`，后端用 device 表里保存的 SSH
       凭据直接 SSH 到目标机跑 install command。可选覆盖 password /
       key。

## 验证

- 后端 `go build ./...` 通过；`go vet ./internal/manager/server/edge/...` 通过。
  （`usecase_test.go` 中 `fakeDeviceRepo` 缺 `ListReachableTargets` 是
  预先存在的问题，与本次改动无关。）
- 前端 `tsc --noEmit` 在我改动的 `pages/Edges.tsx` / `components/EdgeInstallModal.tsx` /
  `api/edges.ts` 三个文件零类型错误。剩余 14 个 tsc 错误都是
  `XTerminal.tsx` / `topology/Graph.tsx` / `FlowEditor.tsx` 缺第三方库
  类型（`xterm` / `@xyflow/react` / `@dagrejs/dagre`）的预先存在问题。
- 没写单元测试（项目规则允许）。

## 与已有逻辑的兼容性

- 旧 `createEdge({ name })` 调用方：`api/edges.ts` 改成显式转发三个字段，
  但后端 / service / usecase 用 variadic + func options，旧调用方
  `createEdge({ name })` 仍能跑（device_id / task_name 留空 = 不传）。
- 没传 device_id / task_name 的 edge 行为：与改动前完全一致 —— agent
  register 阶段走 fingerprint upsert，install worker 走
  `BindEdgeFromAccessKey` 路径。
- 已有 `monitor.go` / `service/edge` / `usecase_test.go` 大量
  `uc.Create(ctx, name, nil)` 调用不需要改。

## 后续可清理（非阻塞）

- `CreateEdgeModal` 仍在 `pages/Edges.tsx` 内部（800+ 行的页面）。可
  后续抽到独立 `components/CreateEdgeModal.tsx`，跟 `EdgeInstallModal`
  对齐。本次保留以最小化文件改动。
- secretKey 暂存到 sessionStorage：刷新后仍在，关闭 tab 后清空。如果
  operator 长时间开 tab + 多人共用浏览器，敏感窗口可达数天。后续可
  改成内存中的 ref 暂存（接受刷新后只能「一键安装」），或在 sessionStorage
  上加 TTL。
