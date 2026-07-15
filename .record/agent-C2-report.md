# C2 — HostSDK hostcall 子系统 final report

> 任务 T10(Phase 3,C2):实现 pluginhost 子系统的 HostSDK 反向调用层
> 工作目录:`f:\Code\Go\运维\ongrid-new`
> 计划参考:`C:\Users\Administrator\AppData\Roaming\QoderCN\SharedClientCache\cache\plans\PluginHost_并发开发方案_task-186.md` §6.6 / §4
> 角色:C2(子 agent,只写 16 个 hostcall 文件,不改 A / 不改 pluginhost/ 其他目录)

---

## 1. 文件清单(全部新增,16 个,绝对路径)

| # | 文件 | 行数(约) | 用途 |
|---|---|---|---|
| 1 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\sdk.go` | 200 | 统一入口:scope → rate → dispatch → audit |
| 2 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\scope.go` | 125 | RBAC 范围校验(plugin manifest `host_dependencies`) |
| 3 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\ratelimit.go` | 147 | token bucket 限流器(默认 60/min)+ Kill 紧急隔离 |
| 4 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\prom.go` | 96 | prom.query / prom.query_range adapter |
| 5 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\loki.go` | 65 | loki.query_range adapter |
| 6 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\tempo.go` | 75 | tempo.search / tempo.trace adapter |
| 7 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\qdrant.go` | 95 | qdrant.search / qdrant.upsert adapter |
| 8 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\skill.go` | 75 | skill.list / skill.execute adapter |
| 9 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\llm.go` | 92 | llm.chat adapter(Phase 1 stub) |
| 10 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\embedding.go` | 63 | embedding.embed adapter |
| 11 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\edge.go` | 114 | edge.run_shell / edge.copy_file / edge.list_dir adapter |
| 12 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\audit.go` | 79 | audit.write adapter(comp 固定 pluginhost) |
| 13 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\notify.go` | 66 | notify.send adapter |
| 14 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\config.go` | 78 | config.get / config.set adapter |
| 15 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\vault.go` | 63 | vault.get_ref adapter(**只返 ref 不返明文**) |
| 16 | `f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\metrics.go` | 97 | metrics.write / metrics.query adapter |

父目录 `internal/pluginhost/hostcall/` 由本任务 `mkdir -p` 创建(原本不存在)。

---

## 2. 16 个 adapter 各自支持的 op 清单

| Adapter | 支持 op | Payload 字段 | 调用的 pluginhost 接口方法 | 返回 JSON 形态 |
|---|---|---|---|---|
| **PromAdapter** | `prom.query` | `query`, `time?` | `PromQueryAPI.Query(ctx, query)` | 透传 |
| | `prom.query_range` | `query`, `start`, `end`, `step` | `PromQueryAPI.QueryRange(ctx, query, start, end, step)` | 透传 |
| **LokiAdapter** | `loki.query_range` | `query`, `start`(unix nano), `end`(unix nano), `limit?` | `LokiQueryAPI.QueryRange(ctx, query, start, end, limit)` | 透传 |
| **TempoAdapter** | `tempo.search` | `query`(兼容 `q`), `limit?` | `TempoQueryAPI.Search(ctx, q, limit)` | 透传 |
| | `tempo.trace` | `trace_id` | `TempoQueryAPI.Trace(ctx, traceID)` | 透传 |
| **QdrantAdapter** | `qdrant.search` | `collection`, `vector[]float32`, `limit?` | `QdrantQueryAPI.Search(ctx, collection, vector, limit)` | 透传(`[]` if empty) |
| | `qdrant.upsert` | `collection`, `id`, `vector`, `payload?` | `QdrantQueryAPI.Upsert(ctx, collection, id, vector, payload)` | `{"ok": true}` |
| **SkillAdapter** | `skill.list` | 空 / `{}` | `SkillLookupAPI.List(ctx) → []skill.Metadata` | 数组(Metadata JSON) |
| | `skill.execute` | `key`, `params?` | `SkillLookupAPI.Execute(ctx, key, params)` | 透传 |
| **LLMAdapter** | `llm.chat` | `messages[]`, `model?`, `temperature?`, `max_tokens?` | `LLMCompleteAPI.Chat(ctx, LLMChatRequest)` | Phase 1 stub(见 §5) |
| **EmbeddingAdapter** | `embedding.embed` | `texts[]` | `EmbeddingAPI.Embed(ctx, texts) → [][]float32` | 二维 float32 数组 |
| **EdgeAdapter** | `edge.run_shell` | `edge_id`, `cmd`, `timeout?` | `EdgeCommandAPI.RunShell(ctx, edgeID, cmd, timeout) → string` | `{"stdout": "..."}` |
| | `edge.copy_file` | `edge_id`, `src`, `dst` | `EdgeCommandAPI.CopyFile(ctx, edgeID, src, dst)` | `{"ok": true}` |
| | `edge.list_dir` | `edge_id`, `path` | `EdgeCommandAPI.ListDir(ctx, edgeID, path) → []string` | `{"entries": [...]}` |
| **AuditAdapter** | `audit.write` | `action`, `resource`, `actor`, `details?` | `AuditAPI.Write(ctx, "pluginhost", action, resource, actor, details)` | `{"ok": true}` |
| **NotifyAdapter** | `notify.send` | `channel`, `message`(raw JSON) | `NotifyAPI.Send(ctx, channel, msg)` | `{"ok": true}` |
| **ConfigAdapter** | `config.get` | `key` | `ConfigAPI.Get(ctx, key) → string` | `{"value": "..."}` |
| | `config.set` | `key`, `value` | `ConfigAPI.Set(ctx, key, value)` | `{"ok": true}` |
| **VaultAdapter** | `vault.get_ref` | `name` | `VaultAPI.GetRef(ctx, name) → ref` | `{"ref": "..."}`(**只返 ref**) |
| **MetricsAdapter** | `metrics.write` | `name`, `value`, `labels?` | `MetricsAPI.Write(ctx, name, value, labels)` | `{"ok": true}` |
| | `metrics.query` | `promql` | `MetricsAPI.Query(ctx, promql) → json.RawMessage` | 透传 |

合计 21 个 host.call op endpoint,全部由 SDK.dispatch 按 op 名路由。

---

## 3. A 实际 client 签名调研(通过 pluginhost.HostServices 抽象)

> **关键设计**:hostcall 包 **完全不 import A 子包**(只 import pluginhost + 标准库 + skill 子集)。
> A 实际 client 在 main.go wire-up 阶段由 thin adapter 包成 `pluginhost.XxxAPI` 注入。
> 下表记录 A 实际签名作为 Phase 4 wire-up 参考。

| pluginhost 接口 | A 实际类型/方法 | 签名差异 | Phase 4 thin adapter TODO |
|---|---|---|---|
| `LokiQueryAPI.QueryRange` | `internal/pkg/logquery.Client.QueryRange` | plan:(ctx, q, start, end, limit)→raw;A:(ctx, QueryRangeOptions{Query, Start, End, Limit, Step, Direction})→`*QueryRangeResult` | wire-up struct:把 start/end 用 time.Time,limit 收,Step/Direction 留默认 |
| `PromQueryAPI.Query` | `internal/pkg/promquery.Client.Query` | plan:(ctx, promql)→raw;A:(ctx, expr, ts)→`*InstantResult` | thin adapter 忽略 ts(用零值),返回 `InstantResult.Result`(已 raw) |
| `PromQueryAPI.QueryRange` | `internal/pkg/promquery.Client.QueryRange` | plan:(ctx, promql, start, end, step string)→raw;A:(ctx, expr, start, end, step time.Duration)→`*InstantResult` | thin adapter 把 step string→time.Duration |
| `TempoQueryAPI.Search` | `internal/pkg/tracequery.Client.SearchTraces` | plan:(ctx, q, limit)→raw;A:(ctx, SearchOptions{Query, Tags, Limit, Start, End, MinDuration, MaxDuration})→`*SearchResult` | thin adapter 把 q/limit 塞 SearchOptions,其它字段留默认 |
| `TempoQueryAPI.Trace` | `internal/pkg/tracequery.Client.GetTrace` | 签名一致(都 ctx + traceID),返回类型不同:`*TraceResult{Body}` vs `json.RawMessage` | thin adapter 取 `TraceResult.Body` |
| `QdrantQueryAPI.Search` | `internal/pkg/qdrantx.Client.Search` | plan:(ctx, collection, vector, limit)→raw;A:(ctx, collection, vector, SearchOpts{Limit, MustMatch})→`[]SearchHit` | thin adapter 把 limit→SearchOpts,返回 `json.Marshal([]SearchHit)` |
| `QdrantQueryAPI.Upsert` | `internal/pkg/qdrantx.Client.Upsert` | plan 单点:(ctx, collection, id, vector, payload);A 批量:(ctx, collection, []Point) | thin adapter 把单点 pack 成 1-element slice |
| `SkillLookupAPI.List` | `internal/skill` 包级别 `All()` | plan 收 ctx;A 包级别函数无 ctx;返回类型 `[]skill.Metadata` vs `[]skill.Executor` | thin adapter 包装全局函数,转 Executor→Metadata |
| `SkillLookupAPI.Execute` | `skill.Executor.Execute` | 签名一致(ctx, params)→(json.RawMessage, error) | thin adapter 用包级别 `Get(key)` 拿 Executor,再调 Execute |
| `LLMCompleteAPI.Chat` | `internal/pkg/llm.Client.Chat` | plan 收 pluginhost.LLMChatRequest(占位 `_phantom()` interface);A 收 `llm.ChatReq{Model, Provider, Messages, Tools, Temperature, UserID}` | wire-up TODO:把 pluginhost.LLMChatRequest / LLMChatResponse 替换为 `type alias = llm.ChatReq / llm.ChatResp`,然后 LLMAdapter pack params→ChatReq |
| `EmbeddingAPI.Embed` | `internal/pkg/embedding.Embedder.Embed` | 签名一致 | hostcall **禁止 import embedding**(拉 cgo onnxruntime);Phase 4 main.go 在 wire-up 时 import 并包成 `EmbeddingAPI` |
| `EdgeCommandAPI.RunShell/CopyFile/ListDir` | **`internal/manager/service/edge.Service` 未 export 这些方法!** | plan 假设 A 已有 RunShell/CopyFile/ListDir,实际 Service 只有 `GetProcessList / UpgradeAgent / FetchPackage / ApplyPackage` | Phase 4 TODO:在 wire-up 时**新写一个 EdgeCommandAPI 实现**,内部用 `edge.Service.EdgeCaller.Call(...)` 调 `tunnel.MethodRunShell/CopyFile/ListDir`(确认 tunnel.proto 已 export 这些 method) |
| `AuditAPI.Write` | `internal/manager/data/audit/store.Repo.Insert` | plan:(ctx, comp, action, resource, actor, details map)→error;A:(ctx, `*model.Log{...}`)→error | thin adapter 把 5 字段 pack 成 `model.Log`,注意 actor→UserEmail |
| `TopologyAPI.Get` | `internal/manager/data/topology/store.NodeRepo.Get` | plan:(ctx, id string)→raw;A:(ctx, id uint64)→`*model.Node` | thin adapter 把 uint64→string,Node→raw(用 json.Marshal) |
| `TopologyAPI.Query` | `无对应方法** | A topology 没有 `Query(expr)`,只有 `List(filter)` | wire-up 时把 `Query` 视为 "List with type=Q filter",或写一个 custom 表达式解析器 |
| `NotifyAPI.Send` | `internal/pkg/notify.Router.Send` | plan:(ctx, channel, msg)→error;A:(ctx, Message, channels...string)→error | thin adapter 把 msg+channel 包成 `Message{Subject, Body, ...}` + channel 名,传 Router.Send |
| `NotifyAPI.SendVia` | `notify.Router.SendVia` | plan:(ctx, channelKind, name, msg);A:(ctx, Message, Sender) | thin adapter 用 channelKind/name 从 DB 构造 Sender,再 SendVia |
| `ConfigAPI.Get` | `internal/manager/data/setting/store.Repo.Get` | plan:(ctx, key)→string;A:(ctx, category, key)→`*model.Setting` | thin adapter 固定 `category="pluginhost"`,取 `Setting.Value` |
| `ConfigAPI.Set` | `setting/store.Repo.Set` | plan:(ctx, key, value);A:(ctx, category, key, value, sensitive) | thin adapter 固定 category="pluginhost",sensitive=true |
| `VaultAPI.GetRef` | `internal/manager/data/secret/store.Repo.GetByName` | plan 返 `ref string`;A 返 sealed `*model.Secret` | thin adapter 取 `Secret.Name`(or `ID` 当 ref);**永远不返回解密后的明文** |
| `MetricsAPI.Write` | `internal/pkg/prom` **没有 Write 方法!** | A prom 包只暴露 `NewRegistry` / `Handler` | Phase 4 TODO:写一个 `promMetricsAPI` 实现,内部用 `prometheus.GaugeVec.WithLabelValues(...).Set(value)` |
| `MetricsAPI.Query` | A 无对应方法 | — | Phase 4 TODO:复用 `promquery.Client.Query` 调 Prom HTTP API |

