# 2026-07-05 install-edge 接口 404 修复

## 背景

`https://192.168.25.30/api/v1/devices/{id}/install-edge`（前端
InstallEdgeModal「开始安装」按钮发起的请求）所有目标设备都返 404，整条
一键安装 edge 流程无法启动。前端 SPA 收到 404 后弹『Failed to start install』
红字，operator 看不到 /dashboard 的设备面板那条「一键安装」实际工作。

## 根因

全栈追踪下来的断点 **不在前端、也不在 nginx**，而是 server 层根本没注册这条
路由：

| 层级 | 文件 | 状态 |
| --- | --- | --- |
| 1. UI | `web/src/components/InstallEdgeModal.tsx:56` | `submit()` 调 `installEdge(device.id, body)` |
| 2. API | `web/src/api/devices.ts:225-234` | 发 `POST /api/v1/devices/{id}/install-edge` |
| 3. nginx | `deploy/nginx/nginx.conf` | 反代 `/api/v1/*` 到 manager 容器 ✓ |
| 4. 后端路由 | `internal/manager/server/installjob/http.go:92-96` | **`Handler.Register()` 只注册 3 条路由**：`GET /v1/install-jobs/{id}`、`GET /v1/devices/{id}/install-jobs`、`POST /v1/install-jobs/{id}:cancel`。**没有 `POST /v1/devices/{id}/install-edge`** |
| 4. 后端 Usecase | `internal/manager/server/installjob/http.go:61-65` | `Usecase` 接口只有 `Get / ListByDevice / Cancel`，**没有 `Create`** |
| 5. Biz 层 | `internal/manager/biz/installjob/{repo,worker,runner}.go` | `Repo.Create` / `Worker.Execute` / `Runner.Enqueue` 全部已存在 |
| 5. Wiring | `cmd/ongrid/main.go:937-961` | repo / worker / runner 已构造并 `installRunner.Start(rootCtx)` |
| 5. Adapter | `cmd/ongrid/main.go:3865-3867` | `installjobUsecaseAdapter` 只持 `repo`，没注入 `deviceRepo` 和 `runner` |

biz 层 + wiring 早已就位（worker 已经在 `main.go:957` 等到 jobID），中间没人拼
server 层 `Usecase.Create` + adapter 实现，chi 收到未注册路径就 404。

顺带清理的死代码：server 层原 `StatusPending = "pending"` 常量没有任何引用，
实际 wire 上 `install_jobs.status` 是 "queued"（biz 层 `installjob.Status` 类型）
而非 "pending"。注释也把状态机写成了 "pending/running/..."，与 biz 层
"queued/running/..." 不一致；这次顺手重写为与 biz 对齐的 5 个值加上 timeout。

## 改动

### 1. `internal/manager/server/installjob/http.go`

- **常量**：`StatusPending` 改名为 `StatusQueued = "queued"`，新增 `StatusTimeout = "timeout"`，
  其余保留；注释改为与 biz 层 `installjob.Status` 对齐的 6 值集合。
- **`CreateResponse` DTO**：新增导出类型，字段 `{install_job_id, status}`，
  对齐前端 `web/src/api/devices.ts:220-223 InstallEdgeResponse`。
- **`Usecase` 接口**：新增 `Create(ctx, deviceID uint64) (*Job, error)`。
  无 body 参数 — SSH 凭据来源由 plan 决定走设备表，不读 override。
- **`Handler.Register()`**：新增 `r.Post("/v1/devices/{id}/install-edge", h.createInstall)`。
- **`createInstall` handler**（新增）：
  - `tenantctx.From` 校验 → 401（与现有 get/listByDevice/cancel 风格一致）。
  - `deviceIDFrom` → 400 / `errs.ErrInvalid`。
  - `uc.Create(ctx, deviceID)` → `*Job`；错误由 `errs.HTTPStatus` 自然映射。
  - 成功返 `200 OK` + `CreateResponse{InstallJobID, Status}`。
