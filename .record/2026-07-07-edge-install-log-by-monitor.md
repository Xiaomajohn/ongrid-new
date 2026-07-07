# 监控设备维度关联安装日志

## 背景

Hosts（主机）页面右侧操作栏的"日志"按钮原本按 device_id 查最近一次安装任务，但 install_jobs 表里同时记录了 device_id + edge_id。一台 device 可能装 0~N 个 edge（不同 task_name），按 device 维度拿"最近一次"会把多个 edge 的安装混在一起失去归属语义。

需求调整：安装日志入口迁到监控设备页面（Edges.tsx），按 edge 而不是 device 取最近一次安装日志；Hosts 页面的"日志"按钮按用户要求移除。

## 改动清单

### 后端（Go）

- `internal/manager/data/installjob/repo.go`：新增 `ListByEdge(ctx, edgeID, limit)`，按 `edge_id = ?` 倒序拉，与 `ListByDevice` 同语义同限流策略。install_jobs.edge_id 是可空列，未关联 edge 的旧任务不会被命中。
- `internal/manager/biz/installjob/repo.go`：Repo interface 加 `ListByEdge` 方法。
- `internal/manager/server/installjob/http.go`：
  - `Job` struct 加 `EdgeID *uint64` 字段（JSON `omitempty`，null 不出现）；
  - `Usecase` interface 加 `ListByEdge` 方法；
  - 注册新路由 `GET /v1/edges/{id}/install-jobs`，handler 实现沿用 `listByDevice` 的 limit 默认 / 上限约定（默认 20，最大 100）。
- `cmd/ongrid/main.go`：`installjobUsecaseAdapter` 加 `ListByEdge` 委托；`bizInstallJobToServerJob` 把 biz 层 `j.EdgeID` 拷到 server 层 DTO。

### 前端（React + TS）

- `web/src/api/devices.ts`：新增 `listInstallJobsByEdge(edgeId, limit=20)`，路由 `/edges/{id}/install-jobs`，与 `listInstallJobsByDevice` 对齐。`InstallJob` 类型原本就有 `edge_id?: number | null`，无需补充。
- `web/src/api/installjob.ts`：re-export `listInstallJobsByEdge`。
- `web/src/pages/Edges.tsx`：
  - import `listInstallJobsByEdge` / `InstallJob` / `ScrollText`；
  - 加 `installJobHistory: Record<number, InstallJob>` state，按 `edge.id` 索引；
  - mount 后 `useEffect` 并发拉每个 edge 最近一次安装任务（limit=1），失败容忍；
  - 在行操作栏"安装"按钮之后插入 `LogButton`，仅当该 edge 有最近一次任务时可点击；
  - 已有 `activeInstallJob: { edgeId, jobId }` 复用，不需要改 InstallLogPanel；
  - 新增 `LogButton` 子组件（私有，与 Hosts 之前的 LogButton 同形态但语义切到 edge 维度）。
- `web/src/pages/Hosts.tsx`：移除"日志"按钮及其全部支撑代码：
  - 删除 import `ScrollText`、`InstallLogPanel`、`listInstallJobsByDevice`、`InstallJob`；
  - 删除 `installJobHistory` state、`activeInstallJob` state 及拉取 effect；
  - 删除操作栏 `<LogButton>` JSX 和底部 `<InstallLogPanel>` 渲染；
  - 删除文件末尾 `LogButton` 子组件定义；
  - `InstallEdgeModal.onStarted` 不再开日志面板，只做 refresh；提示文案指向 /edges 页的"日志"按钮。
- `InstallLogPanel`：保持原样。Job 已有 device_id / edge_id，但 Edges 页面只对单一 edge 操作，没必要再重复展示 edge_id，避免噪音。

## 验证

- `go build ./...`：通过。
- `go vet ./...`：通过。
- `tsc --noEmit -p web`：仅命中 pre-existing 错误（xterm / @xyflow/react / @dagrejs/dagre 类型缺失，FlowEditor / XTerminal / topology/Graph）。我改的 Hosts.tsx / Edges.tsx / devices.ts / installjob.ts / InstallLogPanel 都干净。