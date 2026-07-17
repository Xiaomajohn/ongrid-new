# 2026-07-16 — 还原 Prom 数据流中"用 ongrid 时间替换 edge 采样时间"的修改

## 背景

2026-07-12 的 v3.3 时间戳方案（见 `.record/2026-07-12-metrics-timestamp-server-time-anchored.md`）
做出了一个核心修改：**Prom 的 `sample.timestamp` 由 `TsMs`（edge 本地 scrape 时刻）
改写为 `ServerTimeMs`（ongrid 心跳响应里的服务端正点）**，并以此对付
edge 端 CLOCK_REALTIME 漂移可能触发的 Prom 5min hard-reject window。

近期运维反馈该替换带来几个问题：

1. **数据时间轴向后挪**：edge 真实看到指标的时间点被 ongrid 替换，operator
   在排障时无法直接根据 panel 时间对应回日志 / 事件。这是核心回归。
2. **心跳 30s 周期锚点的延迟错配**：scrape 间隔往往短至 10s，但时间戳锚点是
   30s 前的一次心跳响应，最大可漂 ≈30s+网络延迟。
3. **未来滚动升级兼容复杂**：ingester 双路径（`if ServerTimeMs>0 else TsMs`）让
   老 / 新 edge / 老 / 新 manager 的组合行为是隐式契约，需要多读一份心智。

用户决策：**还原这个替换，Prom 数据流统一采用 edge 本地时间**。clock skew 容忍
另走兜底路径（见下方"时钟漂移容忍策略"）。

---

## 还原内容（全 v3.3 移除）

### 1. manager 端 ingester 不再读 ServerTimeMs 字段

**`internal/manager/biz/promwrite/ingester.go`**

- 删除 `tsMs := s.ServerTimeMs; if tsMs <= 0 { tsMs = s.TsMs }` 双轨分支
- 改为 `tsMs := s.TsMs`，Prom 的 `sample.timestamp` 直接是 edge 本地时间
- 删除 `for k, v := range s.Labels` 内 `case "edge_ts_ms"、"edge_ts"` 的
  reserved drop 段（不再可能注入 → 不必保留兜底 drop）
- labels slice capacity 同步收口 (`+4` → `+3`)

注释从原来"锚定 manager 时钟避免 5min hard-reject"还原为"采用 edge 本地时间，
真实事件时间"。

### 2. edge 端 scrape 不再向 PromSample 注入 ServerTimeMs

**`internal/edgeagent/plugins/metricscommon/scrape.go`**

- 删除 `ServerTimeMsFn func() int64` 类型别名
- `Scrape(ctx, target, serverTimeMsFn)` 签名收为 `Scrape(ctx, target)`
- 函数体内删除 `serverTimeMs := serverTimeMsFn()` 及 `for i := range samples { samples[i].ServerTimeMs = serverTimeMs }` 循环

**`internal/edgeagent/plugins/metrics/scrape.go`**

- `scrapeOnce(ctx, spec, targetURL, serverTimeMsFn)` 签名收为 `scrapeOnce(ctx, spec, targetURL)`
- 删除对应循环 + `metricscommon` import

### 3. edge metrics plugin 不再持有 serverTimeMs 字段

**`internal/edgeagent/plugins/metrics/plugin.go`**

- 删除 `Plugin.serverTimeMs` 字段
- 删除 `New(pusher, edgeID, serverTimeMs, log)` 形参
- 删除 `metricscommon.ServerTimeMsFn` 类型别名（`ServerTimeMsProvider`）
- 删除 `scrapeAndPushOne` 调用 `scrapeOnce` 时的第 4 参

### 4. edge agent 不再缓存 manager 心跳时间

**`internal/edgeagent/biz/agent.go`**

- 删除 `Agent.lastServerTimeMs int64` 字段
- 删除 `Agent.ServerTimeMs() int64` 方法
- `heartbeatLoop` 末尾"缓存 ServerTimeMs 进 a.lastServerTimeMs"段删除
- `registerEdge` 注释简化（不再引用 v3.3 时间戳策略链）

