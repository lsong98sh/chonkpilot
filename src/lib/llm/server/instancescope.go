// 运行态 instance 隔离（2026-09-19，实例隔离第一批缺口 5）：
// server 侧运行态 map（turns / busy / continuePending / asks）以 **instKey(instance, id)** 为键
// —— 同进程内多 instance（gui 服务端 exe / browser）各自一份，互不可见、互不取消。
//
// 兼容口径：instKey 在 instance 为空时**原样返回 id**（未带 instance_id 的旧调用方 → 键与
// 隔离前逐字节等价）；查询入口在 instance 为空时回退「全桶扫描」（= 旧语义）；单 instance
// 进程（desktop）行为不变。
package server

import "time"

// instKey 组合运行态键：`<instance_id>\x00<id>`（instance 为空 → 原键，兼容旧语义）。
// instance_id 与 id（turn/session/ask）都不会含 NUL → 用作分隔符无歧义。
func instKey(instanceID, id string) string {
	if instanceID == "" {
		return id
	}
	return instanceID + "\x00" + id
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

// takeAskLocked 取走并注销 ask 等待登记（**须持 s.mu**；返回 (等待项, 是否命中)）。
// instance 非空 → 只在该 instance 的桶内命中；空 → 先按原键，再全桶回退（ask id 全局唯一，
// 兼容未带 instance_id 的旧调用方）。
//
// **定向投递兜底**（SUBCHAT-3 阶段 F-2）：`ask_user` 卡片按会话所在窗口投递 → 应答来自**投递目标
// 窗口**（其实例 ≠ 登记实例）→ 按 askID 命中「`deliverInstance` == 该实例」的登记（ask id 全局唯一；
// 仅在按 instKey 未命中时兜底，不放松既有按 instance 精确命中语义）。
func (s *Server) takeAskLocked(instanceID, askID string) (*askWaiter, bool) {
	if askID == "" {
		return nil, false
	}
	key := instKey(instanceID, askID)
	if w, ok := s.asks[key]; ok {
		delete(s.asks, key)
		return w, true
	}
	if instanceID != "" {
		for k, w := range s.asks {
			if w.askID == askID && w.deliverInstance == instanceID {
				delete(s.asks, k)
				return w, true
			}
		}
		return nil, false
	}
	for k, w := range s.asks {
		if w.askID == askID {
			delete(s.asks, k)
			return w, true
		}
	}
	return nil, false
}

// purgeExpiredAsksLocked 惰性回收**已过期**的 ask 等待登记（**须持 s.mu**）：expiresAt 到期未应答
// → 摘除，避免无答复的 ask 常驻内存（expiresAt 与 payload `expires_at` 同源，见 ask/askTTL）。
// 无答题的轮次若正常收尾，其等待登记由 clearAsksByTurnLocked 摘除；本回收兜住"轮次未收尾"的场景。
func (s *Server) purgeExpiredAsksLocked(now time.Time) {
	for k, w := range s.asks {
		if !w.expiresAt.IsZero() && now.After(w.expiresAt) {
			delete(s.asks, k)
		}
	}
}

// clearAsksByTurnLocked 摘除某轮次的全部 ask 等待登记（轮次收尾；**须持 s.mu**）。
// instanceID 非空 → 只清该实例；空 → 全桶（turn id 全局唯一，旧语义等价）。
func (s *Server) clearAsksByTurnLocked(instanceID, turnID string) {
	if turnID == "" {
		return
	}
	for k, w := range s.asks {
		if w.turnID != turnID {
			continue
		}
		if instanceID != "" && w.instanceID != instanceID {
			continue
		}
		delete(s.asks, k)
	}
}

// clearAsksByInstanceLocked 摘除某实例的**全部** ask 等待登记（实例退出；**须持 s.mu**）。
// 退出清理时该实例的轮次可能已被移出 s.turns，故按 instance 归属整体清（不依赖 turn 存活）。
// 含**定向投递目标为该实例**的登记（SUBCHAT-3 阶段 F-2：群聊窗口关闭 → 其名下卡片一并失效）。
func (s *Server) clearAsksByInstanceLocked(instanceID string) {
	if instanceID == "" {
		return
	}
	for k, w := range s.asks {
		if w.instanceID == instanceID || w.deliverInstance == instanceID {
			delete(s.asks, k)
		}
	}
}
