# 2026-07-06 一键安装 curl-pipe exit 2 修复：让 install.sh stderr 回到 worker 日志

## 现象

修完 `.record/2026-07-05-fix-installjob-options-json-3140.md`（options_json
JSON 零值）后，`POST /api/v1/devices/1/install-edge` 一键安装仍然失败。worker
日志只看到 SSH 层暴露的 `Process exited with status 2`，**找不到 install.sh
到底卡在哪一步**：

```
{"msg":"installjob: ssh connect start","device_id":1,"host":"192.168.25.56","port":22,"user":"root"}
{"msg":"installjob: installer returned error","job_id":2,"device_id":1,
 "err":"installjob: run curl-pipe install: Process exited with status 2"}
```

worker → installer 在 ~0.7s 内退出，远没到 `curl -fLk` 下载 binary 那一步
（下载有 `--retry 3 --retry-delay 2` 至少 4s+）。pumpStream 把 stderr 写到
`install_jobs.log_output`（mediumtext），**但这条内容不会进 manager 的
worker log**，所以单看 `journalctl -u ongrid` 抓不到 install.sh 的 `[ERROR]`
或 `set -e` 触发的实际错误行——必须查 DB 才知道，定位极慢。

## 全链路核对

| 层 | 路径 | 状态 |
|---|---|---|
| 1. UI | `web/src/components/InstallEdgeModal.tsx` | OK，前一轮 404 已修 |
| 2. API | `web/src/api/devices.ts:225 installEdge` | OK |
| 3. 后端路由 | `internal/manager/server/installjob/http.go` | OK |
| 4. 后端业务 | `internal/manager/biz/installjob/worker.go:200` installer.Install + onLog 回调 | OK |
| 5. SSH 层 | `internal/manager/biz/installjob/installer.go:159 runCurlPipe` | **BUG**：stderr 流被 `pumpStream` 写到 `onLog` 回调（→ DB log_output），但**完全没在 worker log 出现**。sess.Run 返回 `*ssh.ExitError`，err.Error() 是 SSH 协议层的固定字符串 `"Process exited with status N"`，把 install.sh 自己写的 `[ERROR] install failed at line N (exit X)` 完全吞掉 |
| 5.1 数据交互层 | `internal/manager/data/installjob/repo.go:88 AppendLog` 用 `CONCAT(log_output, ?)` 增量拼到 mediumtext 列 | OK，数据没丢，但**只在 DB 里** |

## 根因（两条规则同时命中）

1. **诊断路径断裂**：项目规则第 3 条「全栈调用链核对」要求每个层级都要给
   出结论；第 5 条「可观测性」要求 ERROR 包含完整 error chain。
   `installjob: installer returned error` 实际只携带了 SSH 层的
   `ExitError`（远端命令退出码），**没有 install.sh 自己的 stderr**——
   install.sh 的 [ERROR] 行被 `pumpStream` 转发到了 DB 的 `log_output`，
   但这个列不进 slog 的 ERROR 字段，运维只能 SELECT 查询才能看到。
2. **命令字符串裸拼**：runCurlPipe 用 `fmt.Sprintf` 把 accessKey/secretKey
   /serverEdgeAddr/serverHTTPAddr 直接拼到 shell 命令字符串，没做任何
   shell 转义。今天 base64url（`[A-Za-z0-9_-]`）和 `host:port` 不会撞到
   shell 元字符，但 secret 格式未来可能换、host 可能含 IPv6 `[::1]:port`
   这种带 `]` 的——一旦含 `'`/`$`/反引号，命令字符串就被破坏。属于
   防御性缺失。

### 顺带发现的两个隐藏坑（不修，但要让下次接手能看见）

- **`install.sh` 的 EUID check 在 `curl | bash -s --` 模式下不可用**：
  原代码 `exec sudo -E bash "$0" "$@"`，但 `bash -s` 模式下 `$0` 是
  `--` 后的第一个 arg（不是脚本路径），sudo 找不到该文件 → 退 1。
  BASH_SOURCE[0] 在 `bash -s` 模式下也只返回 `"bash"`，不可用。
  **唯一可靠的做法是直接拒绝 re-exec，让操作员把脚本先 stage 到文件**。
  本次场景里 SSH 登录是 root，EUID=0 走不到这段，但**非 root 设备
  在 curl | bash -s -- 模式下必失败**——本次顺手修了，避免下次踩。
