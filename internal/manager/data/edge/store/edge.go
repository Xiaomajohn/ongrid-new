package store

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	biz "github.com/ongridio/ongrid/internal/manager/biz/edge"
	model "github.com/ongridio/ongrid/internal/manager/model/edge"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Repo is the GORM-backed biz/edge.Repo.
type Repo struct {
	db *gorm.DB
}

// NewRepo constructs the repo around an opened *gorm.DB.
// Exposed as a biz.Repo via provider.go's NewRepo factory for wiring.
func NewRepo(db *gorm.DB) *Repo { return &Repo{db: db} }

// compile-time interface check.
var _ biz.Repo = (*Repo)(nil)

// Create inserts e. Any insert-side failure (unique violation, etc.) is
// returned unwrapped so the caller can errors.Is on gorm.ErrDuplicatedKey
// if desired.
func (r *Repo) Create(ctx context.Context, e *model.Edge) error {
	if e == nil {
		return errs.ErrInvalid
	}
	return r.db.WithContext(ctx).Create(e).Error
}

// GetByID returns the edge by primary key. Soft-deleted rows are scoped out
// by gorm's default DeletedAt handling.
func (r *Repo) GetByID(ctx context.Context, id uint64) (*model.Edge, error) {
	var e model.Edge
	if err := r.db.WithContext(ctx).First(&e, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

// GetByAccessKey returns the edge matching the access_key_id column. Does
// NOT return soft-deleted rows (gorm default).
func (r *Repo) GetByAccessKey(ctx context.Context, accessKey string) (*model.Edge, error) {
	var e model.Edge
	if err := r.db.WithContext(ctx).Where("access_key_id = ?", accessKey).First(&e).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

// GetByName returns the edge matching the human-readable name column. Does
// NOT return soft-deleted rows (gorm default).
func (r *Repo) GetByName(ctx context.Context, name string) (*model.Edge, error) {
	var e model.Edge
	if err := r.db.WithContext(ctx).Where("name = ?", name).First(&e).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

// List returns edges matching f. Sorted by id DESC so the most recently
// registered edges appear first. Soft-deleted rows excluded.
//
// Post-split (May 2026): role filtering moved to the device repo —
// callers that filter by role should query devices and resolve back to
// edges through the edge_devices junction.
//
// Hostname / IP filters look up against the linked Device row (the
// host where the agent runs). The JOIN is an INNER JOIN — edges with
// no host device linked yet are filtered out by these predicates,
// which matches operator intent: "show me the edge on hostname X" only
// makes sense once the agent has registered.
func (r *Repo) List(ctx context.Context, f biz.ListFilter) ([]*model.Edge, error) {
	tx := r.db.WithContext(ctx).Model(&model.Edge{})
	// IncludeDeleted 跳过 GORM 软删除过滤，让已删除的 edge 也能被查询到。
	// 同时排除“确认删除”（purge_marker != 0）的行：这些行日志页面
	// 查询不到，历史数据页也不展示。
	if f.IncludeDeleted {
		tx = tx.Unscoped().Where("edges.purge_marker = 0")
	}
	if f.DeviceID != nil {
		tx = tx.Joins("JOIN edge_devices ed ON ed.edge_id = edges.id AND ed.delete_marker = 0").
			Where("ed.device_id = ?", *f.DeviceID)
	}
	if f.Status != "" {
		tx = tx.Where("status = ?", f.Status)
	}
	if f.Name != nil {
		// 精确匹配 edge.name（不再是 LIKE 模糊搜索）。nil = 不过滤。
		tx = tx.Where("edges.name = ?", *f.Name)
	}
	if f.Hostname != nil || f.IP != nil {
		// INNER JOIN devices：edge 未绑定 host device 的行会被这两个
		// 谓词过滤掉，这与操作员的预期一致（没有 host 就不能按 host 过滤）。
		tx = tx.Joins("JOIN devices d ON d.id = edges.device_id AND d.delete_marker = 0")
		if f.Hostname != nil {
			tx = tx.Where("d.hostname = ?", *f.Hostname)
		}
		if f.IP != nil {
			tx = tx.Where("d.ip_address = ?", *f.IP)
		}
	}
	if f.CreatedBy != nil {
		tx = tx.Where("created_by = ?", *f.CreatedBy)
	}
	if f.Limit > 0 {
		tx = tx.Limit(f.Limit)
	}
	if f.Offset > 0 {
		tx = tx.Offset(f.Offset)
	}
	var out []*model.Edge
	if err := tx.Order("id DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSecretHash replaces secret_key_hash.
func (r *Repo) UpdateSecretHash(ctx context.Context, id uint64, hash string) error {
	res := r.db.WithContext(ctx).Model(&model.Edge{}).Where("id = ?", id).Update("secret_key_hash", hash)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// UpdateStatus sets status + last_seen_at together.
func (r *Repo) UpdateStatus(ctx context.Context, id uint64, status string, lastSeen time.Time) error {
	res := r.db.WithContext(ctx).Model(&model.Edge{}).Where("id = ?", id).Updates(map[string]any{
		"status":       status,
		"last_seen_at": lastSeen,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// UpdateName overwrites the operator-friendly display name. Used by
// edge.HandleRegister to back-fill blank names with the host's
// reported hostname on first tunnel handshake — admins who created
// the edge without a name see it auto-populate when the agent boots.
func (r *Repo) UpdateName(ctx context.Context, id uint64, name string) error {
	res := r.db.WithContext(ctx).Model(&model.Edge{}).Where("id = ?", id).Update("name", name)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// SetDeviceID links an edge row to a device row (post-split data model).
// Called by HandleRegister once the host's Device row has been
// upserted, so subsequent reads can join Device for host facts.
func (r *Repo) SetDeviceID(ctx context.Context, edgeID, deviceID uint64) error {
	res := r.db.WithContext(ctx).Model(&model.Edge{}).Where("id = ?", edgeID).Update("device_id", deviceID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// SetAgentVersion records the agent's self-reported binary version on
// register_edge. Caller filters empty values upstream so we don't blank
// the column when a buggy build reports nothing.
func (r *Repo) SetAgentVersion(ctx context.Context, id uint64, version string) error {
	res := r.db.WithContext(ctx).Model(&model.Edge{}).Where("id = ?", id).Update("agent_version", version)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// UpdateTaskName 写入 edge.task_name 任务标识。
//
// 用原始 SQL 列名引用而非 model.Edge.TaskName 字段——避免在 PR 合并
// 顺序上对 Agent1 同步落地的 struct 字段产生强耦合：当 Agent1 的
// schema PR 尚未合入时，本方法在运行期会因 column not found 而失败
// （不是编译期），便于在 wiring 检查时再补字段；已经合并的环境下，
// gorm 的 raw Update 走的是 column 名，与 struct 字段是否声明无关。
//
// empty taskName 由 caller 侧过滤（见 biz/edge/repo.go UpdateTaskName
// 注释）：这里直接照传，避免覆盖 SPA 上手工设置的非空值。
func (r *Repo) UpdateTaskName(ctx context.Context, id uint64, taskName string) error {
	res := r.db.WithContext(ctx).Model(&model.Edge{}).Where("id = ?", id).Update("task_name", taskName)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Delete soft-deletes an edge (gorm's DeletedAt). Subsequent Get/List hide
// the row.
func (r *Repo) Delete(ctx context.Context, id uint64) error {
	res := r.db.WithContext(ctx).Delete(&model.Edge{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// ConfirmDelete 确认删除（二次删除）：将 purge_marker 置为当前毫秒
// 时间戳。行不做物理删除；include_deleted=true 的列表查询会排除
// purge_marker != 0 的行，日志页面因此查询不到该任务。
// 使用列名直接写 SQL，绕开 GORM 软删除插件的自动 WHERE。
func (r *Repo) ConfirmDelete(ctx context.Context, id uint64) error {
	marker := time.Now().UTC().UnixMilli()
	res := r.db.WithContext(ctx).Exec(
		`UPDATE edges SET purge_marker = ? WHERE id = ? AND purge_marker = 0`, marker, id)
	if res.Error != nil {
		return res.Error
	}
	// RowsAffected==0 表示行不存在或已被确认删除；幂等处理，不报错。
	return nil
}

// Count returns the number of non-soft-deleted edges.
func (r *Repo) Count(ctx context.Context) (int64, error) {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.Edge{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}
