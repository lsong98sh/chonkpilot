// INBOX 门面实现（prjusr 级 `inbox_items` 表）。
//
// 设计见 `docs/agent-chat/中途插话设计方案.md` §6.2.3：
//   - 所有发送先落本表（status=pending），实际插入 / 下一轮开始时才写 `messages`；
//   - type = insert（立即插入）| next_turn（等下一轮）；
//   - 消费 = 落 messages + 置 consumed（调用方保证原子 / 幂等键去重）。
package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

const inboxTable = "inbox_items"

// inboxSourceLLM / inboxSourceMobile 是 `source` 的合法取值：
//   - `llm`（NEXTRUN-1 §3.4：模型经 `next_run` 登记）；
//   - `mobile`（2026-11-01：H5 / 远程接入发送，永不插话、只排队 next_turn）；
//   - 空 = 桌面用户输入。
const (
	inboxSourceLLM    = "llm"
	inboxSourceMobile = "mobile"
)

// 收件箱的存储语义（幂等键扫描 / pending 上限 / 消费事务 / 终态 TTL）实现唯一 = 会话组件
// （`component.Inbox*` 与 `component.CleanupStale`，P4-2 批次 A/B，2026-10-31）——
// 本包只保留输入校验与 DTO 整形，不再保留并行实现。

// InboxAppend 写入一条待处理发送。
func (s *Service) InboxAppend(req facade.InboxAppendRequest) (facade.InboxAppendResponse, error) {
	if req.SessionID == "" {
		return facade.InboxAppendResponse{}, errors.New("inbox: session_id required")
	}
	if req.Type != "insert" && req.Type != "next_turn" {
		return facade.InboxAppendResponse{}, errors.New("inbox: type must be insert|next_turn")
	}
	if strings.TrimSpace(req.Content) == "" {
		return facade.InboxAppendResponse{}, errors.New("inbox: content required")
	}
	// 来源标识（NEXTRUN-1 §3.4 / 2026-11-01 H5）：`llm` / `mobile`；空 = 桌面用户输入。
	if req.Source != "" && req.Source != inboxSourceLLM && req.Source != inboxSourceMobile {
		return facade.InboxAppendResponse{}, errors.New("inbox: source must be llm|mobile")
	}
	if req.DialogID == "" {
		return facade.InboxAppendResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxAppendResponse{}, err
	}
	return s.componentInboxAppend(prj, req)
}

// InboxList 按会话读 inbox 项（status 缺省 = pending，按 created_at 升序）。
func (s *Service) InboxList(req facade.InboxListRequest) (facade.InboxListResponse, error) {
	if req.SessionID == "" {
		return facade.InboxListResponse{}, errors.New("inbox: session_id required")
	}
	if req.DialogID == "" {
		return facade.InboxListResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxListResponse{}, err
	}
	return s.componentInboxList(prj, req)
}

// InboxCommit 原子消费一条 inbox（MOD-1 M6）：**同一 bbolt 写事务**内落 messages + 置 consumed。
//
//   - 消息主键确定性（`kernel.InboxMessageKey`）→ 重试幂等：消息行已存在则只补置 consumed；
//   - 已 discarded → 明确失败（不消费）；
//   - SessionID 空 → 取 inbox 行的 session_id；
//   - 消息行额外写 `inbox_id`（仅本路径）——排障 / 回查用，普通消息行形状不变。
func (s *Service) InboxCommit(req facade.InboxCommitRequest) (facade.InboxCommitResponse, error) {
	if req.InboxID == "" {
		return facade.InboxCommitResponse{}, errors.New("inbox: id required")
	}
	if req.TurnID == "" {
		return facade.InboxCommitResponse{}, errors.New("inbox: turn_id required")
	}
	if req.DialogID == "" {
		return facade.InboxCommitResponse{}, kernel.ErrDialogRequired
	}
	m := data.MessageFromFacade(req.Message)
	// INPUTVAL-1 P0-4：与 MessageAppend 同口径（角色枚举 + 正文长度）。
	if !messageRoleValid(m.Role) {
		return facade.InboxCommitResponse{}, fmt.Errorf("inbox: invalid message role %q", m.Role)
	}
	if err := validateMessageContent(m.Role, m.Content); err != nil {
		return facade.InboxCommitResponse{}, err
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxCommitResponse{}, err
	}
	// 消息行由门面构造（形状与 MessageAppend 共用 buildMessageRecord）；组件负责同事务落库 + 置 consumed。
	rec := buildMessageRecord(req.TurnID, req.SessionID, time.Now().UTC().Format(kernel.RFC3339FixedNano),
		m, req.ToolCallStatus, req.Brief, "", "", nil)
	return s.componentInboxCommit(prj, req, rec)
}

// inboxStatusValid 判定 inbox 状态是否属既定三态（pending / consumed / discarded）。
func inboxStatusValid(st string) bool {
	return st == "pending" || st == "consumed" || st == "discarded"
}

// InboxUpdate 更新 inbox 项（空字段 = 不改）。
func (s *Service) InboxUpdate(req facade.InboxUpdateRequest) (facade.InboxUpdateResponse, error) {
	if req.ID == "" {
		return facade.InboxUpdateResponse{}, errors.New("inbox: id required")
	}
	// 枚举白名单（INPUTVAL-1 P0-4）：写坏值会污染消费侧相位判定（pending/consumed/discarded、
	// insert/next_turn），故写侧拒绝而非静默落库。
	if req.Status != "" && !inboxStatusValid(req.Status) {
		return facade.InboxUpdateResponse{}, fmt.Errorf("inbox: invalid status %q", req.Status)
	}
	if req.Type != "" && req.Type != "insert" && req.Type != "next_turn" {
		return facade.InboxUpdateResponse{}, fmt.Errorf("inbox: invalid type %q", req.Type)
	}
	if req.DialogID == "" {
		return facade.InboxUpdateResponse{}, kernel.ErrDialogRequired
	}
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.InboxUpdateResponse{}, err
	}
	return s.componentInboxUpdate(prj, req)
}
