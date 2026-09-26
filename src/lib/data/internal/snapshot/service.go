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
func (s *Service) SnapshotGet(req facade.SnapshotGetRequest) (facade.SnapshotGetResponse, error) {
	if req.SessionID == "" {
		return facade.SnapshotGetResponse{}, errors.New("snapshot: session_id required")
	}
	db, err := resolvePrjUsr(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	snap, ok, err := Get(db, req.SessionID)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	if !ok {
		return facade.SnapshotGetResponse{}, nil
	}
	return facade.SnapshotGetResponse{Found: true, Snapshot: data.SnapshotToFacade(req.SessionID, snap)}, nil
}

// SnapshotSet 写回会话快照（会话由 Snapshot.SessionID 指定）。
func (s *Service) SnapshotSet(req facade.SnapshotSetRequest) (facade.SnapshotSetResponse, error) {
	if req.Snapshot.SessionID == "" {
		return facade.SnapshotSetResponse{}, errors.New("snapshot: session_id required")
	}
	db, err := resolvePrjUsr(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	if err := Set(db, req.Snapshot.SessionID, data.SnapshotFromFacade(req.Snapshot)); err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	return facade.SnapshotSetResponse{OK: true}, nil
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
