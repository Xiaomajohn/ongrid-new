---
name: repowiki-builder
description: 扫描仓库源码与配置，构建模块树与跨模块专题知识卡，按业务域生成内容文档，落地到 .qoder/repowiki/<locale>/ 下，覆盖知识索引、模块卡、内容文章与 meta 元数据。适用于：用户要求"生成 repowiki / 重建 wiki / 同步知识卡 / 增量更新 wiki / 文档快照"等场景；典型触发词：repowiki、知识卡、wiki、模块文档、知识快照、文档生成。
---

# repowiki-builder

按仓库事实生成或增量更新 `.qoder/repowiki/<locale>/` 的四类产物：

| 产物 | 路径 | 角色 |
|------|------|------|
| 模块树索引 | `knowledge/<locale>/_index.yaml` | 模块 key / scope / children / related_to |
| 模块/专题知识卡 | `knowledge/<locale>/<slug>/<article>.md` | 跨文件视角的领域说明（含 frontmatter） |
| 业务内容文档 | `content/<topic>/<article>.md` | 用户视角的安装、使用、运维、API 文档 |
| 元数据 | `meta/repowiki-metadata.json` | 模块节点 uuid + PARENT_CHILD 关系 + 运行信息 |

调用前必读：现有基线位于 `f:\Code\Go\运维\ongrid-new\.qoder\repowiki\zh\`，其中 `_index.yaml`、`快速开始.md`、`AI智能运维/LLM模型集成.md` 与 `knowledge/zh/Go_Node 双栈跨端业务架构模型 + 子仓库/Go_Node 双栈跨端业务架构模型 + 子仓库.md` 是必须对齐风格与结构的参考样例。

---

## 何时启用

- 用户给出"生成 repowiki / 重建 wiki / 同步知识卡 / 增量更新 wiki / 文档快照"等关键词。
- 用户指向 `.qoder/repowiki` 路径要求重新生成、对齐、对比、清理或补全。
- 不在以下场景启用：仅修改业务源码、生成 CHANGELOG、写 PR 描述、跑 E2E 测试。

## 必收集的输入

| 输入 | 默认 | 说明 |
|------|------|------|
| `<workspace>` | 当前项目根 | 产物根目录 |
| `locale` | `zh` | 知识库语言标识，常见值 `zh`、`en` |
| `mode` | `full` | `full` 全量重写；`incremental --only <slug>` 仅重写指定模块；`incremental --topic <name>` 仅重写指定 topic |
| `branch` | 当前 git HEAD 分支 | 写入 `_index.yaml.branch` 与 `meta/repowiki-metadata.json.extend_info.branch` |
| `commit_id` | 当前 HEAD commit | 写入 `meta/repowiki-metadata.json.last_commit_id` |

## 输出契约

```
.qoder/repowiki/
└── <locale>/
    ├── knowledge/<locale>/
    │   ├── _index.yaml
    │   ├── <专题-slug>/
    │   │   └── <专题-slug>.md                 # 跨模块专题卡（4 节正文 + frontmatter）
    │   └── <模块-slug>/
    │       ├── <模块-slug>.md                 # 模块概览卡
    │       └── <article>.md                   # 模块下属子卡
    └── content/
        ├── <topic-slug>/
        │   ├── <topic-slug>.md               # topic 总览
        │   └── <article>.md                  # topic 内文章
        └── <top-level>.md                    # 快速开始 / 使用指南 / 故障排查 等
    └── meta/
        └── repowiki-metadata.json
