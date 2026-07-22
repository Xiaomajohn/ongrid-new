# 2026-07-22 日志查询修复与历史数据管理页面

## 背景

三个问题：
1. 日志页面勾选"已删除"后能查出已删除设备，但其关联的已删除 edge 任务查不到——根因是 `ListEdgesForDevices` SQL 硬编码 `WHERE e.delete_marker = 0`。
2. 日志页面任务下拉与设备下拉无关联——`taskOptions` 遍历全部 devices 聚合，不随 `deviceFilter` 变化。
3. 缺少历史数据管理入口——软删除的设备/edge 没有专门页面查看。

## 后端改动

### 1. `internal/manager/biz/device/edge_device.go`
- `EdgeDeviceLinker` 接口 `ListEdgesForDevices` 签名新增 `includeDeleted bool` 参数。

### 2. `internal/manager/data/device/store/edge_device.go`
- `ListEdgesForDevices` 实现：`includeDeleted=true` 时移除 SQL 中 `e.delete_marker = 0` 条件（保留 `ed.delete_marker = 0`，junction 行删除语义不同）。

### 3. `internal/manager/server/device/http.go`
- `list` handler 调用 `links.ListEdgesForDevices(ctx, ids, f.IncludeDeleted)` 传递参数。

### 4. `internal/manager/biz/edge/repo.go`
- `ListFilter` 新增 `IncludeDeleted bool` 字段。

### 5. `internal/manager/data/edge/store/edge.go`
- `Repo.List` 当 `f.IncludeDeleted=true` 时使用 `tx.Unscoped()` 跳过 GORM 软删除过滤。

### 6. `internal/manager/server/edge/http.go`
- `listItem` 结构体新增 `DeletedAt *time.Time` 字段。
- `listEdges` handler 解析 `include_deleted` query 参数，构建响应时传递 `e.DeletedAt`。

## 前端改动

### 7. `web/src/pages/Logs.tsx`
- `taskOptions` useMemo 加入 `deviceFilter` 依赖：当 `deviceFilter` 非空时只从匹配设备的 edges 聚合 task options；为空时保持全局聚合。

### 8. `web/src/api/edges.ts`
- `Edge` 类型新增 `deleted_at?: string | null`。
- `listEdges` 参数新增 `include_deleted?: boolean`，拼装到 URLSearchParams。

### 9. `web/src/pages/DeviceHistory.tsx`（新建）
- 路由 `/devices/history`，历史数据管理页面。
- 两个区域：已删除主机（可展开查看关联已删除 edge）、已删除 Edge 监控任务。
- 数据源：`listDevices({ include_deleted: true })` + `listEdges({ include_deleted: true })`，前端过滤 `deleted_at != null`。

### 10. `web/src/App.tsx`
- 新增 `DeviceHistoryPage` lazy import。
- `/devices/history` 路由放在 `/devices/:edgeId` 之前，避免参数路由冲突。

### 11. `web/src/lib/routes.ts`
- 新增 `/devices/history` 路由条目。

### 12. `web/src/pages/Hosts.tsx`
- 页头区域新增"历史数据"入口链接（History icon + Link to `/devices/history`）。

## 关键决策

- **不做物理删除**：用户明确不需要，软删除即可。
- **不做恢复功能**：用户未要求。
- **不修改侧边栏导航**：入口放在设备页面（Hosts）页头区域内。
- **前端客户端过滤**：历史页面复用 `include_deleted=true` 接口 + 前端过滤 `deleted_at != null`，不新增后端端点。
- **路由优先级**：`/devices/history` 静态路由必须在 `/devices/:edgeId` 参数路由之前注册。
- **junction 表过滤保留**：`includeDeleted=true` 时只移除 `e.delete_marker = 0`，保留 `ed.delete_marker = 0`（junction 行删除语义不同于 edge 实体删除）。

## 编译验证

```bash
go vet ./internal/manager/biz/device/ ./internal/manager/biz/edge/ \
       ./internal/manager/data/device/store/ ./internal/manager/data/edge/store/ \
       ./internal/manager/server/device/ ./internal/manager/server/edge/
# 通过，零错误

cd web && npx tsc --noEmit
# 通过，零错误
```

---

# 第二轮：确认删除（二次删除）+ 侧边栏入口 + 历史页增强

## 背景（用户反馈）

