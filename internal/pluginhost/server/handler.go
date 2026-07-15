// Package server / handler.go 实现 9 个 HTTP 端点对应的业务 handler。
//
// 端点 ↔ 路径 ↔ handler 的对应表见 routes.go。handler 不持有任何
// 全局状态,所有依赖通过 Handler 字段注入;这样 pluginhost.New
// 在 wire-up 阶段可以拼装零副作用的服务对象。
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/pluginhost/biz"
	"github.com/ongridio/ongrid/internal/pluginhost/data"
	"github.com/ongridio/ongrid/internal/pluginhost/invoke"
	"github.com/ongridio/ongrid/internal/pluginhost/manifest"
	"github.com/ongridio/ongrid/internal/pluginhost/registry"
	rtp "github.com/ongridio/ongrid/internal/pluginhost/runtime"
)

// Handler 是 server 子包对外暴露的业务入口集合。
//
// 字段全部是 pluginhost 内部子包的零依赖类型,handler 不会 import
// A 的 manager/iam/edgeagent/api 子包,满足 plan §11 的"A 零改动"
// 约束。Phase 5 接入 sandbox / 签名 / 配额时,这里只需追加字段。
type Handler struct {
	PluginRepo *data.PluginRepo
	CapRepo    *data.CapabilityRepo
	InvokeRepo *data.InvocationRepo
	AuditRepo  *data.AuditRepo
	Reg        *registry.Registry
	Pool       *rtp.Pool
	Router     *invoke.Router
	Installer  *biz.Installer
	Lifecycle  *biz.Lifecycle
}

// ===== 列表 / 详情 ========================================================

// ListPlugins GET /plugins
//
// 拉该租户下全部 plugin_instances(默认 limit=50 offset=0,与
// data.PluginRepo.List 默认值一致),逐 instance 调 CapRepo 统计
// capability 数量(Phase 1 简化为 N+1,Phase 5 起 CapRepo 暴露
// ListByTenant 后改为单次聚合)。
//
// 行为契约:
//
//   - PluginRepo 为 nil → 500。
//   - CapRepo 为 nil   → 仍能返回,capabilities_count=0(降级)。
//   - Repo List 错误   → 500。
func (h *Handler) ListPlugins(w http.ResponseWriter, r *http.Request) {
	if h.PluginRepo == nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, "plugin repo not wired"))
		return
	}
	tenantID := TenantIDFrom(r.Context())
	limit := parseIntQuery(r, "limit", 50)
	offset := parseIntQuery(r, "offset", 0)

	rows, err := h.PluginRepo.List(r.Context(), tenantID, limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, err.Error()))
		return
	}

	out := make([]PluginInstanceDTO, 0, len(rows))
	for i := range rows {
		count := 0
		if h.CapRepo != nil {
			caps, cerr := h.CapRepo.ListByInstance(r.Context(), uint64(rows[i].ID))
			if cerr == nil {
				count = len(caps)
			}
		}
		out = append(out, toInstanceDTO(&rows[i], count))
	}
	writeJSON(w, http.StatusOK, OK(out))
}

// GetPlugin GET /plugins/{id}
//
// 按主键取单条 plugin_instance,统计该 instance 的 capability 数量。
// PluginRepo.Get 在 id 不存在时返 data.ErrNotFound,handler 转 404。
func (h *Handler) GetPlugin(w http.ResponseWriter, r *http.Request) {
	if h.PluginRepo == nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, "plugin repo not wired"))
		return
	}
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	row, err := h.PluginRepo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, data.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, Err(404, "plugin not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, Err(500, err.Error()))
		return
	}

	count := 0
	if h.CapRepo != nil {
		caps, cerr := h.CapRepo.ListByInstance(r.Context(), uint64(id))
		if cerr == nil {
			count = len(caps)
		}
	}
	writeJSON(w, http.StatusOK, OK(toInstanceDTO(row, count)))
}

// ListCapabilities GET /plugins/{id}/capabilities
//
// 列出某插件实例的全部 capability,按 kind+name 排序(由 repo 保证)。
func (h *Handler) ListCapabilities(w http.ResponseWriter, r *http.Request) {
	if h.CapRepo == nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, "capability repo not wired"))
		return
	}
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	rows, err := h.CapRepo.ListByInstance(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, err.Error()))
		return
	}
	out := make([]CapabilityDTO, 0, len(rows))
	for i := range rows {
		out = append(out, toCapDTO(&rows[i]))
	}
	writeJSON(w, http.StatusOK, OK(out))
}

