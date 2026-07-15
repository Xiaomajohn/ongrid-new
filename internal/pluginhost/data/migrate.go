// Package data 是 pluginhost 子系统的持久化层。
//
// 本包封装对四张插件管理表的 GORM CRUD,以及一次性建表的 Migrate 入口:
//   - PluginRepo      -> plugin_instances
//   - CapabilityRepo  -> plugin_capabilities
//   - InvocationRepo  -> plugin_invocations
//   - AuditRepo       -> plugin_audits
//
// 本包只依赖 model 层和 gorm,绝不能 import A 老包;由 Phase 2 的
// biz 层组装这些 repo 后再对外暴露服务。
package data

import (
	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/pluginhost/model"
)

// Migrate 在启动期调用,一次性建表/索引。本函数应嵌入项目原有的
// bootstrap 流程(如 OnMigrate hook),与 A 老表共存于同一个 *gorm.DB。
// 整体放在事务内,失败自动回滚;四张表互相无外键硬依赖,迁移顺序
// 满足外键前提(PluginCapability.PluginInstanceID 必须在 PluginInstance 之后)。
func Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		return tx.AutoMigrate(
			&model.PluginInstance{},
			&model.PluginCapability{},
			&model.PluginInvocation{},
			&model.PluginAudit{},
		)
	})
}
