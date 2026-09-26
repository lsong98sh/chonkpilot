// 运行态 instance 隔离（2026-09-19，实例隔离第一批缺口 5）：
// server 侧运行态 map（turns / busy / continuePending / asks）以 **instKey(instance, id)** 为键
// —— 同进程内多 instance（gui 服务端 exe / browser）各自一份，互不可见、互不取消。
//
// 兼容口径：instKey 在 instance 为空时**原样返回 id**（未带 instance_id 的旧调用方 → 键与
// 隔离前逐字节等价）；查询入口在 instance 为空时回退「全桶扫描」（= 旧语义）；单 instance
// 进程（desktop）行为不变。
package server

// instKey 组合运行态键：`<instance_id>\x00<id>`（instance 为空 → 原键，兼容旧语义）。
// instance_id 与 id（turn/session/ask）都不会含 NUL → 用作分隔符无歧义。
func instKey(instanceID, id string) string {
	if instanceID == "" {
		return id
	}
	return instanceID + "\x00" + id
}

// lookupTurnLocked 取指定 instance 的运行 turn（**须持 s.mu**）：
//   - instance 非空 → 只在该 instance 的桶内命中（跨 instance 不可见）；
//   - instance 空（旧调用方未带 instance_id）→ 先按原键，再全桶扫描（同旧语义）。
func (s *Server) lookupTurnLocked(instanceID, turnID string) *turnCtx {
	if turnID == "" {
		return nil
	}
	if tc := s.turns[instKey(instanceID, turnID)]; tc != nil {
		return tc
	}
	if instanceID != "" {
		return nil
	}
	for _, tc := range s.turns {
		if tc.req.Turn == turnID {
			return tc
		}
	}
	return nil
}

// turnBySessionLocked 按 session 找运行 turn（**须持 s.mu**）：instance 非空 → 限定该 instance；
// 空 → 全表（同旧语义）。
func (s *Server) turnBySessionLocked(instanceID, session string) *turnCtx {
	if session == "" {
		return nil
	}
	for _, tc := range s.turns {
		if tc.req.Session != session {
			continue
		}
		if instanceID != "" && tc.req.InstanceID != instanceID {
			continue
		}
		return tc
	}
	return nil
}

// takeAskLocked 取走并注销 ask 等待登记（**须持 s.mu**；返回 (等待项, 是否命中)）。
// instance 非空 → 只在该 instance 的桶内命中；空 → 先按原键，再全桶回退（ask id 全局唯一，
// 兼容未带 instance_id 的旧调用方）。
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