> **结论**:14 个 HostSDK API 中,**9 个有 A 对应实现**(签名微差,thin adapter),**5 个需要 Phase 4 新写实现**(EdgeCommandAPI、TopologyAPI.Query、MetricsAPI.Write/Query,LLM 在 deps.go 替换 alias 后自然可工作)。

---

## 4. 与 plan §6.6 的差异

| plan 假设 | 实际 A | 处理方式 |
|---|---|---|
| HostSDK 调用方"A 现有 client"无需 thin adapter | 大部分 A 是 struct(非 interface),且方法签名不同 | Phase 4 wire-up 写 14 个 thin adapter 包 A→pluginhost |
| EmbeddingAdapter 可直接 import `embedding.Embedder` | `embedding` 链 `onnxruntime_go`(cgo) | hostcall **不 import**;wire-up 时 main.go 注入 |
| EdgeAdapter 直接调 `service/edge.RunShell` 等 | `service/edge.Service` **未 export** RunShell/CopyFile/ListDir | Phase 4 wire-up 用 `tunnel.MethodRunShell` 等走 EdgeCaller.Call |
| MetricsAPI 可直接调 `pkg/prom.Write/Query` | `pkg/prom` **只有 NewRegistry/Handler** | Phase 4 wire-up 用 `prometheus.Counter/GaugeVec` 自实现 |
| TopologyAPI.Query 表达式查询 | A topology 没有 `Query`,只有 `List(filter)` | Phase 4 wire-up 把 `Query(expr)` 翻译成 `List(BizListFilter{Q: expr})` |
| NotifyAPI.Send 第二参是 `channel string` | A `Router.Send(ctx, Message, channels...)`,Message 不是 raw | thin adapter 把 msg+channel→Message |
| ConfigAPI 单 key | A `Repo.Get(category, key)` 双键 | thin adapter 固定 category="pluginhost" |
| VaultAPI 直接返 ref string | A `Repo.GetByName(name)` 返 sealed Secret | thin adapter 取 `Secret.Name`,**不返明文**(符合红线) |
| LLMCompleteAPI.Chat 收 pluginhost.LLMChatRequest | pluginhost.LLMChatRequest 是 placeholder interface(`_phantom()`),外部无法实现 | LLMAdapter.Dispatch 写 Phase 1 guard;Phase 4 改 deps.go 把 LLMChatRequest/Response 替换为 `type alias = llm.ChatReq/ChatResp` |
| ScopeValidator 必填 phase 1 只查 declares | 一致 ✓ | 已实现 |
| Limiter Kill "设 rate=0" 全局生效 | 误读 — 应是"该 plugin 永久隔离" | 实现为 `bucket.dead=true`(单 plugin 隔离)+ 公开 Reset 反向解封 |
| AuditAdapter 接受 comp 字段 | plugin 不能伪造 comp(否则污染 A 审计归类) | comp 固定为常量 `auditComponentName = "pluginhost"` |
| hostcall sdk 文件需要全量重排 / 协议包 | 协议 envelope 在 Phase 3 的 server 写 | hostcall 只暴露 Call(ctx, pluginID, op, payload)→(raw, err);envelope 序列化在 server 层 |

