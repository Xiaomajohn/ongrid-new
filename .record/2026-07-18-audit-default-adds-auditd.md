# audit 插件默认启用 auditd 模块（系统文件执行监控默认开启）

## 现象

操作员反馈：edge 端 audit 插件渲染出的 `auditbeat.yml` 只有 `file_integrity` 一个模块，
没有任何"哪个进程执行了 /bin、/usr/bin 下的二进制"类事件，怀疑 audit 缺这部分能力。

## 排查

后端 `internal/edgeagent/plugins/audit/render.go` 已支持 `file_integrity / auditd / system`
三个模块，通过 `Spec.modules` 切片控制启用列表；模板注释里也写明 "modules cover the
modules ops actually use (fim / auditd / system)"。实际不是缺失能力，而是
`DefaultSpec()` 与 `AuditDefaultSpec()` 把 `modules` 钉死在 `["fim"]`——auditd 模块
根本没被加载，所以即使用户在 UI 上勾 auditd，看到的 yml 也对（前提是他显式勾了）。

auditbeat 里"系统文件被某个进程执行了"的真实事件源是 `auditd` 模块的
`-w /path -p x -k <key>` 规则抓 execve 系统调用。`system` 模块的 `process` dataset
只是周期进程快照，不算"执行事件"。

## 改动

1. `internal/edgeagent/plugins/audit/render.go`
   - `buildTemplateData()`: `modules` 默认从 `["fim"]` 改为 `["fim", "auditd"]`，附中文注释
     说明变更理由（fim 没有 execve 事件面，要监控执行必须用 auditd 的 -w 规则）。
   - `DefaultSpec()`: 同改，并在函数注释里说明 auditd_rules 故意保持空数组——加载
     auditd 模块本身是 no-op，只有 operator 在 UI 显式填入规则后才真正产出 execve 事件，
     避免误伤存量设备。

2. `internal/manager/model/edge/audit_default.go`
   - `AuditDefaultSpec()` 镜像同步 `modules = []string{"fim", "auditd"}`，注释同步。

3. `internal/edgeagent/plugins/audit/render_test.go`
   - `TestRenderDefaultModules` 改造：原来断言 "默认只有 fim、不渲染 auditd"，现在是
     "默认应有 file_integrity + auditd、不应有 system"，并在 auditd 不出错的情况下
     显式断言 auditd 默认值（`resolve_ids: true`、`audit_rules: |` 块存在）。

4. `web/src/pages/EdgeDetail.tsx` audit 配置卡片
   - `auditd_rules` 字段 hint 增加中文长版说明：默认加载 auditd 模块但不发事件，要监控
     "谁执行了 /usr/bin 下的二进制"请加诸如 `-w /bin -p x -k bin_exec` /
     `-w /usr/bin -p x -k bin_exec` / `-w /sbin -p x -k bin_exec` /
     `-w /usr/sbin -p x -k bin_exec` 这类规则，规则为空则不产出 execve 事件。
   - placeholder 从 `-w /etc/passwd -p wa -k passwd_changes` 换成
     `-w /usr/bin -p x -k bin_exec`（占位符更贴本次主题）。

## 取舍

- **为什么不顺手把 auditd_rules 默认填四条常用路径？** auditd 事件一旦启用会在高 QPS
  机器上产生可观事件量，默认填满可能造成首次部署就有日志风暴、由"挂载能力"升级成
  "对新设备立刻产生大量事件"。把"加载模块"与"产生事件"解耦是更安全的默认行为——
  加载动作只让 auditd YAML 块出现在配置里、运行时是零事件，等运维在 UI 上主动填入
  规则才生效。
- **UI 上其它提示文字（顶部 modules 整体说明）保持原样**：只是 auditd_rules 这一行
  hint 加长，避免大面积文案异动。

## 适用范围

- 本次改动之后新建的 edge：直接拿到 `modules = ["fim", "auditd"]`，audit.jsonl
  多一个 auditd 块（无规则）。
- 已部署的存量 edge：DB 里 `plugin_config.spec` 已存的旧值不被这次改动覆盖，需走
  manager 侧的 reconcile 重新下发 spec；reconcile 逻辑若比对新旧默认值会发现
  `modules` 字段差异并写入新值，从而让存量设备下次重启 audit 时也拿到 auditd 块。
  若 reconcile 不做 spec diff 直接下发默认，需要另外触发一次 spec refresh。
- audit 进程重启后 audit.jsonl 文件结构变化已在 exclude_files RE2 里
  涵盖（`audit\.jsonl(-\d{8}(-\d+)?\.ndjson|(\.\d+)?)$`），不会有 FIM
  自监控噪声回归。

## 双源同步检查

- `render.go` `buildTemplateData()` 默认 modules
- `render.go` `DefaultSpec()` 默认 modules
- `audit_default.go` `AuditDefaultSpec()` 默认 modules
- `render_test.go` `TestRenderDefaultModules` 默认值断言
四处一致为 `["fim", "auditd"]`。修改默认值时只动一处会立即被其它三处测试或前端 UI 反映出来。
