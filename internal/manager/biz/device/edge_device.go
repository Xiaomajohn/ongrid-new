package device

import (
	"context"

	model "github.com/ongridio/ongrid/internal/manager/model/device"
)

// EdgeDeviceRepo is the persistence contract for the edge_devices M:N
// junction table. Implemented in internal/manager/data/device/store.
//
// Communication paths the manager wires through this:
//
//	Push  (metric/log/trace from edge):
//	  edge.tunnel session ──→ manager
//	  manager: edge_id from session ──→ LookupHostDevice(edge_id) ──→ device_id
//	  manager: write Prom samples / Loki streams / Tempo spans labelled by device_id
//
//	Pull  (manager wants to run a command on device X):
//	  manager: device_id=X ──→ LookupEdgeForDevice(device_id, type=host) ──→ edge_id
//	  manager: edge_id ──→ frontier session ──→ tunnel call
type EdgeDeviceRepo interface {
	// Link upserts a (edge_id, device_id, type) row. Idempotent: a second
	// call with the same triple is a no-op.
	Link(ctx context.Context, edgeID, deviceID uint64, t model.EdgeDeviceRelationType) error

	// Unlink removes the (edge_id, device_id, type) row. Returns nil if
	// no row matched (idempotent on the delete side too).
	Unlink(ctx context.Context, edgeID, deviceID uint64, t model.EdgeDeviceRelationType) error

	// LookupHostDevice resolves the host device_id for an edge. Used on
	// the push path: every metric/log/trace coming from the edge tunnel
	// is labelled with the host device_id. Returns ErrNotFound when the
	// edge has no Type=Host junction yet (race during register).
	LookupHostDevice(ctx context.Context, edgeID uint64) (uint64, error)

	// LookupEdgeForDevice resolves the edge_id that owns this device for
	// the given relationship type. Used on the pull path: when a tool
	// wants to run a tunnel RPC against a device, it needs the edge_id
	// to address the geminio session. Type=Host is the common case.
	LookupEdgeForDevice(ctx context.Context, deviceID uint64, t model.EdgeDeviceRelationType) (uint64, error)

	// ListDevicesForEdge enumerates every device this edge has a
	// junction row to (any type). Useful for the edge detail page's
	// "this edge sees N devices" panel.
	ListDevicesForEdge(ctx context.Context, edgeID uint64) ([]*model.EdgeDevice, error)

	// ListEdgesForDevice enumerates every edge that has a junction row
	// to this device (any type). Useful for the device detail page's
	// "this device is seen by N edges" panel.
	ListEdgesForDevice(ctx context.Context, deviceID uint64) ([]*model.EdgeDevice, error)

	// ListEdgesForDevices 批量拉取多台 device 关联的 edge 精简行（不含
	// access_key_id / secret_key_hash 等敏感字段）。用于 GET /v1/devices
	// 列表接口在响应里内嵌每台 device 的 edges 摘要，让 SPA 的 Logs 页面
	// 设备 / 任务下拉能一次拿到 task_name 等字段，省掉 N+1 反查。
	// 返回 map 缺省语义：未关联 edge 的 device 不会出现在 map 里。
	// deviceIDs 为空时直接返回空 map，不打 DB。
	ListEdgesForDevices(ctx context.Context, deviceIDs []uint64) (map[uint64][]EdgeMini, error)
}
