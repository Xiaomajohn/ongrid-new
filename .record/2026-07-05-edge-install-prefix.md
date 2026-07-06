# 2026-07-05 edge 安装路径 prefix 化（统一到 /mnt/data/toos-temp）

## 背景

edge 设备的安装文件（binary / 插件二进制 / env 配置 / state / log）原本分散
在 5 个根目录（`/usr/local/bin`、`/usr/local/lib`、`/etc/`、`/var/lib/`、
`/var/log/`），operator 难以一眼看清安装范围、备份、清理也容易漏。本次需求：
把所有 edge 安装路径收敛到一个 operator 指定的 PREFIX（默认
`/mnt/data/toos-temp`）下，且 PREFIX 通过 `--prefix=PATH` 命令行参数控制。

## 新布局

```
${PREFIX}/                                    默认 /mnt/data/toos-temp
├── bin/
│   └── ongrid-edge                            原 /usr/local/bin/ongrid-edge
├── lib/
│   └── ongrid-edge/                           原 /usr/local/lib/ongrid-edge
│       ├── promtail
│       ├── otelcol-contrib
│       ├── auditbeat
│       ├── node_exporter / process_exporter
│       ├── mysqld_exporter / postgres_exporter / redis_exporter / mongodb_exporter
│       └── apply-pending-upgrade.sh
├── etc/
│   └── ongrid-edge/                           原 /etc/ongrid-edge
│       └── ongrid-edge.env
└── var/
    ├── lib/
    │   └── ongrid-edge/                       原 /var/lib/ongrid-edge
    │       ├── plugins/        (plugin work)
    │       └── .upgrade/       (ADR-024 远程升级 stage)
    └── log/
        └── ongrid-edge/                       原 /var/log/ongrid-edge
```

## 改动

### 1. 路径参数化：引入 `--prefix=PATH`

所有 edge 安装 / 卸载脚本接受 `--prefix=PATH`（默认 `/mnt/data/toos-temp`），
同时尊重等价的 `ONGRID_EDGE_PREFIX` 环境变量（命令行优先）。涉及：

- `deploy/install/edge/install.sh`（curl-pipe / UI 一键安装入口）：
  新增 `--prefix=*` 解析，所有路径（`BIN_DIR` / `LIB_DIR` / `ENV_DIR` /
  `STATE_DIR` / `LOG_DIR` / `APPLY_HOOK`）改为 `${PREFIX}/...`；here-doc
  生成的 systemd unit 用 `<<EOF`（无引号）让 shell 展开 `${PREFIX}` 等变量；
  `--uninstall` 路径同步使用 prefix 变量；`ENV_FILE` 内联写入 override
  (`ONGRID_EDGE_PLUGIN_BIN_DIR` / `_PLUGIN_WORK_DIR` / `_UPGRADE_STAGE_DIR`
  / `_SCRAPE_CONFIG_FILE`) 让 edge 进程内部读到 prefix 路径而非硬编码
  默认值；final report 显示 install root / binary / plugin / env / state /
  log 全部路径，uninstall 命令带 `--prefix=...`。
- `deploy/install/edge/install-edge.sh`（离线 tarball 安装）：
  同步改造，路径基于 `${PREFIX}`；env 模板 `ongrid-edge.env.example` 新增
  `__PLUGIN_BIN_DIR__` / `__PLUGIN_WORK_DIR__` / `__UPGRADE_STAGE_DIR__` /
  `__SCRAPE_CONFIG_FILE__` 占位符，install-edge.sh 在渲染时 sed 注入实际
  prefix 路径；systemd unit 模板改为 `__PREFIX__` / `__BIN_DIR__` /
  `__LIB_DIR__` / `__APPLY_HOOK__` / `__ENV_FILE__` / `__STATE_DIR__` /
  `__LOG_DIR__` 占位符，由 `render_unit()` 函数 sed 注入。
- `deploy/install/edge/uninstall.sh`（curl-pipe 卸载）：
  新增 `--prefix=PATH`（默认 `/mnt/data/toos-temp`），所有清理路径
  基于 PREFIX；`pkill -f` 的 regex 改用 `${BIN_DIR}/ongrid-edge|${LIB_DIR}/`，
  旧的 `/usr/local/{bin,lib}/ongrid-edge` 硬编码移除。

