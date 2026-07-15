package model

import (
	"time"

	"gorm.io/gorm"
)

// PluginInvocation 单次 capability invoke 的调用流水。
//
// 每次(A 业务 / C 反向 host.call)发起的能力调用落一行;
// 用于事后分析、慢查询定位、SLO 计算。
type PluginInvocation struct {
	gorm.Model
	TenantID         uint64 `gorm:"index"`
	PluginInstanceID uint64 `gorm:"index"`
	CapabilityID     uint64 `gorm:"index"`
	Caller           string `gorm:"size:64"` // 调用方 ID,如 "alert-pipeline" / "edge-x" / "web:user:42"
	Component        string `gorm:"size:64"` // 调用来源组件:pluginhost / hostcall
	TraceID          string `gorm:"size:64;index"`
	ParamsJSON       string `gorm:"type:text"`
	ResultJSON       string `gorm:"type:text"`
	ErrorMessage     string `gorm:"size:1024"`
	LatencyMs        int64
	Success          bool      `gorm:"index"`
	InvokedAt        time.Time `gorm:"index"`
}

// TableName 指定 GORM 表名。
func (PluginInvocation) TableName() string {
	return "plugin_invocations"
}
