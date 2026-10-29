package session

import (
	"errors"
	"sort"
	"strings"

	"github.com/chonkpilot/chonkpilot-data"
	"github.com/chonkpilot/chonkpilot-data/facade"
	"github.com/chonkpilot/chonkpilot-data/internal/kernel"
)

// errAskScanHit 是「AskID 精确回查命中首条 → 结束扫描」的内部哨兵（仅本文件使用，不外泄）。
var errAskScanHit = errors.New("session: ask scan hit")

// AskPending 回查 ask 行（MOD-1 M6b）：来源 = `messages` 中带 `ask_id` 的行，**不建 ask 表**。
//
// 未答 ask 的 `ask_id` 在发起时随 running 行落库（turnCtx.persistToolRunning）；
// 本查询供启动扫描（PendingOnly=true）与失效回执（AskID 精确回查，含已终态）复用。
// 命中口径只看 `ask_id` 非空，故「只命中未答 ask」由 PendingOnly 过滤保证。
//
// 一树一库（SESSION-COMP-1 P3-4 残留回填）：`messages` 在对话库内——
//   - `DialogID` 非空 → 只查该对话库；
//   - 空 → **跨对话库扫描**（启动扫描 / 失效回执本就没有单一对话；61 §0.6 已登记该例外）。
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
	items := make([]facade.AskPendingItem, 0, 4)
	// S-2（DATA-OPT-1 P3）：跨对话库枚举复用 forEachDialogDB（与 SessionSubChats* 同一实现：
	// 打不开的库跳过、错误上抛口径不变）。
	if _, err := s.forEachDialogDB(prj, func(dialogID string, db *data.DB) (bool, error) {
		got, err := askPendingIn(db, req, dialogID)
		if err != nil {
			return false, err
		}
		items = append(items, got...)
		return false, nil
	}); err != nil {
		return facade.AskPendingResponse{}, err
	}
	sortAskItems(items)
	return facade.AskPendingResponse{OK: true, Items: items}, nil
}

// askPendingIn 在单个库内回查 ask 行（`dialogID` 非空 → 结果项回带，供调用方续期到该对话）。
//
// F-1（DATA-OPT-1 P4）：原实现 `Query{Where:{}}`（无索引可用）会先把 messages **整表物化**为
// `[]Record` 再逐行过滤——本查询只关心 `ask_id` 非空的行。改 `ForEach` 单事务**流式**扫描：
// 桶序遍历与 Query 无 OrderBy 的全表扫描**同序**（结果顺序逐项不变），且不物化整表；
// `AskID` 精确回查（失效回执热路径）**命中即收**（该 ask 只有一行，故与改前结果等价）。
func askPendingIn(db *data.DB, req facade.AskPendingRequest, dialogID string) ([]facade.AskPendingItem, error) {
	items := make([]facade.AskPendingItem, 0, 4)
	err := db.Table("messages").ForEach(func(_ string, r data.Record) error {
		askID := kernel.Sval(r["ask_id"])
		if askID == "" {
			return nil
		}
		if req.AskID != "" && askID != req.AskID {
			return nil // 等价原 `Where{"ask_id": req.AskID}` 等值过滤
		}
		status := kernel.Sval(r["tool_call_status"])
		if req.AskID == "" && req.PendingOnly && !askNonTerminal(status) {
			return nil
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
		if req.AskID != "" {
			return errAskScanHit // 精确回查：命中首条即收（ask_id 每问唯一，无第二条）
		}
		return nil
	})
	if err != nil && !errors.Is(err, errAskScanHit) {
		return nil, err
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
