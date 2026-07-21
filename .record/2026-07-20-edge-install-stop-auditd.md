# edge 安装链：统一停用 OS 自带 auditd，让 auditbeat 独占 audit 子系统

> 日期：2026-07-20
> 触发：完成 [2026-07-20-edge-install-heredoc-mismatch-and-audit-caps.md](2026-07-20-edge-install-heredoc-mismatch-and-audit-caps.md) 的 service 文件 / caps 修复后，servernode 上的 auditbeat 仍偶发 `failed to create audit data client: failed to get audit status: operation not permitted`，定位到根因是 OS 自带 `auditd.service` 与 auditbeat 的 auditd 模块抢 NETLINK_AUDIT。

## 1. 现象 & 根因

auditbeat 9.x 的 auditd 模块通过 `audit_open()` → `audit_get_status()` 订阅 NETLINK_AUDIT multicast。Linux 内核同一时刻只允许一个进程独占 NETLINK_AUDIT 订阅；如果机器上 `auditd.service` 也在跑（RHEL / Kylin / CentOS Stream 默认启用），两者会互相 EPERM。

auditbeat 拿到 `EPERM` 后会 retry 几次再 Exiting，operator 看到的就是「audit 插件起不来」+ 主机审计页面一片空白。caps 修对了只是必要条件，不是充分条件。

## 2. 改动总览

| 文件 | 函数名 | 调用位置 |
|---|---|---|
| [deploy/install/edge/install.sh](../deploy/install/edge/install.sh) | `stop_auditd_for_auditbeat` | 紧贴 "bundled plugin binaries (ADR-015)" 段之后、system user 创建段之前 |
| [deploy/install/edge/install-edge.sh](../deploy/install/edge/install-edge.sh) | `stop_auditd_for_auditbeat` | 紧贴 system user 创建段 + 群组授权之后、`# ---------- install binary ----------` 之前 |
| [deploy/install/apply-pending-upgrade.sh](../deploy/install/apply-pending-upgrade.sh) | `ensure_auditd_disabled` | 紧贴 `ensure_log_groups` 之后、`maybe_rollback` 之前 |

`install.sh` / `install-edge.sh` 用 `log_info` / `log_warn`（带 `[INFO]` `[WARN]` 前缀），`apply-pending-upgrade.sh` 用原生 `log()`（写入 syslog `ongrid-edge-upgrade` tag）—— 与各自文件既有风格一致。

## 3. 函数定义（install.sh / install-edge.sh 一致）

```bash
stop_auditd_for_auditbeat() {
    command -v systemctl >/dev/null 2>&1 || return 0
    # auditd.service unit 不存在 → 静默跳过
    systemctl cat auditd.service >/dev/null 2>&1 || return 0
    if systemctl is-active --quiet auditd.service 2>/dev/null; then
        if systemctl stop auditd.service 2>/dev/null; then
            log_info "stopped auditd.service (auditbeat's auditd module owns the audit subsystem now)"
        else
            log_warn "failed to stop auditd.service — audit plugin may conflict; continue anyway"
        fi
    fi
    if systemctl is-enabled --quiet auditd.service 2>/dev/null; then
        if systemctl disable auditd.service 2>/dev/null; then
            log_info "disabled auditd.service (auditbeat replaces it on next boot)"
        else
            log_warn "failed to disable auditd.service — it'll restart on next boot; continue anyway"
        fi
    fi
}
stop_auditd_for_auditbeat
```

## 4. apply-pending-upgrade.sh 的等价实现

```bash
ensure_auditd_disabled() {
  command -v systemctl >/dev/null 2>&1 || return 0
  systemctl cat auditd.service >/dev/null 2>&1 || return 0
  if systemctl is-active --quiet auditd.service 2>/dev/null; then
    systemctl stop auditd.service 2>/dev/null \
      && log "stopped auditd.service (auditbeat's auditd module owns the audit subsystem now)" \
      || log "failed to stop auditd.service; audit plugin may conflict"
  fi
  if systemctl is-enabled --quiet auditd.service 2>/dev/null; then
    systemctl disable auditd.service 2>/dev/null \
      && log "disabled auditd.service (auditbeat replaces it on next boot)" \
      || log "failed to disable auditd.service; it'll restart on next boot"
  fi
}
ensure_auditd_disabled
```

函数名加 `ensure_` 前缀是为了体现 idempotent 语义，与同文件里的 `ensure_log_groups` 对齐（后者在 line 70-77，已经稳定运行很久）。

## 5. 三条装机/升级路径全覆盖

| 路径 | 入口脚本 | 何时触发 stop_auditd |
|---|---|---|
| curl-pipe（operator 手跑 / UI 一键） | `deploy/install/edge/install.sh`（nginx `/install.sh` 静态目录） | 装机一次 |
| 离线 tarball | `deploy/install/edge/install-edge.sh` | 装机一次 |
| 远程 whole-bundle 升级（ADR-024） | `apply-pending-upgrade.sh` + `ongrid-edge-upgrade.service` oneshot | **每次开机** + Restart=always 自动重启都跑 |

第三条是关键：远程 bundle 升级不会重跑装机脚本，如果只在 install.sh / install-edge.sh 加函数，存量 edge 通过 bundle 升级到新版本审计插件时仍然停不掉 auditd。把 ensure_auditd_disabled 放到 apply-pending-upgrade.sh 里相当于"每次开机都重试一次"，确保任何路径拿到的 edge 都满足 "auditd 已停" 这条前置条件。

