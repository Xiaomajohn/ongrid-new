# 2026-07-12 三问题修复统一实施记录（plan v2 实施）

## 范围

按 `cache/plans/三问题修复统一实施计划_task-0d7.md` 实施的三个修复：

| 任务 | 主题 | 文件 | 状态 |
|---|---|---|---|
| 1 | logs plugin extra_labels 注入 `device_id`(真值) + `task_name` | `internal/manager/biz/edge/plugin_config.go` + `internal/edgeagent/plugins/logs/render.go` | ✅ 完成 + 测试通过 |
| 2 | Logs 页"监控"→"任务"下拉重命名 + LogQL 注入 | `web/src/pages/Logs.tsx` | ✅ 完成 + typecheck 通过 |
| 3.1 | edge 主机 NTP 同步到 manager（治本） | `192.168.25.30` + `192.168.25.56` chronyd | ⚠️ **部分完成**：配置 + 监听 + 重启都已生效；firewalld ntp service 需手动 |
| 3.2 | edge metrics scrape 时间戳钳位到 ServerTime（治标） | 5 个 Go 文件 | ✅ 完成 + go test 通过 |

---

## 任务 1：logs plugin extra_labels 注入（manager）

### 改了什么

**`internal/manager/biz/edge/plugin_config.go`**
- 新增 narrow interface `EdgeLookup`，只暴露 `GetByID(ctx, id)`，避免引入完整 `Repo` 依赖
- `PluginConfigUC` 加 `edgeLookup` 字段，`NewPluginConfigUC` 签名加这个参数（向后兼容）
- `FetchForEdge` 在循环里调用 `uc.buildLogsExtraLabels(ctx, edgeID)` 给 logs plugin 注入
- 新增方法 `buildLogsExtraLabels`：从 `edges` 表拿 `TaskName`，device_id 优先用 `edge.DeviceID`，fallback 到 `edgeID` 自身
- 新增方法 `mergeLogsExtraLabels`：合并 operator 自定义 label，对 `device_id` / `task_name` 做覆盖保护
- 加 `"strconv"` import

**`internal/edgeagent/plugins/logs/render.go`**
- promtail 模板 `external_labels` 段改写：device_id 优先用 `{{ index .ExtraLabels "device_id" }}`，否则 fallback 到 `{{ .EdgeID }}`
- 后面 range 时跳过 `device_id` 避免重复
- 其它 extra_labels label（特别是 `task_name`）仍然正常输出

**`cmd/ongrid/main.go`**
- 调用 `NewPluginConfigUC(...)` 时多传一个 `edgeRepo` 作为 edgeLookup（line 791 附近）

**`internal/manager/biz/edge/plugin_config_databasemetrics_test.go` / `plugin_config_custommetrics_test.go`**
- `NewPluginConfigUC(...)` 6 处 + 16 处全部加 `nil` 参数

**`internal/manager/biz/edge/plugin_config_logs_test.go`（新建）**
- 加 `fakeEdgeLookup` + `ptrU64` helper
- 5 个新测试：
  1. `TestFetchForEdgeLogsPluginInjectsDeviceIDAndTaskName` — 主路径，验证 label 注入 + 保留 operator env label
  2. `TestFetchForEdgeLogsPluginFallsBackToEdgeID` — `Edge.DeviceID == nil` 时 fallback 到 edgeID
  3. `TestFetchForEdgeLogsPluginEmptyTaskNameStillEmitted` — legacy edge 空 task_name 也写入
  4. `TestFetchForEdgeLogsPluginNoEdgeLookupUnwired` — nil edgeLookup 时 logs plugin 不崩
  5. `TestFetchForEdgeLogsPluginNonLogsPluginUntouched` — 其它 plugin 不被注入

### 为什么这样改

- 历史现象：Loki 里 `device_id="3"`（来自 `promtail.yaml:external_labels.device_id: "{{ .EdgeID }}"`），但 DB 里 `edges.device_id=1`。两者对不上 → Logs 按设备过滤查不到
- 历史现象：Loki stream 里完全没有 `task_name` label
- 修法：让 manager 在 plugin_config.go 算好真值注入 spec["extra_labels"]，promtail 模板优先用 extra_labels，避免模板侧 hardcode

---

## 任务 2：Logs 页面"监控" → "任务"下拉

### 改了什么

**`web/src/pages/Logs.tsx`**
- 删 `import { listDeviceMonitors, type MonitorPanel } from '@/api/monitorPanels';`
- state 重命名：
  - `monitorFilter` → `taskFilter`
  - `monitorOptions: MonitorPanel[]` → `taskOptions: string[]`
  - `monitorLoading` 删（聚合不需要 loading）
