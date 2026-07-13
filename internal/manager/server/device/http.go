// Package device builds the HTTP routes for the manager/device
// sub-domain (May 2026 entity split). The Handler mirrors
// manager/server/edge but is keyed on Device rather than Edge — host
// facts (hostname, OS, CPU/mem/disk capacity, live usage) and the
// operator-assigned roles bit set live here.
package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	devicebiz "github.com/ongridio/ongrid/internal/manager/biz/device"
	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tenantctx"
)

// roleAdmin mirrors iam/model.RoleAdmin without crossing the BC boundary
// (arch-lint forbids manager -> iam imports).
const roleAdmin = "admin"

// Handler exposes /v1/devices.
type Handler struct {
	uc    *devicebiz.Usecase
	edges EdgeLookup // optional; consulted by getSSHInfo, see credentials.go
}

// NewHandler builds the handler around a device biz Usecase.
func NewHandler(uc *devicebiz.Usecase) *Handler { return &Handler{uc: uc} }

// SetEdgeLookup wires the (optional) edge-status adapter consulted by
// getSSHInfo to report agent reachability alongside the stored creds.
// Must be called before Register if the SPA wants edge_online in the
// response; otherwise the SSH endpoints still work and report
// edge_online=false.
func (h *Handler) SetEdgeLookup(e EdgeLookup) { h.edges = e }

// Register attaches the device routes on r.
//
// Routes:
//
//	POST /v1/devices (admin) — register a new logical host + SSH creds
//	GET /v1/devices (any authed) — supports ?include_deleted=true
//	GET /v1/devices/{id} (any authed)
//	PATCH /v1/devices/{id} (admin) — name / description
//	PATCH /v1/devices/{id}/roles (admin)
//	DELETE /v1/devices/{id}?hard=true (admin) — soft by default
//	POST /v1/devices/{id}/restore (admin) — revive soft-deleted row
//	GET /v1/devices/{id}/edges (any authed) — junction edges
//	PUT /v1/devices/{id}/ssh-credentials (admin)
//	GET /v1/devices/{id}/ssh-info (any authed)
//	DELETE /v1/devices/{id}/ssh-credentials (admin)
func (h *Handler) Register(r chi.Router) {
	r.With(h.requireAdmin).Post("/v1/devices", h.create)
	r.Get("/v1/devices", h.list)
	r.Get("/v1/devices/{id}", h.get)
	r.With(h.requireAdmin).Patch("/v1/devices/{id}", h.update)
	r.With(h.requireAdmin).Patch("/v1/devices/{id}/roles", h.updateRoles)
	r.With(h.requireAdmin).Delete("/v1/devices/{id}", h.delete)
	r.With(h.requireAdmin).Post("/v1/devices/{id}/restore", h.restore)
	r.Get("/v1/devices/{id}/edges", h.listEdges)
	// SSH credential endpoints — implementations live in
	// credentials.go to keep this file focused on host facts.
	r.With(h.requireAdmin).Put("/v1/devices/{id}/ssh-credentials", h.putSSHCredentials)
	r.Get("/v1/devices/{id}/ssh-info", h.getSSHInfo)
	r.With(h.requireAdmin).Delete("/v1/devices/{id}/ssh-credentials", h.deleteSSHCredentials)
}

