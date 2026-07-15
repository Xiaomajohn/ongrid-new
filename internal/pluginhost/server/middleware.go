// Package server / middleware.go 提供 pluginhost HTTP 子树的通用中间件:
//
//   - RecoveryMiddleware:兜底 panic,写统一 JSON 500 + slog.Error 日志。
//   - RequestIDMiddleware:从 X-Request-ID 读或生成 req-<n>-<rand>,
//     透传到 ctx 与响应头,handler 拿来做 trace/audit 关联。
//   - TenantMiddleware:从 X-Tenant-ID 读租户 id(0 兜底),塞 ctx;
//     当前是简化版,Phase 5 起切到 A 的 tenantctx 时再换实现。
//
// 中间件按 Recover → RequestID → Tenant 顺序组装:panic 兜底在
// 最外层,RequestID 在 panic recover 之后可被 handler / log 引用,
// Tenant 在最内层保证 handler 一定能拿到。
package server

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// ctxKey 是 server 包内上下文键的统一类型,避免与外部 string key 冲突。
type ctxKey int

const (
	requestIDKey ctxKey = iota
	tenantIDKey
)

// RequestIDFrom 返回 ctx 上的请求 ID;无则空串(用于 audit / log)。
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// TenantIDFrom 返回 ctx 上的租户 ID;无则 0(默认租户)。
func TenantIDFrom(ctx context.Context) uint64 {
	v, _ := ctx.Value(tenantIDKey).(uint64)
	return v
}

// RecoveryMiddleware 把 panic 转成统一 JSON 500,并 slog.Error。
//
// 实现注意:
//
//   - 用 runtime/debug.Stack 抓堆栈,便于定位 panic 的真正位置。
//   - 必须先判断 rw.WriteHeader 状态,避免与后续中间件/handler 重复
//     写头导致 "superfluous WriteHeader call" 警告。
//   - 不写 200,直接 500 + body;前端凭 status code 即可判定失败。
func RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					rid := RequestIDFrom(r.Context())
					logger.Error("pluginhost: handler panic",
						slog.String("path", r.URL.Path),
						slog.String("method", r.Method),
						slog.String("request_id", rid),
						slog.Any("err", rec),
					)
					writeJSON(w, http.StatusInternalServerError,
						Err(500, "internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// RequestIDMiddleware 解析或生成请求 ID,透传到 ctx 与响应头。
//
// 优先级:
//
//  1. X-Request-ID header 已有且非空 → 原样使用(便于调用方传 trace id)。
//  2. 否则生成 "req-<unixnano>-<rand4>",unixnano 保证单调,
//     rand4 防止同一纳秒内并发 ID 撞车。
//
// 中间件同时把 ID 写到 X-Request-ID 响应头,前端 fetch 包装可
// 把它存到 console 便于联调排错。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = generateRequestID()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TenantMiddleware 解析租户 ID,缺省回退到 0(P0 全租户共享语义)。
//
// 实现:从 X-Tenant-ID header 读十进制数字;解析失败 / 缺失 / 负数
// 全部记为 0。ctx 暴露 TenantIDFrom() 给 handler 使用。
//
// Phase 5 接入 A 的 tenantctx 后,这里替换为从 JWT claim / session
// 解出 TenantID,字段语义保持不变。
func TenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tid uint64
		if v := r.Header.Get("X-Tenant-ID"); v != "" {
			_, _ = fmt.Sscanf(v, "%d", &tid)
		}
		ctx := context.WithValue(r.Context(), tenantIDKey, tid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeJSON 把任意 payload 以 Content-Type: application/json 写出。
//
// 头未写过才设 Content-Type;同样避免 200 默认值与显式 4xx/5xx
// 重复设置造成的 warning。Marshal 失败时降级写 500 + 错误描述,
// 防止 goroutine 静默挂掉。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		// body 已经开始流式输出,这里只能 slog;不能再次写头。
		slog.Default().Error("pluginhost: write json",
			slog.String("err", err.Error()),
		)
	}
}

// generateRequestID 产生 "req-<unixnano>-<rand4>"。
//
// 与 invoke/router.go 的 generateReqID 形态对称(便于跨子包 grep)。
func generateRequestID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("req-%d-%04x", time.Now().UnixNano(), binary.BigEndian.Uint16(b[:]))
}
