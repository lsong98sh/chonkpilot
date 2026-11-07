// 运行态 instance 隔离（2026-09-19，实例隔离第一批缺口 5）：
// server 侧运行态 map（turns / busy / continuePending / asks）以 **instKey(instance, id)** 为键
// —— 同进程内多 instance（gui 服务端 exe / browser）各自一份，互不可见、互不取消。
//
// 兼容口径：instKey 在 instance 为空时**原样返回 id**（未带 instance_id 的旧调用方 → 键与
// 隔离前逐字节等价）；查询入口在 instance 为空时回退「全桶扫描」（= 旧语义）；单 instance
// 进程（desktop）行为不变。
package server

import (
	"strings"

	"github.com/lsong98sh/chonkpilot-agent/session/runner"
)

// instKey 组合运行态键：`<instance_id>\x00<id>`（instance 为空 → 原键，兼容旧语义）。
// TURNRUN-1 批次 5 起键与注册表归 `chonkpilot-session/runner`，本函数仅为既有调用点保留适配名。
// instance_id 与 id（turn/session/ask）都不会含 NUL → 用作分隔符无歧义。
func instKey(instanceID, id string) string { return runner.Key(instanceID, id) }

// splitInstKey 拆运行态键（[instKey] 的逆）：`<instance>\x00<id>` → (instance, id)；
// 无 NUL（旧调用方 / instance 为空）→ ("", key)。仅做首 NUL 切分（非二进制内容判定，
// 不适用 textx.IsBinary）；用 strings.Cut 表达"逆拼接"。
func splitInstKey(key string) (instanceID, id string) {
	if instance, id, ok := strings.Cut(key, "\x00"); ok {
		return instance, id
	}
	return "", key
}

// lookupTurnSessionLocked 取指定 instance 的运行 turn（接口形态；**须持 s.mu**）：
//   - instance 非空 → 只在该 instance 的桶内命中（跨 instance 不可见）；
//   - instance 空（旧调用方未带 instance_id）→ 先按原键，再全桶扫描（同旧语义）。
func (s *Server) lookupTurnSessionLocked(instanceID, turnID string) turnSession {
	if turnID == "" {
		return nil
	}
	if sess := s.turns[instKey(instanceID, turnID)]; sess != nil {
		return sess
	}
	if instanceID != "" {
		return nil
	}
	for _, sess := range s.turns {
		if sess.TurnID() == turnID {
			return sess
		}
	}
	return nil
}

// lookupTurnSessionByKeyLocked 按键直取运行 turn（接口形态；**须持 s.mu**；不做跨桶回退）。
func (s *Server) lookupTurnSessionByKeyLocked(key string) turnSession {
	return s.turns[key]
}

// lookupTurnLocked 取指定 instance 的运行 turn（**须持 s.mu**；旧 *turnCtx 语义不变）：
// 内部经 lookupTurnSessionLocked 定位后断言为 *turnCtx；非 *turnCtx（后续 engineTurn）→
// nil + 留痕（该分支在现行代码路径不可达，仅防御）。
func (s *Server) lookupTurnLocked(instanceID, turnID string) *turnCtx {
	sess := s.lookupTurnSessionLocked(instanceID, turnID)
	if sess == nil {
		return nil
	}
	tc, ok := sess.(*turnCtx)
	if !ok {
		logf("[chonkpilot-server] lookupTurnLocked: turn %s 不是 *turnCtx（%T），按未命中处理\n", turnID, sess)
		return nil
	}
	return tc
}

// turnCtxByKeyLocked 按键直取 *turnCtx（**须持 s.mu**；不做跨桶回退）：仅用于仍需内部字段的
// 旧路径（recoverTurnCtx / taskTurnFor）。
func (s *Server) turnCtxByKeyLocked(key string) *turnCtx {
	sess := s.lookupTurnSessionByKeyLocked(key)
	if sess == nil {
		return nil
	}
	tc, ok := sess.(*turnCtx)
	if !ok {
		logf("[chonkpilot-server] turnCtxByKeyLocked: %s 不是 *turnCtx（%T），按未命中处理\n", key, sess)
		return nil
	}
	return tc
}

// registerTurnLocked 登记运行 turn（**须持 s.mu**）：容器只要求 turnSession ——
// 实例退出 / Stop 等生命周期路径只用身份访问器与 Close()，不依赖 turnCtx 内部字段。
func (s *Server) registerTurnLocked(key string, sess turnSession) {
	s.turns[key] = sess
}

// turnBySessionSessionLocked 按 session 找运行 turn 的接口句柄（**须持 s.mu**）：instance 非空 →
// 限定该 instance；空 → 全表（同旧语义）。
func (s *Server) turnBySessionSessionLocked(instanceID, session string) turnSession {
	if session == "" {
		return nil
	}
	for _, sess := range s.turns {
		if sess.SessionID() != session {
			continue
		}
		if instanceID != "" && sess.InstanceID() != instanceID {
			continue
		}
		return sess
	}
	return nil
}