### 5. manager heartbeat handler 不再附 ServerTimeMs

**`internal/manager/service/frontierbound/handlers.go`**

- `MethodHeartbeat` handler `return json.Marshal(tunnel.HeartbeatResponse{})`
  不再带 `ServerTimeMs: time.Now().UnixMilli()`
- 注释从"v3.3 在心跳响应里贴上 ongrid 时间戳"改为说明 manager 不再承担
  时间戳锚点的角色

### 6. tunnel wire schema 不再携带 ServerTimeMs

**`internal/pkg/tunnel/messages.go`**

- `type HeartbeatResponse struct { ServerTimeMs int64 }` 收为 `struct{}`
- `type PromSample struct` 删除 `ServerTimeMs` 字段（连同其 JSON tag）
- 两段大幅精简的注释说明 wire 上无 ServerTimeMs 时间戳字段

### 7. cmd/ongrid-edge 接线

**`cmd/ongrid-edge/main.go`**

- `edgepluginmetrics.New(client, agent.EdgeID, agent.ServerTimeMs, pluginLog)`
  → `edgepluginmetrics.New(client, agent.EdgeID, pluginLog)`

### 8. 其它 caller 同步

- `internal/edgeagent/plugins/custommetrics/plugin.go` 调
  `metricscommon.Scrape(rctx, target, nil)` → `Scrape(rctx, target)`
- `internal/edgeagent/plugins/databasemetrics/plugin.go` 同上
- 三个测试 `internal/edgeagent/plugins/metricscommon/scrape_test.go` /
  `internal/edgeagent/plugins/metrics/scrape_test.go` 中调用 `Scrape / scrapeOnce`
  的多余 `nil` serverTimeMsFn 实参全部去掉；`metrics.New(...)` 也同步收敛为 3 参

### 9. 前端注释更新

**`web/src/components/DeviceMetricsPanels.tsx`**

- `panelExpr` 注释：从"显式 `by (...)` 聚合掉写入侧注入的 edge_ts_ms 动态 label"
  改为"显式聚合即使上游某天注入了动态 label 也兜底防御"
- `matrixToPanel` 第二层 `seenLabels` 去重注释：从"防御后端 edge_ts_ms 动态 label"
  改为"治理多端采样同时上报的兜底"

逻辑代码未改：前端的 `avg by (cpu/device)`、`seenLabels` 去重仍然保留，作为
多年累积的时序稳定性防御。

---

## 时钟漂移容忍策略（替代 v3.3 锚点）

Prom 5min future-window hard-reject、Prom 5min past-window reject 是 edge 端 CLOCK_REALTIME
漂移 forward / backward 时可能触发的限制。v3.3 之前靠 edge 端 `SafeNow(serverTime)` 时钟钳位、
v3.3 走"用 ongrid 时间替换数据时间戳"两种路径都各有代价。本次还原后采用
Loki 已采用的同款兜底策略：

- **Prom 5min hard-reject**：依赖 Prom 服务端配置放宽容忍窗口，或对漂移机器
  单独运维（不改代码）。最坏可在 manager 端加一层时钟钳位（如果产品
  后续无法接受 panel 缺数据），但当前优先走"ongrid 端维护稳定时钟 + edge 端
  自承担漂移" 的非对称策略
- **Loki `creation_grace_period`**：保留 `.record/2026-07-12-metrics-timestamp-server-time-anchored.md`
  任务 5 的 30d grace 不动（`deploy/install/loki-config.yaml` 不改）
- **漂移机器事故登记**：192.168.25.56 的 NTP 漂移观察值（22min forward）登记在
  `.record/2026-07-12-ntp-192-168-25-56.md`，仅作事故信息；按 AGENTS.md 时钟
  管理硬规则，不在代码 / 镜像 / 配置做任何修复

---

## 端到端数据流（v3.3 还原后）

