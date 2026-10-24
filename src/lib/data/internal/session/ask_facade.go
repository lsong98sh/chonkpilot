package session

import (
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
	"github.com/chonkpilot/chonkpilot-data/internal/sessionstore"
)

// AskPending 回查 ask 行（MOD-1 M6b）：来源 = `messages` 中带 `ask_id` 的行，**不建 ask 表**。
//
// 未答 ask 的 `ask_id` 在发起时随 running 行落库（turnCtx.persistToolRunning）；
// 本查询供启动扫描（PendingOnly=true）与失效回执（AskID 精确回查，含已终态）复用。
// 命中口径只看 `ask_id` 非空，故「只命中未答 ask」由 PendingOnly 过滤保证。
//
// 一树一库（SESSION-COMP-1 P3-4 残留回填）：`messages` 在对话库内——
//   - `DialogID` 非空 → 只查该对话库；
//   - 空 + `session.db.shard` 生效 → **跨对话库扫描**（启动扫描 / 失效回执本就没有单一对话）；
//   - 空 + 旧布局 → prjusr。
func (s *Service) AskPending(req facade.AskPendingRequest) (facade.AskPendingResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.AskPendingResponse{}, err
	}
	if req.DialogID != "" {
		db, release, err := data.OpenExistingDialog(prj.Path(), req.DialogID)
		if err != nil {
			return facade.AskPendingResponse{}, err
		}
		defer release()
		items, err := askPendingIn(db, req, req.DialogID)
		return facade.AskPendingResponse{OK: true, Items: items}, err
	}
	if s.ShardOn(req.InstanceID, req.Scope) {
		files, err := sessionstore.ListDialogFiles(prj.Path())
		if err != nil {
			return facade.AskPendingResponse{}, err
		}
		items := make([]facade.AskPendingItem, 0, 4)
		for _, f := range files {
			dialogID, ok := data.DialogIDFromFile(f)
			if !ok {
				continue
			}
			db, release, err := data.OpenExistingDialog(prj.Path(), dialogID)
			if err != nil {
				continue // 打不开（锁/损坏）→ 跳过，不阻断其它对话
			}
			got, err := askPendingIn(db, req, dialogID)
			release()
			if err != nil {
				return facade.AskPendingResponse{}, err
			}
			items = append(items, got...)
		}
		sortAskItems(items)
		return facade.AskPendingResponse{OK: true, Items: items}, nil
	}
	items, err := askPendingIn(prj, req, "")
	return facade.AskPendingResponse{OK: true, Items: items}, err
}

// askPendingIn 在单个库内回查 ask 行（`dialogID` 非空 → 结果项回带，供调用方续期到该对话）。
func askPendingIn(db *data.DB, req facade.AskPendingRequest, dialogID string) ([]facade.AskPendingItem, error) {
	where := data.Record{}
	if req.AskID != "" {
		where["ask_id"] = req.AskID
	}
	recs, _, err := db.Table("messages").Query(data.Query{Where: where})
	if err != nil {
		return nil, err
	}
	items := make([]facade.AskPendingItem, 0, 4)
	for _, r := range recs {
		askID := kernel.Sval(r["ask_id"])
		if askID == "" {
			continue
		}
		status := kernel.Sval(r["tool_call_status"])
		if req.AskID == "" && req.PendingOnly && !askNonTerminal(status) {
			continue
		}
		items = append(items, facade.AskPendingItem{
			AskID:      askID,
			DialogID:   dialogID,
			SessionID:  kernel.Sval(r["session_id"]),
			TurnID:     kernel.Sval(r["turn_id"]),
			ToolCallID: kernel.Sval(r["tool_call_id"]),
			Status:     status,
			Question:   askQuestionOf(kernel.Sval(r["content"])),
			CreatedAt:  kernel.Sval(r["created_at"]),
		})
	}
	return items, nil
}

// sortAskItems 按 created_at 升序、ask_id 次键排序（跨对话库扫描结果稳定）。
func sortAskItems(items []facade.AskPendingItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt < items[j].CreatedAt
		}
		return items[i].AskID < items[j].AskID
	})
}

// askNonTerminal 非终态判定（与 tool_call_status 生命周期一致）。
func askNonTerminal(status string) bool {
	switch status {
	case "", "pending", "running", "provisional":
		return true
	}
	return false
}

// askQuestionOf 从工具消息 content 的 `call.arguments.question` 取提问文本（尽力而为）。
func askQuestionOf(content string) string {
	tc, ok := kernel.ParseToolContent(content)
	if !ok || tc.Call == nil {
		return ""
	}
	args, ok := tc.Call.Arguments.(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(kernel.Sval(args["question"]))
}