### 2. systemd unit 模板化：去除硬编码路径

原 `ongrid-edge.service` / `ongrid-edge-upgrade.service` 把
`EnvironmentFile=/etc/ongrid-edge/ongrid-edge.env` 、
`ExecStart=/usr/local/bin/ongrid-edge` 、`StateDirectory=ongrid-edge` 、
`ReadWritePaths=/var/lib/ongrid-edge /var/log/ongrid-edge` 直接写死。本次：

- 全部硬编码路径替换为占位符：
  - `__ENV_FILE__`、`__BIN_DEST__`、`__STATE_DIR__`、`__LOG_DIR__`、
    `__APPLY_HOOK__`、`__BIN_DIR__`、`__LIB_DIR__`
- **去掉 `StateDirectory=ongrid-edge` 和 `StateDirectoryMode=0755`**：
  - 原 systemd unit 同时设了 `StateDirectory=ongrid-edge`（systemd ≥ 235 才
    生效、CentOS 7 = systemd 219 静默忽略）和 `ReadWritePaths=...`（兜底）
  - `StateDirectory=` 永远把目录钉死在 `${LOCALSTATEDIR}/lib/<name>`
    （默认 `/var/lib/<name>`），无法跟随 `--prefix`，必须去掉
  - 改为完全依赖 `ReadWritePaths=__STATE_DIR__ __LOG_DIR__` + 安装器预创建
    并 chown `${STATE_DIR}` / `${LOG_DIR}` 的方式。注释里更新了这个决定的
    理由（systemd 219 兼容 + prefix 灵活性同时满足）

### 3. `apply-pending-upgrade.sh`（ADR-024 升级 hook）占位符化

脚本原硬编码 4 处：

```bash
STAGE_DIR=/var/lib/ongrid-edge/.upgrade
LEGACY_TARGET=/usr/local/bin/ongrid-edge
find /usr/local/bin /usr/local/lib/ongrid-edge -name '*.previous' ...   # 两处
```

全部改为占位符（`__STAGE_DIR__` / `__BIN_TARGET__` / `__BIN_DIR__` /
`__LIB_DIR__`），install-edge.sh 在 sed 注入时根据 `--prefix` 渲染，
install.sh 通过 nginx 下载下来的版本不再硬编码（每次重装都会重新拉取）。
这样：
- 默认 `--prefix=/mnt/data/toos-temp` → hook 写入
  `${PREFIX}/var/lib/ongrid-edge/.upgrade/...` / 备份 + swap
  `${PREFIX}/bin/ongrid-edge` / `${PREFIX}/lib/ongrid-edge/...`
- 自定义 `--prefix=...` → 一致跟随

### 4. `build-edge-bundle.sh`：**不在本 PR 改造**（决策记录）

bundle MANIFEST.txt 第 4 列 `dest_path` 原本硬编码
`/usr/local/bin/ongrid-edge` 等绝对路径，决定了 edge 端
`apply-pending-upgrade.sh` 把文件 swap 到哪里。理论上应该让这个脚本也跟随
`--prefix` 才能让「一键远程升级」在 prefix 化后还能落对位置，但本次 PR 决策
是**不改造**：

- `deploy/install/edge/build-edge-bundle.sh`（云端 install.sh/upgrade.sh 调
  用）—— 本 PR 实际未改，保持原状
- `dist/build-edge-bundle.sh`（release 阶段，`make package` 调用）—— 本 PR
  实际未改，保持原状

**不进本 PR 的原因**：bundle 是云端共享、分发给所有 edge 的「通用」升级包。
每个 edge 可能用不同 prefix（默认 `/mnt/data/toos-temp`，但允许 operator
自定义），要让 MANIFEST dest_path 与每台 edge 对齐，必须在云端 build bundle
时知道这台 edge 的 prefix 并单独打包，逻辑上是从「云端统一下发」变成「云端
按 edge 个性化打包」，跨多个子系统（device 注册、bundle 仓、edge 元数据），
不属本 PR 范围。

