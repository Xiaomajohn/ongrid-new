# 安装 Edge 弹窗去除任务名（必填）输入

## 背景

用户反馈：安装监控（Edge）的弹窗里有「任务名（必填）」输入框，重复且不合理。
任务名（`edge.task_name`）已在「新建 Edge」（`CreateEdgeModal`）流程里有可选
输入并直接写入 `edge.task_name`，安装弹窗里再要求必填属于重复字段。

需求语义：

> 安装监控的时候，任务名字没有必要添加，监控名字、任务名，应该是
> 添加的时候的添加进来的。

收口方向：任务名（task_name）只允许在「添加监控 / 新建 Edge」时填，
安装弹窗不再二次询问。

## 改动文件

| 文件 | 改动 |
| --- | --- |
| `web/src/components/EdgeInstallModal.tsx` | 删除 `taskName` state、删除「任务名（必填）」Field、删除 `trimmedTaskName` 校验、`canOneClick` 不再依赖任务名、`handleOneClickInstall` 的 body 不再带 `task_name` |
| `web/src/components/InstallEdgeModal.tsx` | 同上；`createEdge` 也不传 `task_name`（设备视角一键安装不再二次询问） |
| `web/src/api/devices.ts` | `InstallEdgeOptions.task_name` 从 `string` 改为可选 `task_name?: string`，注释更新为「可选」语义 |
| `web/src/api/edges.ts` | `Edge.task_name` 注释从「安装时由 operator 在 InstallEdgeModal 输入」改为「由 CreateEdgeModal 填入并直接写入 edge.task_name」 |
| `internal/manager/server/installjob/http.go` | `installCreateReq.TaskName` 加 `omitempty`；`createInstall` 删除 `task_name is required` 校验，trim 后空串视为「未提供」；Usecase.Create 注释、Error mapping 注释同步更新为「可选」 |
| `cmd/ongrid/main.go` | `installjobUsecaseAdapter.Create` 注释从「taskName 必填，HTTP 层是双保险」改为「taskName 可选，下游 worker 走 no-op 路径」 |

## 行为变化

- 之前：两个安装弹窗（`EdgeInstallModal` / `InstallEdgeModal`）都要求用户填
  任务名，`InstallEdgeOptions.task_name` 必填；后端 `task_name is required` 拦
  截空串。
- 之后：两个弹窗都不再出现「任务名」输入框；请求体可不带 `task_name` 字段或
  带 `task_name: ""`；后端 trim 后空串视为「未提供」并放行；下游 `worker`、
  `BindEdgeFromAccessKey` 已有 no-op 逻辑（`parseTaskNameFromOptions` 返回空时
  跳过 `repo.UpdateTaskName`，`installer.Install` 透传空串时 install.sh 不带
  `--task-name`），所以兼容路径无需再改。
- 任务名唯一入口 = `CreateEdgeModal`（`Edges.tsx` 中已有任务名可选输入），
  写到 `edges.task_name` 列。

## 数据流影响

1. **CreateEdgeModal 路径**（推荐）：用户在 Edges 页点「新建」→ 填 `name` +
   可选 `task_name` → POST `/v1/edges` → `edges.task_name` 落库 → 后续
   install 时 `edge.task_name` 已存在。
2. **Edge 行视角安装**（`EdgeInstallModal`）：edge 已存在（多半已经走路径 1
   填过 task_name），安装时不再要求重填，沿用 `edge.task_name`。
3. **设备视角一键安装**（`InstallEdgeModal`）：内部 `createEdge({ name })` 不
   传 `task_name`，所以安装完成后 `edge.task_name` 为空字符串。需求里没有
   要求在设备视角强行取一个任务名（device.name 不等价于任务名），保持
   `edge.task_name` 为空、后续用户在 Edges 页「编辑」或「新建」流程补填。
4. 设备 agent register 时如果上报 `task_name`，仍然走
   `HandleRegister → repo.UpdateTaskName`，仅在 `t != "" && t != edge.TaskName`
   时更新，不会与本改动冲突。

## 验证

- `go build ./...` 通过。
- `npx tsc --noEmit` 中与本改动相关的 `EdgeInstallModal` / `InstallEdgeModal` /
  `src/api/devices.ts` / `src/api/edges.ts` 均无错误（其他错误均为仓库
  pre-existing 缺 `@types/xterm` / `@types/xyflow__react` / `@types/dagrejs__dagre`
  引起，与本次改动无关）。
