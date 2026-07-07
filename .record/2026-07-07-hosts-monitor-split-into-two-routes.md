# 主机详情与监控设备详情拆分为两条独立路由

## 现象
原 `/hosts/{hostId}` 把 5 个 Tab 揉在同一页：

```
基本信息 | 指标 | 探针 | 拓扑 | 元数据
```

其中「指标」和「探针」两个 tab 装的内容（PromQL 度量 / 该 host 下的 Edge 列表）严格属于**监控设备视角**，
而「基本信息 / 拓扑 / 元数据」属于**主机视角**。

两者语义不同，却被塞进同一条 `/hosts/:hostId` 路由。
在 Hosts 列表点 "详情" 进入主机详情，预期看到的是机器本身的属性，但页面又同时有「指标」「探针」模块，
让运维难以分辨当前所在的视角层（Host 视角 vs Monitor 视角）。

## 修复
把 HostDetail 中的「指标」「探针」两个 tab + 头部入口拆出为独立的
**监控设备详情页** `/hosts/{hostId}/monitor`，HostDetail 只保留主机视角的内容。

## 路由拓扑

| 路由 | 页面 | 内容 |
| --- | --- | --- |
| `/hosts/:hostId` | HostDetail | 基本信息 / 拓扑 / 元数据 + 头部「监控设备」按钮 |
| `/hosts/:hostId/monitor` | MonitorDeviceDetail（新增） | 指标 / 探针 / 元数据 + 头部「主机详情」按钮 |

`<Link>` 在两页之间互跳：

- HostDetail 头部右上角：`bg-indigo-600` 「监控设备」按钮 → `/hosts/:hostId/monitor`
- MonitorDeviceDetail 头部右上角：「主机详情」按钮 → `/hosts/:hostId`

`/hosts/:hostId/shell` / `/hosts/:hostId/shell-direct` / `/hosts/:hostId` 在 hosts route group
内并列，不影响。

## 改动

### 1. `web/src/pages/HostDetail.tsx`
- Tab 类型从 `'basic' | 'metrics' | 'probes' | 'topology' | 'meta'` 缩减为 `'basic' | 'topology' | 'meta'`。
- 删除 Tab `metrics` / Tab `probes` 的渲染分支。
- 删除内部 `ProbesTab` 函数与本地的 `listDeviceEdgesLocal` 包装（已迁出）。
- 删除已不再使用的 `useCallback / usePoll / listEdges / deleteEdge / rotateSecret /
  upgradeEdgePackage / Edge / RotateSecretResponse / DeviceMetricsPanels` imports。
- 新增头部「监控设备」按钮（`Activity` 图标 + `bg-indigo-600`），点击跳到
  `/hosts/:hostId/monitor`，保留旧头部「终端」按钮不变。
- `canMutate` 由 `usePermissions()` 注入（替代旧 localStorage hand-rolled 检查）。

### 2. `web/src/components/MonitorDeviceProbes.tsx`（新增）
把 HostDetail 旧 `ProbesTab` 函数提取为可复用组件，签名：
```ts
type Props = {
  deviceId: number;
  deviceName: string;
  canMutate: boolean;
  onAfterChange?: () => void;
};
```
保留相同的 listEdges({ device_id }) 调用与本地 fallback（Edge 字段较全，
不再误用 `/v1/devices/{id}/edges` 的 junction 视图 — 见 7-7 那条 record）。

### 3. `web/src/pages/MonitorDeviceDetail.tsx`（新增）
- 路由 `/hosts/:hostId/monitor` 对应页面。
- 头部借用 HostDetail 的视觉骨架（设备名 + StatusPill + 角色 chips + 副标题），
  但副标题前缀「监控设备」+ IP + 最后心跳，并提供「主机详情」按钮跳回。
- Tab 集合：`metrics`（复用 `DeviceMetricsPanels`）+ `probes`（复用 `MonitorDeviceProbes`）+ `meta`（JSON 卡片）。
- `useEffect` 拉 `getDevice(hostId)` 一次；`onAfterChange` 在删除探针后反向刷一次 device，
  保持父页面信号一致（虽然本页不直接展示设备数，预留给后续 host 总览）。

### 4. `web/src/App.tsx`
- `lazy(() => import('@/pages/MonitorDeviceDetail'))`。
- 新增路由：`/hosts/:hostId/monitor` → `MonitorDeviceDetailPage`。
- 注释解释拆分原因，与既有「设备→主机/探针 / 监控」的导航语义保持一致。

## 文件
- `web/src/pages/HostDetail.tsx`（改造，从 605 行 → 412 行）
- `web/src/components/MonitorDeviceProbes.tsx`（新增，298 行）
- `web/src/pages/MonitorDeviceDetail.tsx`（新增，306 行）
- `web/src/App.tsx`（新增 import + 1 条 route）

## 验证
- 逻辑：`Hosts.tsx` 列表行点击 / 详情按钮依旧跳 `/hosts/:id`（主机视角）。新「监控设备」按钮跳
  `/hosts/:id/monitor`。两页互链闭环。
- 类型：复用已经稳定的 `getDevice / listEdges / DeviceMetricsPanels / StatusPill / useI18n /
  usePermissions`，组件均已在其他页面跑过；MonitorDeviceProbes 是从 HostDetail 原样抽出来，
  类型契约不变。新 MonitorDeviceDetail 与 HostDetail 共享同一份 `MonitorDevice` 本地扩展类型，
  字段集相同，避免后端未补齐时出现 cast 报错。

## 不改动的部分
- `web/src/pages/Hosts.tsx` —— 列表行点击 / 「详情」按钮仍落 `/hosts/:id`，与之前一致。
- `web/src/pages/EdgeDetail.tsx` —— 探针视角的详情独立保留，里面「主机」tab 是 host_info JSON
  渲染，不与本拆分冲突。
- `web/src/components/Sidebar.tsx` —— 侧栏「设备」group 已分别列「主机」「监控」入口，未动。
- 后端 API —— 无改动。