**当前行为（本 PR 交付状态）**：edge 装到 `${PREFIX}` 下之后，远程升级的
bundle MANIFEST dest_path 仍是 `/usr/local/...`，与 edge 的 `__BIN_TARGET__`
（`${PREFIX}/bin/ongrid-edge`）不一致。后果是：
- apply-pending-upgrade.sh 的 Mode 2（bundle apply）会把新 binary swap 到
  `/usr/local/bin/ongrid-edge`（MANIFEST 写的路径）
- 而 systemd unit 的 `ExecStart=${PREFIX}/bin/ongrid-edge`（prefix 化后）—
  —新进程根本不会起来
- 旧进程仍在 `__BIN_TARGET__` 下运行，agent 看起来「在线但没收到升级」

**临时缓解（手动）**：operator 发现升级未生效后，可手动 `cp` 新 binary 到
`${PREFIX}/bin/ongrid-edge`，或临时把 systemd unit 的 `ExecStart` 指回
`/usr/local/bin/ongrid-edge`。本质上这是个已知遗留，将在后续 PR 中按
edge 注册时把 prefix 上传到云端、build bundle 时按 prefix 渲染 dest_path
的方向修复。

**本 PR 改了什么**：为了保证 edge 首次安装流程（即
`curl -k -sSL https://<host>/install.sh | bash --access-key=...`
这一路）按 prefix 落地，所有 install/uninstall 脚本、systemd unit 模板、
apply-pending-upgrade.sh 模板都已 prefix 化。**但 cloud → edge 的升级路径
保留原状**（MANIFEST dest_path 仍是 `/usr/local/...`），因为本 PR 的目标是
「装得进来、卸得干净」，「一键远程升级路径与 prefix 协同」是后续 PR 范畴。

### 5. `ongrid-edge.env.example` 模板加 prefix 占位符

新增 4 个占位符（`__PLUGIN_BIN_DIR__` / `__PLUGIN_WORK_DIR__` /
`__UPGRADE_STAGE_DIR__` / `__SCRAPE_CONFIG_FILE__`），install-edge.sh 在
sed 渲染时填入 prefix 路径。Go 端 `internal/pkg/config` 的默认值（兜底）
保留不变，所以 re-run install-edge.sh 时即便漏设 env var 也不会崩。

## 兼容性

- **默认行为**：`--prefix` 缺省 `/mnt/data/toos-temp`，**所有老 operator 的
  /usr/local/... 默认路径不再生效**。但本项目 edge 端 cmd 的内部默认值仍
  保留（`ONGRID_EDGE_PLUGIN_BIN_DIR` 默认 `/usr/local/lib/ongrid-edge` 等），
  只是我们 ENV_FILE 现在显式注入 prefix 路径覆盖，所以 agent 永远用 prefix
  路径。
- **systemd 兼容性**：`StateDirectory=` 原本就只对 systemd ≥ 235 生效（已有
  注释说明），我们去掉它后改为只依赖 `ReadWritePaths=`（systemd ≥ 220 就
  支持）+ 安装器预创建。CentOS 7 systemd 219 的 EROFS 风险已由安装器预创建
  + chown 兜底（这条原本就在 self-check 里验证）。
- **API 兼容性**：curl-pipe `install.sh` 的 `--access-key=*` /
  `--server-edge-addr=*` 等参数语法不变；新加的 `--prefix=*` 是 opt-in。
- **bundle 兼容性**：MANIFEST schema 不变（仍是
  `sha256  mode  src_in_bundle  dest_path` 4 列），**`dest_path` 仍按
  `/usr/local/...` 原版渲染**（本 PR 不改造 build-edge-bundle.sh，
  见上方第 4 节决策）。MANIFEST 与 edge 实际 `__BIN_TARGET__` 不一致是一
  个明确遗留问题，不在本 PR 解决。
- **Manager → Edge bundle**：云端按原版逻辑生成 bundle（dest_path 是
  `/usr/local/...`）；首次安装后 systemd unit 指 `${PREFIX}/bin/ongrid-edge`
  —— 两者分叉，远程升级会写到错误位置（见上方第 4 节）。

## 验证（按规则 3 / 4：逻辑通 + 打包通）