// ===== 生命周期 ===========================================================

// InstallPlugin POST /plugins
//
// 解 InstallRequest → 构造最小 manifest → 调 Installer.Install 落
// DB+registry+audit。失败时 Installer 包了 ErrInstallFailed,handler
// 透传 500。
//
// P1 阶段:本方法用 request 字段直接构造 PluginManifest(ID=path
// basename, Version="0.0.0", Entry=Path, Capabilities=[]);Phase 5
// 起会替换为"先调 manifest.LoadDirs 再调 Installer.Install"的
// 二段式,本方法签名保持不变。
func (h *Handler) InstallPlugin(w http.ResponseWriter, r *http.Request) {
	if h.Installer == nil {
		writeJSON(w, http.StatusNotImplemented, Err(501, "installer not wired"))
		return
	}
	var req InstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, Err(400, "invalid json body: "+err.Error()))
		return
	}
	if req.Source == "" {
		writeJSON(w, http.StatusBadRequest, Err(400, "source required"))
		return
	}
	if req.Source == "local" && req.Path == "" {
		writeJSON(w, http.StatusBadRequest, Err(400, "path required for source=local"))
		return
	}
	if req.Source == "remote" && req.URL == "" {
		writeJSON(w, http.StatusBadRequest, Err(400, "url required for source=remote"))
		return
	}

	// P1 minimal manifest:Phase 5 接入 LoadDirs 后,这里改为先
	// 扫描目录得到完整 manifest,再把 source 当做元数据保留。
	mf := &manifest.PluginManifest{
		ID:      derivePackIDFromReq(req),
		Name:    derivePackIDFromReq(req),
		Version: "0.0.0",
		Format:  manifest.FormatPluginHost,
		Entry:   req.Path,
	}
	inst, err := h.Installer.Install(r.Context(), mf, req.Source)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, err.Error()))
		return
	}

	count := 0
	if h.CapRepo != nil {
		caps, _ := h.CapRepo.ListByInstance(r.Context(), uint64(inst.ID))
		count = len(caps)
	}
	dto := toInstanceDTOFromRegistry(inst, count)
	// 新建实例的 CreatedAt/UpdatedAt 在 P1 拿不到(DTO 内部用
	// model.PluginInstance 的时间戳),这里以 now() 兜底展示;
	// DB 真实值以 get 路径为准。
	dto.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	dto.UpdatedAt = dto.CreatedAt
	writeJSON(w, http.StatusOK, OK(dto))
}

// UninstallPlugin DELETE /plugins/{id}
//
// 调 Lifecycle.Uninstall 删 DB 行 + 摘 registry + 写 audit。失败
// 时 Lifecycle 已包 ErrLifecycleFailed;handler 透传 500。
func (h *Handler) UninstallPlugin(w http.ResponseWriter, r *http.Request) {
	if h.Lifecycle == nil {
		writeJSON(w, http.StatusNotImplemented, Err(501, "lifecycle not wired"))
		return
	}
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	if err := h.Lifecycle.Uninstall(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EnableCapability POST /plugins/{id}/capabilities/{capName}/enable
func (h *Handler) EnableCapability(w http.ResponseWriter, r *http.Request) {
	h.setCapabilityEnabled(w, r, true)
}

// DisableCapability POST /plugins/{id}/capabilities/{capName}/disable
func (h *Handler) DisableCapability(w http.ResponseWriter, r *http.Request) {
	h.setCapabilityEnabled(w, r, false)
}

// setCapabilityEnabled 是 Enable/Disable 共用的实现。
//
//   - Lifecycle.Enable/Disable 内部已包 SetEnabled 流程,handler
//     不再二次调 CapRepo.SetEnabled(避免状态漂移)。
//   - capName 缺省 / 空白都视作 400。
func (h *Handler) setCapabilityEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if h.Lifecycle == nil {
		writeJSON(w, http.StatusNotImplemented, Err(501, "lifecycle not wired"))
		return
	}
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	capName := chi.URLParam(r, "capName")
	if capName == "" {
		writeJSON(w, http.StatusBadRequest, Err(400, "capability name required"))
		return
	}
	var err error
	if enabled {
		err = h.Lifecycle.Enable(r.Context(), id, capName)
	} else {
		err = h.Lifecycle.Disable(r.Context(), id, capName)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InvokeCapability POST /plugins/{id}/capabilities/{capName}/invoke
//
// 调 Router.Invoke 把 params 透传到 capability 端;同时度量
// latency_ms,转成 InvokeResultDTO。Router 的 audit 回调由
// Router.WithAudit 在 wire-up 阶段注入(plan §13 信道 3),本方法
// 不直接调 audit。
func (h *Handler) InvokeCapability(w http.ResponseWriter, r *http.Request) {
	if h.Router == nil {
		writeJSON(w, http.StatusNotImplemented, Err(501, "router not wired"))
		return
	}
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}
	capName := chi.URLParam(r, "capName")
	if capName == "" {
		writeJSON(w, http.StatusBadRequest, Err(400, "capability name required"))
		return
	}

	var req InvokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, Err(400, "invalid json body: "+err.Error()))
		return
	}
	if len(req.Params) == 0 {
		// 空 body 时给字面量 null,避免 router 端碰到 nil。
		req.Params = json.RawMessage("null")
	}

	opts := invoke.InvokeOptions{
		Caller:   buildCaller(r),
		Deadline: time.Time{}, // 零值由 Router 兜底为 +30s
		TraceID:  RequestIDFrom(r.Context()),
	}
	start := time.Now()
	resp, err := h.Router.Invoke(r.Context(), id, capName, req.Params, opts)
	latency := time.Since(start)

	dto := InvokeResultDTO{
		Result:    resp.Result,
		LatencyMs: latency.Milliseconds(),
	}
	if err != nil {
		dto.Error = err.Error()
	}
	if resp.Error != "" && dto.Error == "" {
		dto.Error = resp.Error
	}
	status := http.StatusOK
	if err != nil || resp.Error != "" {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, OK(dto))
}

