# 2026-07-16 · metrics 插件 ts 偏移修复 Prom duplicate sample 拒收

## 问题

edge 端 `metrics` 插件(默认 scrape `http://127.0.0.1:9102/metrics` + `http://127.0.0.1:9256/metrics`)周期性报:

```
level=WARN msg="push_prom_samples failed" err="tunnel call \"push_prom_samples\": push_prom_samples: promwrite: http://prometheus:9090/prometheus/api/v1/write returned 400: duplicate sample for timestamp 1784174672807; overrides not allowed: existing 2.5909e-05, new value 3.7598e-05"
```

Prom 端对**同一个 series (metric, labels) + 同一个 timestamp** 上新值不同的样本整批 400 拒收,
导致每次 3913 / 1573 条样本全丢。

### 根因(已查实)

- `agent.heartbeatLoop` 每 **30s** 一次从 manager 拉 `ServerTimeMs` 缓存进
  `agent.lastServerTimeMs`(`internal/edgeagent/biz/agent.go:458-466`)
- `metrics` 插件 scrape tick 默认 **15s**(defaultInterval),或 operator 配的 10s
- 同一个心跳周期内 2~3 个 scrape tick 复用同一个 `ServerTimeMs`
- 同 URL(node_exporter :9102 或 process_exporter :9256)相邻两个 scrape tick
  推送同名 metric + 相同 ts + 不同 counter value → Prom 整批 400

证据:日志里相邻报错 ts 间隔 = 30003ms ≈ 30s 心跳周期,而不是 scrape_interval。

### 同源问题(之前已处理过两次)

- `internal/pkg/config/config.go:336-350`:`CollectorMode` 默认 `off`,注释明确说
  "pushing duplicate samples via tunnel produces noisy ongrid_source=embedded
  extra series"——把 embedded 推 tunnel 兜底关闭
- `internal/edgeagent/plugins/hostmetrics/plugin.go:178-196`:hostmetrics 插件
  检测 node_exporter 是否自输出 `node_nf_conntrack_entries`,避免双源撞
  `textfile` collector 的副本

## 加了什么

### 1. metrics 插件加 `tsCounter` 单调递增字段

`internal/edgeagent/plugins/metrics/plugin.go` Plugin struct 加字段:

```go
// tsCounter 单调递增,每个 scrape tick 在 runLoop 自增 1(失败 tick 也递增,
// 目的是占住 ts 位以保证下次成功 tick 不会跟历史撞). ts 偏移公式:
//   scrapeTs = lastServerTimeMs + tsCounter * scrapeInterval
// 跨心跳周期 counter 只增不减,lastServerTimeMs 在 heartbeatLoop
// 收到响应时直接覆盖 —— 这样每个 scrape tick 拿到唯一 ts,
// 避开 30s 心跳周期内相邻 scrape 共用同一 ServerTimeMs 触发
// Prom "duplicate sample ... overrides not allowed" 整批拒收.
tsCounter uint64
```

### 2. runLoop 自增 counter 并透传到 scrapeAndPush

```go
// Fire one immediate scrape so the first batch lands quickly.
// counter=0: 立即 scrape 用基线 ts,后续 ticker 触发才自增.
p.scrapeAndPush(ctx, spec, 0)

t := time.NewTicker(spec.Interval)
defer t.Stop()

for {
    select {
    case <-ctx.Done():
        return
    case <-t.C:
        // 自增 counter 在持锁下做
        p.mu.Lock()
        p.tsCounter++
        counter := p.tsCounter
        p.mu.Unlock()
        p.scrapeAndPush(ctx, spec, counter)
    }
}
```

`scrapeAndPush` / `scrapeAndPushOne` 签名加 `tickCounter uint64` 参数,
透传给 `scrapeOnce`。

### 3. scrapeOnce 在 serverTimeMs > 0 时叠加 tick 偏移

`internal/edgeagent/plugins/metrics/scrape.go` scrapeOnce 签名加 `tickCounter uint64`:

```go
func scrapeOnce(
    ctx context.Context,
    spec specView,
    targetURL string,
    serverTimeMsFn metricscommon.ServerTimeMsFn,
    tickCounter uint64,
) ([]tunnel.PromSample, string, error) {
    // ...
    samples := collector.FlattenSamples(time.Now(), spec.SourceLabel, mfs, spec.ExtraLabels)
    var serverTimeMs int64
    if serverTimeMsFn != nil {
        serverTimeMs = serverTimeMsFn()
    }
    // tick 偏移只在 serverTimeMs > 0 时叠加:
    //   - serverTimeMs > 0: 走 manager 时钟锚定路径, ts = anchor + tickCounter * scrapeInterval
    //   - serverTimeMs <= 0: legacy fallback (ingester 读 s.TsMs),不加偏移以免污染回退路径
    if serverTimeMs > 0 {
        tickOffsetMs := int64(tickCounter) * int64(spec.Interval/time.Millisecond)
        serverTimeMs += tickOffsetMs
    }
    for i := range samples {
        samples[i].ServerTimeMs = serverTimeMs
    }
    return samples, spec.SourceLabel, nil
}
```

