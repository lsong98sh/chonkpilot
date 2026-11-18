/**
 * 停止按钮「乐观置态 → 后端终态幂等收敛」纯状态机（无 Vue 依赖 / 无副作用）。
 *
 * 语义：
 *   - 用户点「停止」→ 调用方立即收起加载态（hidePending 等），并 `markInterruptedLocally`
 *     置位本地标志；不伪造 assistant 消息、不发 MQ、不写库。
 *   - 后端 llm-complete 到达 → 仍走既有收尾路径；`convergeOnComplete` 只允许**同一 turn**
 *     的首次终态事件收尾（重复终态幂等），并清空本地乐观标志。
 *   - 新一轮开始 / 切换会话 → `resetTurnInterruptState` 复位。
 *
 * 状态字段：
 *   local          = 已本地乐观置态，尚未收到后端终态
 *   convergedTurn  = 已按后端终态收尾的 turn 标识（同 turn 重复终态 → 跳过收尾）
 */
export function createTurnInterruptState() {
  return { local: false, convergedTurn: null }
}

/** 用户点停止：本地立即置态。首次返回 true；重复点击返回 false（幂等）。 */
export function markInterruptedLocally(state) {
  if (!state || state.local) return false
  state.local = true
  return true
}

/**
 * 后端 llm-complete 到达：清空本地乐观标志。
 * @param {object} state
 * @param {string} turnKey 本次终态所属 turn（d.turn / d.turn_id / currentTurnId 回退）
 * @returns {boolean} 本轮是否应执行既有收尾（同 turn 首次终态 true；重复终态 false）
 */
export function convergeOnComplete(state, turnKey) {
  if (!state) return true
  state.local = false
  const key = turnKey == null ? '' : String(turnKey)
  // turn 标识缺失（异常/旧事件）：不写收敛标记、始终放行——宁可重复收尾（handleDone 幂等），
  // 也不能因为一次空键把后续真实终态吞掉。
  if (key === '') return true
  if (state.convergedTurn === key) return false
  state.convergedTurn = key
  return true
}

/** 新一轮开始 / 切换会话：复位本地乐观标志与收敛标记。 */
export function resetTurnInterruptState(state) {
  if (!state) return
  state.local = false
  state.convergedTurn = null
}