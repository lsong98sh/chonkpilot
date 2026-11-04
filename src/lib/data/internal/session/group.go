// group 域（**群聊落库两表**：参与者名单 + 大轮次投递对象）门面实现。
//
// 设计见 `docs/agent-chat/群聊会话设计方案.md` §5 · D44 / D3 / D26 / D32 / D33（SUBCHAT-3 阶段 E）。
// 落点 = **prjusr 库**（与会话/轮次/消息同层；`<usr 数据根>/data/<project-id>/chonkpilot.db`）。
//
// 两表的形状（bbolt 桶 + JSON 行；**主键首段恒为 `session_id`** → 按前缀 Seek 即该群聊的全部行）：
//
//	① `group_participants`  主键 `<session_id>\x00<agent>`
//	   行 {session_id, agent, role, seq, created_at, updated_at}
//	   —— 逻辑外键 session_id → `sessions.kind == group_chat`；`(session_id, agent)` 逻辑唯一。
//	② `group_round_targets` 主键 `<session_id>\x00<round 6 位零填充>`
//	   行 {session_id, round, agents[], created_at, updated_at}
//	   —— 每【大轮次】（= 群聊依次叫号的那一轮，**非**子会话内部轮次）一条。
//
// 为什么**分两表**（设计稿允许合并）：生命周期 / 写入频率 / 主键形态三者都不同（参与者名单低频、
// 一会话一份；投递对象每轮一条、高频追加）—— 详见 `facade/group.go` 文件头。
//
// **无 -refresh 广播**（名册/进度类表，同 `memory_extract`）：读方 = 群聊调度器，按其自身节奏读。
//
// P4-2 批次 E（2026-10-31）：主键形态与读写实现唯一 = 会话组件（`component.Group*`）；
// 本文件只保留门面方法（校验 + DTO 映射），不再保留并行实现。
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chonkpilot/chonkpilot-data/facade"
)

// groupStrList 取行内字符串数组列（落库恒为 JSON 数组；历史/异常形态为 JSON 文本 → 兼容解析）。
func groupStrList(v any) []string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return nil
		}
		var arr []string
		if json.Unmarshal([]byte(t), &arr) == nil {
			return arr
		}
		return []string{t}
	}
	return nil
}

// ── 门面方法（GroupAPI 实现）────────────────────────────────────────

// GroupParticipantsLoad 读某群聊的参与者名单（单聊/群聊共用会话域库；见 facade/group.go）。
func (s *Service) GroupParticipantsLoad(req facade.GroupParticipantsLoadRequest) (facade.GroupParticipantsLoadResponse, error) {
	if req.SessionID == "" {
		return facade.GroupParticipantsLoadResponse{}, errors.New("session: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.GroupParticipantsLoadResponse{}, err
	}
	list, err := s.componentGroupParticipantsLoad(prj, req.SessionID)
	if err != nil {
		return facade.GroupParticipantsLoadResponse{}, fmt.Errorf("load group participants: %v", err)
	}
	return facade.GroupParticipantsLoadResponse{List: list}, nil
}

// GroupParticipantsSave 整表替换某群聊的参与者名单（Seq 按 List 顺序赋值）。
func (s *Service) GroupParticipantsSave(req facade.GroupParticipantsSaveRequest) (facade.GroupParticipantsSaveResponse, error) {
	if req.SessionID == "" {
		return facade.GroupParticipantsSaveResponse{}, errors.New("session: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.GroupParticipantsSaveResponse{}, err
	}
	if err := s.componentGroupParticipantsSave(prj, req); err != nil {
		return facade.GroupParticipantsSaveResponse{}, err
	}
	return facade.GroupParticipantsSaveResponse{OK: true}, nil
}

// GroupRoundTargetsLoad 读某群聊的大轮次投递对象（Round 0/缺省 = 全部轮次）。
func (s *Service) GroupRoundTargetsLoad(req facade.GroupRoundTargetsLoadRequest) (facade.GroupRoundTargetsLoadResponse, error) {
	if req.SessionID == "" {
		return facade.GroupRoundTargetsLoadResponse{}, errors.New("session: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.GroupRoundTargetsLoadResponse{}, err
	}
	list, err := s.componentGroupRoundTargetsLoad(prj, req.SessionID, req.Round)
	if err != nil {
		return facade.GroupRoundTargetsLoadResponse{}, fmt.Errorf("load group round targets: %v", err)
	}
	return facade.GroupRoundTargetsLoadResponse{List: list}, nil
}

// GroupRoundTargetsSave 写某 `(群聊, 大轮次)` 的投递对象（幂等覆盖）。
func (s *Service) GroupRoundTargetsSave(req facade.GroupRoundTargetsSaveRequest) (facade.GroupRoundTargetsSaveResponse, error) {
	if req.SessionID == "" {
		return facade.GroupRoundTargetsSaveResponse{}, errors.New("session: session_id required")
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.GroupRoundTargetsSaveResponse{}, err
	}
	if err := s.componentGroupRoundTargetsSave(prj, req); err != nil {
		return facade.GroupRoundTargetsSaveResponse{}, err
	}
	return facade.GroupRoundTargetsSaveResponse{OK: true}, nil
}