## 6. 降级策略

| 场景 | 行为 |
|---|---|
| `systemctl` 不存在（容器、darwin、init=/bin/bash） | `command -v systemctl` 直接 `return 0`，silent skip |
| `auditd.service` unit 不存在（极简镜像、WSL） | `systemctl cat auditd.service` 失败 → `return 0`，silent skip |
| `auditd.service` 已 inactive | 跳过 stop 分支，继续 disable 分支（如果 enabled） |
| `auditd.service` 已 enabled + active | 停 + 禁用都执行 |
| `systemctl stop` 失败 | `log_warn` 但 continue，不阻断装机主流程 |
| `systemctl disable` 失败 | `log_warn` 但 continue，不阻断装机主流程 |

不动 audit 规则文件（`/etc/audit/rules.d/`、`/var/lib/audit/rules.d/`）—— 保留现场便于事后排查。

uninstall 不恢复 auditd 开机自启 —— 用户没要求；少一个动作少一次决策；如果后续 operator 想恢复，手动 `systemctl enable --now auditd.service` 即可。

## 7. 取舍

- **为什么不直接清空 audit 规则 + 卸 auditd 包？** 过于侵入。RHEL/Kylin 上 auditd 是 OS 包的一部分，卸包会触发依赖问题；operator 可能在别处（合规日志）依赖 auditd 规则。本改动只 stop + disable，规则文件原样保留。
- **为什么用 `systemctl cat auditd.service` 探测 unit 而不是 `systemctl list-unit-files`？** `systemctl cat` 在 unit 不存在时 exit 1 + stderr 报错 `Unit auditd.service not found`，但 stdout 是空，刚好作为"是否存在"的探测信号；`list-unit-files` 在某些发行版（特别是被 mask 掉的 unit）行为不一致，跨发行版兼容性不如 `cat`。
- **为什么不放在 service unit 的 `ExecStartPre=` 里？** service unit 是 non-root + sandboxed（`ProtectSystem=strict` + `NoNewPrivileges=true`），systemctl 控制本机 service 需要 elevated 权限，sandbox 内会 EPERM。把这一步放到 installer / upgrade hook 里（都跑在 root context）才是对的。
- **为什么不在 self-check 段加 auditd-running 检测？** 可以加，但收益小：self-check 已经把 service file 关键字段（User=root、AmbientCapabilities=）断言了，auditd 状态对装机主流程是 best-effort。如果未来要硬卡，再补 SELFCHECK_FAIL=1 分支。

## 8. 验证

```bash
# 语法：三个文件都能被 bash -n 解析
bash -n deploy/install/edge/install.sh
bash -n deploy/install/edge/install-edge.sh
bash -n deploy/install/apply-pending-upgrade.sh

# 调用点：三个文件都至少出现一次 stop_auditd_for_auditbeat / ensure_auditd_disabled
grep -nE 'stop_auditd_for_auditbeat|ensure_auditd_disabled' \
    deploy/install/edge/install.sh \
    deploy/install/edge/install-edge.sh \
    deploy/install/apply-pending-upgrade.sh
```

按 project rule 本地不跑 .sh 实际逻辑；现场侧由 operator 在 192.168.25.56 上重跑装机确认：
- 重跑前：`systemctl is-active auditd` → active；`systemctl is-enabled auditd` → enabled
- 重跑后：`systemctl is-active auditd` → inactive / failed；`systemctl is-enabled auditd` → disabled
- journal：`journalctl -u ongrid-edge -n 200 | grep -E 'auditd'` 应能看到 auditbeat 的 auditd 模块正常 register，没有 Exiting

## 9. 关联 record

- [2026-07-18-audit-default-adds-auditd.md](2026-07-18-audit-default-adds-auditd.md) —— audit 插件默认启用 auditd 模块（前置事件，把 auditbeat 推到 audit 子系统争夺战）
- [2026-07-20-edge-install-heredoc-mismatch-and-audit-caps.md](2026-07-20-edge-install-heredoc-mismatch-and-audit-caps.md) —— service 文件 User=root + AmbientCapabilities 含 CAP_AUDIT_* 等 audit caps 透传（必要不充分条件）
- [2026-07-12-fix-auditbeat-94-schema.md](record/2026-07-12-fix-auditbeat-94-schema.md) —— auditbeat 9.x 路径 / 权限相关历史修复

## 10. 仓库层验证脚本

```bash
# 三个文件函数定义都在
grep -nE '^(stop_auditd_for_auditbeat|ensure_auditd_disabled)\(\)' \
    deploy/install/edge/install.sh \
    deploy/install/edge/install-edge.sh \
    deploy/install/apply-pending-upgrade.sh
# 期望：
#   deploy/install/edge/install.sh:        stop_auditd_for_auditbeat() {
#   deploy/install/edge/install-edge.sh:   stop_auditd_for_auditbeat() {
#   deploy/install/apply-pending-upgrade.sh: ensure_auditd_disabled() {

# 调用点都在
grep -nE '^stop_auditd_for_auditbeat$|^ensure_auditd_disabled$' \
    deploy/install/edge/install.sh \
    deploy/install/edge/install-edge.sh \
    deploy/install/apply-pending-upgrade.sh
# 期望每个文件各出现一次调用点
```

## 11. 回滚方案

`git revert <commit>` 一次回滚所有改动。现场层 auditd 不会被反向启用，需要 operator 手动 `systemctl enable --now auditd.service` 才会恢复（设计上保留现场；audit 规则文件未动）。