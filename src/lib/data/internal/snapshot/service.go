// snapshot 域的门面实现（`chonkpilot-data/facade` 的 API）。
//
// 门面 = 翻译层（23 §7）：本文件负责「实例 → 数据根」解析 + DTO ↔ 存储模型的翻译，
// 再落到 store.go 的表访问器。调用方只认领域标识（instance_id / session_id）。
package snapshot

import (
	"errors"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// Service 是 snapshot 域门面实现（inline 绑定直接持有本类型）。
type Service struct{ base *kernel.Base }

// 编译期断言：实现完整 snapshot 域门面面（缺方法即编译不过）。
// 注：门面的**域面**（facade.SnapshotAPI / facade.ConfigAPI）由各自实现分别满足，
// 合成（facade.API）在绑定侧（facade/inline）——见 41 G-34。
var _ facade.SnapshotAPI = (*Service)(nil)

// New 构造 snapshot 域门面实现（base 提供与其它域同源的实例/显式 scope 数据根解析；
// 缺省自建空 Base —— 仅测试/独立使用，解析回落 data 绑定表，与改造前口径一致）。
func New(bases ...*kernel.Base) *Service {
	var base *kernel.Base
	if len(bases) > 0 {
		base = bases[0]
	}
	if base == nil {
		base = kernel.NewBase(nil, kernel.Options{})
	}
	return &Service{base: base}
}

// SnapshotGet 读会话快照（无快照 → Found=false，不是错误）。
//
// 一树一库：`DialogID` 必填 → **会话组件**（读不建库）；缺 → `dialog_required`。
func (s *Service) SnapshotGet(req facade.SnapshotGetRequest) (facade.SnapshotGetResponse, error) {
	if req.SessionID == "" {
		return facade.SnapshotGetResponse{}, errors.New("snapshot: session_id required")
	}
	if req.DialogID == "" {
		return facade.SnapshotGetResponse{}, kernel.ErrDialogRequired
	}
	catalog, err := s.base.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	snap, ok, err := GetInDialog(catalog.Path(), req.DialogID, req.SessionID)
	if err != nil {
		return facade.SnapshotGetResponse{}, err
	}
	if !ok {
		return facade.SnapshotGetResponse{}, nil
	}
	return facade.SnapshotGetResponse{Found: true, Snapshot: data.SnapshotToFacade(req.SessionID, snap)}, nil
}

// SnapshotSet 写回会话快照（会话由 Snapshot.SessionID 指定；布局口径同 SnapshotGet）。
func (s *Service) SnapshotSet(req facade.SnapshotSetRequest) (facade.SnapshotSetResponse, error) {
	if req.Snapshot.SessionID == "" {
		return facade.SnapshotSetResponse{}, errors.New("snapshot: session_id required")
	}
	if req.DialogID == "" {
		return facade.SnapshotSetResponse{}, kernel.ErrDialogRequired
	}
	catalog, err := s.base.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	if err := SetInDialog(catalog.Path(), req.DialogID, req.Snapshot.SessionID, data.SnapshotFromFacade(req.Snapshot)); err != nil {
		return facade.SnapshotSetResponse{}, err
	}
	return facade.SnapshotSetResponse{OK: true}, nil
}