// UploadTarball POST /plugins/upload
//
// multipart/form-data,tarball 文件由 web 端 append 到 FormData。
// P1 阶段:返回 501,等 Phase 5 接入 manifest.LoadDirs + 解包。
func (h *Handler) UploadTarball(w http.ResponseWriter, r *http.Request) {
	// 显式 drain,避免连接复用时 body 残留;真正解包留 Phase 5。
	_ = r.Body.Close()
	writeJSON(w, http.StatusNotImplemented, Err(501, "tarball upload not yet implemented; use POST /plugins with source=local"))
}

// ===== 辅助函数 ============================================================

// parseIDParam 从 URL 读 {id},转 uint64,失败直接 400 写回。
func parseIDParam(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	raw := chi.URLParam(r, "id")
	if raw == "" {
		writeJSON(w, http.StatusBadRequest, Err(400, "id required"))
		return 0, false
	}
	v, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || v == 0 {
		writeJSON(w, http.StatusBadRequest, Err(400, "invalid id"))
		return 0, false
	}
	return v, true
}

// parseIntQuery 解析 query string 的 int 字段,缺省或非法返 def。
func parseIntQuery(r *http.Request, key string, def int) int {
	if v := r.URL.Query().Get(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// buildCaller 从 ctx 拼一个 rtp.Caller 给 router/hostcall 用。
//
// TenantID 直接从 ctx 取(X-Tenant-ID);UserID 暂走 X-User-ID
// header(P1 阶段无 auth,默认 0);Component 默认 "web"。
func buildCaller(r *http.Request) rtp.Caller {
	c := rtp.Caller{
		Component: "web",
		TenantID:  TenantIDFrom(r.Context()),
		TraceID:   RequestIDFrom(r.Context()),
	}
	if v := r.Header.Get("X-User-ID"); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			c.UserID = n
		}
	}
	return c
}

// derivePackIDFromReq 用 path/url 末尾段作为 PackID 占位,Phase 5
// 替换为从 manifest 中读取。
func derivePackIDFromReq(req InstallRequest) string {
	p := req.Path
	if p == "" {
		p = req.URL
	}
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// toInstanceDTOFromRegistry 把 registry.PluginInstance 转为 DTO。
//
// registry 层不带 CreatedAt/UpdatedAt(进程级对象不需要),由调用方
// 兜底填充;字段语义与 toInstanceDTO 对齐。
func toInstanceDTOFromRegistry(p *registry.PluginInstance, capCount int) PluginInstanceDTO {
	if p == nil {
		return PluginInstanceDTO{}
	}
	return PluginInstanceDTO{
		ID:                p.ID,
		TenantID:          p.TenantID,
		PackID:            p.PackID,
		Version:           p.Version,
		Source:            p.Source,
		InstallPath:       p.InstallPath,
		ManifestSHA256:    p.ManifestSHA256,
		Enabled:           p.Enabled,
		HealthStatus:      p.HealthStatus,
		CapabilitiesCount: capCount,
	}
}
