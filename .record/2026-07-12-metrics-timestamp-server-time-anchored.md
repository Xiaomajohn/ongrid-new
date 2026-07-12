# 2026-07-12 监控指标时间戳方案 v3.3 实施（plan 路径）

## 范围

按 `cache/plans/三问题修复统一实施计划_task-0d7.md` 实施的 v3.3 方案：

| 任务 | 主题 | 文件 | 状态 |
|---|---|---|---|
| 0 | AGENTS.md 加"禁止 NTP 校时"硬规则 | `AGENTS.md` | ✅ 完成 |
| 1 | 回退 v2 SafeNow 时钟钳位 + edge 端不动系统时钟 | 5 个 Go 文件 + 1 个 cmd 文件 | ✅ 完成 |
| 2 | HeartbeatResponse 加 `ServerTimeMs` 字段；edge 周期性拿 manager 时间 | `tunnel/messages.go` + `frontierbound/handlers.go` + `agent.go` | ✅ 完成 |
| 3 | tunnel.PromSample 加 `ServerTimeMs` 字段；TsMs 语义不动 | `tunnel/messages.go` + `scrape.go` ×2 + `plugin.go` + `main.go` | ✅ 完成 |
| 4 | manager promwrite ingester 用 ServerTimeMs 作 Prom `sample.timestamp` + 加 `edge_ts_ms` label | `promwrite/ingester.go` | ✅ 完成 |
| 5 | Loki `creation_grace_period` 30m → 30d 兜底（仅配置） | `deploy/install/loki-config.yaml` | ✅ 完成 |
| 7 | 写 .record（本文件 + NTP 漂移机器登记） | `.record/` | ✅ 完成 |

撤回 v2 三处：

- v2 任务 3.1：edge 主机 NTP 同步到 manager（191.168.25.30 / 191.168.25.56 chronyd 修改）—**全部撤回**（按用户最终决策 + 新增的 AGENTS.md 时钟管理硬规则）
- v2 任务 3.2：edge 端 `SafeNow(serverTime)` 时钟钳位逻辑 —**全部撤回**（edge 端不再触碰时间戳，靠数据时间戳策略绕开）
- v2 .record 文件 `2026-07-12-three-fixes-implementation.md`（任务 3.1 + 3.2 部分）—**作废**，本文件取代

---

## 任务 0：AGENTS.md 加"禁止 NTP 校时"硬规则

### 改了什么

`AGENTS.md` 的 `### 运维` section 末尾新加 `### 时钟管理` 子节，规则包括：

- edge 端 + ongrid 服务端的系统时钟不允许通过任何代码路径 / 配置 / 部署脚本修改（包括 NTP / chrony / systemd-timesyncd / timedatectl / date -s / sntp / w32tm）
- 时钟漂移通过"数据时间戳用受信源（ongrid 服务端时间）"兜底，不通过修改本地系统时钟治本
- 漂移机器（如 192.168.25.56）登记到 `.record/`，仅作事故信息，不在代码 / 镜像 / 配置做修复
- 任何 PR 涉及时钟修改 / NTP 配置 / 时间同步逻辑，review 时重点拒绝

### 为什么这样改

- v2 任务 3.1 的 NTP 同步方向（"修改系统时钟治本"）违反"不动系统时钟"的设计原则
- 用户最终决策：取 ongrid 时间作为数据时间戳，不通过 NTP 校时治本
- 把规则写进 AGENTS.md 让后续 agent 在改动评审时自动发现违例

---

## 任务 1：撤回 edge 端 SafeNow 时钟钳位（5 文件 + 1 cmd）

### 改了什么

**`internal/edgeagent/plugins/metricscommon/scrape.go`**
- 删 `ServerTimeFn func() int64` 类型别名
- 删 `SafeNow(serverTime int64) time.Time` 函数（一整套 ±1m/+4m 时钟钳位）
- 新加 `ServerTimeMsFn func() int64` 类型别名（语义不同：从"钳位后入 SafeNow"改为"直接填 ServerTimeMs 字段"）
- `Scrape` 签名：`serverTimeFn ServerTimeFn` → `serverTimeMsFn ServerTimeMsFn`
- 函数体改：`collector.FlattenSamples(SafeNow(st), ...)` → `collector.FlattenSamples(time.Now(), ...)`，然后循环写 `samples[i].ServerTimeMs = serverTimeMs`
- 注释清楚 v2 钳位迁移到任务 4 的 ingester 里

