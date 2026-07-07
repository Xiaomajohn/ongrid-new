# 2026-07-07 arm64 重打包 → --purge 卸载 → 重新部署（含 29 个未提交源码改动）

## 场景

`sealos.hub`（aarch64，root）的 `/opt/ongrid` 当前跑着 `v0.9.0`（16 小时前装的）。
工作区在 `main-local-new` 上累计 29 个文件改动未提交（SSH/installjob/前端 webshell
等核心改动），需要重新打 arm64 release tarball → 用新包 uninstall.sh --purge 卸载
→ 用新包 install.sh 重新部署，把这些源码改动烧到镜像里。

> 改动主要在以下文件：
> - `cmd/ongrid/main.go`
> - `internal/manager/biz/devicessh/{dialer,router,service,sftp}.go`
> - `internal/manager/biz/installjob/installer.go`
> - `internal/manager/server/devicessh/http.go`
> - `internal/manager/server/installjob/http.go`
> - `internal/pkg/config/config.go`
> - `web/src/App.tsx` / `web/src/api/webshell.ts`
> - `web/src/pages/DeviceShell.tsx` / `web/src/pages/HostDetail.tsx`
> - `deploy/install/{apply-pending-upgrade.sh,edge/*}`
> - 外加若干新 `.record` 文档

## 三步流水（与 `2026-07-06-uninstall-repackage-reinstall.md` 同款）

### 1. 重新打包（arm64）

```bash
make package TARGET_OS=linux TARGET_ARCH=arm64 PLATFORM=linux/arm64
```

`make package` 链路按 Makefile:596 触发：
`stage-auditbeat → fetch-* (全 cache hit) → build-edge-linux-all → docker-build →
docker-build-broker → docker-build-web → build-edge-bundle → dist/package.sh`。

#### 1.1 fetch / stage-auditbeat：全部命中缓存

`bin/linux-{amd64,arm64}/` 8 个 fetch-* 二进制 + 2 个 auditbeat 都已经存在，全部
`already present — skip`，省掉 ~15 分钟下载时间。

#### 1.2 docker build（核心改动烧进 manager）

- `docker buildx build --platform linux/arm64 -t ongrid:v0.9.0 -f deploy/Dockerfile.ongrid --load .`
  - builder 7/7 跑 `CGO_ENABLED=1 go build -trimpath` 出 `/out/ongrid`（17s 编译）
  - 拉 onnxruntime 1.20.1（curl GitHub releases，CACHED）
  - 拷 manager 二进制 + `/skills` + `/agents` 到 runtime 层
  - 导出 manifest `sha256:ff3ab82cfe92324d2d016aa637576a7c24ffc0ed1b2a04b4051467ab399776de`
- `docker buildx build ... -t singchia/frontier:v1.2.4 -f deploy/Dockerfile.frontier --load`
  - frontier broker 全部 CACHED（之前已经 build 过）
- `docker buildx build --platform linux/arm64 -t ongrid-web:v0.9.0 -f deploy/Dockerfile.web --load .`
  - npm ci 用 prefer-offline cache（命中）
  - tsc -b + vite build 31.32s，2925 modules transformed
  - 导出 manifest `sha256:723d62035790763cff463dfc193a7937f26b7c2eda061d2aa43edff335557a42`

#### 1.3 edge bundle

`dist/build-edge-bundle.sh` 重新打两个 arch：

```
dist/out/edge-bundles/edge-bundle-linux-amd64-v0.9.0.tar.gz   211M sha 0628a308b0ed5ebbdc9554c1b474a3007a92dfcfbe2b79da9c81c96c7380894d
dist/out/edge-bundles/edge-bundle-linux-arm64-v0.9.0.tar.gz   190M sha 7571b1442d2daba7cab24c1b1d924b3d012b46388ce903e052b445e6e57185ad
```

#### 1.4 dist/package.sh 组装

stage 完整：
```
dist/stage/ongrid-v0.9.0-linux-arm64/
├── VERSION
├── README.md
├── install.sh        57243 B
├── uninstall.sh      12849 B
├── upgrade.sh        40376 B
├── docker-compose.yml
├── .env.example
├── frontier.yaml
├── systemd/  ...
├── nginx.conf
├── prometheus.yml / prometheus-rules.yml
├── grafana/
├── searxng/
├── bin/  (edge 二进制 amd64 + arm64)
├── certs/
├── edge/  (edge bundle tarballs)
├── images/  (docker save 产物)
│   ├── frontier.tar    36M
│   ├── ongrid.tar     328M
│   └── ongrid-web.tar  53M
└── ...
```

