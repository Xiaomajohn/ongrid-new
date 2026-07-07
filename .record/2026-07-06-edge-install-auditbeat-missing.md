# 2026-07-06 修复 edge 安装脚本漏下载 auditbeat

## 背景

edge 一键安装完成（ongrid-edge 已 systemd 注册 + 心跳上报后），Promtail / node_exporter / otelcol-contrib 等进程都正常起来了，但 **auditbeat 没起**。operator 在 ongrid 后台「审计 / 主机审计」里查不到任何主机审计事件。`deploy/nginx/` 里手搓的反代看到 auditbeat 二进制明明存在 `bin/linux-amd64/auditbeat`、`bin/linux-arm64/auditbeat`，证明打包阶段是有这个 plugin 的。问题在「如何把这个二进制安全搬到设备上 + 在设备上启动」这一步。

## 根因

按全栈调用链顺下来：

| 层级 | 文件:行 | 现状 | 修复 |
| --- | --- | --- | --- |
| 1. 包打包 | `dist/package.sh` | 第 512–522 行 stage auditbeat，hardfail 缺资源（offline-only 强制）✓ | 不动 |
| 2. nginx 服务 | `deploy/nginx/nginx.conf` | `/plugins/auditbeat` 路径正常提供二进制 | 不动 |
| 3. curl-pipe 安装器（设备侧执行） | `deploy/install/edge/install.sh:275-302` | fetch_plugin_bin 循环里只列 promtail / node_exporter / process_exporter / otelcol-contrib / mysqld_exporter / postgres_exporter / redis_exporter / mongodb_exporter **没有 auditbeat** ❌ | 加进去 |
| 4. self-check | `deploy/install/edge/install.sh:513-525` | 同样漏 auditbeat → 装完也不会在 ${LIB_DIR} 检查 auditbeat | 加进去（soft warn） |

具体坏代码（之前的样子）：

```bash
for pbin in promtail node_exporter process_exporter otelcol-contrib mysqld_exporter \
            postgres_exporter redis_exporter mongodb_exporter; do
    fetch_plugin_bin "$pbin"
done
```

按 user 给的修复方向「安装脚本的 auditbeat 下载的名字不对，导致请求不对」字面对齐：`fetch_plugin_bin` 内部用 `$pbin` 拼 URL `https://${CLOUD_URL}/plugins/${pbin}`，上游 nginx 提供的 URL 也是 `/plugins/auditbeat`，脚本的循环里漏了这个 token，所以 `curl` 请求根本不会发。即便改了 fetch 路径，没改主循环也不会执行。

## 改动

最小变更：只在 `deploy/install/edge/install.sh` 改两处循环 + 注释块。

1. fetch_plugin_bin 注释块加 auditbeat 说明（line 270–289）。
2. fetch 循环加 `auditbeat`（line 297–302）。
3. self-check 循环加 `auditbeat`，且 auditbeat 缺席从 hardfail 降级为 soft warn——理由：auditbeat 是 Elastic 闭源二进制，offline 资源包可能不一定含；其他 plugin（promtail / node_exporter / db_exporter）才是基线，缺席必须 hardfail 阻断 self-check。

```bash
# fetch 循环（line 297–302）
for pbin in promtail node_exporter process_exporter otelcol-contrib \
            mysqld_exporter postgres_exporter redis_exporter mongodb_exporter \
            auditbeat; do
    fetch_plugin_bin "$pbin"
done
```

```bash
# self-check 循环（line 513–525）
for tool in promtail otelcol-contrib node_exporter process_exporter \
            mysqld_exporter postgres_exporter redis_exporter mongodb_exporter \
            auditbeat; do
    if [[ -x "${LIB_DIR}/${tool}" ]]; then
        log_ok "plugin binary present: ${tool}"
    else
        log_warn "plugin binary missing: ${LIB_DIR}/${tool} — that plugin will not run (auditbeat is optional; rest surface a hard problem)"
        # auditbeat 缺席不阻断 self-check（离线资源包可能不含），其他 plugin 缺席计 hard-fail
        if [[ "${tool}" != "auditbeat" ]]; then
            SELFCHECK_FAIL=1
        fi
    fi
done
```

## 不在范围内

- auditbeat 配置（policy / ruleset）由 `deploy/edge/bash-policy.example.yaml` 这条独立路径跑，本 PR 不碰。
- 自动重打包包审计路径在 dist/package.sh，**未做**——auditbeat 的资源是从 `resource/auditbeat-9.4.2-linux-{amd64,arm64}.tar.gz` 解压出来的，前置 worktree 已经有这个文件；本 PR 只修「脚本有没去 fetch 它」。
- nginx 端 `/plugins/auditbeat` 路径校验：本 PR 用 `fetch_plugin_bin` 既有命名约定（lower-case 与目录下文件名一一对应），未单独 curl 探测。如果后续命中 404，再补一行 smoke test。

## 验证

1. `bash -n deploy/install/edge/install.sh` 语法 ✓
2. `grep -n 'auditbeat' deploy/install/edge/install.sh` —— 至少出现 4 次（注释块 + fetch 循环 + self-check 循环 + soft-warn 分支）✓
3. 在一台干净 ubuntu 上跑 curl-pipe（受限于沙箱没跑；逻辑只多 download 一个 token）