```
edge metricscommon.Scrape
  collectedAt := time.Now()                              ← edge CLOCK_REALTIME
  sample.TsMs         = collectedAt.UnixMilli()           ← 是写入 Prom 的最终时间戳
  ↓
tunnel RPC: {"ts_ms": <edge>}
  ↓
manager handler
  json.Unmarshal 透传
  ↓
promwrite.Ingester.Push
  tsMs := s.TsMs                                          ← edge 本地时间
  label __name__     = s.Name
  label device_id    = <resolved host device id>
  label ongrid_source = source
  Prom sample.timestamp = edge 本地采集时刻
  
  ↓
promwrite.Client.Write
  ↓
Prom 服务端
  写入 TSDB，sample.timestamp = edge 本地时间
  edge 端 CLOCK_REALTIME 漂移 forward 时可能撞 5min hard-reject —
  这是 Prom 内置约束，不在 ongrid 控制范围内（AGENTS.md 时钟管理硬规则
  禁止动本地系统时钟）
```

---

## 兼容性

- 老 wire edge（v3.3 前）：不发 ServerTimeMs / 不查 ServerTimeMs，本来就 OK
- 老 wire manager（v3.3 前）：JSON unmarshal 对未知字段静默忽略；删字段后
  行为完全兼容
- 滚动部署：双向兼容。两种顺序混合，Prom sample.timestamp 一律是 edge 本地时间
- 老 manager 在 ingester 里走 `if ServerTimeMs>0` 分支：删除分支后该代码路径消失
  但不影响 Prom 写入（直接走 TsMs）

---

## 部署

- manager / ongrid-edge 都需重新打包（见 Makefile `package` 或 `build-arm64`）
- ongrid container restart：`docker compose restart ongrid`
- ongrid-edge 重新一键安装：管理后台 Edges 页面 Upgrade；或手动按
  `.record/2026-07-07-edge-create-bind-device-and-install.md` 走一行 curl
- 验证命令：
  ```bash
  # 1) Prom sample 写入用的是 edge 本地时间
  curl -sG http://prom:9090/api/v1/query --data-urlencode 'query=count by(__name__) ({__name__=~"node_cpu_seconds_total"})' | jq
  # 期望：返回最近一个 sample 的 timestamp ≈ scrape 时刻的 edge 本地 time.Now()，
  #       不再是 ongrid 服务端心跳的 Unix 毫秒
  
  # 2) 不再有 edge_ts_ms / edge_ts 动态 label（v3.3 那次修复后已无，这次起也不再有）
  curl -sG http://prom:9090/api/v1/query --data-urlencode 'query=count({edge_ts_ms!=""})' | jq
  # 期望：0
  
  # 3) Loki 仍然能容忍 30d 时钟漂移
  docker logs --since 10m ongrid-loki 2>&1 | grep 'timestamp too new' | wc -l
  # 期望：0（30d grace 兜底）
  ```

---

## 任务清单

- [x] 还原 `internal/manager/biz/promwrite/ingester.go` 替换逻辑 + 删 edge_ts_ms reserved
- [x] 还原 `internal/edgeagent/plugins/metricscommon/scrape.go` Scrape 签名 + ServerTimeMsFn 类型
- [x] 还原 `internal/edgeagent/plugins/metrics/scrape.go` scrapeOnce 签名 + 循环
- [x] 还原 `internal/edgeagent/plugins/metrics/plugin.go` serverTimeMs 字段 + ServerTimeMsProvider
- [x] 还原 `internal/edgeagent/biz/agent.go` lastServerTimeMs 字段 + ServerTimeMs() 方法
- [x] 还原 `internal/manager/service/frontierbound/handlers.go` heartbeat 响应
- [x] 还原 `internal/pkg/tunnel/messages.go` HeartbeatResponse / PromSample 字段
- [x] 还原 `cmd/ongrid-edge/main.go` metrics.New 调用
- [x] 同步清理 caller（custommetrics / databasemetrics / 3 个测试文件）
- [x] 前端 DeviceMetricsPanels 注释更新
- [x] go vet 涵盖所有改动范围，无编译错
- [x] 本 .record
