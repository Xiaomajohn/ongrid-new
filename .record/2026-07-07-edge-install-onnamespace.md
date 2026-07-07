# 2026-07-07 edge 安装路径嵌套 ongrid-edge 命名空间

## 场景

2026-07-05 PR 把 edge 安装路径统一到 `${PREFIX}` 下（默认 `/mnt/data/tools-temp`）。
本机 operator 用 `--prefix=/opt/tools-temp` 时，安装完成后 `/opt/tools-temp/` 根目录
直接被 edge 占满——`bin/` `etc/` `lib/` `var/` 这 4 个目录直接散在 `tools-temp/`
下，跟 PREFIX 根目录里其他工具（如果将来放别的）共享同一层，污染了 prefix 的
命名空间语义。

希望：edge 安装路径全部嵌套在 `${PREFIX}/ongrid-edge/` 下，作为一个独立的命名
空间，operator 只需清理整个 `ongrid-edge/` 目录就能完整卸载 edge，不影响
PREFIX 根目录里别的工具。

## 改动前的结构（不期望）

```
/opt/tools-temp/
├── bin/                                # 污染 PREFIX 根目录
│   └── ongrid-edge
├── lib/
│   └── ongrid-edge/                    # plugin 二进制 + apply-pending-upgrade.sh
├── etc/
│   └── ongrid-edge/
│       └── ongrid-edge.env
└── var/
    ├── lib/ongrid-edge/                # state, plugin work, .upgrade
    └── log/ongrid-edge/                # logs
```

## 改动后的结构（期望）

```
/opt/tools-temp/
└── ongrid-edge/                        # 单一命名空间，整层清理即可
    ├── bin/
    │   └── ongrid-edge
    ├── lib/
    │   └── ongrid-edge/                # plugin 二进制 + apply-pending-upgrade.sh
    ├── etc/
    │   └── ongrid-edge/
    │       └── ongrid-edge.env
    └── var/
        ├── lib/ongrid-edge/            # state, plugin work, .upgrade
        └── log/ongrid-edge/            # logs
```

`uninstall.sh` 默认不清 `LOG_DIR` / `STATE_DIR`（保留 logs 供 post-mortem）；
要做完整清理只需：

```bash
sudo rm -rf /opt/tools-temp/ongrid-edge     # 或 --prefix=/opt/tools-temp 时
```

## 改动

### 1. 引入 `EDGE_ROOT=${PREFIX}/ongrid-edge` 作为单一命名空间根

所有 path 常量从 `EDGE_ROOT` 派生，不再直接拼接 `${PREFIX}`：

```bash
# 之前
BIN_DIR="${PREFIX}/bin"
LIB_DIR="${PREFIX}/lib/ongrid-edge"
ENV_DIR="${PREFIX}/etc/ongrid-edge"
STATE_DIR="${PREFIX}/var/lib/ongrid-edge"
LOG_DIR="${PREFIX}/var/log/ongrid-edge"

# 现在
EDGE_ROOT="${PREFIX}/ongrid-edge"
BIN_DIR="${EDGE_ROOT}/bin"
LIB_DIR="${EDGE_ROOT}/lib/ongrid-edge"
ENV_DIR="${EDGE_ROOT}/etc/ongrid-edge"
STATE_DIR="${EDGE_ROOT}/var/lib/ongrid-edge"
LOG_DIR="${EDGE_ROOT}/var/log/ongrid-edge"
```

### 2. 三个 install/uninstall 脚本的 path 推导同步

- `deploy/install/edge/install.sh`：默认 + `--prefix=` 解析后两处路径重推导均改为
  EDGE_ROOT 派生；ENV_FILE here-doc 写入的 `ONGRID_EDGE_PLUGIN_BIN_DIR` /
  `_PLUGIN_WORK_DIR` / `_UPGRADE_STAGE_DIR` / `_SCRAPE_CONFIG_FILE` 全部跟随；
  systemd unit here-doc 写入的 `EnvironmentFile` / `ExecStart` / `ReadWritePaths`
  也全部跟随；uninstall 路径同步。
- `deploy/install/edge/install-edge.sh`：默认 + `--prefix=` 解析后两处路径重推导；
  `apply-pending-upgrade.sh` 的 sed 占位符渲染、systemd unit 模板渲染、env 模板
  渲染都跟随；uninstall 路径同步。
- `deploy/install/edge/uninstall.sh`：BIN_DIR / LIB_DIR / ENV_DIR / LOG_DIR /
  STATE_DIR 全部从 EDGE_ROOT 派生；`pkill -f` regex 跟随；末尾"install root was"
  / "full wipe" 提示改用 `$EDGE_ROOT`，避免误导 operator `rm -rf $PREFIX`
  把 prefix 根目录里的其他工具也干掉。

### 3. systemd unit / env / apply-pending-upgrade 模板注释更新

- `deploy/install/edge/ongrid-edge.service`：占位符对应的实际路径注释改为
  `EDGE_ROOT/...`，体现命名空间层。