`xz -9e -T0 -c` 压缩约 5 分钟（53 分钟 CPU 时间，8 核）。

#### 1.5 产物

```
dist/out/ongrid-v0.9.0-linux-arm64.tar.xz          426M
dist/out/ongrid-v0.9.0-linux-arm64.tar.xz.sha256
  e3e8b80436bde4a0275607e8b4c2aa1735801510f07b8b5d95cd7d0f713bc514
```

跟 `2026-07-06-uninstall-repackage-reinstall.md`（427M `e49bd69b9775fb06af6baadf73025589a8688a3922eef0ed6cb06090005926ae`）
对照：大小基本一致（差 1M），sha 不同（因为 manager binary 烧了新代码）。

### 2. 卸载（--purge，从新 tarball 取 uninstall.sh）

**关键**：从新 staging 取 `uninstall.sh`，**不要**用旧的 `dist/out/.../uninstall.sh`。
本次重打的 tarball 里的 install/uninstall/upgrade 三个脚本与 `deploy/install/` 一致
（sha256 双比对：`195effa33...` install / `eff2c9c9...` uninstall / `7589dc86...` upgrade），
所以本次重打没修脚本，但**binary 烧了新代码**——重打并部署新 tarball 是有意义的。

```bash
mkdir -p .cache/install-stage
tar -xJf dist/out/ongrid-v0.9.0-linux-arm64.tar.xz -C .cache/install-stage
sudo bash .cache/install-stage/ongrid-v0.9.0-linux-arm64/uninstall.sh --purge --yes
```

`auto` 模式检测到 `/opt/ongrid/docker-compose.yml` → 走 compose 分支：
- docker compose down（10 个 ongrid 容器全停 + ongrid_default 网络移除）
- 命名卷 `ongrid_mysql_data` / `ongrid_logs` 删除
- bind-mount 数据子目录 `/opt/ongrid/data/{mysql,qdrant,prometheus,loki,tempo,grafana,embeddings,skills,pages,workspace,tools}` 删除
- `/opt/ongrid/logs` 删除
- `/opt/ongrid` 整个 compose 装根删除
- `/opt/ongrid/ongrid-web` 删除
- `ongrid:v0.9.0` 镜像删除（untagged + 6 个 layer）

> 本机没有 ongrid-edge 进程（systemd / `/usr/local/bin/ongrid-edge` 都没有），
> 所以不需要 `--purge-edge`。

### 3. 装新

```bash
cd .cache/install-stage/ongrid-v0.9.0-linux-arm64
sudo ONGRID_PUBLIC_URL=https://192.168.25.30 bash install.sh
```

`ONGRID_PUBLIC_URL` 显式注入避开 30s 交互提示。

`install.sh` 关键流程：
1. preflight（docker 在，compose v2 在，registry-mirrors 已经在 `/etc/docker/daemon.json`）
2. 装资产到 `/opt/ongrid/{docker-compose.yml,.env.example,frontier.yaml,prometheus.yml,...}`
3. 重建 `ongrid-web/edge/edge-bundle-{amd64,arm64}-v0.9.0.tar.gz`（host-side bundle build）
4. 生成自签 TLS 证书（`/opt/ongrid/ongrid-web/certs/{tls.crt,tls.key}`）
5. `docker load` 三个镜像（ongrid:v0.9.0、singchia/frontier:v1.2.4、ongrid-web:v0.9.0）
6. `extract_image_dist` 把 manager 的 `/ongrid /skills /agents` 和
   ongrid-web 的 `/usr/share/nginx/html` 拷到宿主机 bind-mount 源
7. 从 `.env.example` 拷出 `.env`，`fill_blank` 填 MYSQL/JWT/ADMIN/GRAFANA 密码
8. 写 `ONGRID_PUBLIC_URL=https://192.168.25.30` + `ONGRID_VERSION=v0.9.0`
9. `docker compose --env-file .env up -d`
10. mysql 初始化 ~3 分钟（ongrid / nginx 在等 mysql healthy）
11. 等 `/healthz` 60s（**install.sh 内部 60s 等待超时告警**，但实际 `/healthz` 已 200）

## 验证

