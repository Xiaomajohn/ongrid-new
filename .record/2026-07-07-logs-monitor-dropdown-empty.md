# 日志页"监控"下拉框数据为空修复

## 现象
日志页 `/logs` 顶部"监控"下拉框只显示"全部监控"占位项，**没有任何可选项**；同行的"设备"下拉框却能正常显示设备列表。截图：监控下拉红框内空。

## 根因
后端 `/v1/devices/{id}/monitors` 在 `monitor_panels` 表上用 `WHERE device_id = ?` 严格匹配指定设备，**排除了 `device_id IS NULL` 的"全局面板"**。

但实际上：

1. `web/src/components/MonitorPanelModal.tsx`（Monitor 页「新增面板」弹窗）**没有任何 `device_id` 输入字段**
2. `web/src/api/monitorPanels.ts:MonitorPanelInput.device_id` 标注是可选，UI 也没让运维选
3. 结果：库里**所有**通过该弹窗创建的 panel，`device_id` 全部为 NULL
4. 日志页选了设备 → 调 `GET /api/v1/devices/{id}/monitors` → 后端 WHERE 把全局面板排除 → 返回 `[]` → 下拉只剩 placeholder

设备下拉不受影响，因为 `GET /v1/devices` 不带 `device_id` 过滤条件，**全部 device 行都返回**。

## 全栈调用链

| 层 | 文件 | 关键行 |
| --- | --- | --- |
| 前端 Logs 页 useEffect | `web/src/pages/Logs.tsx` | L475-L510 调 `listDeviceMonitors(deviceId, { include_deleted })` |
| 前端 API 封装 | `web/src/api/monitorPanels.ts` | L90-L100 `GET /devices/{id}/monitors?include_deleted=...` |
| HTTP handler | `internal/manager/server/device/monitors_http.go` | L45-L67 强制把 `DeviceID` 设为 path 参数 |
| biz Service.List | `internal/manager/biz/monitor/service.go` | L139-L141 透传 |
| repo SQL | `internal/manager/data/monitor/store/repo.go` | L30-L47 `tx.Where("device_id = ?", *f.DeviceID)` ← **核心过滤** |

## 修复
**最小且零破坏的方案**：把 per-device 接口的 SQL 改成「device 绑定 OR 全局」，单次请求解决。

```diff
- tx = tx.Where("device_id = ?", *f.DeviceID)
+ tx = tx.Where("device_id = ? OR device_id IS NULL", *f.DeviceID)
```

## 影响面分析（修复前已确认）

| 调用方 | 传 DeviceID? | 修复后行为 | 是否破坏 |
| --- | --- | --- | --- |
| `monitors_http.go:listMonitorsForDevice`（Logs 页） | 强制 | 设备绑定的 panel + 全局 panel，符合 operator 心智 | ✅ **预期变化** |
| `monitor/http.go:list`（`/v1/monitor/panels?device_id=`） | 来自 query，当前**无任何前端调用方传** | 同上，对齐语义 | ❌ 无破坏 |
| `service.go:SyncNow` | 零值 | DeviceID==nil，WHERE 不变 | ❌ 无破坏 |
| `service.go:kickSync` | 零值 | 同上 | ❌ 无破坏 |

- 前端 `listDeviceMonitors` 仅 Logs 页 1 处使用
- 后端 `biz/monitor.Service.List` 仅 2 个 HTTP handler 透传
- 没有任何调用方**依赖**"DeviceID 非空时排除全局面板"这个旧语义

## 改动

### 1. `internal/manager/data/monitor/store/repo.go`（L26-L47）
- SQL: `device_id = ?` → `device_id = ? OR device_id IS NULL`
- 同步更新 `List` 函数 doc 注释，说明 per-device 视角的新语义与设计理由

### 2. `internal/manager/biz/monitor/service.go`（L89-L110）
- 修正 `ListFilter.DeviceID` 注释：旧注释写「Global panels are excluded — callers wanting "device-specific AND global" should issue two List calls」是**反 operator 心智的**，改为「per-device 视角包含 device-bound + global」语义
- 旧注释的"callers should issue two List calls"建议已被前后端实际行为否定：Logs 页 1 个调用方，且只发了 1 个请求

### 3. `internal/manager/server/device/monitors_http.go`（L39-L47）
- `listMonitorsForDevice` 注释从「panels bound to the device」改为「panels scoped to the device (device-bound + global)」
- 指向 `biz/monitor.Service.List` 的注释作权威语义来源

### 4. `web/src/api/monitorPanels.ts`（L57-L66）
- 修正 `listMonitorPanels` 注释：旧注释写「NULL values are NOT included in the response by default」与新行为冲突
- 改为「NULL values (global panels) ARE included by default — the Logs page's per-device dropdown expects device-bound OR global」

## 不改动的部分
- `web/src/components/MonitorPanelModal.tsx` —— 弹窗是否加 `device_id` 下拉是另一个独立的产品决定，本次修复不动 UI
- `web/src/pages/Logs.tsx` —— 监控下拉框 `useEffect` 行为与字段不变（仅后端响应里多出全局 panel）
- 后端 `monitorPanels` 表的 schema 不变，存量 NULL 行无需回填
- `SyncNow / kickSync` 走零值 `ListFilter{}`，自动绕过新过滤

## 验证
- 后端：`go build` + `go vet` 在 `internal/manager/data/monitor/...`、`internal/manager/biz/monitor/...`、`internal/manager/server/device/...` 三个包均通过
- 前端 `tsc --noEmit` 中 `monitorPanels.ts` 与 `Logs.tsx` 零错误（其他文件的预先存在错误与本修复无关）
- 逻辑：选设备 → 调 `listDeviceMonitors` → 响应里既包含 `device_id = N` 的 panel，也包含 `device_id IS NULL` 的 panel → 下拉框列出所有可见 panel → operator 选后 `monitorFilter` 正确写入 LogQL 子句
