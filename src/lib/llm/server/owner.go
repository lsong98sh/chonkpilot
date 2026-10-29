// 运行态归属表（SCOPE-1 强制不变式，2026-10-28）：**同一 dialog 不得在两个窗口打开**。
//
// 判据 = **运行态归属**（不是「显示」，视图不参与 —— session 与 view 没有必然联系：不显示也可以
// 有活动 session，view 只是订阅某个 session 的内容）：
//
//	归属   = 某 session 在某窗口实例上**有活动运行态**（turn 未结束 / inbox 有 pending /
//	         有 async task）→ 该实例是它的 owner；三者皆无 = 该 session「无主」。
//	产生   = 受理 `llm-start` / `llm-send` 且该 session 当前无主 → 请求里的 `instance_id` 即 owner
//	         （「用户选择即激活」；同一 instance 重入不改变归属）。
//	拒绝   = 该 session 有主、owner ≠ 请求 instance 且**仍活动** → 走既有 `accepted:false` 应答
//	         （不新增字段、不发新消息）。
//	释放①  = 窗口关闭 / `instance-exit` → 整窗释放（forgetOwnersOfInstance）。
//	释放②  = 空闲自动结束（turn 结束 ∧ inbox 无 pending ∧ 无 async task）→ 只释放该 dialog
//	         （releaseOwnerIfIdle，调用点 = 轮次 loop 收尾）；切走 / 不显示 / 关标签都**不释放**。
//
// 数据来源 = 本服务已有的运行态（`busy` / inbox 内存索引 / 任务树内存视图），**消息面零变更**
// （不新增主题、不新增字段）。宿主侧另有同口径镜像（`src/lib/gui/owner.go`，只做「谁在跑」的
// 镜像供 `gui.window.open-chat` 激活既有窗口），二者独立、互不依赖。
package server

// ownerConflictText 是「同 session 已被另一窗口实例持有活动运行态」的拒绝文案（走既有 ok:false /
// accepted:false 应答；不新增错误码字段）。
const ownerConflictText = "session is running in another window: "

// sessionOwner 返回该 session 当前的活动 owner 实例（无主 → 空串）。
func (s *Server) sessionOwner(session string) string {
	if session == "" {
		return ""
	}
	s.ownersMu.Lock()
	defer s.ownersMu.Unlock()
	return s.owners[session]
}

// claimSessionOwner 在**受理运行态**时占用归属（llm-start / llm-send）：
//   - 无主 / 已是本实例 → 置为本实例（幂等）；
//   - 有主但 owner 已无活动运行态（空闲自动结束漏收）→ 由本次受理接管（「用户选择即激活」）；
//   - 有主且仍活动、owner ≠ 本实例 → 返回 (owner, false)，调用方按既有 ok:false 应答拒绝。
func (s *Server) claimSessionOwner(instanceID, session string) (string, bool) {
	if session == "" || instanceID == "" {
		return "", true
	}
	if owner := s.sessionOwner(session); owner != "" && owner != instanceID && s.sessionActive(owner, session) {
		return owner, false
	}
	s.ownersMu.Lock()
	if s.owners == nil {
		s.owners = map[string]string{}
	}
	s.owners[session] = instanceID
	s.ownersMu.Unlock()
	return instanceID, true
}

// ownerConflict 是**投递类**受理（llm-send）的归属预检：该 session 已被另一个实例持有活动运行态
// → (owner, false) 由调用方按既有 accepted:false 应答；否则 (_, true) 放行（无主 / 本实例 / 已空闲）。
// 会话 id 优先取载荷，载荷只带 turn 时按 turn **全局**反查（不按 instance 过滤 —— 只有这样才认得出
// 「别的窗口正在跑」）。
func (s *Server) ownerConflict(instanceID, session, turn string) (string, bool) {
	if session == "" && turn != "" {
		session = s.turnSession(turn)
	}
	if session == "" {
		return "", true
	}
	owner := s.sessionOwner(session)
	if owner == "" || owner == instanceID || !s.sessionActive(owner, session) {
		return owner, true
	}
	return owner, false
}

// sessionActive 报告该 session 在该实例上是否仍有活动运行态（释放判据 = 三者皆无）。
// 三个来源全部是进程内既有数据，**不做数据面往返**。
func (s *Server) sessionActive(instanceID, session string) bool {
	if instanceID == "" || session == "" {
		return false
	}
	s.mu.Lock()
	_, busy := s.busy[instKey(instanceID, session)]
	s.mu.Unlock()
	if busy {
		return true
	}
	s.inboxMu.Lock()
	pending := len(s.inboxPending[session]) > 0
	s.inboxMu.Unlock()
	if pending {
		return true
	}
	return s.tasks != nil && s.tasks.activeBySession(session)
}

// releaseOwnerIfIdle 空闲自动结束 → 只释放该 dialog 的归属（turn 已结束 ∧ inbox 无 pending ∧
// 无 async task）。调用点 = 轮次 loop 收尾（busy 已摘除、tc.Done 之前）；不匹配 / 仍活动 → 无操作。
func (s *Server) releaseOwnerIfIdle(instanceID, session string) {
	if instanceID == "" || session == "" || s.sessionOwner(session) != instanceID {
		return
	}
	if s.sessionActive(instanceID, session) {
		return
	}
	s.clearOwnerIf(session, instanceID)
}

// clearOwnerIf 仅当归属仍指向该实例时清空（并发接管 / 已释放 → 无操作）。
func (s *Server) clearOwnerIf(session, instanceID string) {
	s.ownersMu.Lock()
	if s.owners[session] == instanceID {
		delete(s.owners, session)
	}
	s.ownersMu.Unlock()
}

// forgetOwnersOfInstance 整窗释放（`instance-exit` / 窗口关闭）：清空该实例名下的全部归属，
// 防止进程/窗口退出后永久占位。
func (s *Server) forgetOwnersOfInstance(instanceID string) {
	if instanceID == "" {
		return
	}
	s.ownersMu.Lock()
	for session, owner := range s.owners {
		if owner == instanceID {
			delete(s.owners, session)
		}
	}
	s.ownersMu.Unlock()
}