// requireAdmin is a thin middleware that 403s non-admin callers.
func (h *Handler) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, ok := tenantctx.From(r.Context())
		if !ok {
			writeErr(w, errs.ErrUnauthorized)
			return
		}
		if t.Role != roleAdmin {
			writeErr(w, errs.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- DTOs ---

type deviceItem struct {
	ID             uint64     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description,omitempty"`
	Hostname       string     `json:"hostname,omitempty"`
	OS             string     `json:"os,omitempty"`
	OSVersion      string     `json:"os_version,omitempty"`
	Arch           string     `json:"arch,omitempty"`
	KernelVersion  string     `json:"kernel_version,omitempty"`
	IPAddress      string     `json:"ip_address,omitempty"`
	CPUCount       int        `json:"cpu_count,omitempty"`
	MemTotalBytes  uint64     `json:"mem_total_bytes,omitempty"`
	DiskTotalBytes uint64     `json:"disk_total_bytes,omitempty"`
	CPUUsagePct    float32    `json:"cpu_usage_pct"`
	MemUsagePct    float32    `json:"mem_usage_pct"`
	DiskUsagePct   float32    `json:"disk_usage_pct"`
	Roles          []string   `json:"roles"`
	Online         bool       `json:"online"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	// Reachable / LastReachableAt 是 ping 服务定时写入的结果，与 Edge
	// 推送的 Online 解耦。SPA 的 Hosts 页面按 Reachable 渲染状态列。
	Reachable       bool       `json:"reachable"`
	LastReachableAt *time.Time `json:"last_reachable_at,omitempty"`
	// DeletedAt: 非 nil 表示当前行已被软删除。`include_deleted=true` 时
	// 后端才会把这些行返给前端，让 Logs / Hosts 的"显示已删除"开关能
	// 渲染灰标 + "已删除"后缀。Pointer 让未删行直接省字段。
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// SSH fields echoed in clear (internal ops system — plaintext is
	// the documented contract; see model/device/model.go + the 行为变化
	// entry in CHANGELOG.md v0.9.1). Host / port / user / auth_kind are
	// non-secret context that operators expect back; password / key are
	// the secrets, returned on purpose so the SPA can review / rotate.
	SSHHost     string `json:"ssh_host,omitempty"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user,omitempty"`
	SSHAuthKind string `json:"ssh_auth_kind,omitempty"`
	SSHPassword string `json:"ssh_password,omitempty"`
	SSHKey      string `json:"ssh_key,omitempty"`
	// NodeID is the link to the topology `nodes` table. Lets
	// the SPA's device-detail Topology tab resolve neighbours without
	// a separate /v1/topology lookup. Nullable until topology.Migrate
	// has run its backfill for this row.
	NodeID *uint64 `json:"node_id,omitempty"`
	// Edges 列出与本 device 关联的所有 edge 精简行（不含凭据字段），
	// 用于 SPA 的 Logs 页面设备 / 任务下拉一次拿到 task_name 等，避免
	// 单独再发 N 次反查。空 slice（不是 nil）表示该 device 没装任何
	// edge；nil 表示后端批量查失败时降级不返。列表接口 best-effort：
	// 失败仅记 stderr，不影响 device 主表数据返回。
	Edges []edgeMiniItem `json:"edges"`
}

// edgeMiniItem 是 device 列表内嵌的 edge 精简版：仅保留与 SPA 下拉 /
// 详情展示相关的字段。不含 access_key_id / secret_key_hash —— 列表
// 接口授权面比 GET /v1/edges 更广，不能借机泄漏 agent 凭据。源数据由
// biz/device.EdgeMini 经 devToItem 在 list handler 中转译而来。
type edgeMiniItem struct {
	ID         uint64     `json:"id"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	TaskName   string     `json:"task_name,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

type listResp struct {
	Items []deviceItem `json:"items"`
	Total int          `json:"total"`
}

// updateReq 是 PATCH /v1/devices/{id} 的 wire body。所有字段都是可选
// 的（指针类型，nil=不改）。operator 在 UI 上点"编辑主机"提交一个
// 局部变更，服务端只回写被显式设的字段——这样下面的 SSH 凭据修改不会
// 误清空现有未提交的密码 / 私钥。
type updateReq struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Hostname    *string `json:"hostname,omitempty"`
	// SSH 连接块。EmptyString ("") 在服务端视为"清空该字段"（仅对
	// port / auth_kind / password / key 适用，host/user 有合法性校验）。
	SSHHost     *string `json:"ssh_host,omitempty"`
	SSHPort     *int    `json:"ssh_port,omitempty"`
	SSHUser     *string `json:"ssh_user,omitempty"`
	SSHAuthKind *string `json:"ssh_auth_kind,omitempty"`
	SSHPassword *string `json:"ssh_password,omitempty"`
	SSHKey      *string `json:"ssh_key,omitempty"`
}

type updateRolesReq struct {
	Roles []string `json:"roles"`
}

// createReq is the wire body for POST /v1/devices. Field names match
// the SPA's CreateDeviceInput (api/devices.ts) so the frontend doesn't
// need a remap shim. Host facts (OS / Arch / CPU / Mem / disk) are
// intentionally absent — those arrive from the edge agent's register
// call, not from the operator.
type createReq struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	SSHHost     string `json:"ssh_host"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHAuthKind string `json:"ssh_auth_kind"` // "password" | "key"
	SSHPassword string `json:"ssh_password,omitempty"`
	SSHKey      string `json:"ssh_key,omitempty"`
}

// createResp is what the SPA expects back from CreateDeviceResponse:
// id / name / hostname / description / created_at, plus the SSH block
// echoed in clear. The project decided SSH secrets come back plaintext
// in API responses — ongrid is an internal ops platform and the
// operator-UX trade-off beats envelope crypto. The wire contract is
// also documented in server/device/credentials.go (ssh-info) and the
// model-level comment on Device.SSHPassword / SSHKey.
type createResp struct {
	ID          uint64    `json:"id"`
	Name        string    `json:"name"`
	Hostname    string    `json:"hostname,omitempty"`
	Description string    `json:"description,omitempty"`
	SSHHost     string    `json:"ssh_host,omitempty"`
	SSHPort     int       `json:"ssh_port"`
	SSHUser     string    `json:"ssh_user,omitempty"`
	SSHAuthKind string    `json:"ssh_auth_kind,omitempty"`
	SSHPassword string    `json:"ssh_password,omitempty"`
	SSHKey      string    `json:"ssh_key,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type edgeLinkRow struct {
	EdgeID    uint64    `json:"edge_id"`
	DeviceID  uint64    `json:"device_id"`
	Type      string    `json:"type"` // host | discovered
	CreatedAt time.Time `json:"created_at"`
}

// --- handlers ---

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	q := r.URL.Query()
	f := devicebiz.ListFilter{
		Hostname: q.Get("hostname"),
		Name:     q.Get("name"),
		IP:       q.Get("ip"),
	}
	if rolesParam := q.Get("roles"); rolesParam != "" {
		var mask uint8
		var unknownOnly bool
		for _, raw := range strings.Split(rolesParam, ",") {
			n := strings.TrimSpace(raw)
			switch n {
			case "":
				continue
			case devicemodel.RoleUnknown:
				unknownOnly = true
			default:
				if !devicemodel.IsValidRoleName(n) {
					writeErr(w, errors.Join(errs.ErrInvalid, fmt.Errorf("unknown role %q", n)))
					return
				}
				mask |= devicemodel.EncodeRoles([]string{n})
			}
		}
		if unknownOnly && mask != 0 {
			writeErr(w, errors.Join(errs.ErrInvalid, errors.New("cannot combine 'unknown' with named roles")))
			return
		}
		f.RolesAny = mask
		f.RolesUnknownOnly = unknownOnly
	}
	if v := q.Get("online"); v != "" {
		switch strings.ToLower(v) {
		case "true", "1":
			t := true
			f.Online = &t
		case "false", "0":
			t := false
			f.Online = &t
		}
	}
	if s := q.Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.Limit = n
		}
	}
	if s := q.Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			f.Offset = n
		}
	}
	if v := strings.ToLower(strings.TrimSpace(q.Get("include_deleted"))); v == "true" || v == "1" || v == "yes" {
		f.IncludeDeleted = true
	}

	rows, err := h.uc.List(r.Context(), f)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Best-effort 批量拉每台 device 关联的 edge 精简行。失败不阻断主
	// 列表（device 数据照样返），只是 edges 字段缺省；前端下拉会
	// 退化为只有 device 主信息可用。空 rows 时直接跳过，省一次 DB。
	var edgesByDevice map[uint64][]devicebiz.EdgeMini
	if links := h.uc.Links(); links != nil && len(rows) > 0 {
		ids := make([]uint64, 0, len(rows))
		for _, d := range rows {
			ids = append(ids, d.ID)
		}
		if m, lerr := links.ListEdgesForDevices(r.Context(), ids); lerr == nil {
			edgesByDevice = m
		} else {
			fmt.Fprintf(os.Stderr, "device list: list edges for devices failed: %v\n", lerr)
		}
	}
	out := make([]deviceItem, 0, len(rows))
	for _, d := range rows {
		item := devToItem(d)
		if edges, ok := edgesByDevice[d.ID]; ok {
			item.Edges = make([]edgeMiniItem, 0, len(edges))
			for _, e := range edges {
				item.Edges = append(item.Edges, edgeMiniItem{
					ID:         e.ID,
					Name:       e.Name,
					Status:     e.Status,
					TaskName:   e.TaskName,
					LastSeenAt: e.LastSeenAt,
				})
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, listResp{Items: out, Total: len(out)})
}

// create registers a new logical host + SSH credentials. Admin only
// — matches the legacy edge CreateEdge admin gate. The handler is a
// thin DTO mapper; validation + persistence live in biz/device.Create.
//
// Wire shape matches CreateDeviceResponse in web/src/api/devices.ts so
// the SPA can re-use its existing `createDevice` helper without a
// remap shim.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	t, ok := tenantctx.From(r.Context())
	if !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	in := devicebiz.CreateInput{
		Name:        req.Name,
		Description: req.Description,
		Hostname:    req.Hostname,
		SSHHost:     req.SSHHost,
		SSHPort:     req.SSHPort,
		SSHUser:     req.SSHUser,
		SSHAuthKind: req.SSHAuthKind,
		SSHPassword: req.SSHPassword,
		SSHKey:      req.SSHKey,
	}
	var createdBy *uint64
	if t.UserID != 0 {
		uid := t.UserID
		createdBy = &uid
	}
	d, err := h.uc.Create(r.Context(), in, createdBy)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, createResp{
		ID:          d.ID,
		Name:        d.Name,
		Hostname:    d.Hostname,
		Description: d.Description,
		SSHHost:     d.SSHHost,
		SSHPort:     d.SSHPort,
		SSHUser:     d.SSHUser,
		SSHAuthKind: d.SSHAuthKind,
		SSHPassword: d.SSHPassword,
		SSHKey:      d.SSHKey,
		CreatedAt:   d.CreatedAt,
	})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	d, err := h.uc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, devToItem(d))
}