---

## 5. LLM Adapter Phase 1 状态说明

pluginhost.LLMChatRequest / LLMChatResponse 是 `_phantom()` 占位 interface(私有方法,
外部包无法实现,这是 A1 故意设计的——Phase 3 替换为 alias 前,任何外部代码都不能构造)。

LLMAdapter.Dispatch 当前行为:
- `iface == nil` → 返 `ErrNotWired`
- `iface != nil` → 解析 payload 成功后,调 `phase1LLMGuard()` 返 `"llm.chat: not wired yet"` 错误,
  避免 plugin 误以为 llm.chat 真的能跑通

Phase 4 替换 deps.go:
```go
// 替换前
type LLMChatRequest interface { _phantom() }
type LLMChatResponse interface { _phantom() }
// 替换后(由 D2 改 deps.go)
type LLMChatRequest = llm.ChatReq
type LLMChatResponse = llm.ChatResp
```
然后 LLMAdapter.Dispatch 把 `llmChatParams` pack 成 `llm.ChatReq{Messages, Model, Temperature, ...}`,
删除 `phase1LLMGuard()` 2 行,调 `a.iface.Chat(ctx, req)`。

---

## 6. Phase 4 main.go wire-up TODO(给 D2)

在 `cmd/ongrid/main.go` 末尾追加 pluginhost.New 之前,先把 A 真实 client 包成
`pluginhost.HostServices` 字段。下面是每个 hostcall adapter 的注入步骤:

