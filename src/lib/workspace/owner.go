// 运行态归属的**存储单源**（SANDBOX-WRITE-1 P5 步骤 2 / C15；41 G-101）。
//
// 背景：SCOPE-1 不变式「同一 dialog 不得在两个窗口打开」的判据是**运行态归属**
// （session → 正在跑它的实例 + 轮次）。该镜像原先存在框架窗口层（`host/src/gui/owner.go`），
// 与 PM 的 L3/L4 关系重复。依赖方向规定 framework 不得 import 主仓 workspace，故**反转依赖**：
// 框架定义 `gui.OwnerStore` 接口，PM 实现并通过 `Env.SetOwnerStore` 注入；
// 框架的归属读写全部委托 PM（框架不再自持副本），无 PM 形态（测试 / flash）回落内置 map。
//
// 并发：归属由事件派发线程（turn-start / session-complete 后台复核 / 窗口关闭）读写 →
// 独立互斥锁，不与 Manager.mu（项目表）竞争。
package workspace

import "sync"

// ownerRec 是某 session 的运行态归属（实例 + 产生归属的那一轮）。
// turn 用于区分同一 session 的连续两轮：上一轮的异步复核不得清掉新一轮的归属。
type ownerRec struct {
	instance string
	turn     string
}

var ownerMu sync.Mutex
var owners_ = map[string]ownerRec{} // 进程内单例；Manager 也是进程内单例

// OwnerOf 返回该 session 归属的 (instance_id, turn)（无主 → ok=false）。
func (m *Manager) OwnerOf(sessionID string) (instanceID, turn string, ok bool) {
	if sessionID == "" {
		return "", "", false
	}
	ownerMu.Lock()
	defer ownerMu.Unlock()
	rec, ok := owners_[sessionID]
	return rec.instance, rec.turn, ok
}

// SetOwner 登记归属（turn-start；同一 session 的新轮次覆盖旧轮次）。
func (m *Manager) SetOwner(sessionID, instanceID, turn string) {
	if sessionID == "" || instanceID == "" {
		return
	}
	ownerMu.Lock()
	owners_[sessionID] = ownerRec{instance: instanceID, turn: turn}
	ownerMu.Unlock()
}

// ClearOwnerIf 仅当归属仍是「该实例 + 该轮」时清空（并发换代 / 已释放 → 无操作）。
func (m *Manager) ClearOwnerIf(sessionID, instanceID, turn string) {
	ownerMu.Lock()
	if rec, ok := owners_[sessionID]; ok && rec.instance == instanceID && rec.turn == turn {
		delete(owners_, sessionID)
	}
	ownerMu.Unlock()
}

// ForgetInstance 整窗释放：清空该实例名下全部归属（窗口关闭 / instance-exit）。
func (m *Manager) ForgetInstance(instanceID string) {
	if instanceID == "" {
		return
	}
	ownerMu.Lock()
	for session, rec := range owners_ {
		if rec.instance == instanceID {
			delete(owners_, session)
		}
	}
	ownerMu.Unlock()
}
