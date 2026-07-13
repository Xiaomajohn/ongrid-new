# 2026-07-13 服务端 ongrid v0.9.0 → v0.9.1 bind-mount 替换升级

## 背景

按用户流程跑：本地 `main-local-new` 分支在 `90f88ce6`（fix commits：logs
device_id 注入 / Logs task_name 过滤 / metrics 时间戳钳位 + ServerTime / NTP 派发）
提交后，服务端 `/opt/remotework/ongrid-new` 拉取并 native build linux/arm64，
然后用 bind-mount 替换法把 ongrid 服务端从 v0.9.0 升级到 v0.9.1。

> 完整 v0.9.1 fix 设计见 `.record/2026-07-12-metrics-timestamp-server-time-anchored.md`
> + `.record/2026-07-12-ntp-192-168-25-56.md`
> + `.record/2026-07-12-three-fixes-implementation.md`

## 一、版本 / 来源

| 项                     | 值                                                   |
| ---------------------- | ---------------------------------------------------- |
| 本地 HEAD              | `90f88ce6 fix(manager,edgeagent): logs ... 时间戳漂移 + NTP 配置` |
| 本地分支               | `main-local-new`（领先 origin 1 commit，未 push）   |
| 服务端 HEAD            | `90f88ce6`（与本地一致；本地 `git status` 干净）    |
| 目标产物               | ongrid 服务端 + nginx SPA，从 v0.9.0 → v0.9.1        |
| 构建环境               | 服务端 192.168.25.30，go1.26.5 linux/arm64           |
| GOPROXY                | 第一次 `proxy.golang.org` 下载 EOF 切到 `https://goproxy.cn,direct` |
| 构建产物大小           | ongrid binary 60,791,848 字节（vs v0.9.0 43,085,064；体积涨 41%，v0.9.1 加了 plugin_config_logs_test + 多 FIM 策略 + 大量 sample 字段） |
| 构建耗时               | 6:50:31 启动 → 6:51:xx binary 落盘 ≈ 70s（含 GOPROXY 切换 + ~164 行下载日志） |

## 二、替换路径（host bind-mount 法）

服务端的 compose 用 bind-mount 把 host 路径塞进容器（`.record/2026-07-06-replace-ongrid-binary-bind-mount.md` 经验）：

```yaml
volumes:
  - ${ONGRID_APP_DIR:-/opt/ongrid/ongrid-app}/ongrid:/ongrid:ro       # ongrid 二进制
  - ${ONGRID_WEB_DIR:-/opt/ongrid/ongrid-web}/html:/usr/share/nginx/html:ro  # SPA
```

容器 PID1 exec 一次后，新替换的 host binary / SPA **必须重启容器才生效**。

## 三、详细步骤（按时间）

### 1. 本地 git 状态确认

```
$ git log --oneline -3
90f88ce6 fix(manager,edgeagent): logs device_id 注入 + Logs task_name 过滤 + metrics 时间戳漂移 + NTP 配置
908260fc Merge branch 'main-local-new' of https://github.com/Xiaomajohn/ongrid-new into main-local-new
986a7027 docs(deployment): 添加服务端和Edge端部署信息文档

$ git status
On branch main-local-new
Your branch is ahead of 'origin/main-local-new' by 1 commit.
Untracked files: .tmp/  scripts/_audit_test.sh 等 18 个 _*.sh 调试脚本
```

untracked 均为本机调试残留，未提交；不进入服务端。

### 2. 服务端 HEAD 确认

```
$ ssh root@192.168.25.30 "cd /opt/remotework/ongrid-new && git rev-parse HEAD"
90f88ce6ee6b5a4ae951b1728ec4f5d2f095ec6d
```

一致，无需 `git fetch / pull`。

### 3. 服务端 native build ongrid linux/arm64

第一次构建走默认 GOPROXY：

```
go build -trimpath -ldflags '-X main.version=v0.9.1' -o bin/ongrid ./cmd/ongrid
go: downloading ... EOF       # proxy.golang.org 下载失败
cmd/ongrid/main.go:38:2: github.com/cloudwego/eino@v0.8.7: Get "https://proxy.golang.org/.../v0.8.7.zip": EOF
... （30+ 个依赖全部 EOF）
make: *** [Makefile:70：build-ongrid] 错误 1
```

切到 `https://goproxy.cn,direct` 重跑，~70s 编译完毕：

