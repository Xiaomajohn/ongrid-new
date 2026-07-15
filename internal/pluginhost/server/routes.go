// Package server / routes.go 把所有 HTTP 路由集中注册到传入的 chi.Router。
//
// 路由命名规则(plan §13 信道 3:admin → B):
//
//   - 全部相对路径(不带 /api 前缀),前缀由 main.go wire-up 阶段用
//     chi.Mount("/api/pluginhost", mux) 挂载,本 mux 上只有相对路径。
//   - 全部 GET/POST/DELETE 动词,无 PATCH/PUT(manager server 风格)。
//
// 中间件顺序(从外到内):RequestID → Recovery → Tenant。
// 顺序含义:
//
//   - RequestID 最外层:panic recover 也能拿到 rid(写到日志里)。
//   - Recovery 第二层:吃 panic 后,handler 还能正常消费 ctx 上的
//     request id 与 tenant id(但要保证 Recovery 自己写响应时
//     不依赖后续中间件)。
//   - Tenant 最内层:handler 一定能拿到 tid(被 Recovery 包住时
//     也能用,因为 ctx.WithValue 是写时复制)。
package server

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Register 把 h 注册到 mux 上,挂载在 pluginhost 子树下。
//
// mux 必须是 chi.NewRouter() 出来的子 mux(由 main.go 提供);
// 调用方在 wire-up 时负责把它 Mount 到 /api/pluginhost 前缀。
//
// 行为:
//
//   - logger 为 nil 时,slog.Default() 兜底。
//   - h 为 nil 时不挂路由(防御性,避免 nil deref;实际由调用方保证)。
//   - 中间件只挂一次;若调用方已在外层挂过 RequestID,本函数仍会
//     重复挂(chi 不去重,行为由调用方保证)。
func Register(mux chi.Router, h *Handler, logger *slog.Logger) {
	if mux == nil {
		return
	}
	if h == nil {
		// 无 handler 时只挂中间件 + 404,避免暴露 nil deref。
		mux.Use(RequestIDMiddleware, RecoveryMiddleware(logger), TenantMiddleware)
		mux.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusNotFound, Err(404, "not found"))
		})
		return
	}
	mux.Use(RequestIDMiddleware, RecoveryMiddleware(logger), TenantMiddleware)

	mux.Route("/plugins", func(r chi.Router) {
		r.Get("/", h.ListPlugins)
		r.Post("/", h.InstallPlugin)
		r.Post("/upload", h.UploadTarball)

		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", h.GetPlugin)
			r.Delete("/", h.UninstallPlugin)
			r.Get("/capabilities", h.ListCapabilities)

			r.Route("/capabilities/{capName}", func(r chi.Router) {
				r.Post("/enable", h.EnableCapability)
				r.Post("/disable", h.DisableCapability)
				r.Post("/invoke", h.InvokeCapability)
			})

			// Asset Server:plugin 前端资源(/api/pluginhost/{id}/assets/{path...})
			// 走 http.ServeFile,由 sandbox 校验路径不越狱;只允许 GET
			r.Get("/assets/*", h.AssetPlugin)
		})
	})
}
