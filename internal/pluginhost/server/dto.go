// Package server 实现 pluginhost 子系统的 HTTP/gRPC 入口。
//
// 本文件定义 wire DTO 与统一响应壳;业务 handler 之外的胶水代码全部
// 内聚在本包,不再 import A 的任何子包(handler 依赖的 biz / data /
// invoke / model / registry / runtime 全部在 pluginhost 内部)。
package server

import (
	"encoding/json"
	"time"

	"github.com/ongridio/ongrid/internal/pluginhost/model"
)

// Response 是所有 HTTP 响应的统一外壳;约定 {code, message, data},
// 与 A 的 manager/server/* 包返回字段保持一致(code=0 表示成功,非
// 零是业务/系统错误码)。
//
// Data 使用 any + omitempty,handler 在写入 JSON 时不必先判空;空
// 响应时由 MarshalJSON 处理为省略字段。
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// OK 构造成功响应。code=0 固定为成功;message="ok" 是占位,后续
// 若需要 i18n 化,允许扩展为带 lang 参数的变体。
func OK(data any) Response {
	return Response{Code: 0, Message: "ok", Data: data}
}

// Err 构造失败响应。code 取业务/系统错误码(>0);message 是给前端
// toast 用的人可读描述。data 留空,允许调用方通过 WithData 自填。
func Err(code int, msg string) Response {
	return Response{Code: code, Message: msg}
}

// PluginInstanceDTO 是 GET /plugins 与 GET /plugins/{id} 的返回体。
//
// 字段与 web/src/api/pluginhost.ts 的 PluginInstance 接口保持 snake_case
// 对齐:CapabilitiesCount 是 server 端聚合的能力计数(原 model 行没有
// 该字段,由 handler 在响应时用 CapRepo.ListByInstance 一次性 batch
// 出来)。
type PluginInstanceDTO struct {
	ID                uint64 `json:"id"`
	TenantID          uint64 `json:"tenant_id"`
	PackID            string `json:"pack_id"`
	Version           string `json:"version"`
	Source            string `json:"source"`
	InstallPath       string `json:"install_path"`
	ManifestSHA256    string `json:"manifest_sha256"`
	Enabled           bool   `json:"enabled"`
	HealthStatus      string `json:"health_status"`
	CapabilitiesCount int    `json:"capabilities_count"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// toInstanceDTO 把 model.PluginInstance + 能力计数转换为 DTO。
//
// 入参 m 允许为 nil(防御性,handler 在 GetPlugin 路径上会先 repo.Get
// 拿到非 nil);为 nil 时返回全零 DTO,避免空指针解引用。
func toInstanceDTO(m *model.PluginInstance, capCount int) PluginInstanceDTO {
	if m == nil {
		return PluginInstanceDTO{}
	}
	return PluginInstanceDTO{
		ID:                uint64(m.ID),
		TenantID:          m.TenantID,
		PackID:            m.PackID,
		Version:           m.Version,
		Source:            m.Source,
		InstallPath:       m.InstallPath,
		ManifestSHA256:    m.ManifestSHA256,
		Enabled:           m.Enabled,
		HealthStatus:      m.HealthStatus,
		CapabilitiesCount: capCount,
		CreatedAt:         m.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:         m.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// CapabilityDTO 是 GET /plugins/{id}/capabilities 的单行返回。
//
// 字段直接镜像 model.PluginCapability 的 wire 字段;MetadataJSON /
// SchemaJSON 等大块 JSON 字段省略,需要时单独拉详情。
type CapabilityDTO struct {
	ID               uint64 `json:"id"`
	PluginInstanceID uint64 `json:"plugin_instance_id"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	Class            string `json:"class"`
	Enabled          bool   `json:"enabled"`
}

// toCapDTO 转换单个 capability。m == nil 时返回全零 DTO。
func toCapDTO(m *model.PluginCapability) CapabilityDTO {
	if m == nil {
		return CapabilityDTO{}
	}
	return CapabilityDTO{
		ID:               uint64(m.ID),
		PluginInstanceID: m.PluginInstanceID,
		Kind:             m.Kind,
		Name:             m.Name,
		Class:            m.Class,
		Enabled:          m.Enabled,
	}
}

// InstallRequest 是 POST /plugins 的入参。
//
//   - Source 必填,取值为 "local" / "tarball" / "remote",与 web 端
//     pluginhost.ts 的 InstallSpec 字段保持一致。
//   - Path:Source=local 时必填,指向已展开的 plugin 目录。
//   - URL:Source=remote 时必填,指向 https(s) tarball。
//   - tarball 由 /plugins/upload 端点单独接收 multipart/form-data。
type InstallRequest struct {
	Source string `json:"source"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
}

// InvokeRequest 是 POST /plugins/{id}/capabilities/{capName}/invoke 的入参。
//
// Params 是任意 JSON 值,直接透传到 Router.Invoke 内部给 capability
// runtime;用 json.RawMessage 避免在 server 层做二次反序列化。
type InvokeRequest struct {
	Params json.RawMessage `json:"params"`
}

// InvokeResultDTO 是 invoke 端点的返回体,与 web/src/api/pluginhost.ts
// 的 InvokeResult 字段一一对应。
//
// Result 用 json.RawMessage 透传 capability 的返回值(可能是对象/数
// 组/标量),不为 nil 时原样 emit;Error 仅在失败时有值,前端据此
// 切换 toast 风格。
type InvokeResultDTO struct {
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	LatencyMs int64           `json:"latency_ms"`
}