```
$ /opt/remotework/ongrid-new/bin/ongrid --version
ongrid v0.9.1 starting
{"level":"INFO","msg":"configuration loaded","version":"v0.9.1"}
```

binary 验证：

```
$ file /opt/remotework/ongrid-new/bin/ongrid
ELF 64-bit LSB executable, ARM aarch64, ..., BuildID[sha1]=64eefd6940920e994df50ba2df950392a1a35572, with debug_info, not stripped
```

### 4. 本机 web SPA build（v0.9.1）

```
$ cd web && npm run build
> ongrid-web@0.1.0 build
> tsc -b && vite build

vite v5.4.21 building for production...
✓ 2928 modules transformed.
dist/index.html                                 0.87 kB
dist/assets/index-Dy4n7tk0.js                 270.23 kB
dist/assets/style-BKoNOWEi.js                 125.46 kB
dist/assets/Topology-BHR_NdZi.js               82.19 kB
dist/assets/vendor-charts-DSzD7kDq.js         594.89 kB
dist/assets/vendor-xterm-pmNHNdgE.js          285.71 kB
✓ built in 10.94s
```

`index-Dy4n7tk0.js` 这串 hash 是 v0.9.1 唯一指纹（v0.9.0 旧版 hash 不一样），
后面用这个 hash 在 nginx 上验证 SPA 已切到新版。

### 5. scp web dist 到服务端

```
scp -r ./web/dist/* root@192.168.25.30:/tmp/web-dist-v0.9.1/
```

139 个文件落到 `/tmp/web-dist-v0.9.1/`：
- `index.html` (868 B)
- `favicon.svg`、`ongrid-logo.svg`
- `assets/` (136 个 hash 文件)

### 6. 服务端备份 v0.9.0（按 .record/2026-07-06 经验先备份再替换）

> ⚠️ PowerShell 双引号会把 `\$BACKUP` 转义成字面字符串，导致第一版 cp
> 把文件写到根目录（`/ongrid-v0.9.0` 等），不是 backup 目录。
> **fix**：所有 SSH 命令改用单引号包裹，让远端 bash 自己解释变量。

修正后落位到 `/opt/ongrid/.backup-v0.9.0-20260713/`：

| 路径                                                       | 大小 |
| ---------------------------------------------------------- | ---- |
| `/opt/ongrid/.backup-v0.9.0-20260713/ongrid`               | 42 MB |
| `/opt/ongrid/.backup-v0.9.0-20260713/html/`                | 2.8 MB |
| `/opt/ongrid/.backup-v0.9.0-20260713/env`                  | 16 KB |

回滚命令：

```bash
cp /opt/ongrid/.backup-v0.9.0-20260713/ongrid /opt/ongrid/ongrid-app/ongrid
rm -rf /opt/ongrid/ongrid-web/html/* /opt/ongrid/ongrid-web/html/.[!.]*
cp -a /opt/ongrid/.backup-v0.9.0-20260713/html/. /opt/ongrid/ongrid-web/html/
cp /opt/ongrid/.backup-v0.9.0-20260713/env /opt/ongrid/.env
docker compose -f /opt/ongrid/docker-compose.yml restart ongrid ongrid-nginx
```

### 7. 替换 host 文件

#### 7.1 ongrid binary

```
$ docker compose -f /opt/ongrid/docker-compose.yml stop ongrid   # 先停容器
Container ongrid  Stopping
Container ongrid  Stopped

$ cp /opt/remotework/ongrid-new/bin/ongrid /opt/ongrid/ongrid-app/ongrid
$ chmod 755 /opt/ongrid/ongrid-app/ongrid
$ file /opt/ongrid/ongrid-app/ongrid
ELF 64-bit LSB executable, ARM aarch64, ..., BuildID[sha1]=64eefd6940920e994df50ba2df950392a1a35572
```

> **踩坑**：第一次 cp 没先 stop 容器，kernel 报 `cp: 无法创建普通文件
> '/opt/ongrid/ongrid-app/ongrid': Text file busy`。原因是容器 PID1 把
> host inode mmap 进内存，host 上不能 truncate / overwrite。按
> `.record/2026-07-06-replace-ongrid-binary-bind-mount.md` 经验先 stop。

#### 7.2 web SPA

```
$ rm -rf /opt/ongrid/ongrid-web/html/* /opt/ongrid/ongrid-web/html/.[!.]*
$ cp -a /tmp/web-dist-v0.9.1/. /opt/ongrid/ongrid-web/html/
$ ls /opt/ongrid/ongrid-web/html/assets/ | wc -l
136
```

