# edge install 链：双份 service 定义漂移修复 + audit caps 透传

> 日期：2026-07-20
> 触发：servernode 上 auditbeat 9.x 的 auditd 模块报
> `failed to create audit data client: failed to get audit status: operation not permitted`
> （euid=974、无 CAP_AUDIT_*、读 /var/log/audit/audit.log EPERM）

## 1. 现象 & 直接原因

servernode 是一台 Kylin Linux 10 测试机，走 `curl -k -sSL https://192.168.25.30/install.sh | bash -s -- --access-key=... ...` 装机。装机后 vi 出 `/etc/systemd/system/ongrid-edge.service`，关键字段：

```ini
User=ongrid-edge
Group=ongrid-edge
AmbientCapabilities=CAP_NET_ADMIN
```

auditbeat 子进程以 euid=974 启动，调 `audit_open()` → `audit_get_status()` 走 NETLINK_AUDIT，EPERM → Exiting。

## 2. 装机链路 / service 文件来源矩阵

`/etc/systemd/system/ongrid-edge.service` 在 edge 端**有两份互不知情的生成源**：

| 装机 / 升级路径 | 入口脚本 | service 文件从哪来 | 当前状态 |
|---|---|---|---|
| curl-pipe（operator 手跑 / UI 一键） | `deploy/install/edge/install.sh`（nginx `/install.sh` 静态目录） | L400-448 bash heredoc `cat > "$SERVICE_FILE" <<EOF ... EOF` 硬编码写入 | **仍是 User=ongrid-edge / AmbientCapabilities=CAP_NET_ADMIN** ← 病灶 |
| 离线 tarball | `deploy/install/edge/install-edge.sh` | `cp deploy/install/edge/ongrid-edge.service /etc/systemd/system/` → sed 渲染 `__ENV_FILE__` 等占位符 | 跟 d809b3fc 后的模板一致（User=root，但 caps 仍只有 NET_ADMIN） |
| 远程 whole-bundle 升级（ADR-024） | `apply-pending-upgrade.sh` + `build-edge-bundle.sh` | 不写 service 文件，MANIFEST 里只有 binary / apply-hook | **永远无法刷新 service 文件** |