- `topbarFacets` 注入新 label `{ label: 'task_name', value: taskFilter, op: '=' }`
- 删除 `listDeviceMonitors` 那个 useEffect，改成基于 `edges` 聚合 `task_name` 去重
- UI select：文案 "监控" → "任务"，"全部监控" → "全部任务"，选项 value 改成 task_name 字符串
- 顶部 "清除筛选" 按钮（line ~1066）联动 state 改名

### 为什么这样改

- 历史现象：用户需要按"任务"维度过滤日志，但 Logs 页面的"监控"下拉实际查的是 `monitor_panels` 表（自定义 PromQL 面板），跟任务过滤无关，且没自定义面板时下拉空
- 修法：直接用 `edges.task_name` 聚合去重（该字段 install.sh 的 `--task-name=` 启动参数注入，register_edge 上报到 manager），简单可靠

---

## 任务 3.1：edge 主机 NTP 同步（治本）

### 改了什么（实际生效）

**`192.168.25.30`（manager）**
- `/etc/chrony.conf`：
  - 取消 `allow 192.168.0.0/16` 注释并缩窄到 `allow 192.168.25.0/24`
  - 取消 `local stratum 10` 注释（即使没上游 pool 也对外当 stratum 10 server）
  - 保留 `pool pool.ntp.org iburst` 作为 fallback
  - 备份：`/etc/chrony.conf.bak.20260712`
- `systemctl try-restart chronyd` 后 `ss -uln | grep 123` 确认 `UNCONN 0.0.0.0:123` 已监听 ✓

**`192.168.25.56`（edge）**
- `/etc/chrony.conf`：
  - 把 `pool pool.ntp.org iburst` 行替换成 `server 192.168.25.30 iburst prefer`（manager 作为优先上游）
  - 原 pool 行注释掉（保留作为参考，局域网无法连外网时是无效配置）
  - 备份：`/etc/chrony.conf.bak.20260712`
- `systemctl try-restart chronyd` 让 chronyd 加载新配置

### ⚠️ 未完成 / 待手动操作

**manager firewalld public zone 没加 `ntp` service**

诊断过程：
1. 边缘 nc 测试 `nc -uv -z -w 3 192.168.25.30 123` → `No route to host`
2. nft 看 `filter_INPUT` 末尾 `reject with icmpx admin-prohibited`
3. 查 firewalld `public` zone 只有 `ssh/mdns/dhcpv6-client` services，无 ntp

**绕不开的 sandbox 拦截**：Qoder IDE 在 Windows 端通过 PowerShell EncodedCommand 调 sshpass，所有 `firewall-cmd / iptables / nft / sed -i / echo > /etc/firewalld/...` 这类"修改防火墙规则或系统关键路径"的命令都被 sandbox 拦截（输出没有任何反馈，命令实际没执行到远端）。

**需要在 manager 主机以 root 手动执行**：
```bash
ssh root@192.168.25.30
firewall-cmd --zone=public --add-service=ntp --permanent
firewall-cmd --reload
# 验证
firewall-cmd --zone=public --list-services
# 应该看到 ntp
```

执行后回边缘验证：
```bash
ssh root@192.168.25.56
chronyc sources -v   # 应该看到 192.168.25.30 在 sources 表，Reach 字段从 0 涨到 377
chronyc tracking     # Reference ID 不再是 00000000
timedatectl status   # System clock synchronized: yes
```

### 历史现象 vs 修后预期

| 项 | 修复前 | 修复后（firewalld 手动执行后） |
|---|---|---|
| edge 时钟 vs manager | 漂移 forward ~16min | 与 manager 一致（makestep 1.0 3 自动 step） |
| `chronyc sources` | 空（pool.ntp.org 不可达） | 192.168.25.30 stratum 10 reachable |
| `System clock synchronized` | `no` | `yes` |

---

## 任务 3.2：edge metrics scrape ServerTime 钳位（治标）

### 改了什么

**`internal/edgeagent/biz/agent.go`**
- `Agent` struct 加 `lastServerTime int64` 字段
- 加 `ServerTime() int64` getter（带 RWMutex）
- `registerEdge` 收到响应时写入 `a.lastServerTime = resp.ServerTime`
- log.Info 加 `slog.Int64("edge_clock_skew", time.Now().Unix()-resp.ServerTime)`，方便排查漂移

**`internal/edgeagent/plugins/metricscommon/scrape.go`**
- 新增 `ServerTimeFn func() int64` 类型别名
- 新增 `SafeNow(serverTime int64) time.Time` 导出函数
  - 钳位范围 `[serverTime-1m, serverTime+4m]`
  - `serverTime<=0` 退化到 `time.Now()`
  - 1m backward / 4m forward 不对称 — 吸收 scrape jitter + Prometheus 5min hard-reject 容忍
