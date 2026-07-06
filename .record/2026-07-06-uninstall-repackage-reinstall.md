# 2026-07-06 ongrid 卸载 → 重新打包 → 装新（arm64 单机一气呵成）

## 场景

开发机（`sealos.hub`，aarch64，root）上原本 `ongrid v0.9.0`（compose 模式）在跑，
本地工作树有一批未提交改动（63 个文件，覆盖 install/uninstall/upgrade/.env、edge、
proto、biz、web 等），需要把改动烧到镜像里 → 走一遍 release tarball → 装回。

## 三步流水

### 1. 卸载（--purge）

```bash
sudo bash /root/builder/ongrid-new/dist/out/ongrid-v0.9.0-linux-arm64/uninstall.sh --purge --yes
```

走 `auto` 模式检测到 compose 模式，按 `uninstall.sh:78-103` 走 compose 分支。
完整清掉：
- docker compose down（10 个容器全停）
- 命名卷 `ongrid_mysql_data` / `ongrid_logs`
- bind-mount 数据子目录 `/opt/ongrid/data/{mysql,qdrant,prometheus,loki,tempo,grafana,embeddings,skills,pages,workspace,tools}`
- `/opt/ongrid/logs`
- `/opt/ongrid` 整个 compose 装根
- `/opt/ongrid/ongrid-web` 整个 nginx inputs 装根
- `ongrid:v0.9.0` 镜像

> 注意点：本机没有 ongrid-edge 进程（systemd / `/usr/local/bin/ongrid-edge` 都没有），
> 所以不需要 `--purge-edge`。

### 2. 重新打包（arm64）

```bash
make package TARGET_ARCH=arm64 PLATFORM=linux/arm64
```

`make package` 链路按 Makefile:596 顺序触发：
`stage-auditbeat → fetch-* → build-edge-linux-all → docker-build → docker-build-broker → docker-build-web → build-edge-bundle → dist/package.sh`。

#### 2.1 路上的坑：auditbeat 二进制缺失

首次跑在 `stage-auditbeat` 直接 hard-fail（按设计，Makefile:373-392 / dist/package.sh 一致）：

```
[auditbeat] error: resource/auditbeat/linux-amd64/auditbeat missing
[auditbeat] error: resource/auditbeat/linux-arm64/auditbeat missing
make: *** [Makefile:375: stage-auditbeat] 错误 1
```

`resource/auditbeat/<arch>/` 子目录里只有 `.gitkeep` 占位文件，auditbeat 二进制
要从 Elastic 官方下载页下载（按 `resource/auditbeat/README.md` 流程）。

本机 `resource/` 下其实有 tarball：
- `auditbeat-9.4.2-linux-arm64.tar.gz`
- `auditbeat-9.4.2-linux-x86_64.tar.gz`

直接 tarball 解压到对应 arch 目录即可（注意 `/tmp` 在沙箱里是只读，得用工作区内
的 `.cache/`）：

```bash
mkdir -p .cache/auditbeat-extract
tar -xzf resource/auditbeat-9.4.2-linux-arm64.tar.gz -C .cache/auditbeat-extract
tar -xzf resource/auditbeat-9.4.2-linux-x86_64.tar.gz -C .cache/auditbeat-extract
cp .cache/auditbeat-extract/auditbeat-9.4.2-linux-arm64/auditbeat \
   resource/auditbeat/linux-arm64/auditbeat
cp .cache/auditbeat-extract/auditbeat-9.4.2-linux-x86_64/auditbeat \
   resource/auditbeat/linux-amd64/auditbeat
chmod +x resource/auditbeat/linux-{amd64,arm64}/auditbeat
rm -rf .cache/auditbeat-extract
```

`file` 验证：
- `linux-arm64/auditbeat`: ELF 64-bit LSB, ARM aarch64
- `linux-amd64/auditbeat`: ELF 64-bit LSB, x86-64

> 之后会写一个 `make prep-auditbeat` 之类的 helper 脚本（或在 README 里强调
> `make package` 之前先 `make stage-auditbeat` 触发硬失败时给一条明确指引），
> 避免下次再踩这个坑。本次先按手工流程跑通。

#### 2.2 打包过程

`make package` 一次走完，关键日志（节选）：

- 跨编 `ongrid-edge`（linux/amd64 + linux/arm64），CGO_ENABLED=0
- `docker buildx build --platform linux/arm64 -t ongrid:v0.9.0 -f deploy/Dockerfile.ongrid --load .`
  - builder 阶段跑 `go build -trimpath` 出 `/out/ongrid`（CGO_ENABLED=1 onnxruntime 链）
  - 拉 onnxruntime 1.20.1（先 GitHub releases，回退 nuget）
  - 拷 manager 二进制、`/skills`、`/agents` 到 runtime 层