- `deploy/install/edge/ongrid-edge-upgrade.service`：`__APPLY_HOOK__` 注释
  同样改为 `EDGE_ROOT/...`。
- `deploy/install/edge/ongrid-edge.env.example`：头部注释说明 env file 渲染到
  `EDGE_ROOT/etc/ongrid-edge/ongrid-edge.env`；Path placeholder 注释说明
  渲染到 EDGE_ROOT 之下。
- `deploy/install/apply-pending-upgrade.sh`：顶部注释说明 stage dir 路径是
  `EDGE_ROOT/var/lib/ongrid-edge/.upgrade/incoming/`；占位符注释同步。

### 4. build-edge-bundle.sh 不改（决策记录延续）

`deploy/install/edge/build-edge-bundle.sh` + `dist/build-edge-bundle.sh` 生成的
bundle MANIFEST `dest_path` 仍写死 `/usr/local/bin/ongrid-edge` 和
`/usr/local/lib/ongrid-edge/...`。这跟 edge 实际 `__BIN_TARGET__` =
`${EDGE_ROOT}/bin/ongrid-edge`（默认 `/mnt/data/tools-temp/ongrid-edge/bin/ongrid-edge`）
继续不一致。

这是 2026-07-05 PR 决策明确**不改造**的遗留问题（详见
`.record/2026-07-05-edge-install-prefix.md` 第 4 节）：bundle 是云端共享、
分发给所有 edge 的「通用」升级包，要让 MANIFEST dest_path 与每台 edge 对齐，
必须在云端 build bundle 时知道这台 edge 的 prefix 并单独打包——跨多个子系统
（device 注册、bundle 仓、edge 元数据），不在本 PR 范围。

**本 PR 决策延续**：嵌套 ongrid-edge/ 层是固定命名空间（不依赖 operator 自定义
prefix），但 build-edge-bundle.sh 要按 edge 渲染 dest_path 必须先解决 prefix
上传到云端这一前置条件，所以本 PR 不动 build-edge-bundle.sh。后续 PR 在做
「cloud → edge 远程升级路径与 prefix 协同」时，把 ongrid-edge 这一层一起
打包渲染进去。

## 兼容性

- **新装**：用默认 `--prefix` 或自定义 `--prefix=PATH`，edge 内容都装到
  `${PREFIX}/ongrid-edge/` 下，operator 旧期望的"rm -rf ${PREFIX} 全清"依然
  有效（因为 PREFIX 里只有这一层 ongrid-edge/）。
- **重装**：已有 edge 是装在 `${PREFIX}/{bin,etc,lib,var}/` 旧布局下的，
  本 PR 不主动迁移；operator 重装时新内容会落到 `${PREFIX}/ongrid-edge/` 下，
  与旧内容并存。后续清理用 `rm -rf ${PREFIX}` 即可一步搞定（包括旧布局）。
- **operator 自定义 `--prefix=PATH`**：路径按 PATH 拼接，与之前一致；唯一区别
  是多一层 `ongrid-edge/`。
- **API / 命令行参数**：所有 `--prefix=*` 参数语法不变，仅语义上多一层
  ongrid-edge/ 嵌套；`ONGRID_EDGE_PREFIX` 环境变量语义同步。
- **systemd 兼容性**：跟 2026-07-05 PR 完全一致——`StateDirectory=` 仍不用，
  依赖 `ReadWritePaths=` + installer 预创建（systemd 219+ 都支持）。
- **Go 端默认值**：`internal/pkg/config/config.go` 默认值仍是历史
  `/etc/ongrid-edge/scrape.yaml` 等，作为兜底。ENV_FILE 显式注入
  `ONGRID_EDGE_PLUGIN_BIN_DIR` / `_PLUGIN_WORK_DIR` / `_UPGRADE_STAGE_DIR` /
  `_SCRAPE_CONFIG_FILE` 等 env vars 覆盖默认，所以 agent 实际行为始终跟随
  prefix 路径。
- **bundle 兼容性**：MANIFEST schema 不变；dest_path 仍按 `/usr/local/...` 原版
  渲染（已知遗留，与 edge 实际 `__BIN_TARGET__` 不一致，见上方决策记录）。

## 验证（按规则 3 / 4：逻辑通 + 打包通）

- **bash -n**：所有改动脚本通过：
  - `deploy/install/edge/install.sh` OK
  - `deploy/install/edge/install-edge.sh` OK
  - `deploy/install/edge/uninstall.sh` OK
  - `deploy/install/apply-pending-upgrade.sh` OK
  - `deploy/install/edge/build-edge-bundle.sh` OK（本 PR 未改）
  - `dist/build-edge-bundle.sh` OK（本 PR 未改）

- **残留扫描**：`grep -rnE '\$\{PREFIX\}/bin|\$\{PREFIX\}/lib/ongrid-edge|\$\{PREFIX\}/etc/ongrid-edge|\$\{PREFIX\}/var/'`
  在 `deploy/install/edge/{install.sh,install-edge.sh,uninstall.sh,*.service,*.env.example}`
  + `deploy/install/apply-pending-upgrade.sh` 中**无残留**（全部已替换为
  `${EDGE_ROOT}/...`）。