1. 历史数据页面位置不对——应加到侧边栏“设备”下级菜单，而非只放在 Hosts 页头。
2. 页面缺少“确认删除”按钮与提示。数据流：主机/监控页删除 → 历史页可见（日志勾选“已删除”可查）→ 历史页点“确认删除”→ 不物理删除但日志页面查询不到。
3. 要考虑“主机未删除、仅监控任务删除”的情况。

## 设计：purge_marker（确认删除标记）

- Device / Edge 模型新增 `PurgeMarker int64`（`purge_marker` 列，default 0）。
- `purge_marker != 0` = 已确认删除：行不物理删除，但 `include_deleted=true` 的列表查询会排除它 → 日志页面查不到，历史页不再展示。
- `purge_marker = 0` + `deleted_at != null` = 普通软删除：日志勾选“已删除”可查，历史页展示。
- 确认删除设备时，同时通过 junction 表把该设备关联的所有 edge 一并标记（事务）。

## 后端改动

### 模型
- `model/device/model.go` Device 新增 `PurgeMarker int64`。
- `model/edge/model.go` Edge 新增 `PurgeMarker int64`。

### 接口 + 实现
- `biz/device/repo.go` Repo 新增 `ConfirmDelete(ctx, id)`；`biz/device/usecase.go` 新增 `Usecase.ConfirmDelete`。
- `data/device/store/device.go` `Repo.ConfirmDelete`：事务内先 `UPDATE devices SET purge_marker=?`，再 `UPDATE edges ... WHERE id IN (SELECT edge_id FROM edge_devices WHERE device_id=? AND delete_marker=0)`。
- `biz/edge/repo.go` Repo 新增 `ConfirmDelete(ctx, id)`；`biz/edge/usecase.go` 新增 `Usecase.ConfirmDelete`；`data/edge/store/edge.go` `Repo.ConfirmDelete`。
- `service/edge/service.go` 新增 `Service.ConfirmDelete`；`server/edge/http.go` `EdgeService` 接口新增 `ConfirmDelete`。

### HTTP 端点
- `POST /v1/devices/{id}/confirm-delete`（`server/device/http.go` `confirmDelete` handler）。
- `POST /v1/edges/{id}/confirm-delete`（`server/edge/http.go` `confirmDeleteEdge` handler，走 deleteMW）。

### 列表查询排除 purged
- `data/device/store/device.go` `List`：`IncludeDeleted` 时 `Unscoped().Where("purge_marker = 0")`。
- `data/edge/store/edge.go` `List`：`IncludeDeleted` 时 `Unscoped().Where("edges.purge_marker = 0")`。
- `data/device/store/edge_device.go` `ListEdgesForDevices`：两个分支都加 `e.purge_marker = 0`。

### 响应字段
- `server/device/http.go` `deviceItem` 新增 `PurgeMarker int64 \`json:"purge_marker"\``，`devToItem` 传递。
- `server/edge/http.go` `listItem` 新增 `PurgeMarker int64 \`json:"purge_marker"\``，`listEdges` 传递。

### 测试 fake 补齐
- `server/edge/http_test.go` fakeDeviceRepo + fakeSvc 加 `ConfirmDelete`。
- `biz/edge/usecase_test.go` fakeDeviceRepo + fakeRepo 加 `ConfirmDelete`。
- `biz/aiops/tools/registry_test.go` fakeEdgeRepo 加 `ConfirmDelete`。
- `biz/aiops/agent/agent_test.go` fakeEdgeRepoAgent 加 `ConfirmDelete`。

## 前端改动

### 侧边栏
- `components/Sidebar.tsx`：设备 CollapsibleSection 新增 `<SidebarNavItem to="/devices/history" icon={History} label={历史数据} />`；import `History` icon。

### API 层
- `api/devices.ts`：Device 新增 `purge_marker?: number`；新增 `confirmDeleteDevice(id)`。
- `api/edges.ts`：Edge 新增 `purge_marker?: number`；新增 `confirmDeleteEdge(id)`。

### 历史页重写
- `pages/DeviceHistory.tsx`：
  - 顶部黄色提示条说明数据流（删除→历史页可见/日志勾选可查；确认删除→日志查不到/本页不展示）。
  - `allDevices`（未删+已删未确认）用于给“已删除监控任务”回填所属主机名；主机未删除时标注“（主机正常）”。
  - 已删除主机行：展开查看关联已删除 edge + “确认删除”按钮（window.confirm 二次确认，提示会连带删除关联任务）。
  - 已删除监控任务行：显示所属主机 + “确认删除”按钮（仅删任务不影响主机）。
  - 客户端兑底过滤 `purge_marker` 为 0 的行。