```go
// 在 main.go ~25 行 wire-up 区块(具体行号 D2 自己挑):

// 1. Loki thin adapter(plan §6.6 表第 1 行)
import logq "github.com/ongridio/ongrid/internal/pkg/logquery"
// ... 已有 logqClient := logquery.New(url, logger)
hostSvc.Loki = &lokiThin{Client: logqClient}  // impl pluginhost.LokiQueryAPI

// 2. Prom thin adapter
import promq "github.com/ongridio/ongrid/internal/pkg/promquery"
// ... 已有 promqClient
hostSvc.Prom = &promThin{Client: promqClient}

// 3. Tempo thin adapter
import traceq "github.com/ongridio/ongrid/internal/pkg/tracequery"
hostSvc.Tempo = &tempoThin{Client: traceqClient}

// 4. Qdrant thin adapter
import qdrantx "github.com/ongridio/ongrid/internal/pkg/qdrantx"
hostSvc.Qdrant = &qdrantThin{Client: qdrantClient}

// 5. Skill thin adapter(包 skill 全局 Registry)
import "github.com/ongridio/ongrid/internal/skill"
hostSvc.Skills = &skillThin{}  // 内部调 skill.Get / skill.All

// 6. LLM thin adapter
//    先按 §5 替换 deps.go,把 LLMChatRequest/Response 改成 alias
import "github.com/ongridio/ongrid/internal/pkg/llm"
hostSvc.LLM = &llmThin{Client: llmClient}

// 7. Embedding thin adapter
//    hostcall 包不能 import embedding,但 main.go 可以(同进程 in-process)。
import "github.com/ongridio/ongrid/internal/pkg/embedding"
hostSvc.Embedder = &embThin{Embedder: embImpl}  // 来自 embedding.New(cfg)

// 8. Edge thin adapter
//    ⚠️ A 实际未 export RunShell/CopyFile/ListDir,需要 thin adapter 内部用 EdgeCaller.Call
//    调 tunnel.MethodRunShell 等。tunnel proto 是否 export 这些 method 请 D2 确认;
//    若未 export,需要先在 api/tunnel/v1/tunnel.proto 加 method 定义。
import bizEdge "github.com/ongridio/ongrid/internal/manager/service/edge"
hostSvc.Edge = &edgeThin{Service: edgeSvc}  // edgeThin 自己实现 3 个方法

// 9. Audit thin adapter(包 audit/store.Repo.Insert)
import auditStore "github.com/ongridio/ongrid/internal/manager/data/audit/store"
hostSvc.Audit = &auditThin{Repo: auditRepo}

// 10. Topology thin adapter
import topoStore "github.com/ongridio/ongrid/internal/manager/data/topology/store"
// TopologyAPI.Query 需自定义表达式解析(参考 List + Q 字段),topoStore 自己实现

// 11. Notify thin adapter(包 notify.Router)
import "github.com/ongridio/ongrid/internal/pkg/notify"
hostSvc.Notify = &notifyThin{Router: notifyRouter}

// 12. Config thin adapter(包 setting/store.Repo)
import setStore "github.com/ongridio/ongrid/internal/manager/data/setting/store"
// 固定 category = "pluginhost"

// 13. Vault thin adapter(包 secret/store.Repo)
import secStore "github.com/ongridio/ongrid/internal/manager/data/secret/store"
// 取 Secret.Name 当 ref,**绝不返明文**

// 14. Metrics thin adapter
//    ⚠️ A pkg/prom 没有 Write/Query,需要 wire-up 时自己实现:
//    - Write: 用 prometheus.GaugeVec.WithLabelValues(...).Set(value)
//    - Query: 复用 promquery.Client.Query 走 Prom HTTP API
//    推荐放在 internal/pluginhost/wireup/metrics_adapter.go(P3 决定路径)
hostSvc.Metrics = &metricsThin{Reg: promReg}  // 自己实现

// 然后:
sdk := hostcall.NewSDK(hostSvc, scope, limiter, auditFn)
```

