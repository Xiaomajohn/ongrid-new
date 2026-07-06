package edge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	devicebiz "github.com/ongridio/ongrid/internal/manager/biz/device"
	devicemodel "github.com/ongridio/ongrid/internal/manager/model/device"
)

// InstallEdgeIssuer is the concrete EdgeIssuer used by the installjob
// worker. It wraps Usecase.Create — which already mints an access_key
// + secret_key, argon2id-hashes the secret, and seeds default plugin
// configs — and exposes only the (access, secret) pair the worker
// pipes into install.sh.
//
// The install path uses an auto-generated name so the operator can
// tell which edge rows came from "click install" vs. "manually create
// edge in the UI":
//
//	auto-install-<deviceID>-<unix>
//
// Operators can rename later via the SPA's Edges page.
//
// task_name（用户在一键安装 SPA 填的任务名）独立于 edge.name：
// edge.name 保持 “auto-install-...” 便于运维区分“一键安装 vs 手工
// 创建”，task_name 走 edge.task_name 列。空 taskName 时不写列，保持
// agent 上报 register_edge 路径的语义（agent 可以用同名或留空）。
type InstallEdgeIssuer struct {
	uc    *Usecase
	links devicebiz.EdgeDeviceRepo // edge_devices junction; Link(edge,device,type=host)
	log   *slog.Logger
}

// NewInstallEdgeIssuer wires the concrete. uc must be non-nil; the
// install worker calls CreateEdgeForDevice before installer.Install so
// a nil usecase is a wiring bug, not a soft-failure. links may be nil
// in tests / partial wirings — CreateEdgeForDevice treats a nil links
// repo as a best-effort step that just logs WARN. log may be nil.
func NewInstallEdgeIssuer(uc *Usecase, links devicebiz.EdgeDeviceRepo, log *slog.Logger) *InstallEdgeIssuer {
	if log == nil {
		log = slog.Default()
	}
	return &InstallEdgeIssuer{
		uc:    uc,
		links: links,
		log:   log.With(slog.String("comp", "edge-install-issuer")),
	}
}

// BindEdgeFromAccessKey 是前端 createEdge + install-edge 双步流程下的
// 边缘绑定入口：worker 从 install_jobs.options_json.command 里取到
// --access-key=<frontend-created-edge.access_key_id>，反查 edge.ID，
// 按 deviceID 关联起来（SetDeviceID + edge_devices Link + task_name
// 写回）。
//
// 设计要点：
//   - 不创建新 edge —— 前端 POST /v1/edges 已经创好了，worker 再创建会
//     产生第 2 个 edge（凭证浪费 + 关联错乱）。
//   - GetByAccessKey 查不到时返回 error（不是 fallback 到 CreateEdge）：
//     误调用 / install_jobs 是手工手填的 cmd 时会走到这里，让 worker
//     按“bad install command”记一条事件，operator 能看到。
//   - SetDeviceID / Link 失败仅 warn：agent register 时 HandleRegister
//     会以 edge.DeviceID 优先重写关联（按 Get(device) 而非 fingerprint
//     upsert）。所以这里的丢失不会让 install 失败，只是 waitEdgeOnline
//     可能等不到关联 device。
func (i *InstallEdgeIssuer) BindEdgeFromAccessKey(ctx context.Context, deviceID uint64, accessKey, taskName string) error {
	if i == nil || i.uc == nil {
		return fmt.Errorf("edge: InstallEdgeIssuer not wired (nil uc)")
	}
	if deviceID == 0 {
		return fmt.Errorf("edge: BindEdgeFromAccessKey: deviceID is 0")
	}
	if accessKey == "" {
		return fmt.Errorf("edge: BindEdgeFromAccessKey: accessKey is empty")
	}

	// 1) 按 access_key 反查 edge.ID。
	ed, err := i.uc.repo.GetByAccessKey(ctx, accessKey)
	if err != nil {
		return fmt.Errorf("edge: BindEdgeFromAccessKey: GetByAccessKey(%q): %w", accessKey, err)
	}
	if ed == nil {
		return fmt.Errorf("edge: BindEdgeFromAccessKey: edge not found for access_key=%q", accessKey)
	}

	// 2) SetDeviceID + edge_devices Link —— 都写上后 HandleRegister
	// 拿到 edge.DeviceID 就能正确 get device，installjob.waitEdgeOnline
	// 也能查到这个 device 下的 edge 上线。
	if err := i.uc.repo.SetDeviceID(ctx, ed.ID, deviceID); err != nil {
		i.log.Warn("install-edge: SetDeviceID failed",
			slog.Uint64("edge_id", ed.ID),
			slog.Uint64("device_id", deviceID),
			slog.Any("err", err))
	}
	if i.links != nil {
		if err := i.links.Link(ctx, ed.ID, deviceID, devicemodel.EdgeDeviceRelationHost); err != nil {
			i.log.Warn("install-edge: edge_devices Link failed",
				slog.Uint64("edge_id", ed.ID),
				slog.Uint64("device_id", deviceID),
				slog.Any("err", err))
		}
	}

	// 3) task_name 透传。空 taskName 时跳过，避免覆盖 agent register
	// 阶段可能上报的值。
	if trimmed := strings.TrimSpace(taskName); trimmed != "" {
		if err := i.uc.repo.UpdateTaskName(ctx, ed.ID, trimmed); err != nil {
			i.log.Warn("install-edge: update task_name failed",
				slog.Uint64("edge_id", ed.ID),
				slog.Uint64("device_id", deviceID),
				slog.String("task_name", trimmed),
				slog.Any("err", err))
		}
	}

	i.log.Info("install-edge edge bound to device",
		slog.Uint64("device_id", deviceID),
		slog.Uint64("edge_id", ed.ID),
		slog.String("access_key", accessKey),
	)
	return nil
}