- `docker save ongrid:v0.9.0` + `docker save singchia/frontier:v1.2.4` + `docker save ongrid-web:v0.9.0`
- 用 `dist/build-edge-bundle.sh` 重新打 arm64 + amd64 两个 edge bundle
  - amd64: 203M sha256 `635e83de8…`
  - arm64: 182M sha256 `20fba6c2c…`
- `dist/package.sh` 组装最终 tarball

注意：本次 `make fetch-embedding-model` 没跑，所以 `dist/package.sh` 给了
"bundled embedding model not pre-cached" 警告，离线 RAG 首次会走
download-on-first-use fallback。**不影响本次安装**（要离线 RAG 的话跑
`make fetch-embedding-model && make package` 重打一遍即可）。

#### 2.3 产物

```
dist/out/ongrid-v0.9.0-linux-arm64.tar.xz          427M
dist/out/ongrid-v0.9.0-linux-arm64.tar.xz.sha256
  e49bd69b9775fb06af6baadf73025589a8688a3922eef0ed6cb06090005926ae
```

tarball 体积从旧的 ~354M 涨到 427M —— 不是 auditbeat（amd64 那份从 153M → 171M，
arm64 那份从 137M → 153M，差 ~20M×2 = ~40M），其余是 manager binary 体积增量
（43M）+ 新拉的 stack-deps 缓存命中差异。

### 3. 装新

```bash
mkdir -p .cache/install-stage
tar -xJf dist/out/ongrid-v0.9.0-linux-arm64.tar.xz -C .cache/install-stage
cd .cache/install-stage/ongrid-v0.9.0-linux-arm64
sudo ONGRID_PUBLIC_URL=https://192.168.25.30 bash install.sh
```

`ONGRID_PUBLIC_URL` 显式注入避开 30s 交互提示。沙箱里没有公网（metadata 服务
`100.100.100.200` 和 `api.ipify.org` 都超时），默认会回退到内网 NIC，
和本次显式注入的 192.168.25.30 是一致的。

`install.sh` 关键流程：
1. preflight（docker 在，compose v2 在，registry-mirrors 已经在 `/etc/docker/daemon.json`）
2. 装资产到 `/opt/ongrid/{docker-compose.yml,.env.example,frontier.yaml,prometheus.yml,...}`
3. 重建 `ongrid-web/edge/edge-bundle-{amd64,arm64}-v0.9.0.tar.gz`
4. 生成自签 TLS 证书（`/opt/ongrid/ongrid-web/certs/{tls.crt,tls.key}`）
5. `docker load` 三个镜像
6. `extract_image_dist` 把 manager 的 `/ongrid /skills /agents` 和
   ongrid-web 的 `/usr/share/nginx/html` 拷到宿主机 bind-mount 源
7. 从 `.env.example` 拷出 `.env`，`fill_blank` 填 MYSQL/JWT/ADMIN/GRAFANA 密码
8. 写 `ONGRID_PUBLIC_URL=https://192.168.25.30` + `ONGRID_VERSION=v0.9.0`
9. `docker compose --env-file .env up -d`
10. 等 `/healthz` 60s（实际 ~50s 内 ongrid 起来）
11. 打印 banner

## 验证

- `docker ps` 10 个 ongrid 容器全部 `Up X minutes`，mysql `Up X minutes (healthy)`
- `curl -sk https://localhost/healthz` → `HTTP 200`
- `curl -sk https://localhost/readyz` → `HTTP 200`
- `curl -sk https://localhost/` → `HTTP 200`（nginx 命中 ongrid-web SPA）
- `/opt/ongrid/VERSION` = `v0.9.0`
- `/opt/ongrid/ongrid-app/ongrid` = 42M（CGO_ENABLED=1 + 嵌入 onnxruntime.so）
- `/opt/ongrid/ongrid-web/edge/edge-bundle-{amd64,arm64}-v0.9.0.tar.gz` 都有

banner 关键信息：
- Web UI: `https://192.168.25.30/`
- API URL: `https://192.168.25.30/api/v1`
- Tunnel endpoint: `192.168.25.30:40012`
- bootstrap admin: `admin@ongrid.local` / `ongrid_admin_pwd`（.env.example 默认值，
  生产前改）

## 不属于本任务范围

- 没有改任何源码、配置、脚本
- 没有 commit/push
- 没有改 VERSION（仍是 `v0.9.0`，所以 tarball 文件名还是 v0.9.0）
- 没有打 amd64 tarball（`make package-all` 可同时打 amd64+arm64）
- 没有 `make fetch-embedding-model`（首次冷启会下载模型）
- 没有重新生成 proto（`make proto`）

## 下次可改进

- `make package` 之前如果 `resource/auditbeat/<arch>/auditbeat` 缺失，
  在 stage-auditbeat 报错信息里直接提示"已在 resource/ 找到 tarball，是否
  自动解压？y/N" 一键搞定，避免手工 tar -xzf
- 沙箱里 `/tmp` 只读、`.cache/` 可写 — 写 helper 时统一放 `.cache/<name>` 别用 `/tmp`
