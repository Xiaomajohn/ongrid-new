# 2026-07-15 · 新增 repowiki-builder skill

## 问题
当前 `.qoder/repowiki/zh/` 已经是 Qoder 内置「Repo Wiki」产物的最终态（cosy_version 1.6.0），
但仓库内没有可复用的 skill 引导 AI 在新项目上重新生成或增量更新 repowiki。
后续要在其他仓库 / 当前仓库重建知识快照时，需要把现有结构、模板、约束再口述一遍，效率低且容易走样。

## 加了什么
新增项目级 skill 目录 `.qoder/skills/repowiki-builder/`：

- `SKILL.md`（241 行，< 500 行）
  - 含 frontmatter（name / description 触发词：repowiki / 知识卡 / wiki / 模块文档 / 知识快照 / 文档生成）
  - 6 步工作流：扫描仓库 → 构建模块树 → 识别跨模块专题 → 生成内容文档 → 生成元数据 → 校验
  - 输出契约表、并发安全规则、关键约束、自检清单
- `reference.md`（442 行）
  - `_index.yaml` 字段 schema + 完整骨架样例
  - 知识卡 frontmatter 字段 + 12 种 kind 枚举 + 完整正文样例
  - 内容文档完整骨架（含真实路径对齐样例）+ 引用语法 + mermaid 注意事项
  - `meta/repowiki-metadata.json` 字段 + uuid 派生算法 + 关系无环校验伪代码
  - 反例清单（缺 cite / 引用 dist / mermaid 未引号 / frontmatter 错误）
  - 7 类命令清单（扫描 / 校验 YAML / 校验 JSON / 校验引用真实 / 校验无 dist / 校验 mermaid 引号 / 校验仅改 repowiki）
  - 8–10 个子 agent 并发落地建议

## 目的
让任何会话在收到"生成 repowiki / 重建 wiki / 同步知识卡 / 增量更新 wiki / 文档快照"等指令时，
按本 skill 的 6 步流程 + 自检清单，在 `.qoder/repowiki/<locale>/` 下产出与现有 `zh/` 风格一致的产物：
- `knowledge/<locale>/_index.yaml`
- `knowledge/<locale>/<slug>/<article>.md`（含 frontmatter）
- `content/<topic>/<article>.md`（cite + 目录 + 10 节模板 + 图表来源 + 章节来源）
- `meta/repowiki-metadata.json`（稳定 uuid + PARENT_CHILD）

## 不影响
- 未改动任何业务源码（`cmd/`、`internal/`、`web/src/`、`api/`、`deploy/install/`、`tests/`、`Makefile`、`go.mod` 等）
- 未改动既有 `.qoder/repowiki/zh/` 内容
- 未改动 `.qoder/rules/project-rule.md`
- 唯一新增路径：`.qoder/skills/repowiki-builder/{SKILL.md,reference.md}`

## 后续
- 下次重建 repowiki 时，按 SKILL.md §工作流 6 步跑；
- 自检清单通过后才能标记完成；未通过需回到对应 Step 修复后重跑 Step 6。