// update 是 PATCH /v1/devices/{id} 的主 handler。处理两个逻辑块：
//
//   1) name / description / hostname 走 UpdateNameDescription。三个都是
//      可选；只传 name 不传 description 时不破坏现有 description；
//   2) SSH 连接块 (host/port/user/auth_kind/password/key) 走
//      SetSSHCredentials。任何一个字段被提供都触发重写；为避免“只改了
//      ssh_host 却不小心被 password 同一个 nil 指针干掉现有 password
//      ”的 bug，未提供的字段从当前行读回再 setSSHCredentials（merge
//      语义）。空字符串 ("") 仍然是“清空”语义（仅对 password / key /
//      auth_kind 有效，host/user 的空串在调用层拒绝）。
//
// 返回 200 + 最新一行的 DTO，让 SPA 不用再发一次 GET。
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var in updateReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	d, err := h.uc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	// --- Block 1: 展示字段 ---
	if in.Name != nil || in.Description != nil || in.Hostname != nil {
		name := d.Name
		desc := d.Description
		hostname := d.Hostname
		if in.Name != nil {
			name = *in.Name
		}
		if in.Description != nil {
			desc = *in.Description
		}
		if in.Hostname != nil {
			hostname = *in.Hostname
		}
		if err := h.uc.UpdateNameDescription(r.Context(), id, name, desc, hostname); err != nil {
			writeErr(w, err)
			return
		}
		// 后面 SSH 块要拿最新 name/desc/hostname 走 merge，
		// 这里补一下 d 的镜像（避免后面从 DB 重读一次）。
		d.Name, d.Description, d.Hostname = name, desc, hostname
	}

	// --- Block 2: SSH 连接块 ---
	// 任意一个 SSH 字段被提供则走 SetSSHCredentials；未提供的字段从
	// d 镜像里读回，保证现有 password / key 不被 nil 推成零值。
	if in.SSHHost != nil || in.SSHPort != nil || in.SSHUser != nil ||
		in.SSHAuthKind != nil || in.SSHPassword != nil || in.SSHKey != nil {
		creds := devicebiz.SSHCredentials{
			Host:     d.SSHHost,
			Port:     d.SSHPort,
			User:     d.SSHUser,
			AuthKind: d.SSHAuthKind,
			Password: d.SSHPassword,
			Key:      d.SSHKey,
		}
		if in.SSHHost != nil {
			creds.Host = *in.SSHHost
		}
		if in.SSHPort != nil {
			creds.Port = *in.SSHPort
		}
		if in.SSHUser != nil {
			creds.User = *in.SSHUser
		}
		if in.SSHAuthKind != nil {
			creds.AuthKind = *in.SSHAuthKind
		}
		if in.SSHPassword != nil {
			creds.Password = *in.SSHPassword
		}
		if in.SSHKey != nil {
			creds.Key = *in.SSHKey
		}
		if err := h.uc.SetSSHCredentials(r.Context(), id, creds); err != nil {
			writeErr(w, err)
			return
		}
	}

	// 重读一次，回最新 DTO（包括可能侧边变化的 ssh_* 字段）。如果
	// 本次 PATCH 一个字段都没改，也走这里——响应一个 200 而不是 204，
	// 让 SPA 的“保存后刷新列表”逻辑能收到一个明确的成功信号。
	updated, err := h.uc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, devToItem(updated))
}

