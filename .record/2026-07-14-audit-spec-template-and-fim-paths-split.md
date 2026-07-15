# audit 模板下发 + 结构化 form + fim_paths 拆分

## 问题

Edge 详情页 `audit` 卡片显示异常：

1. **模板没下发**：audit 在 manager 侧没有默认 spec，UI 拿到的 spec 是空 `{}`，前端只看到一个空 textarea，操作员看不到完整模板无从下手。
2. **PLUGIN_META 没条目**：audit 走 fallback 分支，显示「未知 plugin（subprocess wrapper 由 manager 注册）」。
3. **form/json 切换按钮缺失**：audit 不在 `supportsForm` 名单里，操作员只能写 JSON。
4. **fim_paths 默认含 `/mnt/data` 误命中 ongrid-edge 安装树**：默认 PREFIX=`/mnt/data/tools-temp`，整个 `${PREFIX}/ongrid-edge/` 树都在 `/mnt/data` 下，被 auditbeat FIM 默认 watch + hash。每次 supervisor reconcile 重写 `auditbeat.yml` / `promtail.yaml` / `positions.yaml`、每次 ongrid-edge 日志 append 都产生事件。

## 改动

### 1. `internal/edgeagent/plugins/audit/render.go`

- 默认 `fim_paths` 从 `["/opt", "/tmp", "/mnt/data", "/root/x1"]` 改为 `["/opt", "/tmp", "/mnt/data/apps", "/mnt/data/components", "/root/x1"]`，把 `/mnt/data` 拆成 apps / components 两个细分路径，避免拖入整个 ongrid-edge 安装树。其他目录（/opt、/tmp、/root/x1）保持不变，env override / spec 显式配置仍生效。
- 新增导出 `DefaultSpec()` 函数，返回 audit 默认 spec 的 JSON 化形式（与 `buildTemplateData` 中所有非运行时注入字段的默认值一一对应）。供 manager 侧在 UI / edge 拉取时填默认用。

### 2. `internal/manager/model/edge/audit_default.go`（新建）

- 新增 `AuditDefaultSpec()` 函数，**镜像 `audit.DefaultSpec()`**。
- 双源原因：manager 不能直接 import edgeagent 包（内部跨域隔离规则），所以在 model 层镜像一份。**修改默认值时必须同步两边**，两边都有注释提醒。

### 3. `internal/manager/biz/edge/plugin_config.go`

- `ListForUI`（约 line 174-187）：audit spec 为空时填入 `model.AuditDefaultSpec()`。
- `FetchForEdge`（约 line 307-323）：同上，确保 edge 端 fetch 到的 spec 跟 UI 看到的模板一致。

### 4. `web/src/pages/EdgeDetail.tsx`

- `PLUGIN_META` 新增 `audit` 条目（红色 pill `bg-red-500/10 text-red-300`，语义：安全告警类插件）。
- `supportsForm` 加入 `audit`，让 audit 也能切换 form/json 模式（与 logs / traces / custommetrics / databasemetrics 同级）。
- `PluginSpecEditor` 新增 `name === 'audit'` 渲染分支，调用 `AuditSpecForm`。
- 新增 `AuditSpecForm` 组件（line 3363-3672，约 310 行），覆盖 spec 全部 18 个字段：
  - `modules` 多选（fim / auditd / system，至少保留一项）
  - `fim_paths` StringListField
  - `fim_recursive` / `fim_scan_at_start` 复选
  - `fim_scan_rate_per_sec` / `fim_hash_types` / `output_file` SpecInput
  - auditd 子分组（仅当勾选 auditd）：`auditd_resolve_ids` / `auditd_failure_mode` select（silent/log/warn）/ `auditd_backlog_limit` / `auditd_rate_limit` SpecNumberInput / `auditd_rules` StringListField
  - system 子分组（仅当勾选 system）：`system_login` / `system_package` / `system_user` / `system_process` / `system_socket` 复选 / `system_state_period` SpecInput
- 顶部 hint 卡解释 modules / fim_paths / exclude_files 三个核心概念，说明默认 fim_paths 已拆分 `/mnt/data`。

## 目的

- 让操作员在 Edge 详情页直接看到 audit 完整默认模板，而不是空 `{}`，可以按需修改后存回 DB。
- form/json 两种编辑方式都可用，与 logs/traces 体验对齐。
- 默认 fim_paths 拆分后，auditbeat 不再默认监控整个 ongrid-edge 安装树，新装 edge 不再产生自我监控事件。

## 验证

- `go build ./internal/edgeagent/plugins/audit/...` 通过
- `go build ./internal/manager/model/edge/...` 通过
- `go build ./internal/manager/biz/edge/...` 通过
- `go vet ./internal/manager/biz/edge/...` 通过
- `npx tsc --noEmit` 通过
- render_test.go 现有测试无需改动（默认 spec 改动不影响 render 函数行为，仅影响新 `DefaultSpec()` 函数）

## 风险与后续

- 双源默认值（audit 包 + manager model 层）必须同步修改。已在两边 GoDoc 中标注。
- 操作员若显式把 `/mnt/data` 写进 fim_paths，仍会扫到自己（默认已拆分）。若需更强防护，后续可加 `ONGRID_EDGE_INSTALL_PREFIX` env 暴露 install prefix 自动加排除规则（不在本次范围内）。
- AuditSpecForm 中 modules 至少保留一项的约束在 toggleModule 里 silently 不写回，需要用户看到 UI 不变化时意识到原因。