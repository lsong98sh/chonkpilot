// 子会话驱动：委派子代理（task_llm）与群聊干活子会话的**完整 LLM 轮次**。
//
// runChildTurn 是「派生并等待」的入口：子轮次与主轮次同构——复用 turnCtx 状态机
// （含工具循环/gateway 调用/tasks 节点/llm-receive 流/llm-complete 终态），只是
// session/turn 不同且 payload 带 parents（父会话链）。子轮次是 server 内部启动的
// 完整 LLM 会话：**可使用全部工具**（hot 集；`agent` 指定时按其定义收窄），可再派生
// 下一级子轮次（多层委派）。
//
// 2026-11-04（task_llm 全能力委派）：拆出 `startChildTurn` —— 起轮后立即返回句柄，
// 调用方可选择等待（默认）或先返回 running（detach），后续经 task_status / tool_result
// 按 turn id 取结果。委派子轮次使用**独立 ctx**（不随父 turn 收尾而取消），取消走
// `task_stop`/`tool_stop` → 关闭子轮次（见 closeDelegatedTurn）。
package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// childTurn 是一个已启动、尚未（或已经）收尾的子轮次句柄。
type childTurn struct {
	tc      *turnCtx
	Session string
	Turn    string
}

// startChildTurn 建会话 / 轮次、登记运行态、广播 turn-start 并投递首个用户消息。
// 调用方负责等待收尾（`(*childTurn).wait`）或直接返回（detach）。
//
// parentCtx 是子轮次 ctx 的父（取消沿链下传）。**task_llm 委派传 context.Background()**：
// 子代理是独立作业，父轮次正常收尾不应把它一起取消；取消由 task_stop 显式下发。
func (s *Server) startChildTurn(parentCtx context.Context, parentReq StartReq, subSession, prompt, agent string) (*childTurn, error) {
	store := newSessionStore(s.bus, parentReq.InstanceID)
	if err := store.EnsureSessionWithParent(subSession, parentReq.Session); err != nil {
		return nil, err
	}
	subTurn := newID()
	if err := store.EnsureTurn(subTurn, subSession); err != nil {
		return nil, err
	}
	subReq := parentReq
	subReq.ReqID = ""
	subReq.Session = subSession
	subReq.Turn = subTurn
	subReq.Parents = append(append([]string{}, parentReq.Parents...), parentReq.Session)
	subReq.Agent = agent

	tc := newTurnCtx(parentCtx, s, subReq)
	// 子轮次同样登记 busy/turns：转后台工具完成（onGatewayTaskDone）按 turn 定位续轮。
	// 键 = instKey(instance, id)（缺口 5：子轮次与主轮次同 instance 桶）。
	s.mu.Lock()
	s.busy[instKey(subReq.InstanceID, subSession)] = subTurn
	s.registerTurnLocked(instKey(subReq.InstanceID, subTurn), tc)
	s.mu.Unlock()
	// 子轮次开始广播（turn-start 带 parents 链；前端会话树插入 + history 过滤主会话）
	s.emitTurnStart(subReq)

	tc.FeedText(prompt)
	return &childTurn{tc: tc, Session: subSession, Turn: subTurn}, nil
}

// wait 等子轮次收尾（自然完成 / parentCtx 取消），返回 (结果文本, 错误)。
// parentCtx 判据与 runChildTurn 同口径：用**发起方** ctx 而**不是** tc.ctx ——
// 子轮次自身收尾（Close → cancel）同样会取消 tc.ctx，用它判定会把正常完成误判成取消。
func (h *childTurn) wait(parentCtx context.Context) (string, error) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	select {
	case <-h.tc.Done():
	case <-parentCtx.Done():
	}
	// 父侧取消分支：不再等子步跑满（子轮次已随 ctx 终止，其 LLM 请求被中止）。
	// 竞态守卫：若此刻子轮次恰好也已收尾（done 已关），按完成结果返回。
	select {
	case <-h.tc.Done():
		if h.tc.turnErr != nil {
			return "", h.tc.turnErr
		}
		return h.tc.result, nil
	default:
	}
	// 级联 Close（幂等）：唤醒仍阻塞在输入等待的子 loop → 其 defer 释放 busy/turns 登记。
	// onLLMCancel 只 Close「被请求的那个 turn」，子 turn 不在其列 —— 不补这一步则取消后
	// 子轮次登记/goroutine 常驻（子 loop 在 ctx 取消时是静默 return 回 select，不自行 Close）。
	h.tc.Close()
	if err := h.tc.ctx.Err(); err != nil {
		return "", fmt.Errorf("子轮次被取消: %w", err)
	}
	return "", nil
}