**`internal/edgeagent/plugins/metrics/scrape.go`**
- `scrapeOnce` 签名同步改：第三个参数类型从 `ServerTimeFn` 改成 `ServerTimeMsFn`
- 函数体改：`metricscommon.SafeNow(st)` 删；用 `time.Now()` + 同上循环注入 ServerTimeMs

**`internal/edgeagent/plugins/metrics/plugin.go`**
- 类型别名 `ServerTimeProvider = metricscommon.ServerTimeFn` → `ServerTimeMsProvider = metricscommon.ServerTimeMsFn`
- `Plugin` 字段 `serverTime ServerTimeProvider` → `serverTimeMs ServerTimeMsProvider`
- `New(...)` 形参同步改名
- `scrapeAndPushOne` 调 `scrapeOnce` 的实参改 `p.serverTimeMs`

**`internal/edgeagent/plugins/databasemetrics/plugin.go` / `custommetrics/plugin.go`**
- 不需要改（仍调 `metricscommon.Scrape(rctx, target, nil)`，nil 是合法值）

**`internal/edgeagent/biz/agent.go`**
- 字段 `lastServerTime int64` → `lastServerTimeMs int64` + 整段注释重写（讲明语义已变：从 "register 一次性 ServerTime" 改为 "heartbeat 周期 ServerTimeMs"）
- 方法 `ServerTime() int64` → `ServerTimeMs() int64`
- `registerEdge` 不再写 `a.lastServerTime = resp.ServerTime`
- `registerEdge` log.Info 删 `slog.Int64("server_time", ...)` 和 `slog.Int64("edge_clock_skew", ...)` 字段
- `heartbeatLoop` 改为：`var hbResp tunnel.HeartbeatResponse` + `client.Call(..., &hbResp)` + 成功后 `a.lastServerTimeMs = hbResp.ServerTimeMs`（带锁）

**`cmd/ongrid-edge/main.go`**
- `edgepluginmetrics.New(client, agent.EdgeID, agent.ServerTime, pluginLog)` → `edgepluginmetrics.New(client, agent.EdgeID, agent.ServerTimeMs, pluginLog)`

### 为什么这样改

- v2 SafeNow 在 edge 端把本地时间"钳位"到 manager 时钟附近 — 这个动作等价于"在 edge 端修改时间戳"，违反最终决策
- v3.3 的设计：edge 端照常 `time.Now()`，把 manager 时间作为另一个独立字段带过去；"屏蔽"由 manager 端 ingester 用 ServerTimeMs 替换 Prom sample.timestamp 完成
- 这样 Prom 5min hard-reject / Loki 30min grace 的问题在 manager 端解耦处理，edge 端不需要知道有这回事

---

## 任务 2：HeartbeatResponse.ServerTimeMs（3 文件）

### 改了什么

**`internal/pkg/tunnel/messages.go`**（`type HeartbeatResponse struct{}` → 非空）
- 加 `ServerTimeMs int64 \`json:"server_time_ms,omitempty"\`` 字段（unix 毫秒）
- 注释清楚：这是数据时间戳，不是 NTP 时钟同步；field 名跟 `api/tunnel/v1/tunnel.proto` 的 `HeartbeatResponse.server_time_ms` 字段名保持 wire 兼容

**`internal/manager/service/frontierbound/handlers.go`**
- `MethodHeartbeat` handler 在最后 `return json.Marshal(tunnel.HeartbeatResponse{})` → `return json.Marshal(tunnel.HeartbeatResponse{ServerTimeMs: time.Now().UnixMilli()})`
- 加注释说明 ongrid / Prom 同机同源时钟，永不漂移

**`internal/edgeagent/biz/agent.go`**（在任务 1 改动里已合并）
- `heartbeatLoop` 每 30s 调一次 MethodHeartbeat，解析 resp.ServerTimeMs 写入 `a.lastServerTimeMs`
- 注释清楚 30s 锚点跟真实 ongrid 时间最大差 = 心跳周期 + 网络延迟；Prom 5min future-window 余量 4.5min，足够

