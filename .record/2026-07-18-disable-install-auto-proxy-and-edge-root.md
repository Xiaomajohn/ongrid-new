# 2026-07-18 — 取消 install/upgrade 自动代理写入 + ongrid-edge.service 改 root

## 需求

1. install.sh / upgrade.sh 不再自动检测 shell 环境 / `/etc/environment` 代理并写到 `.env`；`ONGRID_HTTP_PROXY` / `ONGRID_HTTPS_PROXY` / `ONGRID_NO_PROXY` 三行**永远留空**，由 operator 手动填
2. `ongrid-edge.service` 改用 `User=root` / `Group=root` 启动，因为启用了 audit 模块（NETLINK_AUDIT + `/var/log/audit/audit.log` 读取需要 root 权限）

接续 [2026-07-18-dev-compose-ongrid-proxy.md](./2026-07-18-dev-compose-ongrid-proxy.md)。前一篇只动了 dev compose 的代理写法；本篇聚焦 install 包的安装/升级脚本 + edge systemd unit。

## 改动 1：install.sh / upgrade.sh 取消自动代理写入

### 背景问题（重现 install 现场）

[2026-07-18-dev-compose-ongrid-proxy.md](./2026-07-18-dev-compose-ongrid-proxy.md) 里给出的现场：

```
ONGRID_NO_PROXY=localhost,localhost4,localhost4.localdomain4,...   ← 这就是污染源
```

这是 install.sh 的 `detect_host_proxy()` → `apply_host_proxy_to_env()` → `fill_blank ONGRID_NO_PROXY` 链路把系统 / docker daemon 默认的 `no_proxy` 抓下来写进去的。**install.sh 当时 shell 没设 `http_proxy` / `HTTPS_PROXY`，所以 `ONGRID_HTTP_PROXY` / `ONGRID_HTTPS_PROXY` 留空；唯一被填的就是 `ONGRID_NO_PROXY`，而且填的还是个污染值。**

这个行为有两个根因：

1. `detect_host_proxy()` 把 `no_proxy=localhost,localhost4,...` 也当成"业务代理配置"抓——但这其实是 docker daemon / 某些发行版 `/etc/environment` 的系统级默认，不是 operator 想要的业务 NO_PROXY
2. `fill_blank` 语义虽然尊重 operator 手动值，但**只在"空白时"** 写——而 operator 第一次安装时 ONGRID_NO_PROXY 是模板里的空行，等于"空白"，于是 install.sh 抢先把系统默认值塞进去了，operator 后面再来改就晚了

### 改动

**`deploy/install/install.sh`**：把第 977 行的 `apply_host_proxy_to_env` 调用点替换为 17 行注释，说明：

- 三行代理现在是 operator-managed only
- `detect_host_proxy` / `apply_host_proxy_to_env` / `append_internal_no_proxy_domains` 三个函数定义保留（不动），作为参考 / 将来 opt-in 重启的口子
- 将来如果 corp 想要恢复自动检测，必须显式 `ONGRID_AUTODETECT_PROXY=1` 开关，避免再次发生 silent overwrite

**`deploy/install/upgrade.sh`**：同步把第 719 行的 `upgrade_apply_host_proxy` 调用点注释掉。

### 为什么保留函数定义、不删代码

- install/upgrade 是长生命周期脚本，删函数可能误伤其他隐藏调用路径
- 注释化调用点 + 保留函数 = 改动最小、风险最低、回滚只需去掉注释前缀
- 函数本身语义没问题（`fill_blank` 仍然尊重 operator 手动值），问题在于**调用时机**

### install-systemd.sh 不动

`deploy/install/systemd/install-systemd.sh` 也有 `systemd_setup_proxy_env` 调用，但那是**systemd 直装模式**（不是 docker-compose 模式）的脚本。当前部署用的是 docker-compose 模式（`deploy/install/docker-compose.yml` + `deploy/docker-compose.yml`），所以 install-systemd.sh 不在本期改动范围。如未来要切 systemd 直装，需单独走一遍同样的"取消自动写入"流程。

## 改动 2：ongrid-edge.service 改 root 启动

### 原因

audit 模块需要：
- `NETLINK_AUDIT` 套接字（kernel audit 子系统）→ 需要 `CAP_AUDIT_READ` / `CAP_AUDIT_WRITE` / `CAP_AUDIT_CONTROL` 之一
- `/var/log/audit/audit.log` 读取 → 需要 DAC override（root 默认有）

