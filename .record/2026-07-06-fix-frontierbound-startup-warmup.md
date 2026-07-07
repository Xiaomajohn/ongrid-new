# 2026-07-06 frontierbound 启动 race 修复(5s warmup)

## 背景

P0 现象:manager 容器重启后,frontierbound 启动 race 导致 push / heartbeat 报 "record not found"。13:07 那次启动后,7 分钟内(13:14:21 前)broker 端 service 表只对 `register_edge` 可见,其他 6 个 RPC(`heartbeat` / `push_host_metrics` / `push_prom_samples` / `get_plugin_configs` / `shell_output` / `shell_exit`)仍在"待 register"状态;13:14:21 首个 edge 上线 register 成功后,后续 push/heartbeat 持续失败,manager 端 grep 不到任何 handler log,`devices.last_seen_at` 冻结。

临时止血:`docker compose restart ongrid` 重启后现象消失(14:27 启动后 FetchForEdge 持续 30 min 稳定 27 次,0 push 失败)。

## 根因分析(已查源码 + 验证)

### fbsvc 内部机制

- `fbsvc.NewService(dialer, opts...)` 内部走 `client.NewRetryEndWithDialer`(`/root/frontier/api/dataplane/v1/service/service_end.go:67-112`),即 geminio **RetryEnd**(异步拨号 + 后台重连)
- `serviceEnd.Register`(service_end.go:215-228)委托给 `geminio.End.Register`,底层 `stream.Register`(geminio `application/rpc.go:42-67`):
  ```go
  pkt := sm.pf.NewRegisterPacketWithSessionID(sm.dg.DialogueID(), []byte(method))
  sync := sm.shub.New(pkt.ID())
  sm.writeInCh <- pkt  // ← 立即把 register 包写进 geminio write 通道
  select {
  case event := <-sync.C():  // ← 等 broker ACK
      if event.Error != nil { sm.delLocalRPC(method); return event.Error }
  }
  ```
- `RetryEnd.Register` 把成功 register 的 method 记到 `re.rpcs` map(用于重连后 reinit 重新推送)
- 关键观察:Install 末尾的 `c.Register(ctx, ...)` 同步等 broker ACK 才返回,所以**manager 端"看到"已注册不等于 broker 端 service 表已对外可见**。ACK 跟 broker 端 service 表的"对外可见"之间存在 race

### 源码证据(13:07 启动 vs 14:27 启动)

- **13:07 启动 log**:有 `frontierbound: connected` + `frontierbound: handlers installed`(c.Register 全部 sync 等 ACK 返回)
- **14:27 启动 log**:同样有这两个 log
- **两次启动行为完全一致**,c.Register 都返回 nil(无 error)
- 13:07 启动后 7 分钟内 broker 端 service 表只对 `register_edge` 可见,其他 6 个 method 仍在 "待 register" 状态 → **broker 端 lazy register 行为**:**只有当 edge 真正 dial broker 触发 service 表更新时,manager service 端 register 的 method 才"对外可见"**
- 14:27 启动时 broker 端 service 表初始化比 13:07 快(可能跟 broker 容器状态 / 缓存命中有关)

### agent 端已填 `EdgeID`(自愈代码已就位)

- `internal/edgeagent/biz/agent.go:411, 487, 512` —— push / heartbeat body 都填了 `EdgeID: a.EdgeID()`
- `internal/manager/service/frontierbound/handlers.go:233-236, 293-296, 340-343` —— manager 端 `in.EdgeID != 0` 时自动 `bindEdgeTransport` 自愈
- 自愈机制在"manager 重启后 transport→canonical 映射丢失但 edge 端 a.EdgeID() 还能拿到正确值"时能 work
- 但 13:07 那次 case 是 broker 端 service 表都空了,push 根本到不了 manager,自愈机制没机会触发

## 修复方案

**最小成本方案:在 Install 之后、HTTP server 启动前插 5s warmup**

### 改动文件

1. `internal/pkg/config/config.go`
   - `FrontierClientConfig` 加 `Warmup time.Duration` 字段
   - `Load()` 加 `c.FrontierClient.Warmup = getEnvDuration("ONGRID_FRONTIER_WARMUP", 5*time.Second)`
   - 默认 5s,env `ONGRID_FRONTIER_WARMUP` 可调;`<=0` 关闭

2. `cmd/ongrid/main.go` Install 成功后(SetNotifier / SetDatabaseMetricsSecretWriter 之后,HTTP server 之前)
   ```go
   if !cfg.FrontierClient.Disabled && cfg.FrontierClient.Warmup > 0 {
       log.Info("frontierbound: warmup window — letting broker flush service table",
           slog.Duration("d", cfg.FrontierClient.Warmup))
       select {
       case <-time.After(cfg.FrontierClient.Warmup):
           log.Info("frontierbound: warmup complete")
       case <-rootCtx.Done():
           log.Info("frontierbound: warmup aborted by shutdown")
           return
       }
   }
   ```
   - 响应 rootCtx 取消,SIGTERM 不会被暖 5s 拦住
   - 单线程同步代码,Install 完成到 HTTP server 启动之间没有任何 goroutine,并发安全

