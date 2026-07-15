# Agent A4 — InvokeRouter 报告

**Phase**: 1.5(串行,依赖 A1 registry + A3 runtime interface)
**Agent**: A4
**完成时间**: 2026-07-14
**Windows 编译验证**: `go build ./internal/pluginhost/invoke` ✅ PASS

---

## 1. 文件清单

| 文件 | 状态 | 行数 | 说明 |
|---|---|---|---|
| `f:\Code\Go\运维\ongrid-new\internal\pluginhost\invoke\router.go` | 新建 | 228 | Router 路由层唯一文件 |

**未触碰**:
- A 老文件(`cmd/ongrid/main.go`、`internal/manager/*`、`internal/iam/*`、`internal/edgeagent/*`、`internal/skill/*`、`internal/pkg/*`、`api/*`、`web/*`、`deploy/*`):0 改动
- 同 Phase 其他 agent 文件(`pluginhost.go`、`deps.go`、`registry/*`、`runtime/*`、`sandbox/*`、`manifest/*`、`model/*`):0 改动
- 测试文件:无(任务明确要求不写测试)

---

## 2. Router interface 与方法表

### 2.1 类型定义

```go
// 错误码(4 个,errors.Is 区分)
var ErrUnknownCapability   = errors.New("invoke: unknown capability")
var ErrPluginDisabled      = errors.New("invoke: plugin disabled")
var ErrRuntimeUnavailable  = errors.New("invoke: runtime unavailable")
var ErrInvalidParams       = errors.New("invoke: invalid params")  // Phase 2 启用

// 默认超时
const defaultTimeoutSeconds = 30

// 审计回调签名(nil 合法,Router 跳过 audit)
type AuditFunc func(ctx context.Context, pluginID uint64, capName string, latency time.Duration, err error)

// Router 主结构(并发安全)
type Router struct {
    mu    sync.RWMutex
    reg   *registry.Registry
    pool  *rtp.Pool
    audit AuditFunc
}

// 单次调用选项
type InvokeOptions struct {
    Caller   rtp.Caller      // 发起方(component / user / tenant / trace)
    Deadline time.Time       // 零值 → Router 用 defaultTimeoutSeconds 兜底
    TraceID  string          // 链路追踪 ID(暂存于 Caller.TraceID;字段保留供 server 用)
}
```

### 2.2 方法签名表

| 方法 | 签名 | 语义 | 副作用 |
|---|---|---|---|
| 构造 | `func NewRouter(reg *registry.Registry, pool *rtp.Pool) *Router` | 零副作用构造,reg / pool 由调用方保证非 nil | 无 |
| 注入审计 | `func (r *Router) WithAudit(f AuditFunc) *Router` | 链式注入,返回 self | 修改 `r.audit`(mu 保护) |
| 主入口 | `func (r *Router) Invoke(ctx, pluginID uint64, capName string, params json.RawMessage, opts InvokeOptions) (rtp.Response, error)` | 路由 + 度量 + audit | 调 `reg.Lookup`、`reg.List`、`pool.Get`、`rt.Invoke`、可选 `audit` |

### 2.3 私有辅助

| 函数 | 签名 | 说明 |
|---|---|---|
| `findPlugin` | `func findPlugin(reg *registry.Registry, pluginKey string, pluginID uint64) *registry.PluginInstance` | 在 reg.List() 中匹配 PackID 或 ID,Phase 1.5 兼容两种 key 形式 |
| `generateReqID` | `func generateReqID() string` | 返回 `inv-<unixnano>-<rand4>`,rand4 = 4 个十六进制字符 |

---

## 3. 错误码清单

| 错误 | 触发条件 | HTTP 含义(供 Phase 4 server 映射) |
|---|---|---|
| `ErrUnknownCapability` | reg.Lookup 不存在 或 reg.List 找不到 plugin | 404 not_found |
| `ErrPluginDisabled` | plugin.Enabled == false(SetEnabled(false) 后) | 403 forbidden |
| `ErrRuntimeUnavailable` | reg 有 capability 但 pool.Get 未加载(典型:进程刚启动还没 Add) | 503 unavailable |
| `ErrInvalidParams` | Phase 2 接入 schema 校验后启用;Phase 1.5 占位 | 400 bad_request |
| `rtp.Response.Error` 字段 | runtime.Invoke 内部错误(子进程崩溃 / 远程 5xx / ctx 超时等) | 透传 status |

