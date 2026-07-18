# 2026-07-18 — Edge 不再向 Server 推送数据：根因 + 排查

## 现象（用户反馈）

2026-07-18 接到用户反馈：192.168.25.56 不再向 192.168.25.30 推送数据。

## 排查过程

### 第一步：状态确认（两台机器）

**Edge 端 (192.168.25.56)**：
- ongrid-edge 主进程 PID 2236119 已运行 14h（启动于 2026-07-17 18:05 CST）
- 子进程全在：node_exporter (:9102) / process_exporter (:9256) / promtail / auditbeat / otelcol-contrib
- tunnel 链路活跃：每 15s 一次 `writePkt running, clientID: 2, packets processed: 9402→9404→9407…`
- ping 192.168.25.30：0.233ms 正常
- SSH 到 192.168.25.30:8080 / :9000：连接被拒（ongrid 容器内只暴露给 docker network，不直接对外）

**Server 端 (192.168.25.30)**：
- ongrid 容器运行中（PID 1614463），v0.9.1，started 17h ago
- ongrid-prometheus 容器运行 15h
- ongrid-loki 容器运行 22h

**网络 / 进程层都没问题。**

### 第二步：观察 edge 日志中的报错

journalctl -u ongrid-edge 显示 metrics 插件每 15 秒一次失败：

```
WARN push_prom_samples failed url=http://127.0.0.1:9102/metrics samples=1573
  err="tunnel call \"push_prom_samples\": push_prom_samples: promwrite:
  http://prometheus:9090/prometheus/api/v1/write returned 400:
  out of bounds: timestamp is too far in the future"
```

`http://127.0.0.1:9256/metrics`（process_exporter）同样失败，samples ≈ 4064。

**首次失败时间**：2026-07-16 19:40:27 CST（edge 端日志）。

### 第三步：观察 Prom 端拒收样本的精确偏差

`docker logs ongrid-prometheus` 显示：

```
ts=2026-07-18T00:21:18.334Z ... err="out of bounds: timestamp is too far in the future"
  series="{__name__=\"go_gc_duration_seconds\", device_id=\"1\", quantile=\"0\"}"
  timestamp=1784385347850
```

把样本毫秒时间戳转回 UTC：

| 项 | UTC 时间 |
|---|---|
| server 收到时刻 | 2026-07-18 00:21:18.334 |
| sample.TsMs 转 UTC | **2026-07-18 14:35:47.850** |
| **偏差** | **+14h14m29s（future）** |

边缘端 ssh date 与 node_time_seconds 完全一致：2026-07-18 00:27:49 UTC（采样前后）。

→ **样本时间戳比 edge 本地系统时间超前 14h14m**，远超 Prom 默认 10min future-tolerance hard-reject，所以所有样本被服务端拒收。

### 第四步：检查源码层面是否清理干净

git HEAD 是 `312f2a89 revert(timestamp): 还原 Prom 数据流中用 ongrid 时间替换 edge 采样时间的修改`（2026-07-17 09:25 +0800 = 01:25 UTC）。

`git grep` 在 HEAD 上搜索：
- `tsCounter|tickCounter|tickOffset` → **0 命中**
- `PromSample.ServerTimeMs` 字段 → 已删除（`internal/pkg/tunnel/messages.go:362`）
- `internal/edgeagent/plugins/metricscommon/scrape.go` 第 93 行 `now := time.Now()`，第 94 行 `FlattenSamples(now, ...)`
- `internal/edgeagent/collector/mapper.go:69-83` `FlattenSamples` 用 `now.UnixMilli()` 写 TsMs，**仅当 `m.TimestampMs != nil && *m.TimestampMs > 0` 才用上游**
- `internal/manager/biz/promwrite/ingester.go:118` 直接 `tsMs := s.TsMs`

**源码层干净。**

### 第五步：检查实际部署的 binary 内容

**edge binary strings 检查**（192.168.25.56）：

| 字符串 | 命中数 |
|---|---|
| `ServerTimeMs` | **9 处** ← v3.3 特征 |
| `tsCounter` | **1 处** ← 0eada550 防撞机制特征 |
| `tickCounter` | 0 |
| `tickOffset` | 0 |
| `json:"ts_ms"` | 1（PromSample 字段，正常） |
| `collector.FlattenSamples` | 1（正常） |

→ **当前跑的 edge binary 仍包含 v3.3 + tsCounter 时代代码**（0eada550 引入 / 90f88ce6 引入 v3.3 路径 / 312f2a89 与 ea72a4e5 已 revert 这两层）。

**ongrid server binary strings 检查**（192.168.25.30 容器内）：

| 字符串 | 命中数 |
|---|---|
| `tsCounter` | 0 |
| `tickCounter` | 0 |
| `tickOffset` | 0 |
| `ServerTimeMs` | 1（仅一处，可能是 HeartbeatResponse struct 定义残留，无关键路径影响） |
| 二进制 mtime | 2026-07-15 00:30 UTC（在 312f2a89 revert commit 之前 build） |