### 为什么不选更复杂的方案

候选方案对比(详见 subagent 4 评估):

| 方案 | 改动成本 | 风险 | 选不选 |
|---|---|---|---|
| A. WaitReady+fatal(主动探 broker) | frontierbound 加 ready.go ~80 行 | 中 | ❌ 治标不治本,broker 重启场景无法覆盖 |
| B. **main.go 5s sleep** | main.go 加 ~20 行 | **低** | **✅ 选** |
| C. Watchdog + Re-Register on reconnect | frontierbound 改造 + delegate 钩子 ~250 行 | 中 | ❌ retry-end 内部已有 memorize + reinit 重发机制,加这层冗余 |
| D. fork fbsvc 改 Register 为 sync push | go.mod replace + fork ~50 行 | 高 | ❌ 改错层,治不到根 |
| E. Ready Barrier + Watchdog(组合) | frontierbound ~250 行 | 中 | ⏸️ 留作未来,先上 B 止血 |

B 方案是当前已知的最低成本止血。后续如果再出现"broker 重启后 service 表 7 分钟不完整"或"stale transport 持续推 heartbeat 但 canonical 映射没建"的 case,再上 E。

## 部署

- 编译:`go build -o /tmp/ongrid-v6 ./cmd/ongrid/` —— 60MB,md5 `f91a6742760f860142e868ef904b1351`
- 替换:bind-mount `/opt/ongrid/ongrid-app/ongrid`(host)→ `/ongrid`(container)
  - 生产当前 v5 md5 `5cf4df729432e6c5b36ff7d19a576cbc`
  - `docker compose -p ongrid stop ongrid` → `cp /tmp/ongrid-v6 /opt/ongrid/ongrid-app/ongrid` → `docker compose -p ongrid start ongrid`
  - 容器和 host md5 一致:`f91a6742760f860142e868ef904b1351`

## 验证(2026-07-06 23:11 启动)

启动 log 关键 3 行:
```
2026-07-06T23:11:01.196Z frontierbound: connected       addr=frontier:40011 service_name=ongrid-manager
2026-07-06T23:11:01.211Z frontierbound: handlers installed
2026-07-06T23:11:01.211Z frontierbound: warmup window — letting broker flush service table  d=5s
2026-07-06T23:11:06.212Z frontierbound: warmup complete
```

5s warmup 准时完成,前后无 error。

frontier broker 端 service-end RPC 表(13:07 失败的 case 在这里能看到了):
```
I0706 23:11:01.210007 service remote rpc registration, rpc: heartbeat            serviceID: 7659556463544249133
I0706 23:11:01.210391 service remote rpc registration, rpc: push_host_metrics    serviceID: 7659556463544249133
I0706 23:11:01.210706 service remote rpc registration, rpc: push_prom_samples    serviceID: 7659556463544249133
I0706 23:11:01.211016 service remote rpc registration, rpc: get_plugin_configs  serviceID: 7659556463544249133
I0706 23:11:01.211377 service remote rpc registration, rpc: shell_output         serviceID: 7659556463544249133
I0706 23:11:01.211756 service remote rpc registration, rpc: shell_exit           serviceID: 7659556463544249133
```

6 个 RPC 全部 register 成功。双向 packets 持续增长(manager 12→16,edge 15→19)。

## 已知遗留问题(非本次修复 scope)

1. **stale transport 7659556450659347244** 持续 30s 一次推 heartbeat,manager 端 `edge_id=0` not found
   - 原因:broker 端残留的 stale edge connection(用户已删 25.56 edge),manager 重启后 GetEdgeID 没被触发,transport→canonical 映射没建
   - 影响:每 30s 一条 WARN log,但不影响 manager→broker 推送(manager 端 FetchForEdge 等路径完全正常)
   - 后续方案:stale transport 应该在 broker 端加 connection TTL,manager 端可以加 watchdog 强制 evict

2. **record not found 噪声**(20 条 setting/store/repo.go:33 / 1 条 alert/store/repo.go:138 / min 级)
   - 跟 push 路径无关,是后台 timer 周期性查 setting key 找不到的"已存在但 noise"行为
   - 后续可加 zap filter 抑制 setting/alert store 的 record not found 日志

## 回滚

如果 warmup 引入新问题:
1. `cd /opt/ongrid && docker compose -p ongrid stop ongrid`
2. `cp /opt/ongrid/ongrid-app/ongrid.bak.v5 /opt/ongrid/ongrid-app/ongrid`(如果保留 v5 备份)
3. `docker compose -p ongrid start ongrid`
4. 或 `ONGRID_FRONTIER_WARMUP=0` env 关闭 warmup(下次启动生效)