- **Swagger 注释**（AGENTS.md 红线）：`@Summary / @Router / @Success` 三件套
  落在 `createInstall` 的 godoc 上，匹配 `internal/manager/server/systemupgrade/http.go:36-41`
  的现有风格。

### 2. `cmd/ongrid/main.go`

- **`installjobUsecaseAdapter`**：字段从 `repo` 单字段扩成
  `repo / deviceRepo / runner` 三字段。
- **`Create(ctx, deviceID)`**：新实现，按 plan 走流程
  `deviceRepo.Get → SSH 三件套校验 → 构造 InstallJob → repo.Create → runner.Enqueue`
  → `bizInstallJobToServerJob`。
  - 设备不存在 / soft-deleted：自然走 `errs.ErrNotFound` → 404。
  - SSH host / user / (password|key) 任一缺失：包装 `errs.ErrInvalid` → 400。
    （参考 `devicessh.ErrSSHConfigMissing` 注释说的 428 映射；但 `errs.HTTPStatus`
    当前没有 428 分支，遵循现有 devicessh shell handler 实际行为降级为 400。
    后续如需严格 428，可单独立 PR 加 sentinel + HTTPStatus 分支。）
  - `port=0` 兜底为 22；`SSHAuthKind` 非枚举值兜底为 `"password"`（与
    `managerbizinstalljob.AuthKindPassword` 对齐）。
  - `runner.Enqueue` 满载只 log 不返错（见 `runner.go:92-97`）—— job 落库后
    SPA 轮询仍能看到 status=queued；不主动返 503 避免误判。
- **wiring**：第 965 行 `NewHandler(installjobUsecaseAdapter{...})` 注入
  `deviceRepo` + `installRunner`，二者周边已构造好。

### 3. 前端

未改前端。用户决策「禁止连点用 loading 效果」—— 现有
`InstallEdgeModal.tsx:78` 的 `Button disabled={submitting}` 已覆盖。
未来若需要服务端防并发闸门，建议放 worker.Execute 入口 ListByDevice + status
filter（不进本 PR）。

### 4. 本文件（.record）

按规则 5「代码改动要添加记录，单独在 .record 中」。

## 验证（按规则 3 / 4）

- 逻辑：list 全栈调用链
  `UI(InstallEdgeModal) → request('/devices/{id}/install-edge', POST)
  → chi 命中 POST /v1/devices/{id}/install-edge → createInstall handler
  → tenantctx check → deviceIDFrom → uc.Create(ctx, deviceID)
  → adapter.Create → deviceRepo.Get(ctx, id) → SSH 三件套校验
  → repo.Create(InstallJob{Status: queued, Host/Port/User/AuthKind/PasswordSnap/KeySnap})
  → runner.Enqueue(jobID) → bizInstallJobToServerJob → CreateResponse wire
  → Runner workerLoop → Worker.Execute → issuer.CreateEdgeForDevice
  → installer.Install (SSH + upload/run install.sh + tail log)
  → presence.WaitOnline → final status updating + ClearCredentialSnap`，
  全程无遗漏。
- 打包：`go build ./internal/manager/server/installjob/ ./cmd/ongrid/` 通过，
  0 输出。本地不验证 .sh / .mk（规则 4）。
- 前端不再需要任何改动。

## 已知非阻塞（不进本 PR）

- `GET /v1/install-jobs/{id}` + `GET /v1/devices/{id}/install-jobs` 当前
  server `Job` DTO 缺前端 `InstallJob` 类型要的
  `edge_id / host / port / user / log_output / exit_code / updated_at`。
  日志面板要读 `log_output` 做实时尾随时需要补齐，留待下一 PR。
- 后端不实现同设备并发闸门，符合用户决策（前端 loading 防连点）。
- `errs.HTTPStatus` 暂无 428 分支；本 PR 的「SSH 凭据缺失」走 400，与现有
  devicessh shell / fs handler 一致。
