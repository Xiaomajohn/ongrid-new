package edge

// AuditDefaultSpec 返回 audit plugin 默认 spec 的 JSON 化形式。
//
// 镜像 internal/edgeagent/plugins/audit 端的默认 spec 字段。manager 不能
// 直接 import edgeagent 包（内部跨域隔离规则），所以在 model 层放一份。
// **修改默认值时必须同步 audit 包 buildTemplateData() 的硬编码默认值，
// 双源必须一致**。
//
// 仅 ListForUI / FetchForEdge 在 audit plugin 的 spec 为空时调用，
// 让 UI 上能直接看到完整模板而不是空 {}，操作员在 form/json 间切换
// 修改后存回 DB。
//
// 不含运行时注入字段（EdgeID / WorkDir / OutputFilename），env override
// 也不影响此函数。
//
// 字段对照（保持与 internal/edgeagent/plugins/audit/render.go 的
// buildTemplateData 默认值同源）：
//   - modules              → []string{"fim"}
//   - fim_paths            → []string{"/opt", "/tmp", "/mnt/data/apps",
//                                 "/mnt/data/components", "/root/x1"}
//   - fim_recursive        → true
//   - fim_scan_at_start    → true
//   - fim_scan_rate_per_sec→ "5 MiB"
//   - fim_hash_types       → "sha1"
//   - output_file          → "audit.jsonl"
//   - auditd_resolve_ids   → true
//   - auditd_failure_mode  → "silent"
//   - auditd_backlog_limit → 8192
//   - auditd_rate_limit    → 0
//   - auditd_rules         → []string{}
//   - system_state_period  → "12h"
//   - system_login         → true
//   - system_package       → true
//   - system_user          → true
//   - system_process       → true
//   - system_socket        → false
func AuditDefaultSpec() map[string]interface{} {
	return map[string]interface{}{
		"modules":               []string{"fim"},
		"fim_paths":             []string{"/opt", "/tmp", "/mnt/data/apps", "/mnt/data/components", "/root/x1"},
		"fim_recursive":         true,
		"fim_scan_at_start":     true,
		"fim_scan_rate_per_sec": "5 MiB",
		"fim_hash_types":        "sha1",
		"output_file":           "audit.jsonl",
		"auditd_resolve_ids":    true,
		"auditd_failure_mode":   "silent",
		"auditd_backlog_limit":  8192,
		"auditd_rate_limit":     0,
		"auditd_rules":          []string{},
		"system_state_period":   "12h",
		"system_login":          true,
		"system_package":        true,
		"system_user":           true,
		"system_process":        true,
		"system_socket":         false,
	}
}
