// snapshot 域的门面实现（`chonkpilot-data/facade` 的 API）。
//
// 门面 = 翻译层（23 §7）：本文件负责「实例 → 数据根」解析 + DTO ↔ 存储模型的翻译，
// 再落到 store.go 的表访问器。调用方只认领域标识（instance_id / session_id）。
package snapshot

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
)

// Service 是 snapshot 域门面实现（inline 绑定直接持有本类型）。
type Service struct{}

// 编译期断言：实现完整 snapshot 域门面面（缺方法即编译不过）。
// 注：门面的**域面**（facade.SnapshotAPI / facade.ConfigAPI）由各自实现分别满足，
// 合成（facade.API）在绑定侧（facade/inline）——见 41 G-34。
var _ facade.SnapshotAPI = (*Service)(nil)

// New 构造 snapshot 域门面实现。
func New() *Service { return &Service{} }

// SnapshotGet 读会话快照（无快照 → Found=false，不是错误）。
//
// 布局选择（P3）：`DialogID` 非空 → 对话库（一树一库，**不创建**、无旧行迁移）；
// 空 → 旧布局（`sessions/<hex>/`，保留 `MigrateFromCatalog` 幂等搬运）。
func (s *Service) SnapshotGet(req facade.SnapshotGetRequest) (facade.SnapshotGetResponse, error) {
	if req.SessionID == "" {
		return facade.SnapshotGetResponse{}, errors.New("snapshot: session_id required")
	}
	catalog, err := resolvePrjUsr(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	sdb, release, err := openSnapshotDB(catalog, req.DialogID, req.SessionID)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	defer release()
	if req.DialogID == "" {
		// 旧布局：历史快照（内嵌在 prjusr.sessions 行）→ 会话库（幂等、非破坏）。
		if err := MigrateFromCatalog(catalog, sdb, req.SessionID); err != nil {
			return facade.SnapshotGetResponse{}, err
		}
	}
	snap, ok, err := Get(sdb, req.SessionID)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	if !ok {
		return facade.SnapshotGetResponse{}, nil
	}
	return facade.SnapshotGetResponse{Found: true, Snapshot: data.SnapshotToFacade(req.SessionID, snap)}, nil
}

// SnapshotSet 写回会话快照（会话由 Snapshot.SessionID 指定；布局选择同 SnapshotGet）。
func (s *Service) SnapshotSet(req facade.SnapshotSetRequest) (facade.SnapshotSetResponse, error) {
	if req.Snapshot.SessionID == "" {
		return facade.SnapshotSetResponse{}, errors.New("snapshot: session_id required")
	}
	catalog, err := resolvePrjUsr(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	sdb, release, err := openSnapshotDB(catalog, req.DialogID, req.Snapshot.SessionID)
	if err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	defer release()
	if req.DialogID != "" {
		if err := SetInDialog(sdb, req.Snapshot.SessionID, data.SnapshotFromFacade(req.Snapshot)); err != nil {
			return facade.SnapshotSetResponse{}, err
		}
	} else if err := Set(catalog, sdb, req.Snapshot.SessionID, data.SnapshotFromFacade(req.Snapshot)); err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	return facade.SnapshotSetResponse{OK: true}, nil
}

// openSnapshotDB 按 dialog_id 是否给出来选库（P3 路由；2G §3.3 不回退）。
func openSnapshotDB(catalog *data.DB, dialogID, sessionID string) (*data.DB, func(), error) {
	if dialogID != "" {
		return data.OpenExistingDialog(catalog.Path(), dialogID)
	}
	return data.OpenSession(catalog.Path(), sessionID)
}

// resolvePrjUsr 定位实例的 prjusr 主库（会话/快照所在层，12-数据层）：按数据组件的
// 实例绑定表解析；未登记且调用方带入数据根（Scope）→ 自登记后重试
// （与 org 实现 `plugin-compress.prjUsrDB` 的语义一致：分离形态凭事件载荷自给）。
func resolvePrjUsr(instanceID string, scope facade.Scope) (*data.DB, error) {
	db, err := data.PrjUsr(instanceID)
	if err == nil {
		return db, nil
	}
	if scope.Empty() {
		return nil, err
	}
	data.Register(instanceID, scope.WorkDir, scope.DataDir)
	return data.PrjUsr(instanceID)
}