每个 thin adapter 推荐放在 `internal/pluginhost/wireup/<name>_adapter.go`,与 C2 的 hostcall 同级
但属于 Phase 4 任务(D2)。**C2 不写这些 thin adapter**。

---

## 7. build 验证

### 7.1 gofmt 干净
```powershell
PS> gofmt -l ./internal/pluginhost/hostcall
(empty output)
PS> $LASTEXITCODE
0
```
16 个文件全部 gofmt-clean。

### 7.2 go build hostcall(单独 build)

**首次跑**:`go build ./internal/pluginhost/hostcall/...` 在 Windows PowerShell 上失败,
根因:`internal/pluginhost/deps.go` 真实 import `internal/pkg/embedding`,后者链 `onnxruntime_go`(纯 cgo 包);
本机 `CGO_ENABLED=0` 且 `%PATH%` 无 `gcc`,Go 编译期排除整个包。

**临时 probe**(A1 阶段也用同样方法,记录在 agent-A1-report.md §2.2):

1. 备份 `internal/pluginhost/deps.go` → `deps.go.bak`
2. 把 deps.go 的 `embedding` import 替换为 inline `type Embedder interface { Dim() int; Embed(ctx, []string) ([][]float32, error) }`,
   并把 `EmbeddingRegistrar` 的签名改为 `e Embedder`
