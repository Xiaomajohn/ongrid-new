# 2026-07-05 uninstall.sh 安装模式检测失效修复

## 背景

用户用 `docker` 模式安装 (`./install.sh` 默认 → compose) 后，跑
`./uninstall.sh --purge` 出现：
```
[INFO] auto-detected install mode: none
[WARN] no live install detected — running systemd --purge anyway to clean stragglers
```
然后 dispatch 进了 `uninstall-systemd.sh --purge`，但该脚本只清理 systemd 路径
(`/var/lib/ongrid*`、`/etc/ongrid`、`ongrid-*` 服务用户)，导致 compose 模式下：
- docker compose stack 保持运行、容器未 stop
- bind-mount `/opt/ongrid/data` (mysql/prom/loki/tempo/qdrant/grafana 数据) 未清理
- 命名卷 `ongrid_mysql_data` / `ongrid_logs` 未清理
- docker image `ongrid:${VERSION}` 未清理

用户实际效果：`--purge` 完只删了一些不存在的目录，docker stack 一切照旧。

## 根因

`deploy/install/uninstall.sh` 的 `detect_install_mode()` (line 62-74 旧版) 检测 compose 时仍用旧版**双层嵌套路径**：
```bash
local parent="${ONGRID_INSTALL_DIR:-/opt/ongrid}"
[[ -f "$parent/ongrid/docker-compose.yml" ]] && has_compose=1
# 即 /opt/ongrid/ongrid/docker-compose.yml
```
但 `install.sh` line 593 + 注释 line 585-590 已经迁移到**单层布局**：
```bash
INSTALL_DIR="${ONGRID_INSTALL_DIR:-/opt/ongrid}"
cp -f "$SCRIPT_DIR/docker-compose.yml" "$INSTALL_DIR/docker-compose.yml"
# 即 /opt/ongrid/docker-compose.yml（无 /ongrid/ 子目录嵌套）
```
注释明确："There is no separate PARENT_DIR/ongrid nesting — operators shouldn't
have to remember two levels of nesting"。install.sh、upgrade.sh 已统一到单层，但
uninstall.sh 的检测函数 + 后续路径解析仍留在双层，对真实 compose 安装**永远失配**。

## 改动

### 1. `deploy/install/uninstall.sh` — `detect_install_mode()`

- 把主检测路径从 `$parent/ongrid/docker-compose.yml` 改为 `$install_dir/docker-compose.yml`
  (与 install.sh 实际写入路径一致)。
- 保留旧双层路径作为 **best-effort legacy fallback**，命中时打 warn 提醒操作员
  重新安装到当前单层布局（在边界用例下避免误判成 `none`）。

### 2. `deploy/install/uninstall.sh` — compose 分支的目录解析

- 旧的 `PARENT_DIR=$INSTALL_DIR; INSTALL_DIR=$PARENT_DIR/ongrid; WEB_DIR=$PARENT_DIR/ongrid-web; DATA_DIR=$PARENT_DIR/data; LOG_DIR=$PARENT_DIR/logs`
  改为 `INSTALL_DIR=${ONGRID_INSTALL_DIR:-/opt/ongrid}; WEB_DIR=$INSTALL_DIR/ongrid-web; DATA_DIR=$INSTALL_DIR/data; LOG_DIR=$INSTALL_DIR/logs`。
- 如果不修复第 1 步，compose 分支会被走到，但 `COMPOSE_FILE=$INSTALL_DIR/docker-compose.yml`
  仍然指向 `/opt/ongrid/ongrid/docker-compose.yml`，`docker compose down` 同样找不到 compose
  文件。
- 把 line 207 处的 `$PARENT_DIR/data` 残留注释更新为"通过 ONGRID_DATA_DIR 覆盖即可"。

### 3. .record 本文件

记录上述修复，便于后续追溯。

## 验证

- `bash -n deploy/install/uninstall.sh` 语法 ok（plan / verify 步骤执行）。
- 人工对照 install.sh line 593-609 + upgrade.sh line 345-354 + uninstall.sh 路径解析，三处
  已统一在单层：`INSTALL_DIR=/opt/ongrid`（`ONGRID_INSTALL_DIR` 覆盖），subdir 直接挂在其下。

## 不需要做的事

- 不写单元测试（规则 3：本地校验逻辑通过即可）。
- 不改 `.env.example` / compose 文件 / docker image — 纯脚本层 bug。
- 不动 `upgrade.sh` — 它已经走单层。
- 不重打包 release tarball（用户当前部署需要先把 `/opt/ongrid` 路径下的 docker stack
  手动 `docker compose down -v` 再用修复后的 uninstall.sh；后续 release 重新出包即可）。