- **路径推导模拟**（`--prefix=/opt/tools-temp`）：
  ```
  PREFIX       = /opt/tools-temp
  EDGE_ROOT    = /opt/tools-temp/ongrid-edge
  BIN_DIR      = /opt/tools-temp/ongrid-edge/bin
  LIB_DIR      = /opt/tools-temp/ongrid-edge/lib/ongrid-edge
  ENV_DIR      = /opt/tools-temp/ongrid-edge/etc/ongrid-edge
  ENV_FILE     = /opt/tools-temp/ongrid-edge/etc/ongrid-edge/ongrid-edge.env
  STATE_DIR    = /opt/tools-temp/ongrid-edge/var/lib/ongrid-edge
  LOG_DIR      = /opt/tools-temp/ongrid-edge/var/log/ongrid-edge
  APPLY_HOOK   = /opt/tools-temp/ongrid-edge/lib/ongrid-edge/apply-pending-upgrade.sh
  ```
  → 命中用户期望的 `${PREFIX}/ongrid-edge/{bin,etc,lib,var}/...` 布局。

- **ENV_FILE 内容模拟**（install.sh 的 here-doc 渲染）：
  ```
  ONGRID_EDGE_PLUGIN_BIN_DIR=/opt/tools-temp/ongrid-edge/lib/ongrid-edge
  ONGRID_EDGE_PLUGIN_WORK_DIR=/opt/tools-temp/ongrid-edge/var/lib/ongrid-edge/plugins
  ONGRID_EDGE_UPGRADE_STAGE_DIR=/opt/tools-temp/ongrid-edge/var/lib/ongrid-edge/.upgrade
  ONGRID_EDGE_SCRAPE_CONFIG_FILE=/opt/tools-temp/ongrid-edge/etc/ongrid-edge/scrape.yaml
  ```

- **systemd unit 内容模拟**（install.sh 的 here-doc 渲染）：
  ```
  [Service]
    EnvironmentFile=/opt/tools-temp/ongrid-edge/etc/ongrid-edge/ongrid-edge.env
    ExecStart=/opt/tools-temp/ongrid-edge/bin/ongrid-edge
    ReadWritePaths=/opt/tools-temp/ongrid-edge/var/lib/ongrid-edge /opt/tools-temp/ongrid-edge/var/log/ongrid-edge
  [Upgrade oneshot]
    ExecStart=/opt/tools-temp/ongrid-edge/lib/ongrid-edge/apply-pending-upgrade.sh
  ```

- **uninstall 清理模拟**（`--prefix=/opt/tools-temp`）：
  ```
  uninstall.sh 会清理以下目录:
    rm -rf /opt/tools-temp/ongrid-edge/bin/ongrid-edge
    rm -rf /opt/tools-temp/ongrid-edge/etc/ongrid-edge
    rm -rf /opt/tools-temp/ongrid-edge/lib/ongrid-edge
    rm -rf /opt/tools-temp/ongrid-edge/var/log/ongrid-edge
    rm -rf /opt/tools-temp/ongrid-edge/var/lib/ongrid-edge

  full wipe: rm -rf /opt/tools-temp/ongrid-edge
  ```
  → 全部限定在 EDGE_ROOT 内，不会污染 `${PREFIX}` 根目录。

- **打包**：`go build ./cmd/ongrid/` 不需要重 build（纯 shell + unit 模板 + env
  模板 + apply-pending-upgrade.sh 模板改动）。本地按规则 4 不跑 `.sh` / `.mk`。

## 不属于本任务范围

- 没有改任何 Go 源码（`internal/pkg/config/config.go` 等）。
- 没有改 build-edge-bundle.sh（按既有决策保留原状，详见上方第 4 节）。
- 没有改 manager side 的 install.sh / uninstall.sh（仅改 edge 端）。
- 没有 commit / push。

## 不再做的事（避免回归）

- 不要把 `EDGE_ROOT` 写回成 `${PREFIX}/ongrid-edge` 之外的字符串形式——
  那样会破坏命名空间语义，让 PREFIX 根目录再次被 edge 内容污染。
- 不要把 install.sh 的 uninstall 提示改回 `rm -rf ${PREFIX}`——PREFIX 是
  operator 指定的根目录，可能有别的工具，必须只清 EDGE_ROOT。
- 不要动 `build-edge-bundle.sh` / `dist/build-edge-bundle.sh` 的 dest_path——
  这是 2026-07-05 PR 决策明确不改造的范围，本 PR 决策延续。后续 PR 在做
  「cloud → edge 远程升级路径与 prefix 协同」时一并处理。
- 不要把 systemd unit 加回 `StateDirectory=ongrid-edge`——会钉死路径到
  `/var/lib/ongrid-edge`，使 `--prefix` 失效。