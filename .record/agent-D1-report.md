# D1 Final Report — pluginhost server 子包(HTTP handler + routes)

> 日期:2026-07-14
> 范围:internal/pluginhost/server/{dto,middleware,handler,routes,grpc_handler}.go 共 5 个新文件
> 验证:`go build ./internal/pluginhost/server` + `go vet ./internal/pluginhost/server` 均通过(0 错 0 警告)
> A 老文件改动:0(`cmd/ongrid/main.go` 与所有其它 A 子包未触碰)

---

## 1. 文件清单

| 文件 | 行数 | 角色 |
|---|---|---|
| [server/dto.go](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\dto.go) | 144 | 统一响应壳 + 4 类 DTO + model→DTO 转换器 |
| [server/middleware.go](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\middleware.go) | 143 | Recovery / RequestID / Tenant 三个中间件 + 写 JSON helper |
| [server/handler.go](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | 415 | Handler struct + 9 个 HTTP 端点 + URL/query 解析 helper |
| [server/routes.go](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\routes.go) | 69 | Register(mux, h, logger) + chi 路由表 + 中间件装配顺序 |
| [server/grpc_handler.go](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\grpc_handler.go) | 42 | gRPC 占位 stub(GRPCHandler + NewGRPCHandler) |
| **合计** | **813** | |

---

## 2. HTTP 路由表(plan §13 信道 3:admin → B)

注册入口:`func Register(mux chi.Router, h *Handler, logger *slog.Logger)`
调用方(D2 main.go wire-up):`serverMux := chi.NewRouter(); server.Register(serverMux, h, log); mux.Mount("/api/pluginhost", serverMux)`

> ⚠️ 路径口径说明:
> 任务 prompt 要求的内部路径是 `/plugins/...`,**已严格按 prompt 实现**。
> 已知与 `web/src/api/pluginhost.ts` 的 `BASE_PREFIX = '/pluginhost'` + `/instances/...` 不一致(前端在等 `/api/v1/pluginhost/instances/...`)。
> 修复办法有两种,D2 阶段任选其一:
>
> 1. **改本包路径**:把 `mux.Route("/plugins", ...)` 改成 `mux.Route("/instances", ...)` 并把 `{id}` 内的子路径命名同步调整(本任务不动手,留给 D2 决定)。
> 2. **改前端**:调整 `web/src/api/pluginhost.ts` 的 `BASE_PREFIX` 与子路径为 `/plugins/...`。
>
> 推荐方案 1,因为服务端路径已经按 D1 任务规范实现,D2 一次 `SearchReplace` 即可对齐前端。

| Method | 相对路径 | Handler | 依赖(biz / data / Router) |
|---|---|---|---|
| GET | `/plugins/` | [Handler.ListPlugins](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `PluginRepo.List` + `CapRepo.ListByInstance`(N+1,P1 简化) |
| GET | `/plugins/{id}/` | [Handler.GetPlugin](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `PluginRepo.Get` + `CapRepo.ListByInstance` |
| GET | `/plugins/{id}/capabilities` | [Handler.ListCapabilities](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `CapRepo.ListByInstance` |
| POST | `/plugins/` | [Handler.InstallPlugin](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `biz.Installer.Install`(→ biz.Store 写 DB+registry+audit) |
| POST | `/plugins/upload` | [Handler.UploadTarball](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | **P1 返 501**,Phase 5 接入 manifest 解包 |
| DELETE | `/plugins/{id}/` | [Handler.UninstallPlugin](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `biz.Lifecycle.Uninstall` |
| POST | `/plugins/{id}/capabilities/{capName}/enable` | [Handler.EnableCapability](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `biz.Lifecycle.Enable` |
| POST | `/plugins/{id}/capabilities/{capName}/disable` | [Handler.DisableCapability](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `biz.Lifecycle.Disable` |
| POST | `/plugins/{id}/capabilities/{capName}/invoke` | [Handler.InvokeCapability](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go) | `invoke.Router.Invoke` |

中间件链(从外到内,见 [routes.go](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\routes.go)):

1. `RequestIDMiddleware` — 从 `X-Request-ID` 读 / 生成,写 ctx + 响应头
2. `RecoveryMiddleware` — panic recover → JSON 500 + slog.Error
3. `TenantMiddleware` — 从 `X-Tenant-ID` 读租户(0 兜底)→ ctx

---

## 3. Handler 依赖注入表

[Handler](file://f:\Code\Go\运维\ongrid-new\internal\pluginhost\server\handler.go#L29-L40) 字段映射:

| 字段 | 类型 | 作用 | 用到该字段的端点 |
|---|---|---|---|
| `PluginRepo` | `*data.PluginRepo` | 读 / 写 `plugin_instances` 表 | List, Get, Install(成功后 count) |
| `CapRepo` | `*data.CapabilityRepo` | 读 / 写 `plugin_capabilities` 表 | List(cap count), Get(cap count), ListCapabilities, Install(cap count) |
| `InvokeRepo` | `*data.InvocationRepo` | 写 `plugin_invocations` 流水 | 当前 P1 不直接调,留 Phase 5 由 `Router.WithAudit` 注入 |
| `AuditRepo` | `*data.AuditRepo` | 写 `plugin_audits` | 当前 P1 不直接调,生命周期审计由 `biz.Lifecycle.Store.Audit` 内部完成 |
| `Reg` | `*registry.Registry` | 进程级 capability 注册表 | 当前 P1 不直接调(由 `biz.Installer` / `biz.Lifecycle` 内部维护) |
| `Pool` | `*rtp.Pool` | 进程级 runtime pool | 当前 P1 不直接调;Phase 5 Install 完成时由 `biz.Installer` 调 `Pool.Add` |
| `Router` | `*invoke.Router` | 调 plugin capability | Invoke |
| `Installer` | `*biz.Installer` | install 用例入口 | Install |
| `Lifecycle` | `*biz.Lifecycle` | enable / disable / uninstall | Enable, Disable, Uninstall |

---

## 4. 与 plan §11 / §13 L2 的对应

### §11 与 A 的关系(3 处 append)

| plan §11 行 | D1 server 子包实现 |
|---|---|
| `A 的 chi mux: mux.Mount("/api/pluginhost", hdl)` | 由 D2 在 main.go 末尾 append;本包 Register(mux, h, logger) 接收 chi.Router,**不**直接 import A;hdl 即 serverMux = `chi.NewRouter()` 后 `server.Register(serverMux, h, log)`,再 `mux.Mount("/api/pluginhost", serverMux)`。 |
| `0 个 A 老文件改动` | 已严格遵守 — `git status` 确认 `cmd/ongrid/main.go` 与 `internal/manager/**` / `internal/iam/**` / `internal/edgeagent/**` / `api/**` 全部未触碰。 |
| `web 路由前缀 /api/pluginhost` | 见 §2 路径口径说明,`/api` 前缀由 D2 在 main.go 挂载阶段补上。 |

### §13 L2 信道 3:admin → B(管理面)

数据流:`web UI → HTTP /api/pluginhost/* → server/handler → biz → data + registry`

对应到代码:

1. **HTTP 入口**:`server.Register` 把 9 条路由挂到传入的 chi.Router
2. **handler 解析 + DTO 映射**:`dto.go` 提供 Response / PluginInstanceDTO / CapabilityDTO / InstallRequest / InvokeRequest / InvokeResultDTO
3. **biz 转发**:`handler.go` 内部只调 `biz.Installer.Install` / `biz.Lifecycle.{Enable,Disable,Uninstall}` / `invoke.Router.Invoke`,**不**直接动 data 或 registry
4. **biz → data + registry**:`biz/install.go` 写 DB+registry+audit,`biz/lifecycle.go` 写 DB+registry+audit;`invoke/router.go` 走 registry+pool+runtime

---

## 5. Phase 4 D2 main.go wire-up TODO(给 D2 的具体步骤)

> 假设 D2 阶段 A 老 `cmd/ongrid/main.go` 已经按 plan §11 末尾 append ~30 行的 wire-up 段,本节只描述 server 子包相关的 wire-up 代码。

### 5.1 构造 Handler 依赖

在 wire-up 阶段(已有 `db *gorm.DB` / `log *slog.Logger` / `rootCtx` / A 主 mux),按下面顺序构造:

```go
// 1) 构造 data 层(已有的话复用)
pluginRepo := data.NewPluginRepo(db)
capRepo := data.NewCapabilityRepo(db)
invRepo := data.NewInvocationRepo(db)
auditRepo := data.NewAuditRepo(db)

// 2) 构造 pluginhost 进程级对象(由 planhost 主包 New 出 PluginHost 之后取 p.reg / p.pool)
reg := p.Reg()        // 或从 pluginhost.PluginHost 暴露 getter
pool := p.Pool()
router := invoke.NewRouter(reg, pool)
// Phase 5 接入 audit 回调:
//   router.WithAudit(func(ctx, pluginID, capName, latency, err) {
//       invRepo.Record(ctx, &model.PluginInvocation{...})
//   })

// 3) 构造 biz 层(Store 是 data 的 bridge)
installer := biz.NewInstaller(
    &installStoreAdapter{pluginRepo, capRepo, auditRepo},  // 实现 biz.Store
    reg,
    nil,                                                  // Phase 3 之前不强制 sandbox validator
)
lifecycle := biz.NewLifecycle(
    &lifecycleStoreAdapter{pluginRepo, auditRepo},        // 实现 biz.LifecycleStore
    reg,
)

// 4) 构造 server.Handler
h := &server.Handler{
    PluginRepo: pluginRepo,
    CapRepo:    capRepo,
    InvokeRepo: invRepo,
    AuditRepo:  auditRepo,
    Reg:        reg,
    Pool:       pool,
    Router:     router,
    Installer:  installer,
    Lifecycle:  lifecycle,
}

// 5) 注册到 A 主 mux
serverMux := chi.NewRouter()
server.Register(serverMux, h, log)
mux.Mount("/api/pluginhost", serverMux)   // plan §11 接缝点
```

> 注:`installStoreAdapter` / `lifecycleStoreAdapter` 是把 `*data.XxxRepo` 包成 `biz.Store` / `biz.LifecycleStore` interface 的薄壳。B2 阶段已经定义了这两个 interface(本包内 `biz` 已是 100% 实现好的实接口),D2 只需要写两行 type assertion 即可。

### 5.2 gRPC(可选,P1 不必接入)

```go
grpcHandler := server.NewGRPCHandler(h)
// 真正注册到 grpc.Server 留 Phase 5,等 api/manager/pluginhost/v1/pluginhost.proto 落地后
// pluginhostv1.RegisterPluginHostServer(grpcSrv, pluginhostv1.UnimplementedPluginHostServer{...})
// 的 bridge 由 D2 补。当前 P1 阶段 NewGRPCHandler 已经返回非 nil,后端 grpcSrv 可暂不挂载。
```

### 5.3 路径对齐(对齐 web/src/api/pluginhost.ts)

- D2 wire-up 时,如发现 web 前端 `BASE_PREFIX = '/pluginhost'` + `/instances/...` 与服务端 `/plugins/...` 不一致,推荐在 D1 端一次 SearchReplace 改为 `/instances`,或同步把前端 `BASE_PREFIX` 改成 `/pluginhost` + `/plugins` 路径。
- 建议保留 `/plugins/...` 路径(更短、与 pluginhost 子包同名),改前端一个常量即可。

---

## 6. 已知简化 / 留给后续 Phase 的 TODO

1. **N+1 capability 计数**:`ListPlugins` 逐 instance 调 `CapRepo.ListByInstance`;Phase 5 接入 `CapRepo.ListByTenant(instanceID=0)` 一次性聚合。
2. **InstallPlugin P1 minimal manifest**:当前用 `req.Path` 末尾段 + `Version="0.0.0"` 构造 `*manifest.PluginManifest` 调 `Installer.Install`;Phase 5 替换为"先 `manifest.LoadDirs` 拿完整 manifest,再 Install"二段式。
3. **UploadTarball**:当前返 501,Phase 5 接入 multipart 解包 + LoadDirs。
4. **TenantMiddleware**:当前从 `X-Tenant-ID` 读 + 缺省 0;Phase 5 切到 A 的 `tenantctx`(从 JWT claim / session 解出)。
5. **Audit callback**:`invoke.Router.WithAudit` 在 D2 wire-up 时注入,届时调 `InvokeRepo.Record` 写 `plugin_invocations` 流水;本包不直接耦合。
6. **gRPC service method**:仅占位 stub,Phase 5 引入 `api/manager/pluginhost/v1/pluginhost.proto` 后,补 gRPC method 实现(由 D2 整合到 grpc.Server)。

---

## 7. 编译验证记录

```
$ go build ./internal/pluginhost/server
(ExitCode: 0, 无输出)

$ go vet ./internal/pluginhost/server
(ExitCode: 0, 无输出)

$ git status -- internal/ cmd/
On branch main-local-new
Changes not staged for commit:
  (无 internal/ cmd/ 下的 modified 行)
Untracked files:
  internal/pluginhost/...   ← 包含本次新增 5 个文件
  ...
```

注:`go build ./internal/pluginhost/...` 在 Windows 上仍因 `github.com/yalue/onnxruntime_go@v1.7.0` 的 cgo build constraints 失败(plan §11 阶段规划外问题,与本任务无关)。D1 目标子包 `./internal/pluginhost/server` 独立 build 通过,符合任务要求(server 包不依赖 pluginhost 主包,无 cgo 链路)。
