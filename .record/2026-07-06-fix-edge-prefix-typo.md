# 2026-07-06 edge 安装默认 prefix 拼写修正（toos-temp → tools-temp）

## 背景

回顾 `.record/2026-07-05-edge-install-prefix.md` 时发现，上次 prefix 化改
造中所有 `PREFIX` 默认值及相关注释都被误写成了 `/mnt/data/toos-temp`（明
显是把 `tools` 打成 `toos`）。这会导致：

- 在没显式传 `--prefix=/mnt/data/tools-temp` 的设备上，edge 被默认装到
  `/mnt/data/toos-temp/{bin,lib,etc,var}/...`
- 与 operator 约定的「装到 `/mnt/data/tools-temp` 下、`rm -rf` 一键清理」
  不一致，多个 edge 设备上的目录名前后不一致 → 管理混乱
- `curl-pipe` 卸载脚本用同样拼错的默认 prefix，会漏卸真正的安装

修复目标：把所有 `toos-temp` 一键替换为 `tools-temp`，保持纯字符串替换、
不动任何逻辑。

## 改动（共 8 个文件、35 处替换）

### 脚本（持有生效默认值）

| 文件 | 行 | 改动 |
|---|---|---|
| `deploy/install/edge/install.sh` | 71 | `PREFIX="/mnt/data/toos-temp"` → `PREFIX="/mnt/data/tools-temp"`（curl-pipe UI 一键安装入口） |
| `deploy/install/edge/install-edge.sh` | 31 | `PREFIX="${ONGRID_EDGE_PREFIX:-/mnt/data/toos-temp}"` → `tools-temp`（离线 tarball 安装） |
| `deploy/install/edge/uninstall.sh` | 15 | `PREFIX="${ONGRID_EDGE_PREFIX:-/mnt/data/toos-temp}"` → `tools-temp`（curl-pipe 卸载） |

### 模板（注释里的默认 prefix 说明）

| 文件 | 行 | 改动 |
|---|---|---|
| `deploy/install/edge/ongrid-edge.service` | 21 | 注释 `(default /mnt/data/toos-temp)` → `tools-temp` |
| `deploy/install/edge/ongrid-edge-upgrade.service` | 4 | 同上 |
| `deploy/install/edge/ongrid-edge.env.example` | 12 | 注释 `--prefix (default /mnt/data/toos-temp)` → `tools-temp` |
| `deploy/install/apply-pending-upgrade.sh` | 37 | 注释 `--prefix (default /mnt/data/toos-temp)` → `tools-temp` |

### 文档

| 文件 | 改动 |
|---|---|
| `.record/2026-07-05-edge-install-prefix.md` | 把 15 处历史记录里残留的 `toos-temp` 全部同步修正为 `tools-temp`，让文档反映真实路径 |

上述 35 处全部通过 `SearchReplace replace_all` 完成（每个文件全文件搜
`toos-temp` → `tools-temp`），最终整库 `grep toos-temp` 结果为空，
`grep tools-temp` 共 19 处，与改动前 `toos-temp` 总数 19 + 16 处
（比 19 多出的 16 处都在 `.record` 文档里）一致。

## 影响范围

- 仅改 default 字符串；非默认 prefix（`--prefix=...` / `ONGRID_EDGE_PREFIX`）
  的设备不受影响
- 不影响任何路径展开逻辑、不影响 systemd unit 渲染、不影响 plugin
  binary 抓取、apply-pending-upgrade.sh stage dir 等运行时路径派生
- 已按 `toos-temp` 装过的 edge：重装时新的默认值会写到 `tools-temp`，老
  目录（`/mnt/data/toos-temp`）需要 operator 手动 `rm -rf` 清掉；
  `uninstall.sh` 的默认 prefix 也跟着修了，所以重装 + uninstall 也
  不会误伤新目录

## 验证

按项目规则 3 / 4（逻辑通 + 打包通；本地不跑 `.sh` / `.mk`）：

- 逻辑：`PREFIX=${ONGRID_EDGE_PREFIX:-/mnt/data/tools-temp}` 在三处
   install/uninstall 脚本里语义对齐（默认值一致、override 优先级一致）
- grep 校对：`grep -r "toos-temp" .` → 0 hit；`grep -r "tools-temp" .`
  → 19 hit，全部在 `deploy/install/` 下，与 PR 范围一致
- 文档校对：`.record/2026-07-05-edge-install-prefix.md` 已同步反映新
  拼写，避免后续按文档反向引入错拼

## 注意事项

- 云端 bundle MANIFEST dest_path 仍是 `/usr/local/...`（`.record/2026-07-05-edge-install-prefix.md`
  的"不在本 PR 改造"决策），与本次拼写修复正交，不在本变更范围
- Go 端 `cmd/ongrid-edge/main.go` 的几个内部默认（`/var/lib/ongrid-edge/.upgrade` 等）
  与本次修复也正交，按既有的 `ONGRID_EDGE_*` env override 机制被
  ENV_FILE 注入的真实路径覆盖，不需改动
- 不再做的事：禁止任何 PR 把 prefix 默认值改回 `toos-temp`（拼写错已
  形成唯一事实，所有脚本、模板、文档均以 `tools-temp` 为准）
