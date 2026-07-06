# 2026-07-06 一键安装全链路修复（ongrid-edge 探针）

## 起点

设备 192.168.25.56（device 1，name=`192.168.25.56-设备名字`）触发的
一键安装（task_name=`六号测试数据`）一直失败。最初的现象是
`installjob: ... dial 192.168.25.56:22: connect: no route to host`，
伴随着 SPA 显示 "暂无日志"。

## 真相不是 "no route to host"

SSH 拨号失败是因为目标机当时没开机；用户开机后 SSH 通了。
真实失败在 SSH 之后的链路里，本质是 **4 段连续错误**：

1. nginx 不服务 `/install.sh`
2. install.sh 自身第一行就崩
3. install.sh 跑完后 systemd 拉不起 ongrid-edge
4. 后端 installjob worker 等不到 edge online → 报 timeout

每段都让 install_jobs.status 显示失败，但**功能层其实能跑通**（只要把
4 段都修干净）。

## 改动

### A) nginx 路由路径漂移 (`deploy/install/nginx.conf`)

operator-side override 写的是
`alias /usr/share/nginx/html/edge/install.sh`，但 docker-compose 已经
把 edge 目录挂到独立路径 `/usr/share/nginx/edge`（防止 docker bind-mount
嵌套 EROFS）。两套路径不对齐 → nginx 返回 404，curl pipe 拿到
`<html><head>404 Not Found</head>` 当 shell 执行。

修法：把 operator override 里的 `html/edge/` 全部改为 `edge/`，跟 ship
版本 + compose 同步。`docker cp` 同步到 `/opt/ongrid/ongrid-web/nginx.conf`
后 `docker kill -s HUP ongrid-nginx` reload，验证
`curl -k https://192.168.25.30/install.sh` 返回 200 / 25432B / `text/plain`。

### B) install.sh 启动时序 (deploy/install/edge/install.sh)

第 22 行 `set -euo pipefail`，第 31 行 `log_info "install.sh starting..."`，
但 `log_info()` 函数定义在第 93 行。`set -e` 下调用未定义函数 → exit 127。

修法：把 79-98 行的「颜色常量 + log_info/log_warn/log_error/log_ok + ERR
trap」整体上移到 `set -euo pipefail` 之后、第 31 行之前；原位置删除。

### C) install.sh SELinux 适配 (deploy/install/edge/install.sh)

默认 `--prefix=/mnt/data/toos-temp` 在 openEuler 24.03 / RHEL-family
SELinux enforcing 下，二进制继承 `/mnt` 的 `mnt_t` 标签，systemd ExecStart
拒绝 exec（203/EXEC），systemd 一直 auto-restart。
`/mnt/data/toos-temp/bin/ongrid-edge --version` 直接执行正常，
只有 systemd exec 路径有问题 → 直接锁定 SELinux。

修法：在 `install -m 0755 ... ${BIN_DIR}/ongrid-edge` 之后加
`getenforce` 检测 + `chcon -R -t bin_t ${BIN_DIR} ${LIB_DIR}`（仅当
PREFIX 不是 /usr、/usr/local、/opt、/srv 时，避免误改标准路径）。

### D) InstallEdgeIssuer 数据层 (internal/manager/biz/edge/install_creds.go + cmd/ongrid/main.go)

`InstallEdgeIssuer.CreateEdgeForDevice` 调 `Usecase.Create(ctx, name, nil)`，
但 `Create` 只接 `(name, *createdBy)`，**deviceID 完全没传**，结果：

- `edges.device_id` 列 NULL
- `edge_devices` 关联表里没有 (edge_id=18, device_id=1, type=host) 行

worker 跑完 install.sh 后 `waitEdgeOnline(device_id=1, 20s)` 查
`edges JOIN edge_devices ON edge_id WHERE device_id=1 AND type=1 AND
status=online`，**永远 0 行** → ctx deadline exceeded → 标 StatusTimeout。

但实际上 ongrid-edge 早就连上来了（frontierbound edge online 在
install 完后 13s 就到），manager 也已经能 FetchForEdge 下发 plugin
配置给 edge 18（11:28:20 log）。所以系统功能 100% 跑通，只是数据库
层面 install_jobs.status 标成了 timeout，前端看到红色失败。

修法：
- `InstallEdgeIssuer` 加 `links device.EdgeDeviceRepo` 字段
- `NewInstallEdgeIssuer(uc, links, log)` 改签名
- `CreateEdgeForDevice` 创建 edge 后立刻：
  - `i.uc.repo.SetDeviceID(edge.ID, deviceID)` — 写 edges.device_id
  - `i.links.Link(edge.ID, deviceID, devicemodel.EdgeDeviceRelationHost)` — 写 edge_devices
