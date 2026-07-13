# Logs 页面设备/任务下拉重写：可搜索 combobox + edges 内嵌

## 目标

1. 设备下拉：UI 升级为可搜索 combobox，模糊匹配 `name / hostname / ip_address`；
   `GET /v1/devices` 响应内嵌 `edges[]`（含 `task_name`）。
2. 任务下拉：UI 升级为可搜索 combobox；options 从 `devices[].edges[].task_name` 聚合。
3. 去掉 Logs 页面前端 1000 条 device 拉取上限（前端不再传 `limit`，后端有默认上限）。
4. 单一数据源：Logs 页面不再额外调用 `listEdges`，避免双请求 + 数据不一致。

---

## 改动范围

### 后端

1. `internal/manager/biz/device/repo.go`
   - `ListFilter` 增加 `IP string` 字段（`name / hostname / ip` 三选其一 LIKE 过滤）。
   - 同包加 `EdgeMini` 类型（精简版 edge，字段：id/name/status/task_name/last_seen_at）。

2. `internal/manager/biz/device/edge_device.go`
   - `EdgeDeviceRepo` 接口加
     `ListEdgesForDevices(ctx, deviceIDs []uint64) (map[uint64][]EdgeMini, error)`。

3. `internal/manager/data/device/store/device.go`
   - `ListFilter.IP` 实现：`tx.Where("ip_address LIKE ?", "%"+f.IP+"%")`。

4. `internal/manager/data/device/store/edge_device.go`
   - 实现 `ListEdgesForDevices`：一次 SQL `SELECT ed.device_id, e.* FROM edges e
     JOIN edge_devices ed ON ed.edge_id = e.id AND ed.delete_marker=0
     WHERE e.delete_marker=0 AND ed.device_id IN (?)`，
     按 `device_id` 分组返回 map，避免 N+1。
   - 同时过滤 `edges.delete_marker=0` 与 `edge_devices.delete_marker=0`，软删行不出。

5. `internal/manager/server/device/http.go`
   - 新增 `edgeMiniItem`（与 `EdgeMini` 形状对齐）。
   - `deviceItem` 加 `Edges []edgeMiniItem` 字段（向后兼容，旧客户端忽略新字段）。
   - `list` handler：`h.uc.List` 之后若 `Links() != nil`，批量查 `ListEdgesForDevices`；
     失败仅 log warn，edges 字段为空（设备列表照常返回）。
   - `list` handler 解析 `q.Get("ip")` 注入 `ListFilter.IP`。

### 前端

1. `web/src/api/devices.ts`
   - `EdgeMini` 导出类型。
   - `Device` 加 `edges?: EdgeMini[]`。
   - `listDevices(params)` 加 `ip?: string`，同步空串过滤。

2. `web/src/components/ui/SearchableSelect.tsx`（新增）
   - 通用可搜索下拉：input + popover，键盘 ↑↓ Enter Esc Backspace，click-outside 关闭。
   - 选项结构 `{ value, label, hint?, muted? }`：label 用于过滤与主显示，hint 副标题
     （如显示 ip/hostname），muted 让整行变 zinc-500（已删除行用）。
   - 视觉：zinc-950 容器 / zinc-800 边框，匹配项 indigo-500/15 高亮，遵循 AGENTS.md
     「避免 hover:scale / 阴影 / animate-pulse」规范。

3. `web/src/components/ui/index.ts`
   - 导出 `SearchableSelect, type SearchableSelectOption`。

4. `web/src/pages/Logs.tsx`
   - 删除：`edges` state、`taskOptions` state、`deviceInput` state、`deviceMap` state、
     `listEdges` useEffect、`onDevicesChanged` 订阅、`taskOptions` useEffect、
     `setEdges / setTaskOptions / setDeviceInput / setDeviceMap` 调用。
   - `topbarFacets` 数据源从「先 listEdges 再 filter」改为「直接读 devices[].roles」
     （May 2026 拆分后 device 自身带 roles 字段），不再依赖 `edges` state。
   - `listDevices` 调用去掉 `limit: 1000`（按设计后端有默认上限）。
   - 新增 `deviceOptions` useMemo（label = name > hostname > ip，hint = ip 或 hostname，
     已删除行 muted=true / label 加 `（已删除）`）。
   - 新增 `taskOptions` useMemo：从 `devices[].edges[].task_name` 去重排序，
     并附带 `useEffect` 在选中值掉出 options 时重置为空（避免 UI 与注入 LogQL 漂移）。
   - 设备 / 任务两个下拉 UI 改为 `<SearchableSelect>`。
   - 「清除筛选」按钮移除 `setDeviceInput('')` 调用。

---

## 影响面

- 所有调 `GET /v1/devices` 的页面（Edits / Hosts / EdgeDetail / Monitor 等）收到的
  响应多了 `edges` 字段。**这是新增字段，向后兼容**：旧代码直接忽略新字段不影响。
- 若有代码把 `listDevices` 响应直接做深相等比较（如 `JSON.stringify(prev) ===
  JSON.stringify(next)`），会因为新增 edges 数组而误判为变化。本仓库搜索过没找到
  这种用法，详见「回归检查」一节。
- 后端 `GET /v1/devices` 多了一次 `IN (?)` 批量查 edges，N+1 风险消失但响应体
  变大。当前内嵌设备数普遍 < 1000，每台设备平均 edges 数 < 5，响应膨胀 < 20%。
  生产环境大规模部署（> 5000 设备）后再评估是否需要分页 / `include_edges=1` 开关。

---

## 回归检查

按规则 3 不写单测，按规则 4 不本地跑 .sh/make，本地只做编译：

```bash
go build ./internal/manager/biz/device/... \
         ./internal/manager/data/device/... \
         ./internal/manager/server/device/...
cd web && npm run build
```

均通过。`npx tsc --noEmit` 零报错。

线上回归点（192.168.25.30 打包机实跑，参考
`.record/2026-07-13-ongrid-server-upgrade-v0.9.1-bind-mount.md`）：

1. `curl -s 'http://127.0.0.1:8088/v1/devices?include_deleted=true' | jq '.[0].edges'`
   返回数组，元素有 `id/name/status/task_name`。
2. `curl -s 'http://127.0.0.1:8088/v1/devices?name=&hostname=&ip=192.168.25'`
   返回所有 ip 含 `192.168.25` 的设备（与 name/hostname 独立 LIKE）。
3. 浏览器 Logs 页面：
   - 设备下拉输入 `centos` / `192.168.25.5` / `#3` 各能筛出对应行；
   - 任务下拉只显示当前 devices 中所有 edge 的 `task_name` 去重集合；
   - 勾选「显示已删除」后，软删设备灰字 `（已删除）` 出现；
   - 切换设备后任务下拉不重置（task 全局 orthogonal）；
   - 「清除筛选」按钮能一次清空设备/角色/文件/任务四组。

---

## 关联记录

- `2026-07-13-ongrid-server-upgrade-v0.9.1-bind-mount.md`：v0.9.1 升级包基线，本记录
  属于同一发布窗口内的前端体验优化。
- `2026-07-13-fix-edge-list-task-name-missing.md`：edge list/get 响应加 `task_name`
  字段，本记录的上游依赖；该记录修复后 Logs 任务下拉才能从 `e.task_name` 拿到数据。
- `2026-07-13-ongrid-apt-mirror-switch.md`：无关，本轮未触碰 apt 源。