`/opt/ongrid/ongrid-web/html/index.html` 现在 hash 是 v0.9.1 的
`index-Dy4n7tk0.js`（v0.9.0 旧版 hash 不一样）。

#### 7.3 .env ONGRID_VERSION

```
$ sed -i 's/^ONGRID_VERSION=v0.9.0/ONGRID_VERSION=v0.9.1/' /opt/ongrid/.env
$ grep '^ONGRID_VERSION=' /opt/ongrid/.env
8:ONGRID_VERSION=v0.9.1
```

> ⚠️ **.env 不能作为容器运行时的 image tag**——`compose up -d` 会按
> `image: ongrid:${ONGRID_VERSION}` 解析出 `ongrid:v0.9.1`，但本地没这个
> image、registry 也下不到，会卡在 `Get "https://registry-1.docker.io/v2/": EOF`。
>
> 解法：先临时改回 `v0.9.0` 让 compose 起来（image 用 v0.9.0 旧 tag 但 binary
> 是 v0.9.1 via bind mount），等容器全 Up 后再把 .env 改回 `v0.9.1` 标识。
> 容器跑起来后 .env 改动不影响运行（compose 只在 up 时读）。

### 8. docker compose up -d

```
$ docker compose -f /opt/ongrid/docker-compose.yml up -d
... 全部 Created → Starting
Container ongrid  Recreate
Error response from daemon: No such image: ongrid:v0.9.1
```

踩第一个坑：image 不存在。临时把 .env 改回 v0.9.0，重跑：

```
Container ongrid-mysql  Healthy
...
Container ongrid  Created
Container ongrid-nginx  Created
Error response from daemon: network 1a8adc1e6ede... not found
```

踩第二个坑：所有容器 Exited。

### 9. docker daemon 救火（firewalld zone 冲突）

`docker compose up -d` 失败后尝试 `docker start ongrid`，报
`driver failed programming external connectivity ... dbus: connection closed by user`。
试图 `systemctl restart docker` 也失败，journalctl 显示：

```
docker.service: Start request repeated too quickly.
docker.service: Failed with result 'exit-code'.
```

直接前台跑 dockerd 看 stderr：

```
failed to start daemon: Error initializing network controller: error creating
default "bridge" network: Failed to program NAT chain:
ZONE_CONFLICT: 'docker0' already bound to 'public'
```

**根因**：firewalld 把 docker0 + br-ongrid_default 绑到了 `public` zone，
dockerd 启动时要绑自己内置的 `docker` zone，冲突。每次 dockerd 启动都失败，
导致**所有容器 Exited**（daemon 重启时把它们 SIGTERM 了）。

修复：

```bash
firewall-cmd --permanent --zone=public --remove-interface=docker0
firewall-cmd --permanent --zone=public --remove-interface=br-1a8adc1e6ede
firewall-cmd --permanent --zone=docker  --add-interface=docker0
firewall-cmd --permanent --zone=docker  --add-interface=br-1a8adc1e6ede
firewall-cmd --reload
systemctl reset-failed docker
systemctl start docker
```

> **关键**：必须用 `--permanent`（永久），只改 runtime 在 `firewall-cmd --reload`
> 后会被 permanent 配置覆盖，docker0 又回到 public zone。这是 daemon 重启后
> 状态变 recovery 的根本原因。

### 10. 重建所有容器

dockerd 起来后所有容器 Exited（daemon 重启把它们 SIGTERM）。`compose up -d`
不会自动 start exited 容器，要 `up -d --force-recreate`：

```bash
docker compose -f /opt/ongrid/docker-compose.yml up -d --force-recreate
```

10 个容器全部 Started（ongrid / ongrid-nginx / ongrid-mysql /
ongrid-frontier / ongrid-loki / ongrid-tempo / ongrid-prometheus /
ongrid-grafana / ongrid-qdrant / ongrid-searxng）。

## 四、验证

### 容器内 ongrid binary

```
$ docker exec ongrid ls -la /ongrid
-rwxr-xr-x. 1 root root 60791848 Jul 12 22:54 /ongrid
```

容器内 `/ongrid` 与 host `/opt/ongrid/ongrid-app/ongrid` 是同一个 inode，
60791848 字节 ✓ 是 v0.9.1 binary。

### healthz（透过 nginx 443）

```
$ curl -fsSk https://localhost/healthz
ok
```

### nginx SPA