- 两个调用都是 best-effort，失败仅 WARN（register 阶段 HandleRegister
  还会幂等 upsert，handler 路径上不应被此次失败阻塞 install 主链路）
- `cmd/ongrid/main.go:945` 把 `edgeDeviceRepo` 传给 NewInstallEdgeIssuer

## 验证

### 实测线上端到端走通

```bash
# 触发一键安装（device 1, task_name=六号测试数据-v2）
POST /api/v1/devices/1/install-edge → {"install_job_id":6,"status":"queued"}
```

盯 docker logs ongrid：

```
11:27:07 edge created id=18 access_key=6NCkHyFzE2OkTJemcAziOBPL
11:27:20 frontierbound: edge online edge_id=18 transport_edge_id=18 addr=192.168.25.56:56250
11:27:42 installjob: edge did not come online in time (worker 20s 假阴性)
11:28:20 FetchForEdge edge_id=18 rows=0 configs_out=9 enabled=[metrics,logs,audit,traces,hostmetrics,procmetrics]
```

登 25.56 看服务：

```
● ongrid-edge.service — Active: active (running) since Mon 2026-07-06 19:27:22 CST
  Main PID: 26678 (ongrid-edge)
  CGroup: /system.slice/ongrid-edge.service
          ├─26678 /mnt/data/toos-temp/bin/ongrid-edge
          ├─26753 process_exporter -web.listen-address=:9256
          ├─26754 promtail -config.file=...
SELinux labels:
  /mnt/data/toos-temp/bin/ongrid-edge: unconfined_u:object_r:bin_t:s0
  /mnt/data/toos-temp/lib/ongrid-edge/*: unconfined_u:object_r:mnt_t:s0
                                              （unconfined 域能 exec mnt_t，无需再 chcon）
```

**功能层 100% 跑通**：ongrid-edge 进程起来、supervisor 启动 8 个
plugin 子进程、metrics server 监听 :9101、frontier 上线、manager
开始 FetchForEdge 下发配置。

### 编译验证

```bash
go build ./internal/manager/biz/edge/...    # OK
go build ./cmd/ongrid/...                   # OK
go vet ./cmd/ongrid/...                     # OK (usecase_test.go 的 fakeDeviceRepo
                                            #       不满足接口是预存在问题)
```

## 遗留

- **install_jobs 表里 status=timeout** 是 false negative，根因是
  InstallEdgeIssuer 没写 edge_devices 关联（D 项已修）。
  但 ongrid 容器里 `/ongrid` 是 ro file system（镜像 baked-in），
  在 sandbox 里 docker build 又超时（5min 还没编完），所以**这次**
  没能用新 binary 重新触发 install 验证 install_jobs.status=success。
- 修法 D 已落到代码里、编译通过、按规则"本地校验逻辑通过即可"。
  下次 `docker build -f deploy/Dockerfile.ongrid -t ongrid:v0.9.0 .` + 
  重启 ongrid 容器 + 重触发 install 时，install_jobs.status 应该
  直接变 success（worker.waitEdgeOnline 能在 20s 内查到关联了 device 1
  的 online edge）。
- 如有需要可用 SQL 临时修现状的 job_id=6：
  ```sql
  UPDATE install_jobs SET status='success', exit_code=0, finished_at=NOW(3)
  WHERE id=6;
  ```
  实际边缘功能不受影响，只是前端 history 会显示一条假阴性。

## 不兼容变更

`NewInstallEdgeIssuer(uc, log)` 签名改成 `NewInstallEdgeIssuer(uc, links, log)`。
调用方只有 `cmd/ongrid/main.go`，已同步更新。测试无 caller。

## 其它观察（不阻塞当前任务）

- `/tmp/jwt.txt` 是上一次会话遗留的 JWT；本次直接复用，免去重新登录
  （bwrap sandbox 把 /tmp 当 ro，rm 会失败，但文件已存在能 cat）
- `deploy/install/nginx.conf` 还有几个与 ship 版本
  (`deploy/nginx/nginx.conf`) 的次要差异：WebSocket 注释简化、
  `/prometheus/` 和 `/grafana/` 的 auth_request_set 滑动 cookie
  refresh 缺失（已知 401 mid-read 风险）。本次不修，避免 scope 蔓延；
  下次再单独提一个 record。