server binary 不含 tsCounter，但 mtime 在两个 revert commit 之前。

### 第六步：交叉对照 binary mtime 与 commit 时间

| 项 | 时间 (UTC) |
|---|---|
| ea72a4e5 revert tsCounter | 2026-07-16 14:28:24 |
| 312f2a89 revert v3.3 路径 | 2026-07-17 01:25:05 |
| edge binary mtime | 2026-07-17 02:56 |
| ongrid server binary mtime | 2026-07-15 00:30 |

edge binary 编译时间（02:56 UTC）晚于两个 revert commit（01:25 / 14:28），
**理论上** 应该包含 revert 后的代码。但 strings 仍命中 v3.3 + tsCounter 特征。

**唯一合理解释**：edge binary build 时 **source tree 没更新到 HEAD**，或 build 失败保留了旧产物。
打**包机 `/opt/remotework/ongrid-new`** 在 build 时刻可能用的是 `0eada550..90f88ce6` 区间的某个 commit。

### 第七步：核对 .record 历史

`.record/2026-07-16-restore-edge-prom-sample-timestamp.md` 与 `2026-07-17-restore-edge-ts-cleanup-residual.md` 都说"已清理 tsCounter / ServerTimeMs"，但**只清理了源码**——未触发重新编译部署。

## 根因

**当前 192.168.25.56 上运行的 ongrid-edge 二进制（v0.9.1，md5 c6b8948f75cc33040fd3c46f9af57532）是 v3.3 + tsCounter 时代的旧 binary**：

- 内含 `ServerTimeMs` 字段处理路径（v3.3 anchor 方案）
- 内含 `tsCounter` 累加机制（0eada550 防撞方案）
- 这套旧代码生成的样本时间戳 = 基准时间 + 累加 elapsed ≈ 真实当前时间 + 14h14m
- 远超 Prometheus 默认 10 分钟 future-tolerance window → 服务端硬性拒收

git HEAD (312f2a89) 上源码已经清干净，但**没有触发重新打包 + 重装 edge binary**。

## 附带发现（不阻塞数据，但违反硬规则）

- **chronyd 在 192.168.25.56 处于 active running**（enabled，started 17h ago）
  - Reference ID: 192.168.30.104, Stratum 1, offset -106us
  - 配置文件：`/etc/chrony.conf` 含 `server 192.168.30.104 iburst minpoll 6 maxpoll 10` + `makestep 1.0 3`
  - **违反 AGENTS.md 时钟管理硬规则**：禁止 NTP 校时（含 chronyd）
  - 但实测 edge 系统时间与 chronyd ref time 几乎一致（差 < 1ms），说明 chronyd 没把 edge 时间往未来拉——本场景下 chronyd 不是 14h 偏移的直接原因
- server 端 prom log series `device_id="1"`：
  - device_id=1 是已删除旧设备，device_id=2 是当前活动设备
  - push 进来的 series 标 device_id=1 意味着 frontierbound 把 edge_id→device_id 解析时拿到了旧值
  - 这与时间戳问题独立，但属于另一个待排查的小问题（device_id 解析）

## 修复路径（按用户决策）

1. **打包机重 build**：在 192.168.25.30 上 `cd /opt/remotework/ongrid-new && git pull && make build-arm64`（或 `make package`），产物落到 `dist/`
2. **重装 edge**：在 192.168.25.30 上 `scp dist/ongrid-edge-v0.9.1-arm64.tar.gz root@192.168.25.56:/tmp/`，再到 192.168.25.56 上跑 install.sh 流程；或通过管理后台 Edges 页面一键升级
3. **重启 ongrid-edge**：`ssh root@192.168.25.56 'systemctl restart ongrid-edge'`
4. **验证**：`curl -sG 'http://192.168.25.30:8080/...' 或 docker exec ongrid-prometheus 查询 `count by (__name__) (go_gc_duration_seconds)`，看 timestamp 是否 ≈ edge 当前 ±10s

## 关联文件

- 主问题 commit：`312f2a89`（HEAD）
- 历史 revert：`ea72a4e5`（revert tsCounter）、`0eada550`（引入 tsCounter）、`90f88ce6`（v3.3 时间戳方案源头）
- 历史 record：
  - `.record/2026-07-16-restore-edge-prom-sample-timestamp.md`
  - `.record/2026-07-17-restore-edge-ts-cleanup-residual.md`
  - `.record/2026-07-12-metrics-timestamp-server-time-anchored.md`（v3.3 设计，被 312f2a89 取代）
  - `.record/2026-07-12-ntp-192-168-25-56.md`（192.168.25.56 NTP 漂移事故）

## 不动项（按 AGENTS.md 时钟管理硬规则）

- 不复活 0eada550 的 tsCounter 防撞机制
- 不修改 edge / server 端任何系统时钟
- 不动 192.168.25.56 的 chronyd（仅作为事故登记，待用户决策是否停用）