nginx 配置 [nginx.conf L257-261](file://F:/Code/Go/运维/ongrid-new/deploy/install/nginx.conf#L257-L261)：

```nginx
location = /install.sh {
    alias /usr/share/nginx/edge/install.sh;
    ...
}
```

`/usr/share/nginx/edge/` 由服务端 [deploy/install/install.sh L656-660](file://F:/Code/Go/运维/ongrid-new/deploy/install/install.sh#L656-L660) 的 `cp -rf "edge/." "WEB_DIR/edge/"` 维护（无 sed 渲染）。

## 3. 根因

d809b3fc (2026-07-20 14:39:20) `chore(proxy): 移除自动代理配置并调整edge服务权限` 把 `deploy/install/edge/ongrid-edge.service` 模板从 `User=ongrid-edge` 改成 `User=root`，并加了 audit 解释注释，但**只动了模板**，没同步 curl-pipe 装机路径使用的 `install.sh` heredoc。

curl-pipe 装机后 `/etc/systemd/system/ongrid-edge.service` 内容由 heredoc 决定 —— 即便修了模板，离线 tarball 路径之外的所有装机仍保持 `User=ongrid-edge` 不变。

即便 heredoc 也同步成 `User=root`，`AmbientCapabilities=CAP_NET_ADMIN` + `NoNewPrivileges=true` 下，子进程的 pP 仍只有 `CAP_NET_ADMIN` —— audit 子进程拿不到 `CAP_AUDIT_READ`，`audit_open()` 仍 EPERM。

## 4. 修复

### 4.1 [deploy/install/edge/install.sh](file://F:/Code/Go/运维/ongrid-new/deploy/install/edge/install.sh) L400-448（heredoc）

- `User=ongrid-edge` / `Group=ongrid-edge` → `User=root` / `Group=root`
- `AmbientCapabilities=CAP_NET_ADMIN` → `AmbientCapabilities=CAP_NET_ADMIN CAP_AUDIT_READ CAP_AUDIT_WRITE CAP_DAC_READ_SEARCH`
- ADR-024 注释里把 "sandboxed + non-root" 改成 "runs as User=root, sandboxed by ProtectSystem=strict"
- 在 `User=` 行前补 User=root 解释段 + cap 列表注释

### 4.2 [deploy/install/edge/ongrid-edge.service](file://F:/Code/Go/运维/ongrid-new/deploy/install/edge/ongrid-edge.service) L39-58（模板）

- `AmbientCapabilities=CAP_NET_ADMIN` → `AmbientCapabilities=CAP_NET_ADMIN CAP_AUDIT_READ CAP_AUDIT_WRITE CAP_DAC_READ_SEARCH`
- 在 `# itself no longer drops to it.` 后补 cap 解释段 + dual-source sync 备注

### 4.3 [deploy/install/edge/install.sh](file://F:/Code/Go/运维/ongrid-new/deploy/install/edge/install.sh) self-check

在原有 self-check 末尾、`if [[ $SELFCHECK_FAIL -eq 0 ]]` 之前**新增一段** service file 字段断言：

- `^User=root$` 必须存在
- `^AmbientCapabilities=.*\bCAP_AUDIT_READ\b` 必须命中
- `^AmbientCapabilities=.*\bCAP_DAC_READ_SEARCH\b` 必须命中
- 不通过 → `log_error` + `SELFCHECK_FAIL=1`（fail-soft，不影响 service 启动，只在 final report 标黄）

## 5. cap 列表设计

| cap | 用途 | 必要性 |
|---|---|---|
| `CAP_NET_ADMIN` | 历史保留；NETLINK_AUDIT 在部分发行版也走 netlink | 保留 |
| `CAP_AUDIT_READ` | `audit_open()` → `audit_get_status()` 订阅 NETLINK_AUDIT multicast | **必须** |
| `CAP_AUDIT_WRITE` | auditbeat system module / kernel-audit 写路径 | 加稳 |
| `CAP_DAC_READ_SEARCH` | 绕过 DAC 读 `/var/log/audit/audit.log`（root:audit 0600） | **必须** |

不引入 `CAP_AUDIT_CONTROL`（auditctl 类，auditbeat 不需要）、`CAP_DAC_OVERRIDE`（过度授权，会放大成「任何文件都能读写」）。

`NoNewPrivileges=true` 下 ambient 是子进程 cap 的**唯一**来源，所以四个 cap 必须一次性列全，少一个 audit 模块就在对应路径 EPERM。

## 6. dual-source 风险登记

`/etc/systemd/system/ongrid-edge.service` 在 edge 端**目前仍有两份生成源**：

1. `deploy/install/edge/install.sh` 的 heredoc（curl-pipe 路径）
2. `deploy/install/edge/ongrid-edge.service` 模板（离线 tarball 路径）

本次只同步内容 + self-check 兜底。**未来改 service 任何字段**必须同时改：

- `deploy/install/edge/install.sh` heredoc
- `deploy/install/edge/ongrid-edge.service` 模板

并在 self-check 里验证（已经把 `User=root` 和 `AmbientCapabilities=` 含 CAP_AUDIT_READ / CAP_DAC_READ_SEARCH 列为硬断言）。

**长期债务**：消除双份拷贝（让 install.sh 读取模板 + sed 替换）放到独立 RFC。本次未做，理由：

- sed 不能跨 heredoc 行替换，需要把 `cat > $SERVICE_FILE <<EOF ... EOF` 改成多段写入
- curl-pipe 模式下 `$0` 不是文件路径，`$BASH_SOURCE[0]` 也不稳定，模板路径怎么解析要单独设计
- 离线 tarball 路径在 `[SCRIPT_DIR]` 解析也有差异
- 短期看 self-check 兜底足够，长期看要拆 RFC 收敛

## 7. 三套装机 / 升级路径的覆盖情况（修后）

| 路径 | service 文件来源 | 修后能否拿到 User=root + audit caps |
|---|---|---|
| curl-pipe（重装） | install.sh heredoc | ✅ 直接生效（本次 4.1） |
| curl-pipe（升级已装机） | 同上 + `systemctl restart ongrid-edge` | ✅ daemon-reload + restart 即生效 |
| 离线 tarball（重装） | ongrid-edge.service 模板 + sed 渲染 | ✅ 直接生效（本次 4.2） |
| 远程 bundle 升级（ADR-024） | 不动 service 文件 | ❌ **仍不会自动刷新** —— 必须先在装机侧重跑 curl-pipe install.sh 一次，或 operator 手动 `scp deploy/install/edge/ongrid-edge.service root@<edge>:/etc/systemd/system/ && systemctl daemon-reload && systemctl restart ongrid-edge` |

## 8. 回滚方案

### 仓库层

`git revert <本次 commit>` 一次回滚（commit 计划在 plan §6）。

### 现场层

在 edge 上把 service 文件里 `User=root` 改回 `User=ongrid-edge`、`AmbientCapabilities=` 改回 `CAP_NET_ADMIN`：

```bash
ssh root@<edge>
sed -i 's/^User=root$/User=ongrid-edge/' /etc/systemd/system/ongrid-edge.service
sed -i 's/^AmbientCapabilities=.*$/AmbientCapabilities=CAP_NET_ADMIN/' /etc/systemd/system/ongrid-edge.service
systemctl daemon-reload && systemctl restart ongrid-edge
```

回滚后 audit 模块恢复到 2026-07-18 之前的状态（默认 fim-only，不主动启用 auditd）。可在生产误判 `User=root` + audit caps 引入未知风险时使用。

## 9. 装机侧验证脚本（operator 跑）

```bash
# 装机或重启后跑：
systemctl cat ongrid-edge | grep -E '^(User|Group|AmbientCapabilities)='
# 期望：
#   User=root
#   Group=root
#   AmbientCapabilities=CAP_NET_ADMIN CAP_AUDIT_READ CAP_AUDIT_WRITE CAP_DAC_READ_SEARCH

journalctl -u ongrid-edge -n 200 | grep -E 'operation not permitted|audit_open|failed to get audit status'
# 期望：空

journalctl -u ongrid-edge -n 200 | grep -E 'auditd'
# 期望：能看到 auditd 模块正常 register / subscribe，没有 Exiting
```

## 10. 仓库层验证脚本（本地 grep 自查）

```bash
# 双份 service 源关键字段应当完全对齐
grep -nE '^(User|Group|AmbientCapabilities)=' \
    deploy/install/edge/install.sh deploy/install/edge/ongrid-edge.service
# 期望两文件都出现：
#   User=root
#   Group=root
#   AmbientCapabilities=CAP_NET_ADMIN CAP_AUDIT_READ CAP_AUDIT_WRITE CAP_DAC_READ_SEARCH
```

## 11. 关联 record

- [2026-07-18-audit-default-adds-auditd.md](file://F:/Code/Go/运维/ongrid-new/.record/2026-07-18-audit-default-adds-auditd.md) —— auditd 进入默认启用模块（病灶前置事件）
- [2026-07-18-disable-install-auto-proxy-and-edge-root.md](file://F:/Code/Go/运维/ongrid-new/.record/2026-07-18-disable-install-auto-proxy-and-edge-root.md) —— d809b3fc 当时只改 ongrid-edge.service 模板没同步 install.sh heredoc（病灶直接成因）
- [2026-07-12-fix-auditbeat-94-schema.md](file://F:/Code/Go/运维/ongrid-new/record/2026-07-12-fix-auditbeat-94-schema.md) —— auditbeat 9.x 路径 / 权限相关历史修复