### 为什么这样改

- edge 需要周期性拿 manager 时间（不能仅靠 register 一次性写入，否则重启 / tunnel 重连后陈旧）
- heartbeat 是天然的 30s 周期 RPC，把 ServerTimeMs 加到响应里几乎零成本（已经在每 tick 走这条 RPC）
- 让 edge 缓存最近一次心跳响应的时间戳，写 PromSample 时直接搬运，不需要每次触发 RPC

---

## 任务 3：tunnel.PromSample.ServerTimeMs + edge scrape 写两个时间戳

### 改了什么

**`internal/pkg/tunnel/messages.go`**（PromSample 在同一文件，已和 HeartbeatResponse 一起改）
- `type PromSample struct` 加 `ServerTimeMs int64 \`json:"server_time_ms,omitempty"\`` 字段
- 注释清楚：
  - `TsMs` = edge 本地时间 = 事件时间（不动）
  - `ServerTimeMs` = ongrid 服务端时间（trusted source，heartbeat resp）
  - 老 edge 不发 `server_time_ms`（JSON omitempty）→ wire 兼容

**`internal/edgeagent/plugins/metricscommon/scrape.go`**（任务 1 已改）
- Scrape 末尾循环 `samples[i].ServerTimeMs = serverTimeMs`

**`internal/edgeagent/plugins/metrics/scrape.go`**（任务 1 已改）
- scrapeOnce 末尾同样循环写入

**`internal/edgeagent/plugins/metrics/plugin.go`**（任务 1 已改）
- Plugin 持 `serverTimeMs` 字段，从 agent 注入

**`cmd/ongrid-edge/main.go`**（任务 1 已改）
- 构造时传 `agent.ServerTimeMs`

### 为什么这样改

- 用户最终决策："现有字段不动 + 加 server time 字段 + Prom 用 server time 做序列"
- `TsMs` 保留语义（edge 本地时间 / 事件时间）— 改动小、不破坏现有查询、保留审计价值
- `ServerTimeMs` 新加作为 Prom sample.timestamp 的真实来源 — 让 Prom 校验时钟窗口时不再受 edge 漂移影响

---

## 任务 4：manager promwrite ingester 用 ServerTimeMs + edge_ts_ms label

### 改了什么

**`internal/manager/biz/promwrite/ingester.go`**
- `Push` 方法内 for-loop 加：
  ```go
  tsMs := s.ServerTimeMs
  if tsMs <= 0 {
      tsMs = s.TsMs  // legacy fallback
  }
  ```
- labels slice cap 从 `len(s.Labels)+3` 改 `+4`
- 在 `ongrid_source` label 之后加：
  ```go
  if s.ServerTimeMs > 0 {
      labels = append(labels, pkgpromwrite.Label{
          Name: "edge_ts_ms", 
          Value: strconv.FormatInt(s.TsMs, 10),
      })
  }
  ```
- `switch k` 加 `case "edge_ts_ms":`
- 最后 `pkgpromwrite.Sample.TsMs: s.TsMs` 改 `TsMs: tsMs`

### 为什么这样改

- v3.3 把 edge 端时间戳钳位搬到 manager 端 ingester：edge 只负责"搬运"两个时间戳，manager 决定哪个用、哪个作 label
- `edge_ts_ms` label 仅当 `ServerTimeMs > 0` 时加 — 老 edge fallback 时 ServerTimeMs=0 = TsMs，加 label 等于冗余（label 值 = Prom sample.timestamp，没信息量）
- 滚动发布兼容：新 manager 读老 edge 的 ServerTimeMs=0 自动走 legacy；老 manager 读新 edge 的 server_time_ms 字段直接忽略（JSON unmarshal 时不认识的字段忽略）

---

## 任务 5：Loki `creation_grace_period` 30m → 30d

### 改了什么

**`deploy/install/loki-config.yaml`**
```diff
-  # 放宽未来时间容忍度至30分钟，兼容Edge设备时钟漂移
-  creation_grace_period: 30m
+  # 放宽未来时间容忍度至30天(720h), 兼容Edge设备时钟漂移
+  # ... 注释讲明 30 天是当前最坏 clock skew (192.168.25.56 观察值 22min)
+  # 的 ~2000 倍余量, 实际不会触发. Loki 3.4.0 不支持 `30d` 字面量, 必须用 `720h`.
+  creation_grace_period: 720h
```

