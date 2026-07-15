# Agent B2 Report — Biz 业务用例(install / lifecycle / health / audit)

> Phase 2 子任务 T7
> 工作目录:`f:\Code\Go\运维\ongrid-new`
> 子包:`internal/pluginhost/biz`(全新)
> 状态:**已完成,go build ./internal/pluginhost/biz 通过**

---

## 1. 文件清单

| # | 文件 | 行数 | 说明 |
|---|------|------|------|
| 1 | `internal/pluginhost/biz/install.go` | 188 | install 用例 + `Store` interface + `ValidatorFactory` + `Installer` |
| 2 | `internal/pluginhost/biz/lifecycle.go` | 176 | lifecycle 用例(Enable / Disable / Uninstall)+ `LifecycleStore` + `Lifecycle` |
| 3 | `internal/pluginhost/biz/health.go` | 159 | health ticker + `HealthStore` + `Health` |
| 4 | `internal/pluginhost/biz/audit.go` | 187 | 双写审计(sink + store + buffer)+ `Auditor` |

构建验证:
```
$ go build ./internal/pluginhost/biz
(no output, exit 0)
$ go vet ./internal/pluginhost/biz
(no output, exit 0)
```

---

## 2. 4 个 Store interface(供 Phase 2 整合对接 B1 repo)

### 2.1 `biz.Store`(install.go)

```go
type Store interface {
    CreatePlugin(ctx context.Context, p *registry.PluginInstance) error
    CreateCapabilities(ctx context.Context, caps []registry.Capability) error
    Audit(ctx context.Context, pluginID uint64, action, actor string, details map[string]any) error
}
```

对接 B1:
- `CreatePlugin`        ← `data.PluginRepo.Create`
- `CreateCapabilities`  ← `data.CapabilityRepo.CreateBatch`
- `Audit`               ← `data.AuditRepo.Record`

### 2.2 `biz.LifecycleStore`(lifecycle.go)

```go
type LifecycleStore interface {
    SetEnabled(ctx context.Context, id uint64, enabled bool) error
    DeletePlugin(ctx context.Context, id uint64) error
    Audit(ctx context.Context, pluginID uint64, action, actor string, details map[string]any) error
}
```

对接 B1:
- `SetEnabled`   ← `data.PluginRepo.SetEnabled`
- `DeletePlugin` ← `data.PluginRepo.Delete`
- `Audit`        ← `data.AuditRepo.Record`

### 2.3 `biz.HealthStore`(health.go)

```go
type HealthStore interface {
    SetHealth(ctx context.Context, id uint64, status string) error
    ListEnabled(ctx context.Context, tenantID uint64) ([]registry.PluginInstance, error)
}
```

对接 B1:
- `SetHealth`    ← `data.PluginRepo.SetHealth`
- `ListEnabled`  ← `data.PluginRepo.ListEnabled`

### 2.4 `biz.AuditSink` + `biz.StoreAudit`(audit.go)

```go
type AuditSink interface {
    Write(ctx context.Context, comp, action, resource string, latency time.Duration, err error) error
}

type StoreAudit interface {
    Audit(ctx context.Context, pluginID uint64, action, actor string, details map[string]any) error
}
```

对接:
- `AuditSink`  ← A 的 `internal/audit.Log`(由 main.go 通过 `pluginhost.Deps.Audit` 注入,
  形态与 `pluginhost.AuditSink` 一致;Phase 3 主 agent 决定是否切到类型别名)
- `StoreAudit` ← `data.AuditRepo.Record`

---

## 3. 与 manifest / registry / runtime 的依赖关系

### 3.1 import 图(实际 import,不含白名单预批准)

```
biz/install.go
  ├── manifest  (types.go: PluginManifest, Capability, ...)
  └── registry  (registry.go: PluginInstance, Capability, Registry, ...)

biz/lifecycle.go
  └── registry  (registry.go: Registry, PluginInstance)

biz/health.go
  └── registry  (registry.go: PluginInstance, Registry)

biz/audit.go
  └── (无 pluginhost 子包依赖,纯 std lib)
```

### 3.2 不依赖

- **未 import `runtime`**:`Health.Ping` / `Health.tick` P1 阶段未调用 transport;
  Phase 3 接入 `runtime.Pool.Get(pluginID).Ping(ctx)` 时,只需在 install.go / health.go
  增加一行 import,不影响业务签名
- **未 import B1 的 `data`**:Phase 2 整合前 B2 看不到 `data.XxxRepo`;4 个 Store interface
  是 bridge,主 agent 整合时把 NewInstaller / NewLifecycle / NewHealth / NewAuditor
  的实参换成 `*data.XxxRepo`