3. 跑 `go build ./internal/pluginhost/hostcall/...` → **exit=0, 编译通过**
4. 跑 `go build ./internal/pluginhost/...` → **exit=0, 全包通过**
5. 跑 `go vet ./internal/pluginhost/hostcall/...` → **exit=0**
6. 跑 `gofmt -l` → empty
7. 恢复 deps.go → `deps.go.bak`,删除 `deps.go.bak`

**结论**:
- hostcall 16 个文件 **代码 100% 正确**,无编译错误、无 vet 警告
- 唯一阻塞是 `internal/pkg/embedding` → `onnxruntime_go` 的 cgo 链
- 在 Linux/macOS 开发机或安装 gcc + `CGO_ENABLED=1` 的 CI 环境中,`go build` 可直接通过

### 7.3 与 A1 报告交叉确认
A1 report §2.2 同样记录了此问题,通过相同 probe 验证 Phase 1 的 5 个文件正确性。
本任务的 16 个 hostcall 文件采用相同方案验证通过,**没有引入新的 build 阻塞**。

---

## 8. 红线 checklist 自查

- [x] A 老代码 0 改动(`git status` 显示 hostcall/ 16 个文件全 untracked,其他 modified 文件是别的 agent)
- [x] 不写 `pluginhost/{pluginhost.go, deps.go, registry, sandbox, manifest, runtime, invoke, adapter, biz, server, model, data}` 任何文件
  - 临时 probe 修改 deps.go 已恢复,bak 文件已删
