# 2026-07-05 arm64 release 重打包

## 背景
用户需要在 arm64 架构主机上部署 ongrid v0.9.0，已修复 web html 路径 bug（见
`.record/2026-07-05-fix-html-path.md`），需要重新跑 `make package` 生成 arm64 release tarball。

## 命令
```
make package TARGET_OS=linux TARGET_ARCH=arm64 PLATFORM=linux/arm64
```

## 改动
- `dist/package.sh` line 344-382：把 macOS-only `shasum -a 256` 改为跨平台 `SHA256_BIN` 自动检测
  （`sha256sum` 优先 → `shasum -a 256` 回退 → 都缺则 warn 但继续），并把 `curl --max-time 600`
  提到 `--max-time 1800`（30 分钟）应对慢速 CN 网络下 100MB 级别 prometheus 二进制包可能下不完。
  - 触发原因：第一次跑遇到 `shasum：未找到命令`（dist/package.sh 在 Linux 上跑但用了 macOS 风格
    hash 命令），且 prometheus-2.54.0.linux-arm64.tar.gz（~95MB）在 600s 内只下到 53MB 就被
    curl 28 切断。
  - 修复验证：`bash -n dist/package.sh` 语法 ok；重跑后 prometheus/loki/tempo/qdrant 全部下载完
    成并通过 sha 校验（不再走 skip 路径）。

## 产物
| 文件 | 大小 | sha256 |
|------|------|--------|
| `dist/out/ongrid-v0.9.0-linux-arm64.tar.xz` | 372M | `0fd897f874cce339ec085db7111bb5cf44063b255f41fae7227d177cdc2ea8ae` |
| `dist/out/ongrid-v0.9.0-linux-arm64.tar.xz.sha256` | 99B | — |
| `dist/out/edge-bundles/edge-bundle-linux-arm64-v0.9.0.tar.gz` | 144M | `5fb4225c385f98ac8ac32a5ee4d904df61d38724d3e0ee0f4b3f87b519336b57` |

### 验证要点
- `xz -t` 通过 / `sha256sum -c` 通过（"成功"）
- `bin/ongrid` 是 `ELF 64-bit LSB executable, ARM aarch64`（v1 SYSV / ld-linux-aarch64 / stripped）
- `bin/ongrid-frontier` 是 `ELF 64-bit LSB executable, ARM aarch64`（musl）
- `bin/libonnxruntime.so.1.20.1` 在包内
- `images/ongrid.tar` / `images/ongrid-web.tar` / `images/frontier.tar` 三个 docker 镜像都在
- tar 内 `install.sh` sha 与 `deploy/install/install.sh` 完全一致（`195effa3...`）
- tar 内 `upgrade.sh` sha 与 `deploy/install/upgrade.sh` 完全一致（`7589dc86...`）
- 总条目数 87，包含 systemd/grafana-provisioning/searxng/loki/tempo/qdrant 配置全套

### bin/stack-deps 内容
- prometheus（来自 prometheus-2.54.0.linux-arm64.tar.gz）
- loki（来自 loki-linux-arm64.zip v3.4.0）
- tempo（来自 tempo_2.5.0_linux_arm64.tar.gz）
- qdrant（来自 qdrant-aarch64-unknown-linux-musl.tar.gz v1.11.3）
- ARCH（脚本运行时写入）

### auditbeat 缺失
依然按 known issue 处理：edge bundle 会 warn "missing auditbeat — bundle will be incomplete"
但仍正常出包；操作员按 `resource/auditbeat/README.md` 自行放入二进制即可。

## 时间线（命令在 terminal_id=1 后台运行）
- 21:26 make package 启动
- 21:29 docker 镜像 build 完毕 + edge bundle (amd64 + arm64) 出包
- 21:30 package.sh stage + docker save 开始下载 prometheus
- 21:44 prometheus 下完（95M）
- 21:48 loki 下完（29M）；tempo 开始
- 21:55 tempo 下完（41M）；qdrant 开始
- ~22:01 qdrant 下完（25M）
- 22:04 tar.xz + sha256 落地

## 备注
- 此次打包产物会包含 2026-07-05-fix-html-path.md 修复后的 install.sh / upgrade.sh
  （已用 sha256 双向比对确认），新部署将不再有 SPA 路径问题。
- 当前部署的临时修（root /usr/share/nginx/html/html 等）保留；下次 upgrade.sh 走标准路径后
  会自动落到正确位置。