部署侧（不替执行）：
- `loki-config.yaml` 通过 `deploy/install/loki-stack.yaml` ConfigMap 挂载
- 重启 loki pod 生效

### 为什么这样改

- Loki 路径跟 Prom 路径不同：promtail 是独立子进程，写入 entry.timestamp 后锁死，manager 没介入点
- entry.timestamp = journald SO_TIMESTAMP = edge CLOCK_REALTIME = 跟 edge `time.Now()` 同根时钟
- 解法只能是"放宽 Loki 服务端的未来容忍窗口"
- 30 天 = 720h（不是 `30d`，Loki 3.4.0 不支持）
- 当前最坏漂移 22min，30 天余量是 ~2000 倍，实际不会触发
- 这是"兜底"，不是治本 — 治本在 Prom 路径（任务 4 ingester）

---

## 任务 6：补测试

按 user_rule "不需要写单元测试验证，从逻辑上验证通过、打包正常即可"，跳过单元测试。后续若需要可在 PR review 时补 — 见 .record 各历史文件。

---

## 任务 7：本 .record

两个文件：

1. `2026-07-12-metrics-timestamp-server-time-anchored.md` — v3.3 全流程实施记录（本文件）
2. `2026-07-12-ntp-192-168-25-56.md` — NTP 漂移机器事故信息登记（按 AGENTS.md 时钟管理硬规则，不在代码 / 镜像 / 配置中做任何修复）

---

## 关键设计权衡

| 维度 | 决策 | 理由 |
|---|---|---|
| edge 端时间戳处理 | 保留 edge 本地 `time.Now()` 作为 `TsMs`，不钳位 | TsMs 是事件时间的真实记录，业务侧不能丢 |
| manager 时间获取 | heartbeat 30s 周期，响应里带 `ServerTimeMs` | 已经是 heartbeat 路径，几乎零成本；不引入额外 RPC |
| Prom sample.timestamp | 替换为 `ServerTimeMs`（ongrid / Prom 同机同源时钟） | 解决"edge forward 漂移触发 5min hard-reject"的核心问题 |
| edge 真实时间保留 | ingester 把 TsMs 写到 `edge_ts_ms` label | post-hoc 校准"edge 在 X 看到 vs Prom 在 Y 存储" |
| Loki entry.timestamp | 不动，靠 `creation_grace_period` 放宽到 30d | promtail 拿不到 edge agent 内存，无法塞 ServerTimeMs 进去 |
| legacy 老 edge | ingester 检测 `ServerTimeMs<=0` 用 TsMs 作 sample.timestamp | 滚动发布兼容，过一年观察期再清 |
| NTP 校时 | AGENTS.md 硬规则禁止（任务 0） | 不通过修改本地系统时钟治本 |
| 漂移机器 | 仅记录在 `.record/`，不在代码 / 镜像 / 配置做任何修复 | 符合 AGENTS.md 时钟管理硬规则 |

---

## 端到端数据流（v3.3）

```
edge heartbeatLoop (30s 周期)
  → MethodHeartbeat request:  { Ts: time.Now().Unix() }
  → MethodHeartbeat response: { ServerTimeMs: 1752412812000 }     ← 新加字段
  → 存 a.lastServerTimeMs = 1752412812000

edge metricscommon.Scrape
  collectedAt := time.Now()                              ← edge CLOCK_REALTIME
  serverTime  := a.lastServerTimeMs                       ← ongrid 时间
  sample.TsMs         = collectedAt.UnixMilli()           ← edge 本地时间（不动）
  sample.ServerTimeMs = serverTime                        ← ongrid 时间（新增）
  
  ↓ tunnel RPC: {"ts_ms": <edge>, "server_time_ms": <ongrid>}
  ↓

manager handler
  json.Unmarshal 透传
  ↓

promwrite.Ingester.Push
  tsMs := s.ServerTimeMs                                   ← 默认 ongrid 时间
  if tsMs <= 0: tsMs = s.TsMs                              ← legacy fallback
  
  label __name__     = s.Name
  label device_id    = <resolved host device id>
  label ongrid_source = source
  if ServerTimeMs > 0:
      label edge_ts_ms = s.TsMs                            ← edge 本地时间备份
  ↓

promwrite.Client.Write
  Prom Sample.timestamp = ongrid 时间
  label edge_ts_ms      = edge 真实时间
  ↓

Prom 服务端
  写入 TSDB，sample.timestamp = ongrid 时间
  ongrid / Prom 同机同源时钟，永不漂移
```