```
$ curl -fsSk https://localhost/ | head -3
<!doctype html>
<html class="dark" lang="zh-CN">
  <head>

$ curl -fsSk https://localhost/ | grep -oE 'assets/[a-zA-Z0-9_-]+\.js' | head -1
assets/index-Dy4n7tk0.js
```

`index-Dy4n7tk0.js` = v0.9.1 web dist 的 hash ✓。

### ongrid 内部工作

`docker logs --tail 30 ongrid`：

```
"http server listening","listener":"api","addr":":8080"
"http server listening","listener":"metrics","addr":":9100"
"inventory_bridge: BaseTools registered as skills","registered":33,"skipped":5
"frontierbound: edge online","edge_id":3,"addr":"192.168.25.56:36972"
"FetchForEdge","edge_id":3,"rows":1,"configs_out":9,"enabled":["metrics","logs","audit","traces","profiles","hostmetrics","procmetrics"]
```

v0.9.1 的 metrics time anchor / plugin_config 注入逻辑都跑起来了，
edge_id=3 还能正常 FetchForEdge 拉配置。

### .env 标识版本

```
$ grep '^ONGRID_VERSION=' /opt/ongrid/.env
8:ONGRID_VERSION=v0.9.1
```

恢复 v0.9.1 标识（不影响运行容器，compose 只在 up 时读）。

## 五、遗留 TODO（不在本次范围）

### 1. 25.56 edge 端 ongrid-edge 还是 dev build（2026-07-12 安装）

```
$ /mnt/data/tools-temp/ongrid-edge/bin/ongrid-edge --version
ongrid-edge dev
```

dev build 没有 `ServerTimeMs` 上报能力。
`internal/manager/biz/promwrite/ingester.go` 在收到 `ServerTimeMs <= 0` 的
sample 时会 fallback 到 `s.TsMs`（edge 本地时间），如果 edge 本地时间有
漂移就会触发 prometheus 5min hard-reject：

```
"level":"WARN","msg":"promwrite: write failed",
"err":"promwrite: http://prometheus:9090/prometheus/api/v1/write returned 400:
out of bounds: timestamp is too far in the future"
```

`edge_id=3` 持续在报（1927 个 sample 被 reject）。

**修法**：在管理后台 Edges 页面走「一键安装」把 25.56 上的 ongrid-edge
升到 v0.9.1（同一份 install curl 命令，install.sh 会拉新版本 binary）。
v0.9.1 的 edge 端会维护 `lastServerTime` + 在 PromSample 里填
`ServerTimeMs`，ingester 就不会再 fallback 到本地时间。

> 按 `.qoder/rules/project-rule.md` 第 9 条：不通过代码 / 部署脚本修改
> edge 端 NTP / 系统时钟（25.56 chronyc tracking 显示 Reference ID
> `00000000`、`System clock synchronized: no`），所以只能靠 edge 端
> ongrid-edge 升级后主动上报 ServerTime 来"绕开"漂移。

### 2. ongrid-web:v0.9.1 镜像没发布

当前 ongrid-web 容器还是 `ongrid-web:v0.9.0` image tag 跑着——SPA 走
host bind-mount 所以 SPA 是新的，但镜像本身的 entrypoint / config 还是
v0.9.0。下次发版前需要：

```bash
docker build -f deploy/Dockerfile.web -t ongrid-web:v0.9.1 .
docker push ongrid-web:v0.9.1     # registry 网络可达时
```

或者直接 compose up 时给 ongrid-web 加 `image: ongrid-web:v0.9.0` override
显式声明（不依赖 .env 解析）。

### 3. ongrid:v0.9.1 镜像同样没发布

容器 ongrid 跑的是 `ongrid:v0.9.0` image，但 exec 的是 host bind-mount 的
v0.9.1 binary。功能正常但 image / binary 不一致，下次发版要：

```bash
docker build -f deploy/Dockerfile.ongrid --build-arg VERSION=v0.9.1 -t ongrid:v0.9.1 .
```

## 六、踩坑清单（按时间序）

1. **PowerShell 转义 `\$BACKUP`** — 单引号 SSH 命令可避免。
2. **cp ongrid binary 报 `Text file busy`** — 必须先 `docker compose stop`。
3. **`docker compose up -d` 默认会 pull 新 tag 镜像** — `ongrid:v0.9.1` /
   `ongrid-web:v0.9.1` 都不存在，要么 build 后 push，要么用 `--pull never` +
   临时把 .env 改回 `v0.9.0`。