// runChildTurn 递归驱动子会话轮次并等待结果（群聊干活子会话等既有调用方的语义不变）。
func (s *Server) runChildTurn(parentCtx context.Context, parentReq StartReq, subSession, prompt, agent string) (string, string, error) {
	h, err := s.startChildTurn(parentCtx, parentReq, subSession, prompt, agent)
	if err != nil {
		return "", "", err
	}
	text, werr := h.wait(parentCtx)
	return text, h.Turn, werr
}

// closeDelegatedTurn 关闭一个仍在跑的子轮次（task_stop / tool_stop / 父轮次取消），
// 并**递归**关闭由它派生的下一级委派（多层子代理）。幂等：未命中 / 已收尾 → false。
// 终态由 complete 的唯一终态闸门保证只落一次。
func (s *Server) closeDelegatedTurn(instanceID, turnID string) bool {
	children := s.takeDelegated(instanceID, turnID)
	s.mu.Lock()
	tc := s.lookupTurnLocked(instanceID, turnID)
	s.mu.Unlock()
	for _, h := range children {
		s.closeChildTurn(h)
	}
	if tc == nil {
		return len(children) > 0
	}
	// 与 onLLMCancel 同口径：先取消该轮在飞工具，再落 interrupted 终态 + 关轮。
	s.tasks.cancelByTurn(tc.req.InstanceID, tc.req.Turn)
	s.complete(tc, "interrupted", "interrupted", "")
	tc.Close()
	return true
}

// closeChildTurn 关闭一个已知句柄的子轮次（不做 lookup；句柄在登记表内即视为有效）。
func (s *Server) closeChildTurn(h *childTurn) {
	if h == nil || h.tc == nil {
		return
	}
	s.tasks.cancelByTurn(h.tc.req.InstanceID, h.Turn)
	s.complete(h.tc, "interrupted", "interrupted", "")
	h.tc.Close()
}

// registerDelegated 登记「父轮次 → 委派子轮次」（用于父取消 / task-stop 级联）。
func (s *Server) registerDelegated(instanceID, parentTurn string, h *childTurn) {
	if h == nil || h.Turn == "" {
		return
	}
	k := instKey(instanceID, parentTurn)
	s.mu.Lock()
	if s.delegated == nil {
		s.delegated = map[string][]*childTurn{}
	}
	s.delegated[k] = append(s.delegated[k], h)
	s.mu.Unlock()
}

// forgetDelegated 摘除一个已收尾的子轮次登记（键不存在 / 未命中 → no-op）。
func (s *Server) forgetDelegated(instanceID, parentTurn, childTurn string) {
	k := instKey(instanceID, parentTurn)
	s.mu.Lock()
	list := s.delegated[k]
	for i, h := range list {
		if h != nil && h.Turn == childTurn {
			s.delegated[k] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(s.delegated[k]) == 0 {
		delete(s.delegated, k)
	}
	s.mu.Unlock()
}

// takeDelegated 取出并清空某轮次的全部委派子轮次（级联关闭用）。
func (s *Server) takeDelegated(instanceID, parentTurn string) []*childTurn {
	k := instKey(instanceID, parentTurn)
	s.mu.Lock()
	out := s.delegated[k]
	delete(s.delegated, k)
	s.mu.Unlock()
	return out
}

// closeDelegatedChildren 关闭某轮次派生的全部委派子轮次（父轮次取消 / task-stop 级联；
// 递归由 closeChildTurn → closeDelegatedTurn 的下一层登记自然完成）。
func (s *Server) closeDelegatedChildren(instanceID, parentTurn string) {
	for _, h := range s.takeDelegated(instanceID, parentTurn) {
		s.closeChildTurn(h)
	}
}

// partialTurnText 读子轮次当前已产出的 assistant 文本（流式增量回填同一主键 → 最后一条
// 即累计文本）。running 状态下供 task_status / tool_result 附带「已产出」内容。
func (s *Server) partialTurnText(instanceID, turnID string) string {
	if turnID == "" {
		return ""
	}
	msgs := newSessionStore(s.bus, instanceID).LoadMessages(turnID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && strings.TrimSpace(msgs[i].Content) != "" {
			return msgs[i].Content
		}
	}
	return ""
}

// delegatedWait 等待子轮次收尾或超过 wait（wait <= 0 = 一直等到收尾）。
// 返回 (是否已收尾)。定时器仅在该路径分配（热路径无额外开销）。
func delegatedWait(h *childTurn, wait time.Duration) bool {
	select {
	case <-h.tc.Done():
		return true
	default:
	}
	if wait <= 0 {
		<-h.tc.Done()
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-h.tc.Done():
		return true
	case <-timer.C:
		return false
	}
}