之前用 `User=ongrid-edge` + `AmbientCapabilities=CAP_NET_ADMIN` 的非 root 方案在 audit 模块上不够（需要再加 CAP_AUDIT_* + group adm + DAC override 权限链），最简单且合理的方案是**整个 agent 用 root 跑**，配合 systemd sandbox directives 限制 blast radius。

### 改动

**`deploy/install/edge/ongrid-edge.service`**：

| 字段 | 原值 | 新值 | 说明 |
|------|------|------|------|
| `User=` | `ongrid-edge` | `root` | root 启动，能读 audit 日志 + NETLINK_AUDIT |
| `Group=` | `ongrid-edge` | `root` | 同步 root |
| `[Unit]` 顶部注释 | `non-root (User=)` | `as root (User=root, required by the audit plugin)` | 说明 root 的原因 |
| `[Service]` 块新加注释 | — | `User=root / Group=root — required by the audit plugin ...` | 解释为什么 root + 提示 install-edge.sh 仍创建 `ongrid-edge` 系统用户做文件 ownership |

**保留不动**：

- `AmbientCapabilities=CAP_NET_ADMIN` — root 不需要这个 capability，但保留无副作用（root 已经有 NET_ADMIN）
- `ProtectSystem=strict` — 对 root 仍然生效，把可写范围限制在 `ReadWritePaths` 列出的三路径
- `ProtectHome=true` / `PrivateTmp=true` — 仍生效
- `ReadWritePaths=__STATE_DIR__ __LOG_DIR__ __LIB_DIR__` — **关键**：限定 root 进程只能写这三处，不能乱动 `/usr/local` / `/etc` 等其他位置

### install-edge.sh 不动

`deploy/install/edge/install-edge.sh` 仍然创建 `ongrid-edge` 系统用户 + chown `STATE_DIR` / `PLUGIN_WORK_DIR` 到它。这是因为：

1. binary 仍由 root 安装（`install -m 0755 -o root -g root`），root 能执行
2. STATE_DIR / PLUGIN_WORK_DIR owner 是 `ongrid-edge:ongrid-edge`，但 root 能读写（不受 DAC 限制）
3. 未来如果切回非 root 启动，目录 ownership 已经就位，不用再调整

这样 install-edge.sh 不用改，但 edge 进程以 root 跑，能完整访问 audit 子系统。

## 验证

按项目开发规则：
- 本地不跑 .sh / make（参考规则第 6 条：禁止在 Windows 上调试 Linux 上的脚本与打包程序）
- 本地不写单元测试（参考规则第 3 条）
- 静态校验：
  - install.sh / upgrade.sh 的替换点位置已用 grep 确认函数调用点确实注释化，函数定义仍存在
  - ongrid-edge.service 用 Read 整体过一遍：User=root / Group=root、ProtectSystem=strict 仍在、ReadWritePaths 三路径不变
- 实际端到端（deploy 到 192.168.25.56 后）：
  - `systemctl cat ongrid-edge` 应显示 `User=root` / `Group=root`
  - `ps -o user,group,comm -p $(pgrep -f ongrid-edge)` 应显示 `root root /.../ongrid-edge`
  - `journalctl -u ongrid-edge -n 50` 应能看到 audit plugin 正常启动，不再有 `permission denied` / `operation not permitted`
  - 手动清掉 `/opt/ongrid/.env` 里 `ONGRID_NO_PROXY=localhost,localhost4,...`，重跑 `./upgrade.sh`，确认 `ONGRID_NO_PROXY` 不被改回污染值

## 关联文件

- `deploy/install/install.sh` — 调用点注释化（行 974-985 区域）
- `deploy/install/upgrade.sh` — 调用点注释化（行 719-729 区域）
- `deploy/install/edge/ongrid-edge.service` — `User=` / `Group=` 改 root + 注释更新
- `deploy/install/edge/install-edge.sh` — **未改**，仍创建 `ongrid-edge` 系统用户做 ownership
- `deploy/install/systemd/install-systemd.sh` — **未改**（systemd 直装模式不在本期范围）
- `deploy/install/.env.example` — **未改**，三行代理仍保留为模板空行
