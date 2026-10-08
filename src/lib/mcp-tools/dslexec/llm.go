// llm.go — LLM 动作与并发派发。
//
// LLM 步骤本身不在此实现：执行器发 llm_call（stdout）→ gateway 经 MQ 执行子轮次 →
// stdin 回 llm_result → 本文件按 call id 派发到等待者。PARALLEL / 并发 LOOP 可能同时有多个
// LLM 动作在飞，故先用一个 stdin 读协程持续收 llm_result，再按 call 交给对应 channel。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chonkpilot/chonkpilot-lib/dsl"
)

// llmResult 是一次 LLM 调用的回执（text 与 error 互斥）。
type llmResult struct {
	text string
	err  string
}

// llmHub 承载一个作业内的并发 LLM 派发：写 llm_call、按 call 阻塞等待 llm_result。
type llmHub struct {
	job string
	lw  *lineWriter
	seq atomic.Int64 // 调用序号（session = <job>-<seq>，并发安全）

	mu    sync.Mutex
	chans map[string]chan llmResult
}

func newLLMHub(job string, lw *lineWriter) *llmHub {
	return &llmHub{job: job, lw: lw, chans: map[string]chan llmResult{}}
}

// readLoop 持续读 stdin 剩余行，把 llm_result 派发到对应等待者（run 首行已由 runProtocol 消费）。
// 读到 EOF 或出错即返回。
func (h *llmHub) readLoop(sc *bufio.Scanner) {
	for sc.Scan() {
		var m inMessage
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			h.lw.logf("warn", "忽略无法解析的协议行：%v", err)
			continue
		}
		switch m.T {
		case "llm_result":
			h.dispatch(m.Call, m.Text, m.Error)
		default:
			h.lw.logf("warn", "忽略未知下行消息类型 %q", m.T)
		}
	}
}

// dispatch 把 llm_result 交给等待者；无等待者（call 不匹配）记一条 log，不阻塞读协程。
func (h *llmHub) dispatch(call, text, errMsg string) {
	h.mu.Lock()
	ch := h.chans[call]
	h.mu.Unlock()
	if ch == nil {
		h.lw.logf("warn", "收到无等待者的 llm_result（call=%s）", call)
		return
	}
	select {
	case ch <- llmResult{text: text, err: errMsg}:
	default:
	}
}

// invoke 写一条 llm_call 并阻塞等待对应 llm_result；ctx 取消 → ErrExit（作业级终止）。
// onStart 在发出 llm_call 前调用（供进度跟踪发 step{running}；call id = 步骤子会话 id；可 nil）。
func (h *llmHub) invoke(ctx context.Context, agent, prompt, purpose string, onStart func(callID string)) (string, error) {
	n := h.seq.Add(1)
	id := fmt.Sprintf("%s-%d", h.job, n)
	ch := make(chan llmResult, 1)
	h.mu.Lock()
	h.chans[id] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.chans, id)
		h.mu.Unlock()
	}()

	if onStart != nil {
		onStart(id)
	}
	h.lw.sendLLMCall(llmCallMsg{Call: id, Agent: agent, Prompt: prompt, Purpose: purpose, Session: id})
	select {
	case <-ctx.Done():
		return "", dsl.ErrExit
	case r := <-ch:
		if r.err != "" {
			return "", errors.New(r.err)
		}
		return r.text, nil
	}
}

// llmAction 注册 LLM 动作（非 Raw：由 dsl 核心按引号 token 语法解析参数）。
// 语法：LLM "agent" "prompt" "目的" [=> 目标]——三参必填且非空（插值后仍为空 → 该步失败，
// 由引擎记入 Result.Errors）。每次调用经 tree 发 step{running→done/error/cancelled} 进度。
func llmAction(ctx context.Context, hub *llmHub, tree *jobTree) dsl.Action {
	return dsl.Action{
		Name: "LLM",
		Run: func(sc *dsl.Scope, args string) (string, error) {
			parts, err := dsl.SplitArgs(args)
			if err != nil {
				return "", err
			}
			if len(parts) != 3 {
				return "", errors.New(`LLM 需要 3 个参数：agent、提示词、目的（语法：LLM "agent" "prompt" "目的" [=> 目标]）`)
			}
			agent, _ := sc.Interp(parts[0])
			prompt, _ := sc.Interp(parts[1])
			purpose, _ := sc.Interp(parts[2])
			if strings.TrimSpace(agent) == "" {
				return "", errors.New(`LLM agent 不能为空（语法：LLM "agent" "prompt" "目的"）`)
			}
			if strings.TrimSpace(prompt) == "" {
				return "", errors.New(`LLM 提示词不能为空（语法：LLM "agent" "prompt" "目的"）`)
			}
			if strings.TrimSpace(purpose) == "" {
				return "", errors.New(`LLM 目的不能为空（语法：LLM "agent" "prompt" "目的"）`)
			}
			no := tree.nextNo()
			start := time.Now()
			text, ierr := hub.invoke(ctx, agent, prompt, purpose, func(callID string) {
				tree.stepStart(no, purpose, args, callID)
			})
			elapsed := time.Since(start).Milliseconds()
			if ierr != nil {
				status := "error"
				if errors.Is(ierr, dsl.ErrExit) {
					status = "cancelled"
				}
				tree.stepEnd(no, status, elapsed)
				return "", ierr
			}
			tree.stepEnd(no, "done", elapsed)
			return text, nil
		},
	}
}