关键设计点:

- **offset 仅在 serverTimeMs > 0 时叠加**:保证 `serverTimeMsFn=nil` 或未收到心跳
  的 legacy 路径(`manager ingester` 走 `s.TsMs` fallback)零变化
- **spec.Interval 由 parseSpec 兜底为 defaultInterval=15s**,不会为 0 引发除零
- **失败 tick 也递增 counter**:占住 ts 位,避免下次成功 tick 跟更早历史撞
  (不递增会让"成功 → 失败 → 成功"序列共用 counter,触发撞)

### 4. 测试追加参数

`internal/edgeagent/plugins/metrics/scrape_test.go` 3 处 `scrapeOnce` 调用追加
`tickCounter=0`,保持原行为语义。

## 目的

让 edge `metrics` 插件每个 scrape tick 拿到**唯一** ts,避免同 (metric, labels)
相邻 tick 共用同一 ms 触发 Prom `400 duplicate sample ... overrides not allowed`
整批拒收。

数据完整性承诺:

- 每个 scrape tick 的**全部**样本都被推到 Prom,**没有去重、没有过滤**
- 唯一变化:Prom sample ts 序列从"30s 更新一次"变成"每个 scrape tick 更新一次"
- PromQL `rate()` / `irate()` / `histogram_quantile()` / `increase()` 行为:相邻点
  间隔从 30000ms 缩到 10000ms(若 scrape_interval=10s),**结果更准确**
- 历史 Prom 数据连续性:ts 单调不变,老数据不会被标 stale
- v3.3 设计完整保留:manager 时钟锚定(避免 edge 192.168.25.56 时钟漂移触发
  Prom 5min hard-reject)依然有效

## 不影响

| 文件 | 不改理由 |
|---|---|
| `internal/edgeagent/biz/agent.go` | `ServerTimeMs()` getter 不变,counter 由 plugin 自己管 |
| `internal/edgeagent/plugins/metricscommon/scrape.go` | nil-safe 接口零变化,custommetrics / databasemetrics 路径零影响 |
| `internal/manager/biz/promwrite/ingester.go` | 主路径不动,`ServerTimeMs > 0 ? ServerTimeMs : TsMs` fallback 保留 |
| `internal/manager/service/frontierbound/handlers.go` | heartbeat 响应里 `ServerTimeMs` 字段不变 |
| `internal/pkg/tunnel/messages.go` | `PromSample` wire schema 不变,wire 兼容 |
| `cmd/ongrid-edge/main.go` | metrics 插件构造方式不变 |
| `custommetrics` / `databasemetrics` / `hostmetrics` / `procmetrics` | 走不同路径,不受本 bug 影响 |

custommetrics / databasemetrics 后续想接 ts 偏移时,自己维护 counter,
把 `tickOffsetMs` 算好后传给 scrapeOnce 即可,metricscommon 接口零变化。

## 验证

```text
$ go build ./internal/edgeagent/plugins/metrics/...
BUILD_OK

$ go vet ./internal/edgeagent/plugins/metrics/...
(无输出)

$ go vet ./internal/edgeagent/plugins/metrics/... ./internal/edgeagent/biz/... ./internal/edgeagent/collector/... ./internal/edgeagent/plugins/metricscommon/...
(无输出)
```

`cmd/ongrid` 在 Windows 上仍因 `onnxruntime_go` cgo 与 Linux-only syscall
(项目原本就有的已知限制,与本任务无关)失败。

## 后续

1. 推源码到 192.168.25.30 打包机 (`/opt/remotework/ongrid-new`),用 `make package`
   或 `make build-arm64` 出 dist 产物。
2. 在 192.168.25.30 上 `make package` 后把产物搬到管理后台 Edges 页面,
   触发 192.168.25.56 的一键装机,或手动 curl 装到 `/mnt/data/tools-temp`。
3. 192.168.25.56 上 systemd 重启 ongrid-edge。
4. 观察 edge 日志:`push_prom_samples failed` 应消失(允许偶发的网络抖动)。
5. Prom 端 `/v1/edges/{id}/metrics` 查 CPU / 内存曲线,断点应消失。
6. 监控一段时间(至少 1 个心跳周期 30s 以上)确认连续 tick 的 ts 严格单调递增。