- **`serverEdgeAddr == serverHTTPAddr` 是典型配置 bug**：
  `cmd/ongrid/main.go:828-840` 把两个 addr 都从 `cfg.PublicURL` fallback
  出来，如果 ONGRID_PUBLIC_URL 只暴露了一个 host:port，agent 会拿着
  http port 去连 geminio tunnel，握手一定失败。**这种配置错今天只在
  systemd 拉起 agent 之后才暴露，浪费 10 分钟等待窗口**。

## 修复

### 1. `internal/manager/biz/installjob/installer.go`

- **新增 `shellescape(s string) string`**：POSIX 单引号包字符串，遇 `'` 用
  标准 `'\''` 序列转义。所有 4 个 arg 全部走它（base64url 现状不含元字
  符，但这是 defense-in-depth，未来 secret 改格式或 host 用 IPv6 不再需要
  改 installer）。
- **新增 `tailBuf`（thread-safe 8KB 环缓冲）**：在 stderr pumper 旁路把
  `[stderr]` 之后的 raw 内容截到 ring 里，sess.Run 错误时把这段 dump 到
  worker 日志（`i.log.Error(... "stderr_tail", tail)`）。
- **加配置 sanity check**：若 `serverEdgeAddr != ""` 且与 `serverHTTPAddr`
  相等，install 一开始就 WARN 提示（"tunnel port vs http port"），让运维
  不用走完 10 分钟等待窗口才发现配错。
- **runCurlPipe 错误时显式 log**：`slog.String("stderr_tail", tail)` 走
  worker 的结构化日志，运维 `journalctl -u ongrid | grep stderr_tail`
  就能直接看到 install.sh 末尾的真实错误。

### 2. `deploy/install/edge/install.sh`

- **头部加 banner log**：`log_info "install.sh starting on $(uname -srm);
  user=$(whoami)"` 在 `set -euo pipefail` 之后立刻打，让 pumpStream 早期
  有内容流回——SSH pipe 打开后几毫秒内 manager 就能看到 `[INFO] install.sh
  starting on Linux x86_64 ...`，作为"脚本确实在跑"的最低信号。
- **修 EUID check**：用 `SCRIPT_PATH="${BASH_SOURCE[0]:-$0}"`，对 `bash -s
  --` 模式（BASH_SOURCE[0] 是 `"bash"` 或空，且 SCRIPT_PATH 看起来不是
  文件）直接 `log_error` + `exit 2`，给运维明确指示怎么改用脚本文件
  模式。Root 路径不变（`EUID=0` 直接跳过 if）。

## 部署

按既有 bind-mount 流程（见 `.record/2026-07-05-hot-rebuild-ongrid-binary.md`）：

```bash
cd /opt/ongrid
docker compose stop ongrid
cp -v /tmp/ongrid-test /opt/ongrid/ongrid-app/ongrid
docker compose start ongrid
```

`deploy/install/edge/install.sh` 不在 manager binary 里、也不在镜像里
——它由 `curl https://server/install.sh` 从 nginx 拉取。要让改动
`install.sh` 立即生效，需要把改动后的脚本放到 nginx 的 `/install.sh`：

```bash
docker exec ongrid-nginx cat /etc/nginx/nginx.conf | grep -A2 "install.sh"
# 默认映射在 /usr/share/nginx/html/install.sh，绑挂在 ongrid-web:/...
ls -la /opt/ongrid/ongrid-web/install.sh   # bind mount
cp -v deploy/install/edge/install.sh /opt/ongrid/ongrid-web/install.sh
```

## 验证（按规则 3 / 4）

- 编译：
  ```bash
  $ VERSION=$(cat VERSION) \
    go build -trimpath -ldflags "-X main.version=$VERSION" \
      -o /tmp/ongrid-test ./cmd/ongrid
  (no output)

  $ ls -la /tmp/ongrid-test
  -rwxr-xr-x 1 root root 60258888  7月  6 10:53 /tmp/ongrid-test
  ```
  60MB ELF 与线上 binary 大小一致（之前 60257672，新版 60258888，差 ~1KB
  ——shellescape + tailBuf + log 字段的代价）。