## 关键决策

- **purge_marker 用 int64（UnixMilli）而非 bool**：与 delete_marker 风格一致，保留确认时间信息。
- **确认删除设备连带其 edge**：设备确认删除后其任务也不应再被日志查到，事务内一并标记。
- **确认删除 edge 不影响设备**：对应“主机未删除、仅任务删除”场景，只标记 edge 本身。
- **后端 + 前端双重过滤 purged**：后端 `include_deleted=true` 排除 purged；前端再兑底过滤，防旧后端脏数据。

## 编译验证

```bash
go vet <device/edge 相关包>   # 通过，零错误
go test -run xxx_none ./internal/manager/biz/edge/ ./internal/manager/server/edge/ \
  ./internal/manager/data/device/store/ ./internal/manager/data/edge/store/  # ok
cd web && npx tsc --noEmit     # 通过，零错误
```

> 注：`biz/aiops/*` 测试包在 Windows 上因预存的 edgeagent 跨平台问题（`syscall.Setpgid` Linux-only）无法构建，与本次新增的单行 fake stub 无关。

---

# 第三轮：修复日志页仍查不到已删除监控任务（junction 软删回归）

## 背景（用户反馈）

“日志管理页面还是查询不到删除的监控任务。”

## 根因

第一轮保留了 `ListEdgesForDevices` JOIN 里的 `ed.delete_marker = 0`（当时决策“junction 表过滤保留”）。
但 2026-07-20 的 WebSSH 修复（见 `.record/2026-07-20-fix-webssh-orphaned-edge-junction.md`）让
`biz/edge.Usecase.Delete` 在软删 edge 时**连带软删 junction 行**（`softDeleteJunctions` → `Unlink`）。
于是已删除 edge 的 junction 行 `delete_marker != 0`，被 JOIN 条件过滤掉——
即使 `includeDeleted=true` 不过滤 `e.delete_marker`，已删除的 edge 任务也永远查不到。
这是“日志页查不到已删除监控任务”的真正根因（第一轮只移除 `e.delete_marker = 0` 不够）。

## 改动

### `internal/manager/data/device/store/edge_device.go` — `ListEdgesForDevices`
- `includeDeleted=true` 分支：JOIN 条件从 `ON ed.edge_id = e.id AND ed.delete_marker = 0`
  改为 `ON ed.edge_id = e.id`（不再过滤 junction 的 delete_marker），保留 `WHERE e.purge_marker = 0`。
- 行循环新增按 `(device_id, edge_id)` 去重（`dedupeKey` + `seen` map）：
  同一 edge 可能因“删除后重新注册”同时存在软删与存活两条 junction；
  非 includeDeleted 分支也可能因 host/discovered 双 type 关联出现重复。
- 更新函数文档注释，说明为何 includeDeleted 时不能过滤 junction。

## 关键决策（修正第一轮决策）

- **推翻“junction 表过滤保留”**：`includeDeleted=true` 时 junction 行**不能**按 `delete_marker=0` 过滤，
  否则查不到“edge 删除连带软删 junction”的已删除任务。`includeDeleted=false` 分支维持原样（仍过滤 junction）。
- **去重保留第一条**：JOIN 顺序不定，但同一 (device, edge) 的多条 junction 携带的 edge 字段完全相同，
  保留哪条都不影响展示。
- **purge 排除不变**：两分支都保留 `e.purge_marker = 0`，确认删除的任务依然查不到（符合第二轮设计）。

## 影响面验证

- `Unlink`（junction 软删）仅被 `biz/edge.softDeleteJunctions`（edge 删除时）与测试调用，
  不存在“junction 软删但 edge 存活”的常规路径，放开过滤不会引入错误关联。
- 启动 backfill（`cmd/ongrid/main.go`）只软删悬挂 junction、不物理删除，修复后仍能查到。
- DeviceHistory 页走 `data/edge/store/edge.go` `List`（不 JOIN junction），不受影响。

## 编译验证

```bash
go vet ./internal/manager/data/device/store/...   # 通过，零错误
```