调用方用 `errors.Is(err, invoke.ErrXxx)` 区分;rtp 内部错误原样透传。

---

## 4. 与 registry / runtime 的依赖关系

### 4.1 import 关系

```
internal/pluginhost/invoke/router.go
    ├─ context / crypto/rand / encoding/binary / encoding/json / errors / fmt / sync / time
    ├─ rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"   (别名 rtp)
    └─ "github.com/ongridio/ongrid/internal/pluginhost/registry"
```

### 4.2 调用的 interface

| 来源 | 方法 | 在 Router 中的用法 |
|---|---|---|
| `registry.Registry.Lookup(pluginID, capName string) (*Capability, bool)` | 把 uint64 pluginID 转 string 后查 (key1=pluginID, key2=capName) |
| `registry.Registry.List() []*PluginInstance` | 遍历找 Enabled 状态(Phase 1.5 兼容 PackID 与 ID 两种 key) |
| `rtp.Pool.Get(pluginID string) (Runtime, bool)` | 拿 transport,pluginID 同 string 形式 |

### 4.3 与 A 的解耦验证

```
$ go list -deps ./internal/pluginhost/invoke | grep -E "(manager|iam|edgeagent|api|skill|notify|embedding)"
(无输出)
```

✅ **未 import 任何 A 包**,Phase 1.5 在 Windows 上无需 cgo(`onnxruntime_go` / `libtorch`)即可 build。

### 4.4 关键字段映射(registry → rtp)

| `registry.PluginInstance` | `rtp.PluginInstance` | 说明 |
|---|---|---|
| `ID uint64` | `ID uint64` | 直传 |
| `TenantID uint64` | `TenantID uint64` | 直传 |
| `PackID string` | `PackID string` | 直传 |
| `Version string` | `Version string` | 直传 |
| `InstallPath string` | `InstallPath string` | 直传 |
| `InstallPath string` | `Entry string` | Phase 1 兜底;Phase 2 用 `cap.Metadata["entry"]` 或 registry 新字段 |
| `ManifestSHA256 string` | `ManifestSHA256 string` | 直传 |
| `Enabled bool` | `Enabled bool` | 直传 |
| — | `TimeoutSeconds int` | 默认 30,Phase 2 接 model 后改 |

`registry.Capability` → `rtp.Capability`:`PluginID / Kind / Name / Class / Schema` 五个字段直传;`Metadata` 不传(由 hostcall 层在 Phase 3 用)。

---

## 5. Phase 4 server handler 接入 TODO

### 5.1 wire-up 顺序(在 `cmd/ongrid/main.go` 末尾 append)

```go
// 1. 构造 reg / pool(invoker 已有 / Phase 2 biz 提供)
reg := registry.NewRegistry()
pool := rtp.NewPool()

// 2. 构造 Router,链式注入 audit 回调
auditFn := func(ctx context.Context, pluginID uint64, capName string, latency time.Duration, err error) {
    _ = deps.Audit.Write(ctx, "pluginhost", "invoke",
        fmt.Sprintf("%d/%s", pluginID, capName), latency, err)
}
router := invoke.NewRouter(reg, pool).WithAudit(auditFn)

// 3. 把 router 挂到 deps(供 server handler 调)
deps.PluginHost = router

// 4. server.Register(deps.Mux, router) —— Phase 4 D1 实现
```

### 5.2 HTTP handler 调用样例(供 D1 参考)

