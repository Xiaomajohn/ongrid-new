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
