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
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// 群聊两表（prjusr bbolt 桶名；建桶清单见 data/migrate.go）。
const (
	groupParticipantsBucket = "group_participants"
	groupRoundTargetsBucket = "group_round_targets"
)

// groupKeySep 是复合主键分隔符（会话 id / agent 名均不含 \x00；与 memory_extract 同口径）。
const groupKeySep = "\x00"

// groupParticipantKey 参与者名单主键：`<session_id>\x00<agent>`（自然键 —— 同群聊同 agent 唯一）。
func groupParticipantKey(sessionID, agent string) string {
	return sessionID + groupKeySep + agent
}

// groupRoundTargetKey 大轮次投递对象主键：`<session_id>\x00<round 6 位零填充>`
// （零填充使**主键字典序 = 轮次数值序** → 前缀扫描天然按轮次升序，无需内存排序）。
func groupRoundTargetKey(sessionID string, round int) string {
	return fmt.Sprintf("%s%s%06d", sessionID, groupKeySep, round)
}

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

// ── ① 参与者名单 ────────────────────────────────────────────────────

// groupParticipantsLoad 读某群聊的参与者名单（按 Seq 升序；Seq 平局按 agent 字典序保确定性）。
func groupParticipantsLoad(db *data.DB, sessionID string) ([]facade.GroupParticipant, error) {
	out := make([]facade.GroupParticipant, 0)
	prefix := sessionID + groupKeySep
	err := db.Table(groupParticipantsBucket).ForEachPrefix(prefix, func(k string, rec data.Record) error {
		agent := kernel.Sval(rec["agent"])
		if agent == "" {
			agent = strings.TrimPrefix(k, prefix) // 列缺失 → 回落主键尾段
		}
		out = append(out, facade.GroupParticipant{
			SessionID: sessionID,
			Agent:     agent,
			Role:      kernel.Sval(rec["role"]),
			Seq:       intOfRecord(rec["seq"]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Agent < out[j].Agent
	})
	return out, nil
}

// groupParticipantsReplace **整表替换**某群聊的参与者名单（以 list 为准：未出现者删除、
// 出现者写入）。Seq 按 list 顺序重新赋值（1 基）—— 名单顺序 = 入参顺序（D3 固定顺序，
// 权威且无歧义；入参携带的 Seq 被忽略）。重复 Agent 按首现保留。
//
// 事务口径（与 prj `security-*` 的「逐条 upsert + 删除已移除条目」同口径）：逐个键 Upsert/Delete
// 各自独立事务（Bolt 单文件写事务串行）；本操作**幂等**，中途失败重跑即收敛，无"半个名单"可观测
// 语义（读方每次读到的是某一时刻的名单快照，而非跨事务的中间态拼装）。
func groupParticipantsReplace(db *data.DB, sessionID string, list []facade.GroupParticipant) error {
	t := db.Table(groupParticipantsBucket)
	seen := make(map[string]bool, len(list))
	roles := make(map[string]string, len(list))
	order := make([]string, 0, len(list))
	for _, p := range list {
		agent := strings.TrimSpace(p.Agent)
		if agent == "" || seen[agent] {
			continue
		}
		seen[agent] = true
		roles[agent] = p.Role
		order = append(order, agent)
	}
	// 先删不再出现的旧项（按前缀 Seek 只扫本会话）。
	prefix := sessionID + groupKeySep
	keys, err := t.ListPrefix(prefix)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(kernel.RFC3339FixedNano)
	for _, k := range keys {
		if seen[strings.TrimPrefix(k, prefix)] {
			continue
		}
		if err := t.Delete(k); err != nil && !errors.Is(err, data.ErrNotFound) {
			return err
		}
	}
	// 再写入目标名单（Seq = 列表位置 + 1）。
	for i, agent := range order {
		key := groupParticipantKey(sessionID, agent)
		createdAt := now
		// 已存在的行保留原 created_at（名册项「首次加入时间」不为重写所动）。
		var old data.Record
		if ok, err := t.Get(key, &old); err != nil {
			return err
		} else if ok {
			if v := kernel.Sval(old["created_at"]); v != "" {
				createdAt = v
			}
		}
		rec := data.Record{
			"session_id": sessionID, "agent": agent, "role": roles[agent],
			"seq": i + 1, "created_at": createdAt,
		}
		if err := t.Upsert(key, rec); err != nil {
			return err
		}
	}
	return nil
}

// ── ② 每大轮次的投递对象 ─────────────────────────────────────────────

// groupRoundTargetsLoad 读某群聊的大轮次投递对象：round > 0 → 单条（不存在 → 空数组）；
// round <= 0 → 全部轮次（前缀扫描，主键零填充保证**按轮次升序**）。
func groupRoundTargetsLoad(db *data.DB, sessionID string, round int) ([]facade.GroupRoundTarget, error) {
	t := db.Table(groupRoundTargetsBucket)
	if round > 0 {
		var rec data.Record
		ok, err := t.Get(groupRoundTargetKey(sessionID, round), &rec)
		if err != nil || !ok {
			return []facade.GroupRoundTarget{}, err
		}
		return []facade.GroupRoundTarget{groupRoundTargetOf(rec, sessionID, round)}, nil
	}
	out := make([]facade.GroupRoundTarget, 0)
	err := t.ForEachPrefix(sessionID+groupKeySep, func(_ string, rec data.Record) error {
		out = append(out, groupRoundTargetOf(rec, sessionID, 0))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// groupRoundTargetOf 由落库行构造门面行（列缺失时用调用方上下文兜底）。
func groupRoundTargetOf(rec data.Record, sessionID string, round int) facade.GroupRoundTarget {
	sid := kernel.Sval(rec["session_id"])
	if sid == "" {
		sid = sessionID
	}
	r := intOfRecord(rec["round"])
	if r == 0 {
		r = round
	}
	return facade.GroupRoundTarget{SessionID: sid, Round: r, Agents: groupStrList(rec["agents"])}
}

// groupRoundTargetSave 写某 `(群聊, 大轮次)` 的投递对象（幂等覆盖；updated_at 由存储侧自动写）。
func groupRoundTargetSave(db *data.DB, sessionID string, round int, agents []string) error {
	if round <= 0 {
		return errors.New("session: round must be >= 1")
	}
	if agents == nil {
		agents = []string{}
	}
	rec := data.Record{"session_id": sessionID, "round": round, "agents": agents}
	return db.Table(groupRoundTargetsBucket).Upsert(groupRoundTargetKey(sessionID, round), rec)
}

// intOfRecord 读行内整数列（存储值可能是数值或字符串；缺失/非法 → 0）。
func intOfRecord(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(t), "%d", &n); err == nil {
			return n
		}
	}
	return 0
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
	list, err := groupParticipantsLoad(prj, req.SessionID)
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
	if err := groupParticipantsReplace(prj, req.SessionID, req.List); err != nil {
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
	list, err := groupRoundTargetsLoad(prj, req.SessionID, req.Round)
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
	if err := groupRoundTargetSave(prj, req.SessionID, req.Round, req.Agents); err != nil {
		return facade.GroupRoundTargetsSaveResponse{}, err
	}
	return facade.GroupRoundTargetsSaveResponse{OK: true}, nil
}
