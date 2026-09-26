// 子会话驱动：llm 型工具（llm_run DSL 作业）的每个 LLM 步骤复用一个独立子
// session/turn（runChildTurn），完成回填父轮次 + 广播 tasks.done（节点 kind=llm）。
// runChildTurn 是子轮唯一入口：子轮次与主轮次同构——复用 turnCtx 状态机
// （含工具循环/gateway 调用/tasks 节点/llm-receive 流/llm-complete 终态），只是
// session/turn 不同且 payload 带 parents（父会话链）。子轮次是 server 内部启动的
// 完整 LLM 会话：llm_run 的每个 LLM 委派步骤即一个子轮次（同样可使用工具，C13 场景）。
// 广播子轮次启动（turn-start + parents）→ UI 会话树插入；终态（llm-complete）后
// 父侧 goroutine 读取 result 回填父轮次。
package server

import (
	"context"
	"fmt"
)

// runChildTurn 递归驱动子会话轮次：子轮次与主轮次同构——复用 turnCtx 状态机
// （含工具循环/gateway 调用/tasks 节点/llm-receive 流/llm-complete 终态），只是
// session/turn 不同且 payload 带 parents（父会话链）。子轮次是 server 内部启动的
// 完整 LLM 会话：llm_run 的每个 LLM 委派步骤即一个子轮次（同样可使用工具，C13 场景）。
// agent = LLM 指令第一段的委派对象标识（newTurnCtx 注入为子轮次 persona，见 turn.go），
// 返回 (结果文本, 子 turn id, error)。
//
// parentCtx = **发起本子轮次的那个 turn 的 ctx**（G-18 gapA：此前子轮次 ctx 恒由
// context.Background() 派生 → 父轮次被取消时子轮次一无所知、继续跑满全程）。
func (s *Server) runChildTurn(parentCtx context.Context, parentReq StartReq, subSession, prompt, agent string) (string, string, error) {
	store := newSessionStore(s.bus, parentReq.InstanceID)
	if err := store.EnsureSessionWithParent(subSession, parentReq.Session); err != nil {
		return "", "", err
	}
	subTurn := newID()
	if err := store.EnsureTurn(subTurn, subSession); err != nil {
		return "", "", err
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
	s.turns[instKey(subReq.InstanceID, subTurn)] = tc
	s.mu.Unlock()
	// 子轮次开始广播（turn-start 带 parents 链；前端会话树插入 + history 过滤主会话）
	s.emitTurnStart(subReq)

	tc.FeedText(prompt)
	// 等子轮次收尾：自然完成（done 关闭）或**父侧取消**（parentCtx 取消）。
	// G-18 gapB：此前只等 done → 父侧取消后仍阻塞到子步全部跑满（实测 1.5s vs 250ms）。
	// 判据用 parentCtx（发起它的那个 turn 的 ctx）而**不是** tc.ctx：子轮次自身收尾
	// （Close → cancel）同样会取消 tc.ctx，用它判定会把正常完成误判成取消。
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	select {
	case <-tc.done:
	case <-parentCtx.Done():
	}
	// 父侧取消分支：不再等子步跑满（子轮次已随 ctx 终止，其 LLM 请求被中止）。
	// 竞态守卫：若此刻子轮次恰好也已收尾（done 已关），按完成结果返回。
	select {
	case <-tc.done:
		if tc.turnErr != nil {
			return "", subTurn, tc.turnErr
		}
		return tc.result, subTurn, nil
	default:
	}
	// 级联 Close（幂等）：唤醒仍阻塞在输入等待的子 loop → 其 defer 释放 busy/turns 登记。
	// onLLMCancel 只 Close「被请求的那个 turn」，子 turn 不在其列 —— 不补这一步则取消后
	// 子轮次登记/goroutine 常驻（子 loop 在 ctx 取消时是静默 return 回 select，不自行 Close）。
	tc.Close()
	if err := tc.ctx.Err(); err != nil {
		return "", subTurn, fmt.Errorf("子轮次被取消: %w", err)
	}
	return "", subTurn, nil
}