func (h *Handler) updateRoles(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req updateRolesReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}
	if err := h.uc.UpdateRoles(r.Context(), id, req.Roles); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	// hard=true → physical delete via Repo.HardDelete (Unscoped);
	// default → soft delete (recoverable via POST /v1/devices/{id}/restore).
	hard := parseBoolQuery(r.URL.Query().Get("hard"))
	if err := h.uc.Delete(r.Context(), id, hard); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// restore revives a soft-deleted device row. Admin only — same
// justification as the monitor panel restore: re-introducing a row the
// operator had explicitly retired is not something any-authed callers
// should be able to trigger.
func (h *Handler) restore(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := h.uc.Restore(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	// Re-read so the SPA sees the post-restore row (deleted_at cleared).
	updated, err := h.uc.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, devToItem(updated))
}

func (h *Handler) listEdges(w http.ResponseWriter, r *http.Request) {
	if _, ok := tenantctx.From(r.Context()); !ok {
		writeErr(w, errs.ErrUnauthorized)
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	links := h.uc.Links()
	if links == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []edgeLinkRow{}})
		return
	}
	rows, err := links.ListEdgesForDevice(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]edgeLinkRow, 0, len(rows))
	for _, l := range rows {
		out = append(out, edgeLinkRow{
			EdgeID:    l.EdgeID,
			DeviceID:  l.DeviceID,
			Type:      relType(l.Type),
			CreatedAt: l.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// --- helpers ---

func devToItem(d *devicemodel.Device) deviceItem {
	return deviceItem{
		ID:              d.ID,
		Name:            d.Name,
		Description:     d.Description,
		Hostname:        d.Hostname,
		OS:              d.OS,
		OSVersion:       d.OSVersion,
		Arch:            d.Arch,
		KernelVersion:   d.KernelVersion,
		IPAddress:       d.IPAddress,
		CPUCount:        d.CPUCount,
		MemTotalBytes:   d.MemTotalBytes,
		DiskTotalBytes:  d.DiskTotalBytes,
		CPUUsagePct:     d.CPUUsagePct,
		MemUsagePct:     d.MemUsagePct,
		DiskUsagePct:    d.DiskUsagePct,
		Roles:           devicemodel.DecodeRoles(d.Roles),
		Online:          d.Online,
		LastSeenAt:      d.LastSeenAt,
		Reachable:       d.Reachable,
		LastReachableAt: d.LastReachableAt,
		DeletedAt:       d.DeletedAt,
		CreatedAt:       d.CreatedAt,
		SSHHost:         d.SSHHost,
		SSHPort:         d.SSHPort,
		SSHUser:         d.SSHUser,
		SSHAuthKind:     d.SSHAuthKind,
		SSHPassword:     d.SSHPassword,
		SSHKey:          d.SSHKey,
		NodeID:          d.NodeID,
	}
}

func relType(t devicemodel.EdgeDeviceRelationType) string {
	switch t {
	case devicemodel.EdgeDeviceRelationHost:
		return "host"
	case devicemodel.EdgeDeviceRelationDiscovered:
		return "discovered"
	default:
		return "unknown"
	}
}

func parseID(r *http.Request) (uint64, error) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, errors.Join(errs.ErrInvalid, err)
	}
	return id, nil
}

// parseBoolQuery accepts the same wire-truthy spellings the rest of
// the handler set uses (true/1/yes vs false/0/no). Empty / unknown
// defaults to false so query-string omission stays the historical
// "off" path.
func parseBoolQuery(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeErr(w http.ResponseWriter, err error) {
	status := errs.HTTPStatus(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{
		Error: err.Error(),
		Code:  errCode(err),
	})
}

func errCode(err error) string {
	switch {
	case errors.Is(err, errs.ErrNotFound):
		return "not-found"
	case errors.Is(err, errs.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, errs.ErrForbidden):
		return "forbidden"
	case errors.Is(err, errs.ErrInvalid):
		return "invalid"
	case errors.Is(err, errs.ErrNotWiredYet):
		return "not-wired-yet"
	default:
		return "internal"
	}
}

// (compile-time guard) ensure context import is kept lint-clean.
var _ = context.Background