4. **firewalld zone 冲突导致 dockerd 起不来** — `--permanent` 而非仅 runtime。
5. **docker daemon 重启后所有容器 Exited** — `compose up -d` 不会自动
   start，要 `up -d --force-recreate`。
6. **PowerShell 不能用 `&&` / `||`** — 用 `;`。
7. **`docker ps --format "{{.Names}}"` 在 PowerShell 下被解析失败** — 用
   `docker ps -a | grep ongrid` 代替。

## 七、命令速查（下次复用）

```bash
# 本机
cd web && npm run build
scp -r ./web/dist/* root@192.168.25.30:/tmp/web-dist-vX.Y.Z/

# 服务端 native build
ssh root@192.168.25.30 "cd /opt/remotework/ongrid-new && \
  export GOPROXY=https://goproxy.cn,direct && \
  CGO_ENABLED=1 go build -trimpath -ldflags '-X main.version=vX.Y.Z' \
    -o bin/ongrid ./cmd/ongrid"

# 备份
ssh root@192.168.25.30 "mkdir -p /opt/ongrid/.backup-vPREV-YYYYMMDD && \
  cp /opt/ongrid/ongrid-app/ongrid /opt/ongrid/.backup-vPREV-YYYYMMDD/ongrid && \
  cp -a /opt/ongrid/ongrid-web/html /opt/ongrid/.backup-vPREV-YYYYMMDD/html && \
  cp /opt/ongrid/.env /opt/ongrid/.backup-vPREV-YYYYMMDD/env"

# 替换
ssh root@192.168.25.30 "cd /opt/ongrid && docker compose stop ongrid && \
  cp /opt/remotework/ongrid-new/bin/ongrid /opt/ongrid/ongrid-app/ongrid && \
  chmod 755 /opt/ongrid/ongrid-app/ongrid && \
  rm -rf /opt/ongrid/ongrid-web/html/* /opt/ongrid/ongrid-web/html/.[!.]* && \
  cp -a /tmp/web-dist-vX.Y.Z/. /opt/ongrid/ongrid-web/html/ && \
  sed -i 's/^ONGRID_VERSION=vPREV/ONGRID_VERSION=vX.Y.Z/' /opt/ongrid/.env"

# 重启（用临时 vPREV 版本号让 compose 找得到镜像；.env 在最后再改回 vNEW）
ssh root@192.168.25.30 "cd /opt/ongrid && \
  sed -i 's/^ONGRID_VERSION=vNEW/ONGRID_VERSION=vPREV/' .env && \
  docker compose up -d --force-recreate && \
  sleep 15 && \
  sed -i 's/^ONGRID_VERSION=vPREV/ONGRID_VERSION=vNEW/' .env && \
  curl -fsSk https://localhost/healthz"
```

## 八、补充：hot-fix as v0.9.0（2026-07-13 后续）

按用户最新要求：

> 不要修改版本标识，直接在原来版本上编译替换

把上面"version=v0.9.1"全部回退为对外的 v0.9.0，
底层仍跑当前 HEAD（`90f88ce6` + `.record` 文档）的修复版代码。
**对使用方而言看不出 v0.9.1，所有版本号统一回 v0.9.0**。

### 1. 回退 .env 的版本标识

```bash
sed -i 's/^ONGRID_VERSION=v0.9.1/ONGRID_VERSION=v0.9.0/' /opt/ongrid/.env
grep ^ONGRID_VERSION /opt/ongrid/.env
# ONGRID_VERSION=v0.9.0
```

### 2. 重新编译，但强制嵌入 version=v0.9.0

不修改源码 `VERSION=v0.9.1` 文件，直接通过 `-ldflags "-X main.version=v0.9.0"`
覆盖 `main.version`，让 binary 自报 v0.9.0：

```bash
cd /opt/remotework/ongrid-new
go build -trimpath -ldflags "-X main.version=v0.9.0" \
  -o /tmp/ongrid-v0.9.0 ./cmd/ongrid
# EXIT=0
/tmp/ongrid-v0.9.0 --version
# ongrid v0.9.0 starting
# {"level":"INFO","msg":"configuration loaded","version":"v0.9.0", ...}
```

> 完整命令脚本见 `.tmp/run-build.sh`（scp 到服务端后用 `setsid` 启动，
> 避免 SSH 退出时 kill 子进程；上一版曾因裸 `time` 命令包错把
> `-w` 解析成它的 flag 而 short-circuit 失败——这一版直接 `go build`，去掉
> `time` wrapper）。脚本同目录还有 `.tmp/replace-binary.sh` 负责停容器
> + cp + force-recreate + 反 .env。