- **未 import sandbox 包**:任务假设 `sandbox.Validator` interface 已存在,但 Phase 1
  的 sandbox 仅导出 `PathSafeUnderRoot` 函数。本文件用本地 `bizValidator` interface
  占位(`PathSafeUnderRoot(p, root string) error`),Phase 2 由主 agent 在 sandbox/path.go
  落地 Validator 后,把 install.go 的 `bizValidator` 类型别名切到 `sandbox.Validator`

### 3.3 runtime 后续接入点(Phase 3 TODO)

| 文件 | 当前行为 | Phase 3 改造 |
|------|---------|-------------|
| install.go `Install` | 注册到 `registry` 即止 | 注册后 `runtime.Pool.Add(pluginID, rt)` |
| lifecycle.go `Enable / Disable` | 翻 registry + DB | 同步通知 invoke router(`pool.Get(key)` warm-up 或 cool-down) |
| health.go `tick` | 静态字段判定 | `pool.Get(pluginID).Ping(ctx)` 真实探针;degraded 阈值 |
| health.go `Ping` | 调一次 tick | 单点 `pool.Get(pluginID).Ping(ctx)` |

---

## 4. Phase 3 接入 A 的 audit 系统 TODO

### 4.1 接入点

`biz/audit.go` 的 `Auditor.sink` 字段。Phase 3 主 agent 在 `main.go` 中:

```go
// main.go (Phase 3 草案)
import (
    "github.com/ongridio/ongrid/internal/audit"            // A 的 audit 包
    "github.com/ongridio/ongrid/internal/pluginhost"        // B 顶层
    phBiz "github.com/ongridio/ongrid/internal/pluginhost/biz"
)

// 1. 把 A 的 audit.Log 包成 biz.AuditSink 形态
type auditSinkAdapter struct{ log *audit.Logger }
func (a *auditSinkAdapter) Write(ctx context.Context, comp, action, resource string,
                                 latency time.Duration, err error) error {
    return a.log.Write(ctx, audit.Entry{
        Comp: comp, Action: action, Resource: resource,
        LatencyMs: latency.Milliseconds(), Err: errString(err),
    })
}

// 2. 注入到 biz.Auditor
ph := pluginhost.New(pluginhost.Deps{ /* ... */ Audit: &auditSinkAdapter{log: aLog} })
phBiz.NewAuditor(ph.Deps.Audit, pluginRepo)  // 主 agent 在 Phase 2 整合时连起来
```

### 4.2 类型对齐

| biz.audit.go (B) | A / pluginhost | 兼容策略 |
|------------------|----------------|---------|
| `biz.AuditSink` | `pluginhost.AuditSink`(`deps.go` 已定义) | Phase 3 在 biz 用 `type AuditSink = pluginhost.AuditSink` 别名 |

### 4.3 行为 TODO

| 行为 | 当前 | Phase 3 |
|------|------|---------|
| comp 字段 | 硬编码 `"pluginhost"` | 不变 |
| resource 字段 | `"plugin:<id>"` | 主 agent 可改为 `"plugin:<tenantID>:<packID>"` |
| latency 字段 | `time.Since(e.OccurredAt)` | 不变 |
| err 字段 | 固定 `nil`(审计本身没 error 语义) | 不变 |
| details payload | 仅写 store,未透传 sink | sink 透传 details(JSON)给 A 的 audit.Details 字段 |
| sink 失败 buffer | 上限 1000,环形覆盖 | Phase 4 server handler 暴露 `/pluginhost/audit/buffer_size` |
| Flush 周期 | 手动调 | 由 `pluginhost` 后台协程每 30s 调一次 |

### 4.4 Phase 3 整合代码清单

主 agent 在 `internal/pluginhost/pluginhost.go` 的 `startBackgroundLoops`
处插入:

```go
go func() {
    t := time.NewTicker(30 * time.Second)
    defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return
        case <-t.C:       _ = p.auditor.Flush(ctx)
        }
    }
}()
```

并把 `p.auditor` 与 install.go / lifecycle.go 共享同一实例(避免 buffer 漂移)。

---

## 5. Phase 4 server handler 接入 TODO

### 5.1 新增 HTTP 端点(由 `internal/pluginhost/server/` 子包实现,本任务不写)

| Method | Path | 调用方 | 委托到 biz |
|--------|------|--------|-----------|
| POST | `/api/v1/pluginhost/install` | web | `Installer.Install(ctx, m, source)` |
| POST | `/api/v1/pluginhost/{id}/enable` | web | `Lifecycle.Enable(ctx, id, capName)` |
| POST | `/api/v1/pluginhost/{id}/disable` | web | `Lifecycle.Disable(ctx, id, capName)` |
| DELETE | `/api/v1/pluginhost/{id}` | web | `Lifecycle.Uninstall(ctx, id)` |
| GET | `/api/v1/pluginhost/{id}/health` | web | `Health.Ping(ctx, id)` |
| GET | `/api/v1/pluginhost/audit/buffer_size` | ops 监控 | `Auditor.BufferLen()` |

