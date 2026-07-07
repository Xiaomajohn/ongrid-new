# 修复设备详情页状态取错 edge 状态的问题

## 问题

设备详情页（`/hosts/:hostId`）顶部状态显示为"离线"，但机器本身在线（能 SSH 进入）。用户怀疑是错取了 edge 状态。

## 根因

详情页头部 `StatusPill` 渲染的是 `device.online`（edge agent 心跳状态），而项目约定的"机器在线"语义是 `device.reachable`（ping 服务 5min 周期写入的网络层可达性）。

- `web/src/pages/Hosts.tsx:399` 列表页：已按 `reachable` 渲染（注释明确："operator 视角 = 网络层是否能通"）
- `web/src/api/devices.ts:17-20` 类型注释：`reachable` 由 ping 服务写入，与 edge agent 推送的 `online` 解耦，operator 视角"机器在线" = `reachable`
- `web/src/pages/HostDetail.tsx:119` 详情页：用了 `device.online`，与列表页/类型约定不一致

后果：当机器能 ping 通但 edge 探针掉了（用户场景：最后心跳 19 分钟前），详情页就显示"离线"，而列表页会显示"在线"，出现同一台设备两个页面状态打架。

## 改动

### `web/src/pages/HostDetail.tsx`

- 第 119 行：`device.online` → `device.reachable`
- 增加注释说明：状态按 reachable 渲染（operator 视角"机器在线"），与 Hosts.tsx 列表页一致

```tsx
- {device && <StatusPill status={device.online ? 'online' : 'offline'} />}
+ {device && <StatusPill status={device.reachable ? 'online' : 'offline'} />}
+ {/* 状态按 reachable 渲染：ping 服务 5min 周期写入的网络层可达性，
+     即 operator 视角"机器在线"。edge agent 推送的 online 仍在
+     device 对象里，仅供内部诊断；UI 统一走 reachable，与
+     Hosts.tsx 列表页一致。 */}
```

## 目的

让设备详情页状态与列表页语义统一（机器网络可达 = "在线"），不再把 edge 探针在线状态误显示为设备状态。

## 范围

仅前端单文件，单点修复，不动后端（后端 `device.Online` 是 `edges.status` 的反规范化镜像，本身语义清晰，只是不该被 UI 当作"机器在线"使用）。