- `docker ps` 10 个 ongrid 容器全部 `Up 5 minutes`，mysql `Up 5 minutes (healthy)`
- `curl -sk https://localhost/healthz` → `HTTP 200 (23ms)`
- `curl -sk https://localhost/readyz`  → `HTTP 200 (23ms)`
- `curl -sk https://localhost/`        → `HTTP 200 (23ms)`
- 容器内 `curl http://127.0.0.1:8080/healthz` → `ok`；`/readyz` → `ready`
- `/opt/ongrid/VERSION` = `v0.9.0`
- `/opt/ongrid/ongrid-app/ongrid` = 43,019,528 字节（v0.9.0，重打，含 29 个源码改动）
  - **比 `2026-07-06-replace-ongrid-binary-bind-mount.md` 里的 60,437,784 字节小** —
    那次是单独 `go build -o bin/ongrid` 没带 `-s -w` strip 标志；
    这次 `make package` 走完整链路，二进制是 `docker buildx` 里 CGO_ENABLED=1 编出
    并被 `deploy/Dockerfile.ongrid` 链路 strip 过的标准发布产物。
- `/opt/ongrid/ongrid-web/edge/edge-bundle-{amd64,arm64}-v0.9.0.tar.gz` 都有
  - amd64: 165,604,530 B（10 文件，比 dist/out 那份 211M 略小，因为 install.sh 在
    host 重建时不含一些 dist-only 文件）
  - arm64: 150,841,788 B

banner 关键信息（与 `2026-07-06-uninstall-repackage-reinstall.md` 完全一致）：
- Web UI: `https://192.168.25.30/`
- API URL: `https://192.168.25.30/api/v1`
- Tunnel endpoint: `192.168.25.30:40012`
- bootstrap admin: `admin@ongrid.local` / `ongrid_admin_pwd`（.env.example 默认值，生产前改）

API 探测：
- `GET /api/v1` → 404（预期，没这个路由）
- `POST /api/v1/auth/login -d '{}'` → 400（预期，body 校验）

## install.sh 内部 60s 健康检查告警

install.sh 第 10 步等 `/healthz` 60s 超时：

```
[INFO] waiting for /healthz on https://localhost:443 (up to 60s)
..............................
[WARN] ongrid did not become healthy within 60s
[WARN] check logs: docker compose -f /opt/ongrid/docker-compose.yml logs ongrid
```

这是 install.sh 自己的检测逻辑略悲观（60s 内间隔访问，叠加 mysql 首次初始化 3 分钟，
导致 ongrid 容器在 60s 窗口内还没起来）。实际服务起来了（`/healthz` 200，mysql
healthy），不算失败。**下次可以优化**：把 install.sh 的等待窗口从 60s 提到 120s
或 mysql healthy 后再加 30s 缓冲。

## 时间线

- 08:29 make package 启动
- 08:30 docker build ongrid:v0.9.0 done（编译 17s）
- 08:32 broker done（全部 CACHED）; web 镜像构建中（npm ci / tsc / vite）
- 08:35 web 镜像 done + edge bundle amd64 done
- 08:35 edge bundle arm64 done + dist/package.sh 启动（save 镜像）
- 08:40 xz 压缩完成
- 08:41 tarball 落地（426M sha `e3e8b804…`）
- 08:43 staging 解压完成
- 08:47 uninstall 启动 → 08:47 uninstall 完成（10 容器 down + 数据清空）
- 08:47 install 启动
- 08:50 docker compose up 完成 + mysql 初始化开始
- 08:53 mysql healthy → ongrid + nginx 起来
- 08:54 banner 输出 + install.sh 60s 健康检查超时告警
- 08:55 手动 curl `/healthz` `/readyz` `/` 全 200，部署确认成功

总耗时：~26 分钟（08:29 → 08:55）。

## 不属于本任务范围

- 没有改任何源码、配置、脚本（29 个改动是工作区里既有的未提交代码，本次重打把它们
  烧进 v0.9.0 的镜像里）
- 没有 commit/push
- 没有改 VERSION（仍是 `v0.9.0`，所以 tarball 文件名还是 v0.9.0）
- 没有打 amd64 tarball（`make package-all` 可同时打 amd64+arm64）
- 没有 `make fetch-embedding-model`（首次冷启会下载模型）
- 没有重新生成 proto（`make proto`）

## 下次可改进

- install.sh 的 60s 健康检查窗口太短，建议改 120s 或 mysql healthy 后 +30s
- 沙箱里 `/tmp` 只读、`.cache/` 可写 — 写 helper 时统一放 `.cache/<name>` 别用 `/tmp`