### 5.2 接入清单

主 agent 在 Phase 4 server 子包中:

```go
// internal/pluginhost/server/handler.go (Phase 4 草案)
type Handler struct {
    Installer *biz.Installer
    Lifecycle *biz.Lifecycle
    Health    *biz.Health
    Auditor   *biz.Auditor
}

func (h *Handler) Install(w http.ResponseWriter, r *http.Request) {
    var req InstallRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { ... }
    inst, err := h.Installer.Install(r.Context(), req.Manifest, req.Source)
    if err != nil { writeErr(w, err); return }
    writeJSON(w, inst)
}
```

### 5.3 Phase 4 还要做的事

- 把 `pluginhost.PluginHost.Install / Uninstall / Enable / Disable / Invoke`
  5 个 `ErrNotImplemented` 占位换成对 `biz` service 的真实调用(替换 `pluginhost.go` 的骨架)
- 在 `Run` 的 step 3 处把 `startBackgroundLoops(ctx)` 真正实现为 health ticker + audit flush
- `Run` 的 step 4 `server.Register(p.deps.Mux, p)` 接入实际 handler

---

## 6. P1 阶段已知简化(留给后续 Phase)

| 位置 | 简化 | 影响 |
|------|------|------|
| install.go `parentDir` | 自实现,不引 path/filepath | 不处理 symlink / 不调用 `filepath.Clean`;Phase 3 接入 sandbox 后删除 |
| install.go `ValidatorFactory` | 用本地 `bizValidator` 占位 | Phase 2 主 agent 把 `bizValidator` 类型替换为 `sandbox.Validator` |
| install.go audit 失败 | `_ = err` 静默吞 | Phase 4 server handler 把审计失败写入 response header |
| lifecycle.go `Enable / Disable` 回滚 | 仅回滚 registry | DB 已改 / registry 回滚,需要补偿事务(Phase 3) |
| lifecycle.go `Uninstall` 顺序 | 先 DB 后 registry | 反向时 registry 先摘;Phase 3 改事务 |
| lifecycle.go `lookupInstance` | 遍历 `registry.List()` | O(n);Phase 3 改成 DB 主键反查 |
| health.go `deriveStatus` | 纯静态字段 | Phase 3 接入 `rt.Ping` 真实探针 |
| health.go `Interval = 0` 兜底 | 在 `loop` 内 reset | 建议 NewHealth 处直接处理;Phase 3 收紧 |
| audit.go `detailsJSON` 序列化 | 仅本地变量,未透传 sink | Phase 3 改 sink 签名透传 details |
| audit.go `store.Audit` 失败 | 不入 buffer | Phase 4 改为也入 buffer + 异步重试 |

---

## 7. 与 B1 整合位点汇总(供主 agent Phase 2 接入)

```
internal/pluginhost/biz/install.go
  Installer.Store → 换成 *data.PluginRepo + *data.CapabilityRepo + *data.AuditRepo 的复合 adapter
  ValidatorFactory → 换成 sandbox.NewValidator()(先在 sandbox/path.go 加 Validator interface)

internal/pluginhost/biz/lifecycle.go
  Lifecycle.Store → 换成 *data.PluginRepo + *data.AuditRepo 的复合 adapter

internal/pluginhost/biz/health.go
  Health.Store → 换成 *data.PluginRepo(已有 SetHealth / ListEnabled)

internal/pluginhost/biz/audit.go
  Auditor.store → 换成 *data.AuditRepo
  Auditor.sink  → 换成 A 的 audit.Log(经 auditSinkAdapter)
```

整合后建议在 `internal/pluginhost/biz/adapter.go`(主 agent 写)统一封装上述 wire-up。

---

## 8. 自查

- [x] 4 个文件全部创建,均位于 `internal/pluginhost/biz/`
- [x] 4 个 Store interface 形态与 B1 任务预期对齐(CreatePlugin / CreateCapabilities /
      SetEnabled / DeletePlugin / SetHealth / ListEnabled / Audit)
- [x] import 白名单内:std lib(context, encoding/json, errors, fmt, log/slog, sync, time)
      + pluginhost 子包(manifest, registry)
- [x] 未 import A 包任何子包
- [x] 未 import B1 的 data 包
- [x] 未写 pluginhost/{pluginhost.go, deps.go, ...} 任何禁止文件
- [x] 未写测试文件
- [x] `go build ./internal/pluginhost/biz` 通过
- [x] `go vet ./internal/pluginhost/biz` 通过
- [x] Phase 3 / Phase 4 TODO 注释清晰,主 agent 可直接续接