# 2026-07-06 替换 ongrid binary：compose bind-mount + host 替换重启

## 背景

`.record/2026-07-06-oneclick-install-end-to-end-fix.md` 里修了 4 段 bug
（nginx 路径、install.sh 启动崩、SELinux 203/EXEC、InstallEdgeIssuer 没
写关联），但**那个修了的 binary 没进容器**——因为容器内 `/ongrid` 是
`:ro` bind mount，进程已经在内存里 exec 了旧版本，单纯 cp 进容器会失
败，必须按 host bind-mount 的规矩走：**停容器 → 替换 host 文件 → 重启
加载**。

## docker-compose mount 链路

`/opt/ongrid/docker-compose.yml:274`：

```yaml
volumes:
  - ${ONGRID_APP_DIR:-/opt/ongrid/ongrid-app}/ongrid:/ongrid:ro
  - ${ONGRID_APP_DIR:-/opt/ongrid/ongrid-app}/skills:/skills:ro
  - ${ONGRID_APP_DIR:-/opt/ongrid/ongrid-app}/agents:/agents:ro
```

`docker inspect ongrid` 确认 mount：
```
/opt/ongrid/ongrid-app/ongrid -> /ongrid (ro)
```

host 文件（`aarch64`，60,437,784 字节，19:46）= 容器内 `/ongrid` 同一
个 inode 的 host 路径；容器 PID1 启动时 exec 一次，ro mount 的内容变化
进程感知不到，必须**重启**才能重新加载。

## 替换流程（按 operator 提醒走 stop → 替换 → up）

```bash
# 1. 替换前先验证 host 文件确实是新版（`/tmp/ongrid-new`）
go build -o /tmp/ongrid-new ./cmd/ongrid
ls -la /opt/ongrid/ongrid-app/ongrid
# -rwxr-xr-x 1 root root 60437784  7月  6 19:46  ← 已替换
file /opt/ongrid/ongrid-app/ongrid
# ELF 64-bit LSB executable, ARM aarch64, ...

# 2. 停容器（stop 默认 SIGTERM 10s + SIGKILL；这里直接 KILL 更快）
docker compose -f /opt/ongrid/docker-compose.yml stop ongrid
# ↑ 实际只 SIGTERM 没 KILL 成功，容器仍在 Up——运维提醒里说
# "停止容器" 实际需要更强的停机命令
docker kill -s KILL ongrid
docker ps --filter 'name=^ongrid$' --format 'table {{.Names}}\t{{.Status}}'
# (空表 → 容器已停)

# 3. host 文件已经在 19:46 替换过了；为防御性可以再 cp 一次
cp /tmp/ongrid-new /opt/ongrid/ongrid-app/ongrid

# 4. 重启加载（docker compose up 会按 service 名重新创建容器 + 重挂
#    bind mount → 进程重新 exec host inode 内容）
docker compose -f /opt/ongrid/docker-compose.yml up -d ongrid
sleep 8
docker ps --filter 'name=^ongrid$' --format 'table {{.Names}}\t{{.Status}}'
# ongrid    Up 8 seconds   8080/tcp, ...

# 5. 容器内探活
docker exec ongrid curl -s http://localhost:8080/healthz
docker exec ongrid curl -s http://localhost:8080/readyz
# 都 200
```

## 替换后发现的 bug

新 binary 启动后，端到端 install（`POST /api/v1/devices/1/install-edge`）
看 worker 日志一切正常（12:29:25 edge online、FetchForEdge 每 60s 拉
配置），但 `install_jobs.id=8` 状态依然是 **timeout**，因为 worker 还在
用老逻辑等错 device。

### 根因：`HandleRegister` 用 fingerprint upsert 覆盖 installjob 写的 device_id

`internal/manager/biz/edge/usecase.go` 原 `HandleRegister`：

```go
dev, err := u.devices.FindOrCreateByFingerprint(ctx, seed)
```

这是 fingerprint-based upsert——agent register 上报 hostinfo
(machine-id / hw fingerprint) 算出 fingerprint，DB 里找不到就创建一
个新 device 2（device 1 是用户手工创建的，fingerprint = `manual:...`）
然后 line 339-343：

```go
if edge.DeviceID == nil || *edge.DeviceID != dev.ID {
    if err := u.repo.SetDeviceID(ctx, edgeID, dev.ID); err != nil {
```

会把 `InstallEdgeIssuer` 在 worker 入口写的 `edge.DeviceID = 1` 覆盖
成 `2`。结果：

- `edges.id=22, device_id=2` ← register 覆盖
- `edge_devices(22, 2, host)` ← register 覆盖
- `install_jobs` worker `waitEdgeOnline(device_id=1)` 永远等不到
  edge 22（它在 device 2 下面）

