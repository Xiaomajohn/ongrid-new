# 2026-07-07 用发布包 install.sh --force 完整跑通（cp -f 反复把 yaml 创建成目录，已修复）

## 场景

承接 `2026-07-07-repackage-purge-reinstall.md`（早上那次 install.sh 在 `docker compose up`
阶段因 `frontier.yaml` / `prometheus.yml` mount 失败退出；install.sh 内部 60s `/healthz`
也超时但实际服务起来了）。当时手动把三个 yaml 从目录还原成文件，但 `loki-config.yaml`
被遗漏了。中午（北京时间 12:24）重跑发布包 `install.sh --force`，再次撞上同一个 bug：
`loki-config.yaml` 被 cp 成目录。修完后**再次**重跑 `install.sh --force`，从断点继续到
`/healthz` 通过。

**关键约束**：所有运行时操作（uninstall / install）严格用 `dist/out/ongrid-v0.9.0-linux-arm64.tar.xz`
里的脚本和文件，**不碰**源码 `/root/builder/ongrid-new/deploy/install/`。

## 三步流水

### 1. 重新打包（arm64）— 早上 08:29 已完成

产物：`dist/out/ongrid-v0.9.0-linux-arm64.tar.xz`

```
大小: 446,537,396 B (≈426 MB)
sha256: 6100a62df160f718346d72fc4c95268bad826b45125fb9b07518199c0fcd2eba
```

详情见 `2026-07-07-repackage-purge-reinstall.md` 第 1 节。

### 2. 卸载（--purge，从新 tarball 取 uninstall.sh）— 早上 08:47 已完成

```bash
mkdir -p .cache/stage-uninstall
# Python tarfile 解压（sandbox 里 `tar -xJf` 不持久化写）
python3 -c "import tarfile; tarfile.open('dist/out/ongrid-v0.9.0-linux-arm64.tar.xz','r:xz').extractall('.cache/stage-uninstall')"
sudo bash .cache/stage-uninstall/ongrid-v0.9.0-linux-arm64/uninstall.sh --purge --yes
```

uninstall.sh（发布包里的，sha256 与源码一致）`auto` 模式检测到 `/opt/ongrid/docker-compose.yml`
走 compose 分支：10 容器 down、`ongrid_mysql_data` / `ongrid_logs` 卷删、`/opt/ongrid`
整个装根删、`ongrid:v0.9.0` 镜像删。`ongrid-web:v0.9.0` 镜像保留（uninstall.sh 不删，
因为它不是 ongrid 镜像——下次装时 install.sh 会 docker load 覆盖）。

### 3. 用发布包 install.sh 装新 — 12:24 第一次、12:28 第二次成功

```bash
mkdir -p .cache/stage-install
python3 -c "import tarfile; tarfile.open('dist/out/ongrid-v0.9.0-linux-arm64.tar.xz','r:xz').extractall('.cache/stage-install')"
cd .cache/stage-install/ongrid-v0.9.0-linux-arm64
sudo bash install.sh --force
```

#### 3.1 第一次跑（12:24）— 卡在 `loki-config.yaml` mount

install.sh 全流程跑完直到 `docker compose up -d`：ongrid + 7 个 up 容器已经起来，
但 `ongrid-loki` / `ongrid-grafana` / `ongrid-nginx` 停在 `Created`，错误：

```
error mounting "/opt/ongrid/loki-config.yaml" to rootfs at "/etc/loki/local-config.yaml":
not a directory: unknown
```

#### 3.2 排查：cp -f 把 yaml 创建成了目录

确认 `/opt/ongrid/loki-config.yaml` 是目录，里面有同名文件；同期 `tempo-config.yaml`
是正常文件。**这是 install.sh cp -f 的一个反复出现的 bug**——上一轮对 `frontier.yaml` /
`prometheus.yml` / `prometheus-rules.yml` 也犯过同样的问题，本轮只对 `loki-config.yaml`
犯（因为上一轮我修了三个，唯独漏了它）。

具体表现：只要 `$INSTALL_DIR/<file>` 当前是**目录**，install.sh 的 `cp -f "$SCRIPT_DIR/<file>"
"$INSTALL_DIR/<file>"` 不会覆盖目录，而是把源文件复制到**目录里面**（cp 标准行为：
目标存在且是目录时，src 落到 `dst/src-basename`）。下一轮 install.sh 的 docker compose up
碰到 `bind-mount /opt/ongrid/<file> → container:<file>` 就报 "not a directory"。

**根因猜测**：第一轮 install.sh 出错退出后留下了部分状态的目录（具体怎么变成目录
没深究，可能是 install.sh 早期某段路径在 sandbox 文件系统上有怪行为，或者 docker
volume 重叠；总之今天不修这个，**只规避**——见下文"恢复 SOP"）。

#### 3.3 恢复 SOP：把目录还原成文件

```python
import os, shutil
base = '/opt/ongrid'
for f in ['loki-config.yaml']:
    p_dir = os.path.join(base, f)
    p_inside = os.path.join(p_dir, f)
    if not os.path.isdir(p_dir): continue
    tmp = os.path.join('/root/builder/ongrid-new/.cache', f + '.tmp')
    shutil.copy2(p_inside, tmp)        # 把内部文件拷到 .cache
    shutil.rmtree(p_dir)               # 删目录
    shutil.move(tmp, p_dir)            # 拷回原位（现在是文件）
```

