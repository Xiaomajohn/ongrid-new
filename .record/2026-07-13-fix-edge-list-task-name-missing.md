# 修复监控设备列表「任务名」列始终为空的 bug

## 现象
在「设备 → 监控」页面创建一台监控设备时，CreateEdgeModal 弹窗里填了「任务名」输入框，
创建成功跳回列表后，该行的「任务名」列始终显示为 `—`，刷新、轮询、等心跳都无变化。
但同一行的「名称」「所属设备」「主机名」「IP」都正常。

## 排查路径

按全栈调用链顺序自上而下查：

1. **UI 层**（`web/src/pages/Edges.tsx`）：CreateEdgeModal 有「任务名」input，
   onSubmit 把 `taskName` 透给 `onCreate(name, deviceID, taskName)`。
2. **前端 API 层**（`web/src/api/edges.ts`）：`createEdge({ name, device_id?, task_name? })`
   正确把 `task_name` 字段 POST 到 `/v1/edges`；`Edge` 类型也声明了 `task_name?: string`。
3. **后端 Router**（`internal/manager/server/edge/http.go`）：`POST /v1/edges` → `createEdge`。
4. **后端业务层**（`internal/manager/biz/edge/usecase.go:185`）：
   `Usecase.Create` 把 `o.taskName` 写入 `e.TaskName`，再 `u.repo.Create(ctx, e)` 落库。
   没问题。
5. **DB Schema**（`internal/manager/model/edge/model.go:53`）：
   `TaskName string \`gorm:"size:128;column:task_name"\`` 列存在。

## 根因

后端 list/get 响应的 DTO **没有 `TaskName` 字段**：

- `listItem`（http.go 355-369 行）：少 `TaskName`
- `getResp`（http.go 376-388 行）：少 `TaskName`

所以 listEdges / getEdge 在 JSON 序列化时根本没把 `e.TaskName` 写进响应体，
前端拿到的 `task_name` 字段是 `undefined`，UI 走 fallback 分支渲染成 `—`。
**数据库里 task_name 是正确写入的**，只是 list/get 没回包。install 路径里
`InstallEdgeIssuer.BindEdgeFromAccessKey` 也会回写同一列（`UpdateTaskName`），
所以从 install 流程创建的 edge 在 list 里依然看不到任务名——同一 bug。

## 改动

文件 `internal/manager/server/edge/http.go`：

1. `listItem` 加 `TaskName string \`json:"task_name,omitempty"\``，附 3 行注释说明语义。
2. `getResp` 加同样的字段，注释指向 `listItem.TaskName`。
3. `listEdges` handler 的循环里 `listItem{...}` 字面量补 `TaskName: e.TaskName`。
4. `getEdge` handler 的 `getResp{...}` 字面量补 `TaskName: e.TaskName`。

## 没改的地方

- `createResp`（http.go 334-340）也缺 `TaskName` 字段，但用户场景是「创建后回到
  列表看任务名」，list 接口修复后由 `refresh()` 拉一遍就显示出来，create 响应
  不带不影响功能。保留范围最小修复。
- `ListFilter`（`internal/manager/biz/edge/repo.go:29`）当前没有 `TaskName`
  过滤参数；`EdgesFilterBar` 也没暴露该筛选；用户没要该功能，**不擅自加**。
 后续要按任务名筛边缘时按现有 Name/Hostname/IP 同款模式扩展。

## 验证

按规则 3 不写单测、规则 4 不本地跑 .sh/make，本地只做编译：

```bash
go build ./internal/manager/server/edge/...
```

通过，无报错。

按规则 6 不在 Windows 调试 Linux，需要在 192.168.25.30 打包机实跑：
- 重新构建 ongrid 镜像并 hot-restart（参考 `.record/2026-07-13-ongrid-server-upgrade-v0.9.1-bind-mount.md`）
- 在 SPA 创建一台监控设备、填任务名、回到列表，确认该行「任务名」列显示填写的内容（不再为 `—`）
- 打开 Network 面板，`GET /v1/edges` 响应体里 `task_name` 字段非空（与 DB 实际值一致）
- 老 edge（task_name 为空的）继续显示 `—`，与 DB 实际值一致，行为正确

## 影响面

- 仅影响 `/v1/edges`（list/get）响应的 JSON shape：多了 `task_name` 字段（omitempty，空值不出）。
- 前端 `Edge` 类型 `task_name?: string` 已经声明，零类型修改。
- Logs 页面（`web/src/pages/Logs.tsx`）的 `e.task_name` 聚合代码之前因为这个 bug
  一直拿到 undefined，列表里基本为空；**顺手修好之后 Logs 任务下拉会自动恢复**，
  但本记录只针对监控设备列表 bug 描述，Logs 联动修复是自然结果不单独追溯。
- createResp 没改是因为「创建后立即在客户端拼出 task_name 回显」没有用户反馈场景；
  后续要做「创建后立即展示任务名」UI 时再补。
