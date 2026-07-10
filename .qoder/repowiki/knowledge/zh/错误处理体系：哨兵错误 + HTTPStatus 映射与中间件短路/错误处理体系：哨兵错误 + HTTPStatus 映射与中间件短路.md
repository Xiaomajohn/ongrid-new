---
kind: error_handling
name: 错误处理体系：哨兵错误 + HTTPStatus 映射与中间件短路
category: error_handling
scope:
    - '**'
source_files:
    - internal/pkg/errs/errs.go
    - internal/pkg/authzmw/middleware.go
    - internal/pkg/httpserver/server.go
    - internal/manager/server/device/http.go
    - internal/manager/server/devicessh/http.go
    - internal/manager/server/aiops/http.go
    - internal/manager/server/webshell/http.go
---

## 1. 采用的错误模型与工具链
- **Go 原生 errors 包**：使用 `errors.New` 定义跨领域共享的**哨兵错误（sentinel errors）**，并通过 `errors.Is` / `errors.Join` 进行组合与匹配。
- **统一 HTTP 状态码映射**：`internal/pkg/errs.HTTPStatus(err)` 作为所有 HTTP handler 返回状态码的唯一事实来源，将业务哨兵错误映射到标准 HTTP 状态码。
- **无自定义 error 类型**：未发现基于结构体的错误类型或 gRPC status 包装，错误以轻量 sentinel 为主。
- **panic/recover 局部使用**：仅用于隔离不可恢复的插件/子进程崩溃，不替代常规错误传播。
- **无全局 panic 中间件**：HTTP 层未实现统一的 recover 中间件，依赖各 goroutine 自身防护。

## 2. 核心文件与包
- `internal/pkg/errs/errs.go` — 定义全部共享哨兵错误及 `HTTPStatus` 映射表。
- `internal/pkg/authzmw/middleware.go` — Casbin 鉴权中间件，在授权失败时直接通过 `http.Error` + `errs.ErrUnauthorized/ErrForbidden` 短路返回。
- `internal/pkg/httpserver/server.go` — 封装 `net/http.Server`，负责优雅关闭；监听错误通过 slog 记录并向上返回。
- 各 `internal/manager/server/<domain>/http.go` — Handler 中调用 `errs.HTTPStatus(err)` 决定响应状态码。

## 3. 架构与约定
### 3.1 哨兵错误清单与 HTTP 映射
| 哨兵错误 | HTTP 状态码 | 语义 |
|---|---|---|
| `ErrNotFound` | 404 | 资源不存在 |
| `ErrUnauthorized` | 401 | 未认证/凭证缺失 |
| `ErrForbidden`, `ErrTenantMismatch` | 403 | 无权限/租户不一致 |
| `ErrConflict` | 409 | 资源冲突 |
| `ErrInvalid` | 400 | 参数校验失败 |
| `ErrBudgetExceeded`, `ErrTooManyAttempts` | 429 | 配额超限 / 短窗口限流 |
| `ErrEdgeOffline` | 503 | 边缘节点离线 |
| `ErrNotWiredYet` | 501 | 功能尚未接入 |
| 其他未知错误 | 500 | 内部服务器错误 |

### 3.2 错误传播路径
```
biz layer (返回 errs.* 哨兵) → server handler (errors.Is / Join 包装) → errs.HTTPStatus(err) → http.Error(w, msg, status)
```
- biz 层只返回最小化的哨兵错误，必要时用 `fmt.Errorf("%w: detail", errs.ErrXxx)` 或 `errors.Join(errs.ErrXxx, err)` 附加上下文。
- handler 层统一通过 `status := errs.HTTPStatus(err)` 计算状态码，再写入响应。
- 鉴权中间件在解析 tenant 失败时直接短路返回 401/403，不进入下游 handler。

### 3.3 panic/recover 策略
- **插件运行时**（`edgeagent/plugins/*`）在每个采集器 goroutine 内包裹 `defer func(){ if r:=recover();r!=nil{...} }`，防止单个插件崩溃拖垮整个 agent。
- **工作流引擎**（`manager/biz/flow/engine.go`、`report/scheduler.go`）对节点执行做 recover，将 panic 转为可观测的错误事件。
- **测试与基础设施代码**中可见若干 `panic(err)` 作为“不可能到达”的断言，不属于生产错误路径。

## 4. 开发者应遵循的规则
1. **优先返回哨兵错误**：biz 层遇到可分类的业务异常，应返回 `errs.ErrXxx`，而非裸 `fmt.Errorf`。
2. **用 `%w` 或 `errors.Join` 包装细节**：保留哨兵以便上层 `errors.Is` 判断，同时附带用户可读信息。
3. **handler 统一走 `errs.HTTPStatus`**：不要手写 `switch err` 映射状态码，避免遗漏新增哨兵。
4. **鉴权失败由中间件短路**：不要在 handler 里重复写 401/403 逻辑，交由 `authzmw.Require` 处理。
5. **外部依赖错误向上冒泡**：DB/网络等 I/O 错误保持原始 error，让 `HTTPStatus` 默认回退为 500，便于监控区分。
6. **仅在隔离边界 recover**：插件、子进程、定时任务等 goroutine 内 recover，主请求链路不使用全局 recover。
7. **新增哨兵需同步更新映射**：在 `errs.go` 添加新哨兵后，务必在 `HTTPStatus` switch 中补充对应状态码。