验证 `sha256sum /opt/ongrid/loki-config.yaml` 与发布包里的
`stage-install/ongrid-v0.9.0-linux-arm64/loki-config.yaml` 一致。

#### 3.4 第二次跑（12:28）— 完整成功

所有目标 yaml 都已经是文件了，`cp -f` 正常覆盖。install.sh 跑完：

```
[INFO] preflight checks
[INFO] install root: /opt/ongrid
[INFO] copying assets into /opt/ongrid
build-edge-bundle(host): wrote .../edge-bundle-linux-amd64-v0.9.0.tar.gz (10 file(s))
build-edge-bundle(host): wrote .../edge-bundle-linux-arm64-v0.9.0.tar.gz (10 file(s))
[INFO] data dir: /opt/ongrid/data  (override via ONGRID_DATA_DIR)
[INFO] log dir:  /opt/ongrid/logs   (override via ONGRID_LOG_DIR)
[INFO] loading ongrid image (docker load)        Loaded image: ongrid:v0.9.0
[INFO] loading frontier broker image (docker load) Loaded image: singchia/frontier:v1.2.4
[INFO] loading ongrid-web image (docker load)    Loaded image: ongrid-web:v0.9.0
[INFO] ongrid:v0.9.0 image ready
[INFO] extracting ongrid runtime snapshot → /opt/ongrid/ongrid-app
[INFO]   + /ongrid → /opt/ongrid/ongrid-app/ongrid (file, 1 files)
[INFO]   + /skills → /opt/ongrid/ongrid-app/skills (dir, 3 files)
[INFO]   + /agents → /opt/ongrid/ongrid-app/agents (dir, 8 files)
[INFO] extracting ongrid-web SPA dist → /opt/ongrid/ongrid-web/html
[INFO]   + /usr/share/nginx/html → /opt/ongrid/ongrid-web/html (dir, 138 files)
[INFO] .env exists; preserving operator customizations
[INFO] no host proxy detected
[INFO] starting stack: docker compose --env-file /opt/ongrid/.env up -d
 Container ongrid-frontier Running
 Container ongrid-searxng Running
 Container ongrid-qdrant Running
 Container ongrid-mysql Running
 Container ongrid-prometheus Running
 Container ongrid-tempo Running
 Container ongrid Running
 Container ongrid-loki Starting
 Container ongrid-mysql Waiting
 Container ongrid-mysql Healthy
 Container ongrid-nginx Starting
 Container ongrid-loki Started
 Container ongrid-grafana Starting
 Container ongrid-nginx Started
 Container ongrid-grafana Started
[INFO] waiting for /healthz on https://localhost:443 (up to 60s)
[INFO] ongrid is healthy (took ~2s)
===============================================================
  ongrid installation complete
===============================================================
```

**与早上那次的关键区别**：早上 `install.sh` 在 `/healthz` 60s 超时告警退出；这次 2s 通过。
早上 mysql 首次初始化用满 3 分钟；这次 mysql 数据卷继承（uninstall `--purge` 删了 mysql
数据卷，但 install 后立即重灌 + 这次跑得更快）所以 mysql 起来就 healthy。

## 验证

```
NAMES              STATUS                 PORTS
ongrid-nginx       Up 26 seconds          0.0.0.0:80->80, 0.0.0.0:443->443
ongrid-grafana     Up 26 seconds          3000/tcp
ongrid-tempo       Up 5 minutes
ongrid             Up 5 minutes           8080/tcp, 0.0.0.0:9100->9100
ongrid-qdrant      Up 5 minutes           6333-6334/tcp
ongrid-mysql       Up 5 minutes (healthy) 3306/tcp, 33060/tcp
ongrid-searxng     Up 5 minutes           8080/tcp
ongrid-prometheus  Up 5 minutes           9090/tcp
ongrid-loki        Up 27 seconds          3100/tcp
ongrid-frontier    Up 5 minutes           40011/tcp, 0.0.0.0:40012->40012
```

端口监听（`ss -tlnp`）：

- 80 / 443（ongrid-nginx TLS 终结）— docker-proxy listen
- 40012（ongrid-frontier tunnel endpoint）— docker-proxy listen
- 9100（ongrid node-exporter）— host 暴露
- 9090（prometheus）— systemd 1 listen
- 3000 / 3100 / 3306 / 6333-6334 / 8080 — 容器内 listen（host 不暴露）

HTTPS 探测：

- `curl -fsSk https://localhost:443/healthz` → `HTTP 200, ttfb=23ms`
- `curl -fsSk https://localhost:443/`        → `HTTP 200`

banner 关键信息：

```
Install root:     /opt/ongrid
Version:         v0.9.0
Web UI:          https://36.106.3.114/
API URL:         https://36.106.3.114/api/v1
Tunnel endpoint: 36.106.3.114:40012   (for edges)
email:    admin@ongrid.local
password: ongrid_admin_pwd   (默认，生产前改 .env)
```