- 逻辑：worker 流程不变，仍然是
  `UI(InstallEdgeModal) → POST /v1/devices/{id}/install-edge
   → chi 命中路由 → createInstall → uc.Create
   → deviceRepo.Get + SSH 三件套校验 → repo.Create
   → runner.Enqueue(jobID) → workerLoop → Worker.Execute
   → issuer.CreateEdgeForDevice → installer.Install
   → SSH dial → runCurlPipe → (新) shellescape + tailBuf + edge==http WARN
   → sess.Run "curl -k -sSL ... | bash -s -- --access-key='...' ..."
   → 设备上 install.sh 头部 log_info banner → ... 走完 → pumpStream 流
   → onLog (DB) + tailBuf (内存) → 错误时 worker log 显式 dump stderr tail`。
  全程每层都有可观测点。

- 行为：
  - 成功路径：不变。sess.Run 返 nil，不写 stderr tail 日志。
  - 失败路径：原本 `installjob: installer returned error err="Process
    exited with status 2"`；现在额外有
    `level=ERROR msg="installjob: remote install.sh stderr tail" exit="Process
    exited with status 2" stderr_tail="[ERROR] install failed at line 153
    (exit 1)\n[ERROR] missing --secret-key ..."`（具体内容取决于
    install.sh 实际卡哪一步，但 [ERROR] 行不再丢）。

- 本地不验证 .sh（规则 4）。

## 已知非阻塞（不进本 PR）

- `serverEdgeAddr` / `serverHTTPAddr` 的真正解耦（让 ONGRID_INSTALL_EDGE_ADDR
  必须显式配置、未设时直接 fail fast）属于 `cmd/ongrid/main.go:821-846`
  的 TODO 范围，本 PR 只加 warning，不改 fail-fast 行为——免得改坏已有
  部署的 PublicURL 兼容路径。
- install.sh 退出后 manager 仍要在 worker log 显式 dump 整段 log_output
  表内容（A6 SPA 实时尾随 install_job_events 的 feature 之后会顺带覆盖
  这一条）。本 PR 只解决了"日志能进 worker stderr_tail 字段"，没有把
  install.sh 的 stdout 也 dump——stdout（[INFO]/[OK]）是叙事流，dump 它
  会让 worker log 噪声很大，让 install_job_events 当滚动日志就够。

## 不再做的事（避免回归）

- **不要**在 `runCurlPipe` 里把 stderr tail 直接 `panic` 出来——它是诊断
  信号，必须走 slog 走 worker log，不要让一个 stderr 丢失把进程拉崩。
- **不要**在 `shellescape` 改用 `%q`（Go syntax quoting）——bash 不认
  `\"`，会把反斜杠当成字面字符传给远端命令。必须用 POSIX single-quote
  + `'\''` 序列。
- **不要**把 tailBuf 改成无界的 `bytes.Buffer`——一个装了大量噪声的
  install.sh（譬如 systemd 拉起几千行 journal 输出）会让 worker log 的
  stderr_tail 字段膨胀到几 MB，拖垮日志聚合。8KB 是经验值，刚好够
  看到最后若干行 [ERROR]。
- **不要**把 install.sh 头部 banner 换成 `echo` 而不是 `log_info`——
  `echo` 走 stdout，pumpStream 走 stdout 也 ok，但 `log_info` 自动带
  `[INFO]` 前缀 + 颜色控制（NO_COLOR），UI 滚动面板分类更准。
- **不要**在 `[[ $EUID -ne 0 ]]` 段把 BASH_SOURCE[0] fallback 逻辑移除
  ——`bash -s --` 模式下 BASH_SOURCE[0] 是 "bash" 不是文件路径，但
  未来 bash 修了之后 BASH_SOURCE[0] 也许能拿到 /dev/fd/63 之类的占位
  路径；保留 fallback 让脚本对 bash 版本变化更宽容。