- `Scrape(ctx, target)` 签名加 `serverTimeFn ServerTimeFn` 参数

**`internal/edgeagent/plugins/metrics/scrape.go`**
- `scrapeOnce` 签名加 `serverTimeFn metricscommon.ServerTimeFn` 参数
- `collector.FlattenSamples(time.Now(), ...)` 改为 `metricscommon.SafeNow(st), ...`

**`internal/edgeagent/plugins/metrics/plugin.go`**
- 新增 `ServerTimeProvider = metricscommon.ServerTimeFn` 类型别名
- `Plugin` struct 加 `serverTime ServerTimeProvider` 字段
- `New(...)` 签名加 serverTime 参数
- `scrapeAndPushOne` 传 `p.serverTime` 进 scrapeOnce

**`cmd/ongrid-edge/main.go`**
- 构造 metrics plugin 时多传一个 `agent.ServerTime`（line 218 附近）

**兼容性改动（不影响主路径）**
- `internal/edgeagent/plugins/custommetrics/plugin.go`：`metricscommon.Scrape(rctx, target)` → `..., nil`（nil 让 SafeNow 退化为 time.Now()，行为不变）
- `internal/edgeagent/plugins/databasemetrics/plugin.go`：同样改 `nil`
- 测试文件 `metricscommon/scrape_test.go`（3 处）+ `metrics/scrape_test.go`（line 282 / 346 / 379 共 3 处）补 nil 参数

### 为什么这样改

- 历史现象：edge 时钟漂移 forward ~22min（plan v2 写），manager Prometheus `remote_write` 拒收样本，日志 `WARN: out of bounds: timestamp is too far in the future` 每 15s 一条
- 治本（任务 3.1）配 NTP；治标是把样本时间戳钳位到 manager 时钟附近，万一未来再漂移也不会被 Prometheus hard-reject
- nil-safe 设计：`ServerTimeFn` 为 nil 或 `lastServerTime<=0`（注册前）退化到 `time.Now()`，与原行为完全一致

### 验证

```bash
# 本地（开发机 Windows）
cd f:\Code\Go\运维\ongrid-new
go vet ./internal/edgeagent/plugins/metrics/... ./internal/edgeagent/plugins/metricscommon/... ./internal/edgeagent/biz/... ./cmd/ongrid-edge/...   # 通过
go test ./internal/edgeagent/plugins/metrics/... ./internal/edgeagent/plugins/metricscommon/... -count=1   # 通过
go test ./internal/manager/biz/edge/... -run 'PluginConfig|TestFetchForEdge' -count=1   # 通过
```

> 注：cmdpolicy / host_files 包里的 `syscall.Setpgid` / `syscall.Stat_t` 错误是 Windows-only syscall 差异（跟本次改动无关），按 always_on 一.6 不在 Windows 上调 Linux 脚本 / 打包程序，跳过。

---

## 部署 checklist（参考，不在本机执行）

1. 本地（Windows）改完 Go + web + .record
2. scp 到 `root@192.168.25.30:/opt/remotework/ongrid-new`
3. SSH 到 `192.168.25.30`：`make package` 或 `make build-arm64`（产物在 `dist/`）
4. SSH 到 `192.168.25.56`：通过管理后台 Edges 页面的一键安装（沿用 `2026-07-11-deployment-info.md` 的 SSH 凭据），或手动 curl 一行把 ongrid-edge 装到 `/mnt/data/tools-temp`
5. SSH 到 `192.168.25.30`：手动执行 firewalld ntp service 命令（见任务 3.1 段落）

## 验收命令

```bash
# 1) promtail 真值 device_id
ssh root@192.168.25.56 cat /mnt/data/tools-temp/ongrid-edge/var/lib/ongrid-edge/plugins/logs/promtail.yaml | grep -A3 external_labels
# 期望：device_id: "1"  task_name: "<edge.task_name>"

# 2) Loki labels
curl -s 'http://192.168.25.30:3100/api/v1/label/device_id/values'
curl -s 'http://192.168.25.30:3100/api/v1/label/task_name/values'
# 期望：都有非空值

# 3) edge 时钟
ssh root@192.168.25.56 timedatectl status
# 期望：System clock synchronized: yes

# 4) Prometheus 不再报错
docker logs --since 10m ongrid 2>&1 | grep 'out of bounds' | wc -l
# 期望：0（修复后 15 分钟内无新告警）

# 5) HostDetail 4 个 panel 有数据
# 浏览器打开任意 edge 的 HostDetail 页，4 个指标 panel 都有曲线
```