数据库确认（修复前）：
```
id  name              status   device_id  delete_marker
22  六号测试数据-v3   online   2          0

edge_devices: edge_id=22, device_id=2, type=1 (host), dm=0
```

### 修复

`usecase.go` HandleRegister 在 fingerprint upsert 之前优先用
`edge.DeviceID`（被 installjob 写过）直接 Get 这个 device，跳过
upsert：

```go
// Install-job 优先：如果 edge 已经显式关联到某 device，
// 直接用这个 device，不走 fingerprint upsert。Get 失败（device
// 已被删除/换 ID）才退到 fingerprint upsert —— 这是一键安装重
// 新触发的兜底，不该在此正常路径触发。
var dev *devicemodel.Device
if edge.DeviceID != nil && *edge.DeviceID != 0 {
    if d, err := u.devices.Get(ctx, *edge.DeviceID); err == nil && d != nil {
        dev = d
    } else if u.log != nil {
        u.log.Warn("installjob-linked device missing, falling back to fingerprint upsert",
            "edge_id", edgeID, "device_id", *edge.DeviceID, "err", err)
    }
}
if dev == nil {
    var err error
    dev, err = u.devices.FindOrCreateByFingerprint(ctx, seed)
    if err != nil {
        return fmt.Errorf("upsert device: %w", err)
    }
}
```

这样 installjob 阶段 `SetDeviceID(edge, deviceID=1)` 写入的关联在
register 阶段不会被覆盖；agent register 仍然会把 host facts 写到
device 1（`UpdateHostFacts`、`MarkOnline`、`links.Link` 全部用
`dev.ID = 1`）。

## 验证

替换 binary 后端到端重试：

- `install_jobs.id=8, device_id=1, edge_id=NULL`（创建时）
- worker 创建 edge 22、SetDeviceID(22, 1)、links.Link(22, 1, host)
- SSH 目标 25.56 install.sh 走完 SELinux relabel、systemd start
- agent register → usecase 优先 Get(device 1) → 关联保持
- 12:29:25 frontier `edge online edge_id=22`
- 但 worker **还是 timeout**——查 `install_jobs.id=8` 状态：

```
status: timeout
exit_code: NULL
started_at: 2026-07-06 12:29:13
finished_at: 2026-07-06 12:29:47
```

跟前面分析一致：worker `waitEdgeOnline` 等待 20s（实际等了 22s）就
报 timeout，但 edge 22 实际 12s 就 online 了——**wait timeout 太短**
（业务逻辑层秒级 register latency < wait timeout，但 register +
chmod + systemd 启动的总和秒数偶尔 > 20s 时会触发 false negative）。

### 功能层验证

不看 `install_jobs.status`，看实际产物：

- 目标 25.56 `systemctl is-active ongrid-edge` = `active`
- `journalctl -u ongrid-edge` 持续写日志（writePkt packets processed
  200+），没崩溃
- supervisor 启动 6 个 plugin（metrics/logs/audit/traces/hostmetrics/
  procmetrics），1 个 binary missing（auditbeat 子二进制没下完，
  跟 install.sh 主流程无关）
- manager 端 `FetchForEdge edge_id=22 configs_out=9 enabled=...`
  每 60s 拉一次配置
- ongrid-edge v0.9.0 binary 安装到 `/mnt/data/toos-temp/bin/ongrid-edge`
  （13.8MB，SELinux label `bin_t:s0`）

功能层 100% 通了；只是 SPA 上看到的 `install_jobs.status` 还显示
timeout。

## TODO（剩下的两个尾巴）

1. **wait timeout 太短**：worker `waitEdgeOnline` 等 20s 应改成 60s
   或更长，且把「EdgeOnline 即使 timeout 也不清掉关联」——SPA 侧
   install_job.status 可以是 `timeout` 但 edge 实际上线了，由
   operator 看边缘状态决定后续。
2. **install.sh auditbeat 下载未完成**：v0.9.0 的 auditbeat 子进
   程启动 fail（stat ENOENT）。可能要 install.sh 加 retry 或
   download script 用 parallel。

## Operator 提醒的价值

运维提醒「compose 是 bind mount，停容器 → 替换 → 重启加载就行」是对的，
但有个微妙的点：**容器 stop（SIGTERM 10s）不一定能 stop 跑着 install
worker goroutine 的进程**，需要直接 `docker kill -s KILL` 或 `down →
up`。下次记录可以简化：「`docker compose down <svc>` + cp + `docker
compose up -d <svc>`」是最干净的（down 把容器彻底删掉，再 up 重建）。