```

slug 命名规则：以仓库事实为准；中文目录名/文件名保留仓库原文（不可 ASCII 化）；同一 (locale, slug) 必须保持 uuid 稳定。

---

## 工作流（6 步 + 1 步校验）

### Step 1 · 扫描仓库事实

并行执行以下检索（推荐并发 5–8 个子 agent，各自独立检索，输出写入各自 md 中间产物，最后主 agent 整合）：

1. **顶层骨架**：`README*`、`go.mod`、`go.sum`、`Makefile`、`AGENTS.md`、`CHANGELOG.md`、`VERSION`、`.tool-versions`、`.env.example`、`.go-arch-lint.yml`、`.golangci.yml`、`.dockerignore`、`.gitignore`、`CODEOWNERS`。
2. **入口进程**：`cmd/ongrid/main.go`、`cmd/ongrid-edge/main.go`。
3. **API 契约**：`api/**.proto`、`api/buf.yaml`、`api/buf.gen.yaml`。
4. **业务子域**：`internal/manager/biz/<domain>/`、`internal/manager/server/<domain>/`、`internal/manager/data/<domain>/`、`internal/edgeagent/<area>/`。
5. **前端页面**：`web/src/pages/**`、`web/src/pages/settings/**`、`web/src/components/**`、`web/src/api/**`、`web/src/store/**`、`web/src/lib/**`、`web/src/i18n/**`、`web/src/styles/**`。
6. **部署与运维**：`deploy/**/Dockerfile*`、`deploy/**/docker-compose*.yml`、`deploy/install/**`、`deploy/install/systemd/**`、`resource/**`、`scripts/**`。
7. **测试与 skills**：`tests/**`、`skills/**`、`agents/**`。
8. **文档与 wiki**：`docs/**`、`repowiki/**`、`.qoder/**`。

每个并发子 agent 必须返回：

- `scope_paths`：相对仓库根的 glob/路径清单
- `domain_keywords`：3–8 个关键术语（如 `aiops`、`devicessh`、`metric_pipeline`）
- `cross_cutting_themes`：可作为专题知识卡的候选（构建、日志、错误、主题、插件、安全、时间戳等）

> **并发安全规则**：不同模块/不同 topic 分配给不同子 agent；**同一文件只能由一个 agent 写**，主 agent 负责合并并落盘。

### Step 2 · 构建模块树（生成 `_index.yaml`）

```yaml
# 头
schema_version: 1
locale: zh-CN
branch: <当前 git 分支>
nodes_managed: true
exported_at: "<RFC3339 UTC>"

# 模块树
modules:
  <key>:
    dir_name: <仓库事实命名,例：onGrid 云侧管理器（Manager）单体服务>
    title:    <同 dir_name>
    scope:
      - <相对仓库根的路径/glob 列表>
    source_files: []
    children:    [<子节点 key>]
    depends_on:  []
    related_to:
      - path: <其它节点 key>
```

模块拆分原则：

- 顶层 ≥ 9 个：`api_protos / deploy_artifacts / docs_and_wiki / edge_agent / frontend / iam_service / manager_service / shared_libs / tests`。
- `manager_service` 二级按 `internal/manager/biz/<domain>/` 拆分（aiops、alert、device、devicessh、edge、flow、imbridge、installjob、knowledge、marketplace、mcp、metric、monitor、report、secret、setting、skill、topology 等）。
- `edge_agent` 二级按 `internal/edgeagent/<area>/` 拆分（agent_api / agent_core / cmd_policy_sandbox / collectors / plugins）。
- `frontend` 二级按 `web/src/pages` 拆分（alerts_incidents / core_app_shell / devices_hosts / home_dashboard / marketplace / monitoring / reports / settings_admin / topology / shared_*）。
- 三级仅当 ≥ 3 个独立子文件且语义独立时才展开。

`related_to.path` 的 key 必须存在；禁止自指或成环。

### Step 3 · 识别跨模块专题知识卡

从仓库事实里识别 4–10 个专题，每个产出一个独立目录（仅含同名 `.md`）：

| 候选专题 | 触发字段 |
|----------|----------|
| 依赖管理（Go/Node 双栈） | `go.mod`、`web/package.json`、`api/buf.yaml` |
| 构建与发布流水线 | `Makefile`、`deploy/**/Dockerfile*`、`.github/workflows/**` |
| 主题/UI 系统 | `web/src/styles/**`、`web/tailwind.config.ts`、`web/postcss.config.js` |
| 插件/运行时框架 | `internal/pluginhost/**`、`internal/edgeagent/plugins/**` |
| 结构化日志体系 | `internal/pkg/log/**`、含 `slog`/`zap` 的文件 |
| 错误响应体系 | 含 `errorcode`、`HTTPStatus`、`middleware/error*` 的目录 |
| 安全与凭据 | `internal/manager/biz/secret/**`、`internal/manager/server/middleware/auth*` |
| 数据时间戳/时钟策略 | 含 `ServerTime`、`TsMs`、`time.Now` 治理的相关文件 |
| 数据访问层 / ORM | `internal/pkg/db/**`、`internal/manager/data/**` |

专题卡 frontmatter：

```yaml
---
kind: <专题分类,例 dependency_management | build_pipeline | logging_system | error_handling | theme_system | plugin_runtime | security | data_timestamp | data_access>
name:  <仓库事实命名>
category: <同上>
scope:
  - '**'
source_files:
  - <涉及的关键文件, 必须真实存在>
---
```

专题卡正文固定 4 节：

1. 使用的系统与方法
2. 关键文件与位置
3. 架构与约定
4. 开发者应遵循的规则

### Step 4 · 生成内容文档（业务视角）

#### Step 4.1 顶层 `content/<top-level>.md`

至少包含：`快速开始.md`、`使用指南.md`、`故障排查.md` 三篇。

#### Step 4.2 topic 分桶

按以下 12 个 topic 组织：

```
项目介绍 / 快速开始 / 使用指南 / 故障排查 / 安装部署 / 运维指南
架构总览 / 核心服务 / 可观测平台 / API 接口文档 / 安全认证
AI 智能运维 / 插件生态系统 / 前端应用 / 数据库访问
```

每个 topic 一个目录 `content/<topic>/`，内含：

- `<topic>.md`（topic 总览，含 `<cite>` + 目录 + ≥ 6 节）
- `*.md`（topic 内具体文章，按需）

#### Step 4.3 单篇内容文档骨架

```
# <标题>

<cite>
**本文引用的文件**   
- [README.md](file://README.md)
- [path/to/file.go:1-100](file://path/to/file.go#L1-L100)
</cite>

## 目录
1. [简介](#简介)
2. [项目结构](#项目结构)
3. [核心组件](#核心组件)
4. [架构总览](#架构总览)
5. [详细组件分析](#详细组件分析)
6. [依赖关系分析](#依赖关系分析)
7. [性能与…优化](#性能与…优化)
8. [故障排查指南](#故障排查指南)
9. [结论](#结论)
10. [附录](#附录)

## 简介
<一段话概述目标读者 + 解决的问题 + 涉及范围>

## 项目结构
<涉及文件/模块清单 + 1 段 mermaid graph TB 展示主要结构>

## 核心组件
<组件列表，每个一行，附关键文件链接>

## 架构总览
<mermaid sequenceDiagram 或 graph TB + 说明>

## 详细组件分析
### 子组件 A
#### 子子组件
...

## 依赖关系分析
<关键依赖关系 mermaid>

## 性能与…优化
<可选：性能 / 资源 / 成本>

## 故障排查指南
<常见问题 + 排查路径>

## 结论
<3-5 行收束>

## 附录：…
<可选>

图表来源
- [path:lines](file://path#L1-L100)

章节来源
- [path:lines](file://path#L1-L100)
```

允许省略节（如「性能与…优化」可改成「性能与成本优化」或「性能与构建优化」），但下列 5 项必须存在：**简介、项目结构、核心组件、详细组件分析、章节来源**。

### Step 5 · 生成 `meta/repowiki-metadata.json`

```jsonc
{
  "knowledge_relations": [
    {
      "id": 1,
      "source_id": "<父节点 uuid>",
      "target_id": "<子节点 uuid>",
      "source_type": "WIKI_ITEM",
      "target_type": "WIKI_ITEM",
      "relationship_type": "PARENT_CHILD",
      "extra": "Wiki parent-child relationship: <source_id> -> <target_id>",
      "gmt_create": "<RFC3339>",
      "gmt_modified": "<RFC3339>"
    }
  ],
  "recovery_checkpoint": "wiki_generation_completed",
  "last_commit_id": "<当前 HEAD>",
  "last_commit_update": "<RFC3339>",
  "gmt_create": "<首次生成时间>",
  "gmt_modified": "<本次更新时间>",
  "extend_info": "{\"language\":\"<locale>\",\"active\":true,\"branch\":\"<branch>\",\"shareStatus\":\"\",\"server_error_code\":\"\",\"cosy_version\":\"1.6.0\"}"
}
```

- uuid 必须稳定：`SHA1(locale + "|" + slug).hex[0:32]`，同一 (locale, slug) 复用同一 uuid。
- `knowledge_relations` **只**写 `PARENT_CHILD`，不写 `related_to`（后者只在 `_index.yaml` 中维护）。
- 关系无环；新增节点自动追加；被移除的节点同时移除其作为 source 与 target 的关系。

### Step 6 · 校验（必须全过）

- [ ] `_index.yaml` 能被 YAML 解析（2 空格缩进，无 tab，无重复 key）
- [ ] 叶子节点 `scope` 至少 1 条真实路径，全部 glob 在仓库中可命中
- [ ] `related_to.path` 全部存在且不自指/成环
- [ ] 每篇 knowledge .md 含 frontmatter（kind/name/category/scope/source_files）+ 至少 4 节正文
- [ ] 每篇 content .md 含 `<cite>` + `## 目录` + 「简介 / 项目结构 / 核心组件 / 详细组件分析 / 章节来源」5 项
- [ ] 含 mermaid 的文章必有 `图表来源` 块；引用文件路径可被 IDE 跳转
- [ ] `meta/repowiki-metadata.json` 合法 JSON、PARENT_CHILD 关系无环、uuid 稳定
- [ ] 无 `dist/`、`build/`、`node_modules/`、`.next/` 等打包产物路径
- [ ] mermaid 节点含中文/括号/`/` 时用双引号 `"..."` 包起

校验未通过则回到对应 Step 修复后重跑 Step 6，**不**进入完成态。

---

## 关键约束

- **不**修改任何业务源码：`cmd/`、`internal/`、`web/src/`、`api/`、`deploy/install/`、`tests/`、`Makefile`、`go.mod` 等只读。
- 中文路径在 Windows PowerShell 下须用 `-LiteralPath`；控制台乱码是 BOM/代码页问题，不影响逻辑；脚本里用变量传递真实路径，不写字符串字面量。
- 中文目录名/文件名保留仓库原文，**禁止** ASCII 化或翻译。
- 写文件优先 `Write`（小文件 < 1000 行）或 `SearchReplace`（就地改），**禁止**用 `Bash echo/cat` 落盘。
- **同文件并发写会被文件锁卡住**：topic / 模块分配给不同子 agent；同一文件只能由一个 agent（或主 agent 串行）落地。
- 引用源文件时禁止指向 `dist/`、`build/`、`node_modules/`、`.next/`、`web/dist/`、`bin/`；只能指向源码与配置文件。
- 写完所有 markdown 后，跑一遍 `git diff --stat` 校验只动了 `.qoder/repowiki/` 与本 skill 目录。

## 自检清单（落地完成后逐项 ✓）

- [ ] `_index.yaml` 解析通过
- [ ] 模块树叶子节点 `scope` 全部命中
- [ ] 每篇 knowledge .md 含 frontmatter + ≥ 4 节
- [ ] 每篇 content .md 含 cite + 目录 + 5 必备节 + 章节来源
- [ ] 含 mermaid 的文章都有图表来源
- [ ] 所有 `file://` 引用可在 IDE 中打开
- [ ] `meta/repowiki-metadata.json` 合法、关系无环
- [ ] 未引用 dist/build/node_modules
- [ ] 未改动业务源码
- [ ] `git diff --stat` 仅显示 `.qoder/repowiki/` 与本 skill 目录

## 详细参考

- 字段 schema / JSON 模板 / 样例片段 / 反例见 [reference.md](reference.md)