// turnBySessionLocked 按 session 找运行 turn（**须持 s.mu**）：旧 *turnCtx 语义不变
// （内部断言；非 *turnCtx → nil + 留痕）。
func (s *Server) turnBySessionLocked(instanceID, session string) *turnCtx {
	sess := s.turnBySessionSessionLocked(instanceID, session)
	if sess == nil {
		return nil
	}
	tc, ok := sess.(*turnCtx)
	if !ok {
		logf("[chonkpilot-server] turnBySessionLocked: session %s 不是 *turnCtx（%T），按未命中处理\n", session, sess)
		return nil
	}
	return tc
}

// stopAskTimerLocked 停掉显式超时定时器（**须持 s.mu**；取走/清理登记时防泄漏）。
func stopAskTimerLocked(w *askWaiter) {
	if w != nil && w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

// takeAskLocked 取走并注销 ask 等待登记（**须持 s.mu**；返回 (等待项, 是否命中)）。
// instance 非空 → 先按该 instance 的桶，再按 `deliverInstance` 兜底，最后按 ask_id 全局兜底；
// 空 → 先按原键，再全桶回退（ask id 全局唯一，兼容未带 instance_id 的旧调用方）。
//
// **定向投递兜底**（SUBCHAT-3 阶段 F-2）：`ask_user` 卡片按会话所在窗口投递 → 应答来自**投递目标
// 窗口**（其实例 ≠ 登记实例）→ 按 askID 命中「`deliverInstance` == 该实例」的登记（ask id 全局唯一；
// 仅在按 instKey 未命中时兜底，不放松既有按 instance 精确命中语义）。
//
// **Ask 中心兜底**（2026-11-04）：Ask 中心是独立待办窗口（不归属任何会话），按 ask_id 作答 →
// 前两级均不命中时按 ask_id 全局命中（ask_id 全局唯一、登记取走即销，不存在跨 ask 串味）。
func (s *Server) takeAskLocked(instanceID, askID string) (*askWaiter, bool) {
	if askID == "" {
		return nil, false
	}
	key := instKey(instanceID, askID)
	if w, ok := s.asks[key]; ok {
		delete(s.asks, key)
		stopAskTimerLocked(w)
		return w, true
	}
	if instanceID != "" {
		for k, w := range s.asks {
			if w.askID == askID && w.deliverInstance == instanceID {
				delete(s.asks, k)
				stopAskTimerLocked(w)
				return w, true
			}
		}
		// Ask 中心（独立待办窗口）按 ask_id 作答：与归属/投递实例均不同 → 全局命中。
		for k, w := range s.asks {
			if w.askID == askID {
				delete(s.asks, k)
				stopAskTimerLocked(w)
				return w, true
			}
		}
		return nil, false
	}
	for k, w := range s.asks {
		if w.askID == askID {
			delete(s.asks, k)
			stopAskTimerLocked(w)
			return w, true
		}
	}
	return nil, false
}

// clearAsksByTurnLocked 摘除某轮次的全部 ask 等待登记（取消 / 轮次废案；**须持 s.mu**）。
// **正常轮次结束不再调用**（AskUser S4：非阻塞 ask 允许本轮先收尾，提问登记继续存活）。
// instanceID 非空 → 只清该实例；空 → 全桶（turn id 全局唯一，旧语义等价）。
// 返回被摘除的登记（调用方解锁后发 ask-center-remove）。
func (s *Server) clearAsksByTurnLocked(instanceID, turnID string) []*askWaiter {
	var removed []*askWaiter
	if turnID == "" {
		return removed
	}
	for k, w := range s.asks {
		if w.turnID != turnID {
			continue
		}
		if instanceID != "" && w.instanceID != instanceID {
			continue
		}
		delete(s.asks, k)
		s.forgetAskLocked(w)
		stopAskTimerLocked(w)
		removed = append(removed, w)
	}
	return removed
}

// clearAsksByInstanceLocked 摘除某实例的**全部** ask 等待登记（实例退出；**须持 s.mu**）。
// 退出清理时该实例的轮次可能已被移出 s.turns，故按 instance 归属整体清（不依赖 turn 存活）。
// 含**定向投递目标为该实例**的登记（SUBCHAT-3 阶段 F-2：群聊窗口关闭 → 其名下卡片一并失效）。
// 返回被摘除的登记（调用方解锁后发 ask-center-remove）。
func (s *Server) clearAsksByInstanceLocked(instanceID string) []*askWaiter {
	var removed []*askWaiter
	if instanceID == "" {
		return removed
	}
	for k, w := range s.asks {
		if w.instanceID == instanceID || w.deliverInstance == instanceID {
			delete(s.asks, k)
			s.forgetAskLocked(w)
			stopAskTimerLocked(w)
			removed = append(removed, w)
		}
	}
	return removed
}

// forgetAskLocked 从会话批次中摘除一条 ask（取消 / 实例退出语义：已收集的答复一并丢弃，
// 不触发投递）。批次无未答项即删除；**须持 s.mu**。
func (s *Server) forgetAskLocked(w *askWaiter) {
	if w == nil || w.batchKey == "" {
		return
	}
	b := s.askBatches[w.batchKey]
	if b == nil {
		return
	}
	delete(b.open, w.askID)
	delete(b.answered, w.askID)
	if len(b.open) == 0 {
		delete(s.askBatches, b.key)
	}
}