```go
func (h *Handler) invokePlugin(w http.ResponseWriter, r *http.Request) {
    var req struct {
        PluginID uint64          `json:"plugin_id"`
        CapName  string          `json:"cap_name"`
        Params   json.RawMessage `json:"params"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), 400); return
    }
    caller := rtp.Caller{
        Component: "web:user:" + userFromCtx(r.Context()),
        UserID:    uidFromCtx(r.Context()),
        TenantID:  tenantFromCtx(r.Context()),
        TraceID:   traceFromCtx(r.Context()),
    }
    resp, err := h.router.Invoke(r.Context(), req.PluginID, req.CapName, req.Params,
        invoke.InvokeOptions{Caller: caller, Deadline: deadlineFromCtx(r.Context())})
    if err != nil {
        switch {
        case errors.Is(err, invoke.ErrUnknownCapability):  http.Error(w, err.Error(), 404)
        case errors.Is(err, invoke.ErrPluginDisabled):     http.Error(w, err.Error(), 403)
        case errors.Is(err, invoke.ErrRuntimeUnavailable): http.Error(w, err.Error(), 503)
        default:                                            http.Error(w, err.Error(), 500)
        }
        return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(resp)
}
```

### 5.3 TODO 清单

| # | 归属 | 内容 | 影响 |
|---|---|---|---|
| 1 | Phase 2 B2 biz | biz.Install 写完 DB 后,同步调 `reg.Register` + `pool.Add` | 否则 router 收到 Invoke 后返 ErrRuntimeUnavailable |
| 2 | Phase 2 B2 biz | biz.Enable/Disable 同步调 `reg.SetEnabled`;router 无需感知 | Enable 时 runtime 已在 pool;Disable 后 router 自动 ErrPluginDisabled |
| 3 | Phase 2 B2 biz | biz.Uninstall 同步调 `reg.Unregister` + `pool.Remove` | 防止 dangling runtime |
| 4 | Phase 2 T5 model | registry.PluginInstance 增加 `Entry string` 字段(manifest.entry),router 把 `inst.Entry` 透传到 `rtp.PluginInstance.Entry` | 当前 Phase 1.5 用 InstallPath 兜底 |
| 5 | Phase 2 T5 model | model.PluginInstance 增加 `TimeoutSeconds int`,router 优先读该字段,fallback 到 30 | 当前 Phase 1.5 硬编码 30 |
| 6 | Phase 2 biz | 增加 `ErrInvalidParams` 触发点:JSON 合法性 + schema 校验 | 当前 Phase 1.5 占位 |
| 7 | Phase 4 D1 server | 在 server.Handler 里注入 caller / deadline 解析逻辑 | 见 §5.2 |
| 8 | Phase 4 D2 main | wire-up 顺序见 §5.1 | — |

### 5.4 并发与一致性

- **router.audit 替换**:用 `sync.RWMutex`,读路径(Invoke)用 RLock,写路径(WithAudit)用 Lock,Phase 4 仅 wire-up 时调一次,不热替换。
- **reg / pool 漂移**:Phase 2 biz 必须在 Unregister/Remove 前先停掉对应 plugin 的 in-flight 调用,否则 router 可能在 rt.Invoke 拿到 ctx 取消错误。当前 Phase 1.5 暂未实现 in-flight tracking,Phase 6 加。
- **多租户**:Phase 2 接 tenant 隔离时,`packKey(tenantID, packID)` 替换当前 `packKey(packID)`,router 的 pluginKey = `fmt.Sprintf("%d/%s", tenantID, packID)`。router.Invoke 接收的 pluginID uint64 与 TenantID 分离传(或合成结构体),待 Phase 2 biz 与 deps 一起设计。

---

## 6. 编译验证记录

```
$ cd f:\Code\Go\运维\ongrid-new
$ go build ./internal/pluginhost/invoke
$ if ($?) { Write-Output "BUILD_OK" } else { Write-Output "BUILD_FAIL" }
BUILD_OK

$ go vet ./internal/pluginhost/invoke
(无输出,无警告)

$ go list -deps ./internal/pluginhost/invoke | grep -E "(manager|iam|edgeagent|api|skill|notify|embedding)"
(无输出,0 匹配)
```

✅ 编译通过,无 vet 警告,未引入任何 A 包。