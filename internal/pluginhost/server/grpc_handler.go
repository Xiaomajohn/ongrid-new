// Package server / grpc_handler.go 是 pluginhost gRPC 入口的占位 stub。
//
// 背景(plan §5 + §13):
//
//   - HTTP 是 P1 阶段唯一对外接口,前端 / 第三方调用方全部走
//     /api/pluginhost/* 走 HTTP+JSON。
//   - gRPC 通道在 plan 里列出,但不在 P1 的交付范围内;它通常
//     走 pluginhost.proto(pluginhost 内部 proto,待 Phase 5
//     由主 agent 抽出 api/manager/pluginhost/v1/pluginhost.proto),
//     由 buf 生成 *_grpc.pb.go 后,在这里 implement gRPC service interface。
//   - 现在只暴露 GRPCHandler struct + NewGRPCHandler 工厂,便于
//     后续在 D2 wire-up 阶段就把 gRPC server 注册到 grpc.Server
//     上(避免到时候再回来补类型)。
//
// 设计约定:
//
//   - GRPCHandler 嵌入 *Handler,直接复用 HTTP handler 内的 biz 字段。
//   - 真正 gRPC method 实现留给 Phase 5:每个 method 内部再走一次
//     h.Installer / h.Lifecycle / h.Router,把 HTTP 行为转成
//     proto 消息即可,不重复实现业务逻辑。
package server

// GRPCHandler 是 gRPC service 的实现骨架;嵌入 *Handler 复用 biz 层。
//
// P1 阶段不导出任何 gRPC method;Phase 5 接入时新增方法形如:
//
//	func (g *GRPCHandler) InstallPlugin(ctx context.Context, req *pluginhostv1.InstallRequest) (*pluginhostv1.InstallResponse, error) {
//	    return installPluginGRPC(ctx, g.Handler, req)
//	}
type GRPCHandler struct {
	*Handler
}

// NewGRPCHandler 构造 gRPC handler 包装;h 必填,否则返回 nil(由
// 调用方兜底,避免运行时 nil deref)。
func NewGRPCHandler(h *Handler) *GRPCHandler {
	if h == nil {
		return nil
	}
	return &GRPCHandler{Handler: h}
}