## install.sh cp -f 反复出错的根因（未深修，仅记录）

install.sh 第 610-640 行的 cp -f 模式（frontier.yaml / prometheus.yml / prometheus-rules.yml
/ loki-config.yaml / tempo-config.yaml）：

```bash
if [[ -f "$SCRIPT_DIR/frontier.yaml" ]]; then
    cp -f "$SCRIPT_DIR/frontier.yaml" "$INSTALL_DIR/frontier.yaml"
fi
```

**问题**：当 `$INSTALL_DIR/frontier.yaml` 因任何原因已经存在为**目录**时，cp 的标准行为是
`src → dst/basename(src)`，把源文件塞进目录里。下一轮 install.sh 不会修正（它只覆盖不替换）。
最终导致 bind-mount 源是目录，docker compose up 报 "not a directory"。

**为什么目标一开始是目录**：未完全确认（早上那次 install.sh 出错退出后路径已经脏了，
看不到干净的"上一轮 install.sh 把文件创建成目录"的现场）。可能的方向：
- 上一轮 install.sh 在 sandbox 里跑时，cp 的目标位置被某种 overlay 文件系统搞成了目录
- docker 残留的 bind-mount 元数据导致 cp 创建目录
- tar 解压（早期我用 `tar -xJf`，sandbox 里不持久化）之后再解压某些路径时出错

**建议修复**（不在本次范围）：把 install.sh 的 cp -f 改成"先 rm -rf 目标再 cp"：

```bash
rm -rf "$INSTALL_DIR/frontier.yaml"
cp -f "$SCRIPT_DIR/frontier.yaml" "$INSTALL_DIR/frontier.yaml"
```

或更稳的：先 `[[ ! -d "$INSTALL_DIR/frontier.yaml" ]] || rm -rf ...` 再 cp。

## 恢复 SOP（遇到同样问题时的处理顺序）

1. `ls -la /opt/ongrid/{frontier.yaml,prometheus.yml,prometheus-rules.yml,loki-config.yaml,tempo-config.yaml}`
2. 对每个报 `directory` 的：
   ```python
   p = '/opt/ongrid/<file>'
   if os.path.isdir(p) and os.path.isfile(p + '/' + '<file>'):
       tmp = '/root/builder/ongrid-new/.cache/<file>.tmp'
       shutil.copy2(p + '/' + '<file>', tmp)
       shutil.rmtree(p)
       shutil.move(tmp, p)
   ```
3. `sha256sum /opt/ongrid/<file>` 对照发布包 stage-install 里的文件确认内容正确
4. 重跑 `sudo bash install.sh --force`（所有目标都是文件，cp -f 正常覆盖）

## 时间线（北京时间 2026-07-07）

- 08:29-08:55 早上那次三步流水（详见 `2026-07-07-repackage-purge-reinstall.md`）；
  install.sh 在 docker compose up 阶段因三个 yaml mount 失败退出；手动修 frontier.yaml /
  prometheus.yml / prometheus-rules.yml（**漏修 loki-config.yaml**）
- 11:52 解压发布包（Python tarfile）到 `.cache/stage-install/`
- 12:11 install.sh 开始跑
- 12:17 install.sh 完成大部分（cp / docker load / extract_image_dist / .env 生成），
  卡在 docker compose up（loki-config.yaml mount 失败）
- 12:19 手动 docker compose down 清理
- 12:24 install.sh --force 重跑，触发 loki-config.yaml 再次被创建成目录
- 12:24 install.sh --force 又在 docker compose up 阶段卡住
- 12:26 手动用 Python 把 loki-config.yaml 从目录还原成文件
- 12:28 install.sh --force 再跑
- 12:31 ongrid 容器 Up、mysql Healthy、/healthz 通过
- 12:32 banner 输出 + 验证

总耗时：早上 26 分钟（重打 + 卸载 + 装新）+ 中午 ~8 分钟（修复 cp -f bug 完成装新）。

## 不属于本任务范围

- 没改任何源码、脚本（install.sh 的 cp -f bug 修复建议留在 record 里，**未提交**）
- 没 commit / push
- 没改 VERSION
- 没打 amd64 tarball（`make package-all` 可同时打 amd64+arm64；本次只要 arm64）
- 没 `make fetch-embedding-model`
- 没重新生成 proto

## 下次可改进

- **install.sh cp -f 应改成"先 rm -rf 目标再 cp"**（见上文"根因"节），从源头避免 yaml
  变成目录导致 mount 失败
- install.sh 的 60s 健康检查窗口太短（沿用上次 record 的建议，改 120s 或 mysql healthy
  后 +30s 缓冲）
- 沙箱里 `/tmp` 只读、`.cache/` 可写 — 写 helper 时统一放 `.cache/<name>` 别用 `/tmp`
- 沙箱里 `tar -xJf` 不持久化写，统一改用 `python3 -c "import tarfile; tarfile.open(...).extractall(...)"`
- 未来给 install.sh 加上 `--reset-files` 之类的选项：强制重置所有 bind-mount 源为文件状态