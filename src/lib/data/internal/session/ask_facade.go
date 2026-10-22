package session

import (
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// AskPending 回查 ask 行（MOD-1 M6b）：来源 = `messages` 中带 `ask_id` 的行，**不建 ask 表**。
//
// 未答 ask 的 `ask_id` 在发起时随 running 行落库（turnCtx.persistToolRunning）；
// 本查询供启动扫描（PendingOnly=true）与失效回执（AskID 精确回查，含已终态）复用。
// 命中口径只看 `ask_id` 非空，故「只命中未答 ask」由 PendingOnly 过滤保证。
func (s *Service) AskPending(req facade.AskPendingRequest) (facade.AskPendingResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.AskPendingResponse{}, err
	}
	where := data.Record{}
	if req.AskID != "" {
		where["ask_id"] = req.AskID
	}
	recs, _, err := prj.Table("messages").Query(data.Query{Where: where})
	if err != nil {
		return facade.AskPendingResponse{}, err
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
			SessionID:  kernel.Sval(r["session_id"]),
			TurnID:     kernel.Sval(r["turn_id"]),
			ToolCallID: kernel.Sval(r["tool_call_id"]),
			Status:     status,
			Question:   askQuestionOf(kernel.Sval(r["content"])),
			CreatedAt:  kernel.Sval(r["created_at"]),
		})
	}
	return facade.AskPendingResponse{OK: true, Items: items}, nil
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