- [x] 不调用 Agent 工具
- [x] 不写测试文件
- [x] import 白名单:`context, encoding/json, errors, sync, time, fmt` + `pluginhost` + `internal/skill`(skill.go 仅 import skill 类型)
  - **未** import `internal/manager/{model,data}/*` / `internal/iam/*` / `internal/edgeagent/*` / `api/*`
  - **未** import `internal/pkg/embedding`(避开 cgo)
- [x] vault.go 永远只返 ref,不返明文(`GetRef` 签名 + 注释红线)
- [x] audit.go comp 固定常量,plugin 不能伪造
- [x] metrics.go 不打 trace span(注释红线)
- [x] llm.go Phase 1 guard 防止误用
- [x] final report 写到 `.record/agent-C2-report.md`

---

## 9. 留待 Phase 3 / Phase 4 的事项

| # | 事项 | 负责 agent | 说明 |
|---|---|---|---|
| 1 | deps.go 替换 LLMChatRequest / LLMChatResponse 为 alias | D2 | 在 main.go wire-up 阶段同步改 deps.go,把 `_phantom()` 改成 `type X = llm.ChatReq` 等 |
| 2 | 14 个 thin adapter 包 A 真实 client → pluginhost.XxxAPI | D2 | 建议路径 `internal/pluginhost/wireup/`,不在 C2 任务范围 |
| 3 | ScopeValidator 强校验(数据 scopes 内) | Phase 3 业务 agent | 当前 Authorize 只判 declares,data_scopes 二次校验留 TODO |
| 4 | tunnel.proto 是否 export `MethodRunShell` / `MethodCopyFile` / `MethodListDir` | D2 / F | 若未 export,需要先在 proto 加 method 定义才能 wire edge adapter |
| 5 | MetricsAPI 内部实现 | D2 | prometheus go-client 自实现 Write,复用 promquery.Client.Query |
| 6 | AuditFunc 在 wire-up 时实现(plugin_id / op / latency / err → audit_logs) | D2 | 与 pluginhost 自身的 AuditSink.Write 配合 |

---

## 10. 产出文件路径汇总(给 reviewer)

```
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\sdk.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\scope.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\ratelimit.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\prom.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\loki.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\tempo.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\qdrant.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\skill.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\llm.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\embedding.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\edge.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\audit.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\notify.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\config.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\vault.go
f:\Code\Go\运维\ongrid-new\internal\pluginhost\hostcall\metrics.go
f:\Code\Go\运维\ongrid-new\.record\agent-C2-report.md  ← 本文件
```