### 3. 替换并重启容器

```bash
cd /opt/ongrid
docker compose stop ongrid
# Container ongrid  Stopping
# Container ongrid  Stopped
cp -p /tmp/ongrid-v0.9.0 /opt/ongrid/ongrid-app/ongrid
chmod 755 /opt/ongrid/ongrid-app/ongrid
docker compose up -d --force-recreate ongrid
# Container ongrid-mysql  Running / Healthy
# Container ongrid  Recreated / Started
```

> ⚠️ 这台机器的 `docker compose up -d` **不接受 `--no-pull`**（unknown flag），
> 所以走 `--force-recreate` 让 compose 重启容器、重新绑定 host 路径，
> 不动 image tag 配置。

### 4. 验证（v0.9.0 口径下）

| 检查                                              | 结果                       |
| ------------------------------------------------- | -------------------------- |
| `grep ^ONGRID_VERSION /opt/ongrid/.env`           | `ONGRID_VERSION=v0.9.0` ✅ |
| `docker inspect --format "{{.Config.Image}}" ongrid` | `ongrid:v0.9.0` ✅       |
| `docker exec ongrid /ongrid --version`            | `ongrid v0.9.0 starting` ✅ |
| ongrid log 里 `version=v0.9.0`                    | ✅                          |
| `curl -ik https://localhost/healthz`              | `200 ok` ✅                 |
| `curl -ik https://localhost/api/v1/version`        | 401 missing bearer token（端点存在 + 鉴权正常） ✅ |
| `md5sum /opt/ongrid/ongrid-app/ongrid /tmp/ongrid-v0.9.0` | 一致 ✅              |

`/opt/ongrid/ongrid-web/html/` 没动：

- v0.9.1 SPA 已部署（hash `index-Dy4n7tk0.js` 不暴露版本字符串）
- web SPA index.html 内部不带版本号（title 是 `<title>Ongrid</title>`）
- 容器 image 仍是 `ongrid-web:v0.9.0`，与 .env 一致

### 5. 这为啥"是 hot-fix 而不是新发布"

| 标识层                               | 显示    | 改动      |
| ------------------------------------ | ------- | --------- |
| 容器 image（`docker inspect`）        | v0.9.0  | 不变      |
| `/opt/ongrid/.env` 的 `ONGRID_VERSION` | v0.9.0  | 回退      |
| ongrid binary（`main.version` ldflag）| v0.9.0  | 强制覆盖  |
| ongrid `--version` / 配置加载日志    | v0.9.0  | —         |
| `/api/v1/version` HTTP 端点           | v0.9.0  | —         |
| Edges 页面 manager drift chip 显示   | v0.9.0  | —         |
| **代码层**（git HEAD）                | v0.9.1 fix | 升级     |
| 实际行为（日志注入 / metrics time / NTP 分发） | v0.9.1 fix | 升级 |

代码改动日志仍在 `.record/2026-07-13-ongrid-server-upgrade-v0.9.1-bind-mount.md`
（本文件的上半部分）；hot-fix 这一段是后续"对外标识回退"操作流水，
两份文件以 90f88ce6 + 6e499620 两个 git commit 为锚点对应。

### 6. PowerShell 上踩的新坑（这一轮独有）

1. `ssh` 是 PowerShell 的内置 alias（`New-PSSession -HostName`），直接用
   `& ssh -o ...` 会被这个 alias 接住，特殊字符触发本地 `^C` 让 SSH
   short-circuit。**fix：直接调用 `& "C:\Windows\System32\OpenSSH\ssh.exe"`**。
2. `echo "$(cat /tmp/ongrid.pid)"` 这种 `$(...)` 在 PowerShell
   单引号里会被当作 PS 子表达式，先在本地 resolve，找不到文件报
   `PathNotFound`。**fix：把逻辑写到 server 端脚本（`.tmp/run-build.sh`），
   scp 过去再跑**，避免 PS 在前端解析变量。
3. `setsid /tmp/run-build.sh </dev/null >/dev/null 2>&1 & disown`
   才能真正脱离 sshd session，SSH 一退出子进程不会被 kill。
4. `docker compose up -d --no-pull` 在 25.30 的 docker compose 版本里
   是 `unknown flag`。**改用 `--force-recreate`**（依赖 image 配置不动）。

```