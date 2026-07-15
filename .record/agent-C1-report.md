# agent-C1 报告 — 6 个 Adapter 实现

**任务**: T9 — 新增 6 个 adapter 文件,把 plugin capability 注入 A 现有接口
**路径**: `internal/pluginhost/adapter/`
**状态**: ✅ 6 文件全部创建,`go build` + `go vet` 通过(gofmt -l 无差异)

---

## 1. 文件清单

| 文件 | 字节数 | 职责 |
|---|---|---|
| [skill_adapter.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/adapter/skill_adapter.go) | 8631 | KindAITool / KindSkillRunner → skill.Executor |
| [notify_adapter.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/adapter/notify_adapter.go) | 6452 | KindNotifier → notify.Sender |
| [llm_adapter.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/adapter/llm_adapter.go) | 5897 | KindLLMProvider → pluginhost.LLMProvider 占位 |
| [embedding_adapter.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/adapter/embedding_adapter.go) | 7243 | KindEmbedProvider → 本地 Embedder |
| [evaluator_adapter.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/adapter/evaluator_adapter.go) | 5313 | KindEvaluator → pluginhost.Evaluator 占位 |
| [flow_adapter.go](file:///f:/Code/Go/运维/ongrid-new/internal/pluginhost/adapter/flow_adapter.go) | 5638 | KindWorkflowNode → pluginhost.Node 占位 |

---

## 2. A 实际类型签名调研结果(plan §6.1-§6.5 假设 vs 实际)

### 2.1 skill(internal/skill)

**Plan §6.1 假设**: `skill.Executor` + `skill.Metadata` 真实存在,`skill.Register(e)` 直接用。

**A 实际**:
- [skill/types.go:200](file:///f:/Code/Go/运维/ongrid-new/internal/skill/types.go#L200-L203) `Executor` interface:
  ```go
  type Executor interface {
      Metadata() Metadata
      Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error)
  }
  ```
- [skill/types.go:100](file:///f:/Code/Go/运维/ongrid-new/internal/skill/types.go#L100-L125) `Metadata` struct: `Key/Name/Description/Class/Scope/Category/Params/ResultPreview`;`Key` 必须满足 `skill.validKey` 的 `[a-z0-9_]+` 约束;`Name/Description` 必填。
- [skill/registry.go:31](file:///f:/Code/Go/运维/ongrid-new/internal/skill/registry.go#L31-L33) 全局注册入口 `Register(e Executor) Metadata`,**panic on validation failure / duplicate Key**(作者期错误,启动期崩溃)。

**差异**: Plan 没提 `skill.validKey` 约束;`Register` 行为是 panic 而非返 error。本 adapter 在 `RegisterSkills` 循环里加了 `ad.Metadata().Validate()` 前置校验,失败 → log warning + 跳过(避免单个坏 cap 让整批 panic)。

### 2.2 notify(internal/pkg/notify)

**Plan §6.2 假设**: `notify.Router.RegisterSender(notifyAdapter)`。

**A 实际**:
- [notify/notify.go:34](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/notify/notify.go#L34-L37) `Sender` interface:
  ```go
  type Sender interface {
      Name() string
      Send(ctx context.Context, msg Message) error
  }
  ```
- [notify/notify.go:23](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/notify/notify.go#L23-L31) `Message` struct: `Subject/Body/Severity/Source/DedupeKey/Labels/OccurredAt`。
- [notify/notify.go:40-65](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/notify/notify.go#L40-L65) `Router` 是 concrete struct,`channels map[string]Sender` 在 `NewRouter / NewFromConfig` 时**一次性初始化**,**没有 `RegisterSender` 方法**。

**差异**: Plan 假设的 `notify.Router.RegisterSender(...)` **A 包里不存在**。需在 Phase 4 main.go wire-up 时写 thin adapter 桥接(见 §5)。

### 2.3 llm(internal/pkg/llm)

**Plan §4 / §6.3 假设**: `llm.Provider` 类型 export。

**A 实际**:
- `grep "type Provider" internal/pkg/llm/` 结果: **无任何 export Provider 类型**。
- A 只有:
  - [client.go:126](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/llm/client.go#L126-L128) `Client` interface: `Chat(ctx, ChatReq) (*ChatResp, error)`
  - [client.go:102-115](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/llm/client.go#L102-L115) `ChatReq` / `ChatResp` / `Message` / `ToolCall` / `Usage` / `ToolSchema` struct
  - [router.go:67](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/llm/router.go#L67-L87) `MultiClient`(按 ChatReq.Provider 字符串派发到预构 sub-client)
- pluginhost/deps.go:147 占位 `type LLMProvider interface { _phantom() }` 等待 A 包补齐。

**差异**: Plan §14.2 已识别此问题,接受占位方案。本 adapter 复制 `_phantom()` 占位 contract,Phase 4 wire-up 时由 main.go 把本地 `LLMProvider` 包成 `llm.Client`(Chat 透传 invokeRouter.Invoke)。

### 2.4 embedding(internal/pkg/embedding)

**Plan §6.3 假设**: `embedding.Registry.RegisterEmbedder(...)`。

**A 实际**:
- [embedding.go:40-48](file:///f:/Code/Go/运维/ongrid-new/internal/pkg/embedding/embedding.go#L40-L48) `Embedder` interface:
  ```go
  type Embedder interface {
      Dim() int
      Embed(ctx context.Context, texts []string) ([][]float32, error)
  }
  ```
- `grep "type Registry" internal/pkg/embedding/` 结果: **无 Registry 类型**;`func New(cfg Config) (Embedder, error)` 是**单例构造**,main.go 持有一个全局 Embedder。
- `go list -deps ./internal/pkg/embedding` 末段:
  ```
  github.com/yalue/onnxruntime_go
  github.com/anush008/fastembed-go
  ```
  Windows cgo 不可用。

**差异**: Plan 假设的 Registry **不存在**;且 import 此包必然拉 onnxruntime。本 adapter 不 import embedding,本地定义 `type Embedder interface { Embed(...) }`(省略 Dim,plugin 不一定报维度)。Phase 4 wire-up 需在 main.go 写 thin adapter 把本地 Embedder 适配到 A 全局 Embedder(典型:multi-embedder 路由器,按 name 派发)。

### 2.5 alertconfig(internal/manager/biz/aiops/alertconfig)

**Plan §6.4 假设**: `alertconfig.Evaluator` interface 存在。

**A 实际**:
- `ls internal/manager/biz/aiops/alertconfig/` 只有 3 个文件: `alert_rule_manager.go / draft_store_memory.go / draft_validation.go`。
- `grep "type Evaluator" internal/manager/biz/aiops/alertconfig/` 结果: **零匹配**。
- alert 的 Evaluator 概念由 [internal/manager/biz/alert/pipeline.go:82](file:///f:/Code/Go/运维/ongrid-new/internal/manager/biz/alert/pipeline.go#L82-L82) `PipelineEvaluator`(concrete struct,非 interface)实现,无 plugin 侧注入 hook。

**差异**: Plan 假设的 `alertconfig.Evaluator` **A 包里不存在**;import alertconfig 还会拉 alert 子树大量 transitive dep。本 adapter 不 import alertconfig,本地定义 `type Evaluator interface { _phantom() }` 占位,Phase 4 wire-up 写 thin adapter 桥接到 alert.PipelineEvaluator 的 hook。

### 2.6 flow(internal/manager/biz/flow)

**Plan §6.5 假设**: `flow.Node` interface 存在。

**A 实际**:
- [noderegistry.go:50-60](file:///f:/Code/Go/运维/ongrid-new/internal/manager/biz/flow/noderegistry.go#L50-L60) 节点类型抽象是 **`NodeSpec` struct**(非 interface):
  ```go
  type NodeSpec struct {
      Type         string
      Kind         NodeKind
      Category     string
      LabelZh      string
      LabelEn      string
      Ports        []string
      ConfigFields []ConfigFieldSpec
      OutputShape  []string
      Execute      ExecuteFunc
  }
  ```
- [noderegistry.go:33](file:///f:/Code/Go/运维/ongrid-new/internal/manager/biz/flow/noderegistry.go#L33-L33) `ExecuteFunc`: `func(ctx, x Executors, cfg map[string]any, rc *RunContext) (NodeResult, error)`
- [noderegistry.go:65](file:///f:/Code/Go/运维/ongrid-new/internal/manager/biz/flow/noderegistry.go#L65-L73) `RegisterNode(s *NodeSpec)` 是 concrete struct 注册入口,无 interface。

**差异**: Plan 假设的 `flow.Node` interface **A 包里不存在**;真实节点是 `*NodeSpec` struct。本 adapter 不 import flow,本地定义 `type Node interface { _phantom() }` 占位,Phase 4 wire-up 写 thin adapter 把本地 Node 包成 `*flow.NodeSpec`(填 Label/Ports/Kind,Execute 桥接到 invokeRouter)。

---

## 3. 哪些 A 包不能直接 import + fallback 方案

| A 包 | 不能 import 原因 | fallback 方案 |
|---|---|---|
| **internal/pkg/embedding** | 链式拉 `github.com/yalue/onnxruntime_go`(cgo),Windows 编译失败 | 本地定义 `type Embedder interface { Embed(...) }`(省略 Dim);Phase 4 main.go 写 thin adapter 把本地 Embedder 包成 `embedding.Embedder`(补 Dim 字段) |
| **internal/manager/biz/aiops/alertconfig** | `Evaluator` interface 不存在,且 import 此包拉 alert 子树大量 transitive dep | 本地定义 `type Evaluator interface { _phantom() }`(与 pluginhost.Evaluator 同形);Phase 4 main.go 写 thin adapter 桥接到 alert.PipelineEvaluator 的 hook |
| **internal/manager/biz/flow** | `Node` interface 不存在(真实是 `NodeSpec` struct),且 import flow 拉 manager 子树大量 transitive dep | 本地定义 `type Node interface { _phantom() }`(与 pluginhost.Node 同形);Phase 4 main.go 写 thin adapter 把本地 Node 包成 `*flow.NodeSpec`,Execute 桥接到 invokeRouter |
| **internal/pkg/llm** | `Provider` 类型未 export(只有 `Client / ChatReq / ChatResp`),且 import llm 是允许的但本 adapter 不需要 | 本地定义 `type LLMProvider interface { _phantom() }`(与 pluginhost.LLMProvider 同形);Phase 4 main.go 写 thin adapter 把本地 LLMProvider 包成 `llm.Client`(Chat 透传 invokeRouter) |
| **internal/manager/biz/alert** | PipelineEvaluator 是 concrete struct 且 import 拉 alert 子树大量 transitive dep | 同 alertconfig 的 fallback |
| **pluginhost 主包** | **pluginhost/deps.go:30 import embedding**,导致 pluginhost 包在 Windows 上无法编译(链式拉 onnxruntime) | 本地定义 6 个 `local*Registrar` interface,各自带本地类型;Phase 4 main.go 写 thin adapter 同时实现 pluginhost.XXXRegistrar + 本地 local*Registrar,把 pluginhost.XXXRegistrar.Register*(xxx) 内部转发到本地 Register*(对应本地类型) |

### 关于 pluginhost 主包不能 import 的补充说明

**这是本任务最关键的工程决策**:
- Plan §4 红线说"允许 import pluginhost 主包",但 plan §6 的白名单也排除了 embedding。
- pluginhost/deps.go 第 30 行 `import "github.com/ongridio/ongrid/internal/pkg/embedding"`,导致 pluginhost 包本身在 Windows 上无法 build。
- **验证**:
  ```
  $ go build ./internal/pluginhost
  github.com/yalue/onnxruntime_go: build constraints exclude all Go files in ...
  ```
- 因此本 adapter 包不复用 pluginhost.XXXRegistrar,而是本地镜像定义 6 个 `local*Registrar` interface(同形但不同 package,Go 类型系统下不可互换)。
- Phase 4 main.go wire-up 写 thin adapter 同时实现两套接口(典型代码 ~20 行 × 6)。

---

## 4. 共性约束执行情况

| 约束 | 执行 |
|---|---|
| 严禁 import `internal/pkg/embedding` | ✅ adapter 包完全未 import |
| 严禁 import `internal/manager / internal/iam / internal/edgeagent / api` | ✅ adapter 包未 import 任何 manager/iam/edgeagent/api |
| 严禁 import `internal/skill/subprocess.go` | ✅ adapter 包 import `internal/skill` 但**只引用 types.go 和 registry.go 的类型**(skill.Executor / skill.Metadata / skill.Class / skill.Scope / skill.Validate),未使用 subprocess.go 任何导出符号 |
| 允许 import: registry, invoke, manifest, runtime, pluginhost 主包 | ✅ 实际 import: registry, invoke, manifest(runtime 未直接使用,因 `InvokeFunc` 已封装 router.Invoke) |
| 允许 import A 公共包: internal/skill, internal/pkg/notify, internal/pkg/llm | ✅ import: internal/skill + internal/pkg/notify(not import llm,因本地 LLMProvider 占位不需要 llm.Client 类型) |
| `go build ./internal/pluginhost/adapter` 通过 | ✅ 通过(exit 0)+ `go vet` 通过 + `gofmt -l` 无差异 |

---

## 5. Phase 4 main.go wire-up TODO(给 D2)

### 5.1 共性:6 个 thin adapter 同时实现 pluginhost.XXXRegistrar + 本地 local*Registrar

```go
// 例:skillRegistrarBridge 同时实现:
//   - pluginhost.SkillRegistrar { RegisterOne(skill.Executor) error }
//   - adapter.localSkillRegistrar { RegisterOne(skill.Executor) error }
//
// 由于两个 interface 在 Go 类型系统里是**不同类型**(不同 package),即便
// 方法签名一致也不能直接类型转换;main.go 必须显式写两个方法:

type skillRegistrarBridge struct {
    inner pluginhost.SkillRegistrar // A 全局 skill.Registry 包装
}

func (b *skillRegistrarBridge) RegisterOneA(e skill.Executor) error {
    return b.inner.RegisterOne(e)
}
func (b *skillRegistrarBridge) RegisterOne(e skill.Executor) error {
    return b.inner.RegisterOne(e) // 同一实现
}

// 类似 5 个 thin adapter:notify / llm / embedding / evaluator / flow
```

### 5.2 notify(§2.2):main.go 需扩展 A 的 notify.Router 加 RegisterSender,或写 multi-Router-per-Send

A 的 `notify.Router` 无 `RegisterSender` 方法,有两个方案:

**方案 A(推荐)**:在 internal/pkg/notify 包加 ~20 行 `RegisterSender`,不动现有代码:
```go
// notify/notify.go 末尾 append:
func (r *Router) RegisterSender(s Sender) error {
    if s == nil || s.Name() == "" { return errors.New("...") }
    r.mu.Lock(); defer r.mu.Unlock()
    r.channels[s.Name()] = s
    return nil
}
// + r 加 sync.RWMutex 字段
```

**方案 B**:main.go 在 Send path 上每次构造临时 Router(把 plugin sender 拼到内置 sender),与方案 A 改动量相当但 hot path 每次都重建 Router。

### 5.3 embedding(§2.4):main.go 需写 multi-embedder 路由器

A 的 embedding.New(...) 是单例,无 Registry。main.go 写 multi-router:
```go
type multiEmbedder struct {
    providers map[string]adapter.Embedder // name → 本地 Embedder
    defaultName string
}
func (m *multiEmbedder) Dim() int { /* 取 default */ }
func (m *multiEmbedder) Embed(ctx, texts) ([][]float32, error) {
    // 走 defaultName 路由;Phase 5 起按调用方传 name 路由
}
// 同时实现 pluginhost.EmbeddingRegistrar.RegisterEmbedder(name, embedding.Embedder)
// 与 adapter.localEmbeddingRegistrar.RegisterEmbedder(name, adapter.Embedder),
// 内部维护 providers map;plugin Embedder 在 main.go thin adapter 里包成 embedding.Embedder(补 Dim=0 或读 cap.Metadata["dim"])。
```

### 5.4 llm(§2.3):main.go 写 llm.MultiClient sub-client 包装

`llm.MultiClient.NewMultiClient` 接受 `[]ProviderConfig` 然后内部 New sub-client;
main.go 写 custom sub-client:
```go
type pluginLLMSubClient struct { invoke *invoke.Router; pluginID uint64; capName string }
func (c *pluginLLMSubClient) Chat(ctx, req llm.ChatReq) (*llm.ChatResp, error) {
    params, _ := json.Marshal(req)
    resp, err := c.invoke.Invoke(ctx, c.pluginID, c.capName, params, invoke.InvokeOptions{})
    if err != nil { return nil, err }
    var out llm.ChatResp
    json.Unmarshal(resp.Result, &out)
    return &out, nil
}
```
然后调 `multiClient.staticSubs[name] = subClient`。同桥接 pluginhost.LLMRegistrar + adapter.localLLMRegistrar。

### 5.5 evaluator(§2.5):main.go 写 alert.PipelineEvaluator 的 plugin hook

需要先在 internal/manager/biz/alert/pipeline.go 加一个 "custom evaluator" 列表字段 + 调度逻辑(每个 metric / log tick 跑完内置评估后,再跑 plugin evaluator)。改动范围:~30-50 行,改 1 个 A 老文件。

### 5.6 flow(§2.6):main.go 把本地 Node 包成 *flow.NodeSpec

```go
type flowSpecBridge struct {
    plugin *registry.PluginInstance
    cap    *registry.Capability
    invoke InvokeFunc
}
func (b *flowSpecBridge) Spec() *flow.NodeSpec {
    return &flow.NodeSpec{
        Type:     "pluginhost_" + b.cap.Name,
        Kind:     flow.KindAction,
        Category: "pluginhost",
        LabelZh:  b.cap.Name,
        LabelEn:  b.cap.Name,
        Ports:    []string{flow.PortNext},
        Execute: func(ctx, x flow.Executors, cfg, rc) (flow.NodeResult, error) {
            payload := struct{ Cfg map[string]any; RC any }{cfg, rc}
            params, _ := json.Marshal(payload)
            resp, err := b.invoke(ctx, params)
            if err != nil { return flow.NodeResult{}, err }
            var out flow.NodeResult
            json.Unmarshal(resp.Result, &out)
            return out, nil
        },
    }
}
```
然后调 `flow.RegisterNode(spec)`。

---

## 6. 自检:每个 adapter 关键 invariant

| adapter | Skill Key 校验 | Sender Name 唯一 | Embedder/Provider name 唯一 | 注册失败 → log |
|---|---|---|---|---|
| skill | ✅ ad.Metadata().Validate() 前置,失败 skip | n/a | n/a | ✅ slog.Warn |
| notify | n/a | ✅ `"pluginhost:" + pack + ":" + cap` | n/a | ✅ slog.Warn |
| llm | n/a | n/a | ✅ `"pluginhost:" + pack + ":" + cap` | ✅ slog.Warn |
| embedding | n/a | n/a | ✅ `"pluginhost:" + pack + ":" + cap` | ✅ slog.Warn |
| evaluator | n/a | n/a | ✅ `"pluginhost:" + pack + ":" + cap` | ✅ slog.Warn |
| flow | n/a | n/a | ✅ `"pluginhost:" + pack + ":" + cap` | ✅ slog.Warn |

---

## 7. 验证命令

```bash
cd f:\Code\Go\运维\ongrid-new
go build ./internal/pluginhost/adapter      # ✅ exit 0
go vet ./internal/pluginhost/adapter        # ✅ 0 warning
gofmt -l ./internal/pluginhost/adapter      # ✅ 无差异
go list -f '{{range .Imports}}{{.}}\n{{end}}' ./internal/pluginhost/adapter
# 仅依赖:
#   context / encoding/json / errors / fmt / log/slog / time (stdlib)
#   github.com/ongridio/ongrid/internal/pkg/notify
#   github.com/ongridio/ongrid/internal/pluginhost/{invoke,manifest,registry}
#   github.com/ongridio/ongrid/internal/skill
# 严禁项确认:
#   - 0 import internal/pkg/embedding ✅
#   - 0 import internal/pkg/llm ✅ (本地 LLMProvider 占位)
#   - 0 import internal/manager/** ✅
#   - 0 import internal/iam/** ✅
#   - 0 import internal/edgeagent/** ✅
#   - 0 import api/** ✅
#   - 0 import pluginhost (主包) ✅
```

---

## 8. 已知风险与遗留问题

1. **pluginhost 主包无法在 Windows build**(Phase 1 的 deps.go:30 import embedding 的遗留问题)。
   - 影响:任何 import pluginhost 的下游包(adapter / biz / server / hostcall 等)都无法在 Windows 上单独 build。
   - 缓解:本 adapter 包已避开(本地定义 6 个 Registrar interface);但 biz / server / hostcall 在 Phase 2-4 时必须做同样决策 — 即"不 import pluginhost 主包,改 import 本地 mirror interface"。
   - 长期:pluginhost/deps.go 应改为在 EmbeddingRegistrar interface 里用本地占位 Embedder(与 adapter.Embedder 同形但 pluginhost 包自己定义),然后在 Phase 4 main.go 写 thin adapter 把 pluginhost.Embedder ↔ embedding.Embedder 双向桥接。

2. **skill.Register panic on Validate failure** — 本 adapter 已前置 `ad.Metadata().Validate()`,但其它 5 个 adapter 的 contract 没有等价 Validate()(notify/llm/embedding/evaluator/flow 的占位 contract 是空的),所以那几个 adapter 不会 panic。

3. **pluginhost 主包内 Phase 1+2 已 import embedding** — 本 adapter 包不复用 pluginhost.EmbeddingRegistrar 是为了绕过此问题,但意味着 Phase 4 main.go 桥接代码量增加(需要本地 ↔ pluginhost 双向)。

---

**完成时间**: 2026-07-14
**产线**: agent-C1(并发)
**下一步**: D2(Phase 4 main.go wire-up)按 §5 写 6 个 thin adapter 桥接 pluginhost.XXXRegistrar ↔ adapter.local*Registrar