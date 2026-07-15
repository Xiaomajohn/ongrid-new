// Package server / asset.go 实现 plugin 前端资源的 HTTP 静态文件服务。
//
// 端点(Phase 5 E3 Asset Server):
//
//	GET /plugins/{id}/assets/*
//
// 用途:plugin 自身携带的前端产物(JS / CSS / HTML / 任意 binary)
// 通过 pluginhost server 暴露给 web 端,web 端用 pluginLoader 动态 import
// plugin 的 frontend bundle,把 plugin UI 嵌到 ongrid 主前端里。
//
// 安全约束:
//   - 只允许 GET 读;不暴露 DELETE / PUT / POST 写接口
//   - 路径必须在 plugin InstallPath 之内(sandbox.PathSafeUnderRoot)
//   - 拒绝绝对路径、符号链接越狱、.. 跳转
//   - 拒绝 .compiled-manifest.json / plugin.json 等元数据文件(避免泄露
//     内部结构)
//
// 性能:
//   - 走 http.ServeFile,由 net/http 自己做 Last-Modified / ETag 协商
//   - 不缓存中间件,加 ?v=<hash> 由 web 端 pluginLoader 决定
//   - 失败统一写 JSON 4xx/5xx(沿用 pluginhost/{code,message,data} 响应壳)
//
// 错误码:
//   - 400:plugin id 不合法 / path 越狱
//   - 404:plugin 不存在 / 文件不存在
//   - 403:试图访问元数据文件(.json / .compiled-manifest)
//   - 500:打开 / 读文件失败
package server

import (
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ongridio/ongrid/internal/pluginhost/sandbox"
)

// 拒绝暴露的元数据文件名前缀(防止 manifest / 配置 泄露到 web)。
const assetMetadataPrefix = "."

// AssetPlugin 暴露 plugin 前端资源的 handler。
//
// 入参 pluginID 为 URL 中的 {id} 段,assetPath 为 {*} 段。
//
// 流程:
//  1. parseIDParam 解析 pluginID(uint64 主键)
//  2. Reg.LookupByID 拿 instance;不存在 → 404
//  3. 拼接 InstallPath + assetPath(用 path.Join,不是 filepath.Join,
//     避免 windows \ 干扰 URL 语义)
//  4. sandbox.PathSafeUnderRoot 校验不越狱 → 失败 400
//  5. http.ServeFile 流式输出
//
// 注意:assetPath 由 chi {*} 捕获,会带前导 "/",path.Join 会自然吃掉;
// 这里不再 TrimPrefix,直接 path.Join 即可。
func (h *Handler) AssetPlugin(w http.ResponseWriter, r *http.Request) {
	if h.Reg == nil {
		writeJSON(w, http.StatusInternalServerError, Err(500, "registry not wired"))
		return
	}
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	inst, ok := h.Reg.LookupByID(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, Err(404, "plugin not found"))
		return
	}
	if inst.InstallPath == "" {
		writeJSON(w, http.StatusInternalServerError,
			Err(500, "plugin install path empty; cannot serve assets"))
		return
	}

	// assetPath 形如 "/js/main.js" 或 "/index.html";chi {*} 始终带前导 /
	assetPath := chi.URLParam(r, "*")
	if assetPath == "" || assetPath == "/" {
		// 根路径走 manifest.Frontend.Entry 缺省文件
		assetPath = "/index.html"
	}
	// 去掉前导 /,统一以相对路径处理
	assetPath = strings.TrimPrefix(assetPath, "/")

	// 拒绝以 . 开头的元数据文件(.compiled-manifest.json / .git / .env 等)
	if isMetadataPath(assetPath) {
		writeJSON(w, http.StatusForbidden,
			Err(403, "metadata files are not served as plugin assets"))
		return
	}

	// 拼出物理路径(用 path.Join,不调 filepath.Join,避免 Windows
	// \ 与 / 互转带来的语义偏移;Linux 上 path.Join 等同于 filepath.Join)
	fullPath := path.Join(inst.InstallPath, assetPath)

	// 越狱校验:fullPath 必须落在 inst.InstallPath 之内
	if !sandbox.PathSafeUnderRoot(fullPath, inst.InstallPath) {
		writeJSON(w, http.StatusBadRequest,
			Err(400, "asset path escapes plugin install directory"))
		return
	}

	// http.ServeFile 自动处理 304 / Range / Content-Type / Last-Modified
	// 头部;找不到文件自动返 404。文件名嗅探:assetPath 末尾段名 → mime
	// type 映射。
	w.Header().Set("X-Plugin-Asset", "true")
	w.Header().Set("X-Plugin-PackID", inst.PackID)
	http.ServeFile(w, r, fullPath)
}

// isMetadataPath 判定 path 是否为拒绝暴露的元数据文件。
//
// 规则:
//
//   - 以 "." 开头(隐藏文件:compiled-manifest / .env / .git 等)
//   - 文件名 == "plugin.json"(原始 manifest,manifest_data 已经在
//     CompiledManifest 中派生,不再向 web 端直接暴露源文件)
func isMetadataPath(p string) bool {
	if p == "" {
		return true
	}
	// 取路径最后一段(base name)
	base := path.Base(p)
	if base == "" {
		return true
	}
	// 隐藏文件
	if strings.HasPrefix(base, assetMetadataPrefix) {
		return true
	}
	// plugin.json 源文件
	if base == "plugin.json" {
		return true
	}
	return false
}
