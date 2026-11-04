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
//   - 空 → **跨对话库扫描**（启动扫描 / 失效回执本就没有单一对话；61 §0.6 已登记该例外）。
//
// P4-2 批次 D（2026-10-31）：单库扫描实现体切到会话组件（`componentAskPending`）；
// 错误口径不变（显式 dialog 打不开 → 上抛；跨库枚举时打不开 → 跳过）。
func (s *Service) AskPending(req facade.AskPendingRequest) (facade.AskPendingResponse, error) {
	prj, err := s.PrjUsrFor(req.InstanceID, req.Scope)
	if err != nil {
		return facade.AskPendingResponse{}, err
	}
	if req.DialogID != "" {
		items, err := s.componentAskPending(prj, req.DialogID, req)
		if err != nil {
			return facade.AskPendingResponse{}, err
		}
		return facade.AskPendingResponse{OK: true, Items: items}, nil
	}
	items := make([]facade.AskPendingItem, 0, 4)
	// 跨对话库枚举（打不开的库跳过、不阻断其它对话，与既有 forEachDialogDB 同口径）。
	files, err := sessionstore.ListDialogFiles(prj.Path())
	if err != nil {
		return facade.AskPendingResponse{}, err
	}
	for _, f := range files {
		dialogID, ok := data.DialogIDFromFile(f)
		if !ok {
			continue
		}
		got, err := s.componentAskPending(prj, dialogID, req)
		if err != nil {
			continue // 打不开 / 读失败 → 跳过（原 forEachDialogDB 的 continue 语义）
		}
		items = append(items, got...)
	}
	sortAskItems(items)
	return facade.AskPendingResponse{OK: true, Items: items}, nil
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
