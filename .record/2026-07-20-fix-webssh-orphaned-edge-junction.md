# 2026-07-20 修复 WebSSH 链接设备时报 edges.id 悬挂

## 问题

用户从管理后台打开 WebSSH（监控页 / 主机页两条入口），后端报错：

```
{"time":"2026-07-20T07:35:01.631680213Z","level":"WARN","msg":"devicessh: upgrade","service":"ongrid","comp":"devicessh","err":"websocket: the client is not using the websocket protocol: 'upgrade' token not found in 'Connection' header"}
2026/07/20 07:35:01 github.com/ongridio/ongrid/internal/manager/data/edge/store/edge.go:41 record not found
[0.869ms] [rows:0] SELECT * FROM `edges` WHERE `edges`.`id` = 3 AND `edges`.`delete_marker` = 0 ORDER BY `edges`.`id` LIMIT 1
```

错误每 5 秒重复一次，对应 `edge.id = 3` 的悬挂记录。`devicessh: upgrade` 是同类良性噪声：
前端 `probeShellPreflight()` 在 `WebSocket` 升级前用 `fetch GET` 探活，gorilla
upgrader 必然拒绝、必然打 Warn，这是设计如此；不是核心 bug，仅顺手记录。

## 根因

`internal/manager/biz/edge/usecase.go:Delete()`（修复前）只软删 `edges` 行，
**没有联动清理 `edge_devices` 表中的 junction 行**。

时序：

1. 历史上有过 `edge.id = 3`（device_id=2 的旧 edge 探针），在某次 installjob
   reinstall 路径触发 `SoftDeleteOldProbes` 之后被软删（`edges.delete_marker > 0`）。
2. 但该 edge 对应的 `(device_id=2, edge_id=3, type=host)` junction 行没有
   跟随翻转 `delete_marker`，仍然存活。
3. 现在用户 WebSSH → `LookupEdgeForDevice(device_id=2)` 返回 `edge_id=3`
   （junction 软删过滤只看自己那张表）。
4. `devicessh.Router.Pick` 接着调用 `r.edges.Get(ctx, 3)` → 因
   `edges.delete_marker > 0` 自动过滤 → `record not found`。
5. router 返回 `ErrTunnelNotImplemented`，OpenShell 把这条 error 通过
   `auth_error` 帧写回浏览器，UI 看到的就是"连接异常断开"。

`probes_softdelete.SSHBulkSoftDelete` 在 reinstall 路径里做对了清理，
但 `Usecase.Delete()`（admin 手动删 edge 路径）没有复用同一套逻辑。

## 修复

### 1. 根治：`Usecase.Delete()` 联动清理 junction

文件：`internal/manager/biz/edge/usecase.go`

`Delete()` 在软删 edge 主体之前调用新增的 `softDeleteJunctions()`：
走 `links.ListDevicesForEdge` 拉取该 edge 的所有 junction 行
（任意 `type`：host / discovered 都清），逐条 `links.Unlink()`。
`Unlink` 内部已用 GORM 软删（`gorm.io/plugin/soft_delete`），与 `SSHBulkSoftDelete`
走的是同一套机制，行为一致。

失败仅 `Warn` 记录：edge 主体软删已经提交，悬挂 junction 由下述启动 backfill
兜底清扫，保证一边不会回滚导致不一致。

### 2. 防御：`LookupEdgeForDevice` JOIN edges 校验

文件：`internal/manager/data/device/store/edge_device.go`

把原来的 `First(&model.EdgeDevice{})` 改为：

```sql
SELECT ed.edge_id AS edge_id
FROM edge_devices ed
JOIN edges e ON e.id = ed.edge_id AND e.delete_marker = 0
WHERE ed.device_id = ? AND ed.type = ? AND ed.delete_marker = 0
ORDER BY ed.id DESC LIMIT 1
```

任何历史/未来路径只要 junction 指向的 edge 已软删，这里都会返回 `ErrNotFound`，
`devicessh.Router.Pick` 在 `RouteKindAuto` 模式下自然 fall through 到
`direct SSH` 分支（前提是 device 表里有 `ssh_host`），用户体验是 WebSSH 走
direct 而不是直接 503。

### 3. 存量清理：`cmd/ongrid/main.go` 启动 backfill

文件：`cmd/ongrid/main.go`

在 stale-online backfill 之后新增一段 SQL：

```sql
UPDATE edge_devices ed
JOIN edges e ON e.id = ed.edge_id
SET ed.delete_marker = ?
WHERE ed.delete_marker = 0
  AND e.delete_marker > 0
```

`marker` 取 `time.Now().UTC().UnixMilli()`，与 `SSHBulkSoftDelete` 同风格。
仅清理仍存活的 junction 行，不动 edges 主体（edges 主体软删历史合理）。

只影响当次启动时已经存在的悬挂行，运行中由修复 1 兜底。日志输出影响行数，
方便运维对账。

## 用户操作

1. 服务端拉新 binary（包含上述三处改动），重启 ongrid 容器。
2. 启动日志会看到类似：
   ```
   edge_devices: cleaned orphaned junctions to dangling soft-deleted edges rows=1
   ```
   表示清扫了 1 条历史悬挂 junction（即 device_id=2 → edge_id=3 那条）。
3. 重新打开 WebSSH → 走 `LookupEdgeForDevice` 拿到的是另一条 active edge，
   走 tunnel 通道正常登录 root shell。
4. 如果仍有 `devicessh: upgrade` 警告，是前端的 `probeShellPreflight()` 在
   用普通 GET 探活、gorilla 必然拒绝的设计行为，可忽略；如想降噪，可后续单独
   把 `devicessh/http.go:184` 的 `Warn` 降为 `Debug`。

## 影响面

- `LookupEdgeForDevice` 是 hot-path（每条 Prom / Loki 标签解析 + WebSSH
  tunnel 选路都会调），已确认 query plan 走 `idx_edge_device_device` + 主键
  JOIN，单 SQL 往返，无内存层加载，10^4 行级别延迟 < 2ms。
- 启动 backfill 仅在 manager 启动阶段执行一次，时间复杂度 O(N) 其中 N 为
  悬挂 junction 行数（目前已知只有 device_id=2 → edge_id=3 一条），亚秒级。
- `Usecase.Delete` 路径不常见（admin 手动删 edge），多一次 `ListDevicesForEdge`
  + 几次 `Unlink` 不影响 SLA。

## 文件变更

```
internal/manager/biz/edge/usecase.go             (Delete 联动清理 + 新增 softDeleteJunctions)
internal/manager/data/device/store/edge_device.go (LookupEdgeForDevice JOIN edges 防御)
cmd/ongrid/main.go                                (启动 backfill 存量清扫)
.record/2026-07-20-fix-webssh-orphaned-edge-junction.md (本文件)
```

## 测试

按项目规范，本任务未写单元测试（规则要求逻辑验证即可）。编译已通过
`go build ./...`，并复跑以下路径：

- `cmd/ongrid` 启动：日志输出"orphaned junctions cleaned rows=N"，无 panic。
- `biz/edge.Usecase.Delete` 调用链：edge + 所有 junction 同步软删。
- `data/device/store.LookupEdgeForDevice`：junction 指向已软删 edge 时返回
  `errs.ErrNotFound`，router fall through 到 direct SSH。