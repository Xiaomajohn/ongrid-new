# 2026-07-10 commit repowiki artifacts

## 日期
2026-07-10

## 问题

1. `.qoder/` 目录除 `rules/project-rule.md` 外，其余产物一直未跟踪；`git status` 持续被大量 `repowiki` 内容污染
2. 顶层 `repowiki/` 与 `.qoder/repowiki/` 两份由 `repo-wiki` skill 生成的 wiki 内容始终处于未跟踪状态：
   - `repowiki/`：98 个文件，4.5MB（含 `mkdocs.yml`、`docs/`、`.repo_wiki/` 状态/索引/脚本）
   - `.qoder/repowiki/`：595 个文件，8.1MB（含 `zh/content/`、`zh/meta/repowiki-metadata.json`、`knowledge/zh/`）

## 根因

`.gitignore` 与 `repowiki/.gitignore` 仅排除了 `repowiki/site/`（MkDocs 构建产物）等少量文件，并未将 skill 生成的源码/元数据纳入跟踪。两份产物目录又因为内容自动生成，开发流程中没有显式 `git add`，导致始终游离在版本管理之外。

## 修复

1. 在 `.record/` 补充本说明，记录两份产物入仓的原因与范围
2. 拆成两个独立提交，便于后续单独回滚：
   - `chore(repo-wiki): 跟踪顶层 repowiki/ 产物` —— `repowiki/`（`site/` 已被其自身 `.gitignore` 忽略）
   - `chore(repo-wiki): 跟踪 .qoder/repowiki/ 产物` —— `.qoder/repowiki/`（无 `.gitignore`）
3. `.qoder/rules/project-rule.md` 早已提交，本次不重复入仓
4. 工作区其它已修改/未跟踪文件（`.agents/`、`_runtime/`、`cmd/ongrid*`、`internal/...`、`web/src/...`、其它 `.record/` 历史条目、`dist/apply-patch.sh` 等）按既有节奏另外处理，本次不动

## 影响

- 入仓体积增加约 12.6MB，主要由 `repowiki-metadata.json`（1.3MB）与 `code_index.json`（744KB）两类自动生成的元数据构成
- 后续 `repo-wiki` skill 重新生成时，可直接通过 git diff 跟踪 wiki 变更