---

## Loki 数据流（v3.3）— 仅放宽 grace

```
edge promtail (独立进程, 拿不到 edge agent 内存)
  ↓
entry.timestamp = journald SO_TIMESTAMP (edge CLOCK_REALTIME)   ← 锁死
  ↓
POST /loki/api/v1/push
  ↓
nginx auth_request → proxy_pass → loki:3100                       ← manager Go 不介入 (ADR-014)
  ↓
Loki 校验: entry.timestamp <= now(loki) + 720h (30d)
  实际触发概率: 0.05% (192.168.25.56 漂移 22min / 30d 余量)
```

---

## 待办 / 后续

1. **firewalld ntp service**：v2 任务 3.1 在 manager 改了 `/etc/chrony.conf` 但 firewalld 端口未开 — `2026-07-12-three-fixes-implementation.md` 记载的 NTP 部分全部撤回，按本 .record 的 v3.3 设计走（不再依赖 NTP 校时），firewalld ntp service 的手动命令不再适用（AGENTS.md 时钟管理硬规则禁止 NTP 校时）。
2. **`push_host_metrics` 路径也有相似问题**（不在本次范围）：`internal/manager/biz/metric/ingester.go` 从 tunnel 转 InfluxDB 写入时直接用 edge 本地 `HostMetricPoint.Ts`。如有 InfluxDB future-window 拒收问题可复用本方案设计（HostMetricPoint 加 ServerTimeMs 字段，ingester 用 manager 时间做样本时间戳）。
3. **一年观察期**：legacy fallback 分支（`ServerTimeMs<=0` 走 TsMs）保留一年，等所有老 edge 升级完后删除。
4. **NTP 漂移机器登记**：见 `.record/2026-07-12-ntp-192-168-25-56.md`。

---

## 部署 checklist（参考，不在本机执行）

1. 本地（Windows）改完 Go + YAML + AGENTS.md + .record
2. scp 到 `root@192.168.25.30:/opt/remotework/ongrid-new`
3. SSH 到 `192.168.25.30`：`make package` 或 `make build-arm64`（产物在 `dist/`）
4. SSH 到 `192.168.25.56`：通过管理后台 Edges 页面的一键安装（沿用 `2026-07-11-deployment-info.md` 的 SSH 凭据），或手动 curl 一行把 ongrid-edge 装到 `/mnt/data/tools-temp`
5. 服务端重启：`docker compose restart ongrid` 让新 ingester 生效；loki pod 也要重启让 `creation_grace_period: 720h` 生效

## 验收命令

```bash
# 1) edge 心跳响应带 ServerTimeMs
ssh root@192.168.25.56 journalctl -u ongrid-edge --since '5min ago' | grep -i 'heartbeat'
# 期望：心跳日志正常（无失败堆栈）

# 2) manager 端 ingester 写 edge_ts_ms label
docker exec ongrid-prometheus wget -qO- 'http://localhost:9090/api/v1/query?query=count({edge_ts_ms!=""})'
# 期望：> 0（新 edge 升级后才有值，老 edge 客户端走 fallback 不写 edge_ts_ms）

# 3) Prom 不再报 "out of bounds"
docker logs --since 10m ongrid 2>&1 | grep 'out of bounds' | wc -l
# 期望：0（修复后 15 分钟内无新告警）

# 4) Loki 不再报 "timestamp too new"
docker logs --since 10m ongrid-loki 2>&1 | grep 'timestamp too new' | wc -l
# 期望：0（30 天 grace 兜底）

# 5) AGENTS.md 硬规则生效
grep -A4 '### 时钟管理' /opt/remotework/ongrid-new/AGENTS.md
# 期望：能看到 5 条规则（包括"禁止 NTP 校时"）
```
