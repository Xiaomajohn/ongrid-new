# Agent A3 Final Report — pluginhost/runtime (transport 抽象 + subprocess + httpremote)

> 范围:`internal/pluginhost/runtime/{runtime,subprocess,httpremote}.go`
> 验证:`go build ./internal/pluginhost/...` ✅  `go vet ./internal/pluginhost/runtime/...` ✅
> A 老文件改动:0

---

## 1. 文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| [runtime.go](file://f:/Code/Go/运维/ongrid-new/internal/pluginhost/runtime/runtime.go) | 172 | 公共类型(Request/Response/Caller/PluginInstance/Capability/Runtime)+ Pool 注册表 |
| [subprocess.go](file://f:/Code/Go/运维/ongrid-new/internal/pluginhost/runtime/subprocess.go) | 289 | stdio JSON-RPC 子进程 transport;30s 默认超时;panic recover;stderr 持续收集 |
| [httpremote.go](file://f:/Code/Go/运维/ongrid-new/internal/pluginhost/runtime/httpremote.go) | 247 | HTTP remote transport + HMAC-SHA256 签名 + CircuitBreaker(5/30s) |

**import 白名单审计**(grep 已确认):
- 0 引用 `internal/manager/*`、`internal/iam/*`、`internal/edgeagent/*`、`internal/pkg/*`、`internal/skill/*`、`api/*`
- 仅标准库:`bytes`、`bufio`、`context`、`crypto/hmac`、`crypto/sha256`、`encoding/hex`、`encoding/json`、`errors`、`fmt`、`io`、`net/http`、`os/exec`、`sync`、`time`

**包别名提醒**:本包与 stdlib `runtime` 同名,引用方需用别名:
```go
import rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"
```

---

## 2. stdio 协议(JSON-RPC envelope 格式)

**传输**:一行一帧 `\n` 分隔的 UTF-8 JSON,长生命周期 stdin/stdout 双向流。

### 2.1 请求帧(B → C,写到 stdin)

```json
{
  "id":     "uuid-or-trace-id",   // 必填,响应匹配用
  "cap":    "issue.create",        // 必填,capability 名
  "params": { ... }                // 可选,任意 JSON 对象(omitempty)
}
```
> `params` 用 `json.RawMessage` 透传,transport 层不解析。

### 2.2 响应帧(C → B,从 stdout 读)

```json
{
  "id":     "uuid-or-trace-id",   // 必须匹配请求 id
  "result": { ... },               // 成功时非空
  "error":  "human-readable msg"   // 失败时非空
}
```

### 2.3 多路复用 / 流控

- 单 SubprocessRuntime 不保证并发 Invoke 安全(stdio 顺序流,响应行可能与并发请求交错)。读循环会跳过 `id` 不匹配的行,直到命中或 EOF。
- 串行化同一 pluginID 的调用由 Phase 4 的 [InvokeRouter](file://f:/Code/Go/运维/ongrid-new/internal/pluginhost/invoke) 负责。

### 2.4 进程生命周期

| 触发 | 行为 |
|---|---|
| 首次 Invoke | `exec.CommandContext(ctx, Plugin.Entry)` + 接管三管道 |
| 进程退出后再次 Invoke | `ProcessState != nil` 检测到,自动重启(清空旧 stderr buffer) |
| `Close()` | `Process.Kill()` + `cmd.Wait()` + 关 stdin |
| ctx 超时 | kill 子进程 + 写 stderr 警告 + 返 `ErrTimeout` |

### 2.5 stderr 收集

后台 goroutine `drainStderr` 持续把 stderr 读入 `s.stderr` bytes.Buffer(4096B/次)。
- 在 `Invoke` panic / timeout / crashed 时把 stderr 快照拼到错误日志,供 Phase 2 的 audit 落库。
- Close 时 Kill 触发 stderr EOF,goroutine 自然退出。

---

## 3. HMAC 协议(HTTP remote 鉴权)

### 3.1 端点

```
POST {URL}/invoke
Content-Type: application/json
```

### 3.2 请求体(同 stdio envelope)

```json
{
  "id":     "uuid-or-trace-id",
  "cap":    "issue.create",
  "params": { ... }
}
```

### 3.3 必需 headers

| Header | 值 |
|---|---|
| `Content-Type` | `application/json` |
| `X-Plugin-ID` | `plugin.PackID`(插件全局唯一标识) |
| `X-Plugin-Signature` | `hex(HMAC-SHA256(Secret, body))` |
| `X-Request-ID` | 与 body 中 `id` 字段一致(冗余便于服务端日志关联) |

### 3.4 签名计算(Go 示例)

```go
mac := hmac.New(sha256.New, []byte(Secret))
mac.Write(body)               // body = json.Marshal(envelope)
sig := hex.EncodeToString(mac.Sum(nil))
```
- `Secret` 由 ongrid manager 注入到 `HTTPRemoteRuntime.Secret`(从 pluginhost.install_secret 列派生,Phase 2 data 层实现)。
- 远端 C 插件侧用同样的 secret 校验,失败返 401/403(4xx,不计入熔断)。

### 3.5 状态码处理

| 状态码 | 处理 | 是否计入熔断 |
|---|---|---|
| 2xx | 解析 `result` / `error`,返 `Response` | ✅ 成功 → `RecordSuccess` |
| 4xx | 包装 `fmt.Errorf("http runtime: status %d: ...")` | ❌ 客户端错误,不计 |
| 5xx | 返 `ErrRemote5xx` | ✅ `RecordFailure` |
| 网络错误 / ctx 超时 | `ErrTimeout`(若 `ctx.Err() == DeadlineExceeded`) | ✅ `RecordFailure` |

---

## 4. 错误码清单

| 错误 | 来自 | 触发条件 |
|---|---|---|
| `ErrDuplicate` | Pool | Add 时 pluginID 已存在 |
| `ErrNotFound` | Pool | 预留(Phase 4 router 用) |
| `ErrTimeout` | Subprocess / HTTPRemote | 超过 s.Timeout / ctx deadline / http.Client.Timeout |
| `ErrSubprocessCrashed` | Subprocess | 子进程在响应到达前已退出(ProcessState 已设置) |
| `ErrSubprocessPanic` | Subprocess | Invoke 内 panic 被 recover |
| `ErrCircuitOpen` | HTTPRemote | CircuitBreaker 处于 Open 状态 |
| `ErrRemote5xx` | HTTPRemote | 远端返 5xx |

> 调用方判断错误的两种姿势:
> ```go
> if errors.Is(err, rtp.ErrTimeout) { ... }       // 哨兵错误
> var hErr *rtp.HTTPRemoteRuntime                  // 包装错误用 errors.As 解析(Phase 4 再加)
> ```

---

## 5. Pool 接口

```go
rtp.NewPool()                              // 构造空池
rtp.Pool.Add(pluginID, runtime)            // 注入,pluginID 重复返 ErrDuplicate
rtp.Pool.Get(pluginID) (Runtime, bool)     // 读取,不存在返 (nil, false)
rtp.Pool.Remove(pluginID)                  // 摘除 + Close 底层 Runtime
rtp.Pool.CloseAll() error                  // 进程退出时一次性回收所有子进程
rtp.Pool.Count() int                       // 健康检查 / metrics
```

---

## 6. 已知 TODO(留给 Phase 4 invoke/router 集成)

1. **InvokeRouter 串行化**:同一 pluginID 的并发 Invoke 由 router 层用 `chan` / singleflight 串行化,避免 stdio 响应行错配。
2. **Pool.Get 失败语义**:当前 Get 不存在返 (nil,false);router 需要返 `ErrNotFound` 给上层。需扩展 `GetE` 或在 router 里包一层。
3. **stderr → audit 落库**:本层只把 stderr 收集在 buffer,Phase 4 invoke/router 完成后由 biz/invoke 把 buffer 内容写到 `plugin_audits.DetailsJSON`(`comp="pluginhost"`,`action="invoke"`)。
4. **CircuitBreaker 半开探测失败重开**:当前实现 Open 期刚过期 → 探测 1 次;探测成功 `RecordSuccess` 关闭;探测失败 `RecordFailure` 重新打开 openDuration。Phase 4 可调参(阈值 / 打开时长由 plugin 元数据驱动)。
5. **超时 vs ctx cancel 区分**:`ErrTimeout` 当前覆盖"超过 s.Timeout / ctx deadline / http.Client.Timeout"三种场景。Phase 4 可拆成 `ErrPluginTimeout` vs `ErrCallerCanceled` 以便 metric 区分。
6. **host.call 反向通道**:plan §2 信道 2 描述的"C 插件反向往 B 调 host.call(prom/loki/...)" — 当前 stdio 协议只支持 B→C 单向 envelope,Phase 3 的 hostcall SDK 需要在 stdio 上扩展"server-push notification"或独立开一条 outbound 通道(待 Phase 3 选型)。
7. **HMAC 密钥轮换**:当前 Secret 是单值;Phase 2/3 应支持 plugin 多版本 secret(老 secret 留 N 分钟宽限)。
8. **stdio 行长度上限**:bufio.Reader 默认 buffer 64KB,超大响应行会卡 ReadString。Phase 4 视实际 plugin 调整 bufio.NewReaderSize。

---

## 7. 构建产物

```
$ cd f:\Code\Go\运维\ongrid-new
$ go build ./internal/pluginhost/...
(no output, exit 0)

$ go vet ./internal/pluginhost/runtime/...
(no output, exit 0)
```

完整 pluginhost 树(本次新增标 +):
```
internal/pluginhost/
└── runtime/
    +── runtime.go       (公共类型 + Pool)
    +── subprocess.go    (stdio 子进程 transport)
    +── httpremote.go    (HTTP + HMAC + CircuitBreaker)
```