- 路径追踪：
  ```
  edge install (PREFIX=/mnt/data/toos-temp)
    ├─ curl /install.sh | bash --access-key=... --prefix=/mnt/data/toos-temp
    │   → render systemd units with ${PREFIX} inline
    │   → write ENV_FILE with ONGRID_EDGE_PLUGIN_BIN_DIR=/mnt/data/toos-temp/lib/ongrid-edge ...
    │   → fetch plugins (promtail/otelcol/...) into ${PREFIX}/lib/ongrid-edge/
    │   → fetch apply-pending-upgrade.sh into ${PREFIX}/lib/ongrid-edge/
    │   → systemd daemon-reload + restart
    │
    └─ systemd unit ongrid-edge.service
        EnvironmentFile=/mnt/data/toos-temp/etc/ongrid-edge/ongrid-edge.env
        ExecStart=/mnt/data/toos-temp/bin/ongrid-edge
        ReadWritePaths=/mnt/data/toos-temp/var/lib/ongrid-edge /mnt/data/toos-temp/var/log/ongrid-edge

  edge upgrade (cloud side，**本 PR 不改造**)
    └─ build-edge-bundle.sh 仍按原版生成 MANIFEST dest_path=/usr/local/bin/ongrid-edge
        遗留问题：MANIFEST dest_path 与 edge 实际 __BIN_TARGET__=
        ${PREFIX}/bin/ongrid-edge 不一致，远程升级路径落空（决策见第 4 节）
        → tar → nginx /edge/edge-bundle-linux-amd64-v0.7.200.tar.gz

  edge agent upgrade (edge side)
    └─ MethodFetchPackage drops bundle into /mnt/data/toos-temp/var/lib/ongrid-edge/.upgrade/incoming/
    └─ systemd pulls ongrid-edge-upgrade.service oneshot
        ExecStart=/mnt/data/toos-temp/lib/ongrid-edge/apply-pending-upgrade.sh
        → apply-pending-upgrade.sh reads __STAGE_DIR__=/mnt/data/toos-temp/var/lib/ongrid-edge/.upgrade
        → swap each MANIFEST entry to __BIN_TARGET__=/usr/local/bin/ongrid-edge（MANIFEST 原版路径）
        ⚠ 与 systemd ExecStart=/mnt/data/toos-temp/bin/ongrid-edge 不一致（遗留问题）
  ```
  本 PR 覆盖的链路（首次安装 + 卸载 + 本地 upgrade stage）全通：
  UI 触发 → curl → bash → systemd unit 渲染 → ENV_FILE override → plugin
  binaries 到位 → agent 启动。本 PR **未覆盖**的链路（cloud → edge 远程升
  级落地）：bundle MANIFEST dest_path 与 edge systemd ExecStart 不一致，
  远程升级会落到 `/usr/local/...` 而非 `${PREFIX}/...`，需后续 PR 修复。

- 打包：`go build ./cmd/ongrid/` 不需要重 build（纯 shell + unit 模板改动）。
  本地按规则 4 不跑 `.sh` / `.mk`。

## 不再做的事（避免回归）

- 不要把 `${PREFIX}/bin`、`${PREFIX}/lib/ongrid-edge` 等路径写回
  `/usr/local/...` 默认值——那是历史布局，本 PR 之后 operator 期望
  `rm -rf ${PREFIX}` 能完整清理 edge。
- 不要给 systemd unit 加回 `StateDirectory=ongrid-edge`——它会钉死路径到
  `/var/lib/ongrid-edge`，使 `--prefix` 失效。
- 不要让 `apply-pending-upgrade.sh` 把占位符 `__STAGE_DIR__` 等写死回
  `/var/lib/ongrid-edge/.upgrade`——那样会让非默认 prefix 的 edge 升级
  hook 写到错误位置。
- 不要动 `build-edge-bundle.sh` / `dist/build-edge-bundle.sh` 把
  MANIFEST dest_path 改成 `${PREFIX}/...`——这是后续 PR 的范围，本 PR 决
  策明确**不改造**这两个文件（详见上文第 4 节决策记录）。若未来要改，
  需要在云端 device 注册时把 prefix 上传，并让 build-edge-bundle.sh 接
  受按 edge 维度渲染的 prefix 参数。