// component_group_bridge.go —— P4-2 批次 E（2026-10-31）：群聊两表（参与者名单 / 大轮次投递对象）的组件调用桥。
//
// 两表在目录库（prjusr）；存储语义（整表替换 / 幂等覆盖 / 前缀读）归组件，门面只做 DTO 映射与错误文案。
package session

import (
	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/lsong98sh/chonkpilot-agent/session/component"
)

// componentGroupParticipantsLoad 是 `groupParticipantsLoad` 的组件版。
func (s *Service) componentGroupParticipantsLoad(prj *data.DB, sessionID string) ([]facade.GroupParticipant, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return nil, err
	}
	list, err := comp.GroupParticipantsLoad(component.GroupParticipantsLoadCmd{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	out := make([]facade.GroupParticipant, 0, len(list))
	for _, p := range list {
		out = append(out, facade.GroupParticipant{
			SessionID: p.SessionID, Agent: p.Agent, Role: p.Role, Seq: p.Seq,
		})
	}
	return out, nil
}

// componentGroupParticipantsSave 是 `groupParticipantsReplace` 的组件版。
func (s *Service) componentGroupParticipantsSave(prj *data.DB, req facade.GroupParticipantsSaveRequest) error {
	comp, err := componentBridge(prj)
	if err != nil {
		return err
	}
	list := make([]component.GroupParticipant, 0, len(req.List))
	for _, p := range req.List {
		list = append(list, component.GroupParticipant{
			SessionID: req.SessionID, Agent: p.Agent, Role: p.Role, Seq: p.Seq,
		})
	}
	return comp.GroupParticipantsSave(component.GroupParticipantsSaveCmd{SessionID: req.SessionID, List: list})
}

// componentGroupRoundTargetsLoad 是 `groupRoundTargetsLoad` 的组件版。
func (s *Service) componentGroupRoundTargetsLoad(prj *data.DB, sessionID string, round int) ([]facade.GroupRoundTarget, error) {
	comp, err := componentBridge(prj)
	if err != nil {
		return nil, err
	}
	list, err := comp.GroupRoundTargetsLoad(component.GroupRoundTargetsLoadCmd{SessionID: sessionID, Round: round})
	if err != nil {
		return nil, err
	}
	out := make([]facade.GroupRoundTarget, 0, len(list))
	for _, t := range list {
		out = append(out, facade.GroupRoundTarget{SessionID: t.SessionID, Round: t.Round, Agents: t.Agents})
	}
	return out, nil
}

// componentGroupRoundTargetsSave 是 `groupRoundTargetSave` 的组件版。
func (s *Service) componentGroupRoundTargetsSave(prj *data.DB, req facade.GroupRoundTargetsSaveRequest) error {
	comp, err := componentBridge(prj)
	if err != nil {
		return err
	}
	return comp.GroupRoundTargetsSave(component.GroupRoundTargetsSaveCmd{
		SessionID: req.SessionID, Round: req.Round, Agents: req.Agents,
	})
}
