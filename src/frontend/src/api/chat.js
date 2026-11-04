import { EventNames } from '../events/event-names'
import { MsgTopics, SessionRunningListKeys } from '../events/msgkeys'
import mq from '@ckfw/utils/mq.js'

// 域化改造（阶段三，20-gui）：send/队列出队不再走 SendChatMessage /
// SendNoticeTurn RPC，改为发布 llm-start 事件。turn 由前端自分配 uuid（客户端分配
// 语义，server llm-start 的 session/turn 必填）；桥把 llm-start 拆两步：
//   llm-start{session,turn,llm} + llm-send{type:text-user, content}
// 取消走 llm-cancel{session,turn}（21-llm-server），CancelChat RPC 已移除。

// 生成客户端自分配 id（uuid）：session / turn 均由前端分配（21-llm-server）。
function newUuid(prefix) {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return prefix + '-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10)
}

// newTurnId 生成客户端自分配 turn id（uuid）。
export function newTurnId() {
  return newUuid('turn')
}

// newSessionId 生成客户端自分配 session id（uuid）。
export function newSessionId() {
  return newUuid('sess')
}

// 发布 llm-start（前端 send/队列出队时调用；载荷对齐 server 协议：
// session/turn 必填 + llm；q 文本随事件携带，由桥拆为 llm-send{text-user}）。
// scenarioId = 场景目录名/key（空 = 默认场景）。
// 返回 { turn, ack }：
//   - turn：前端自分配的 turn id（MessageList 用它过滤流事件，必须**同步**取用）；
//   - ack：/publish 应答 promise（桥同步受理：server 落库 session/turn 后才返回）
//     —— T5 方案 B 的「落库回执」，user 气泡据此渲染（不再乐观插入）。
// E-25：ack 同为请求-响应面，补 30s 超时（后端卡死 → resolve(null) → 发送端按未受理兜底）。
export function publishLLMStart(sessionId, text, llmName, thinkEnabled, effort, scenarioId) {
  const turn = newTurnId()
  const ack = mq.emit(EventNames.llmStart, {
    session: sessionId,
    turn: turn,
    llm: llmName || '',
    think: thinkEnabled || '',
    effort: effort || '',
    scenario_id: scenarioId || '',
    q: text,
  }, { timeout: 30000 })
  return { turn, ack }
}

// ── INBOX（中途插话 / 等下一轮，2026-10-17；2026-11-01 语义调整）────────────
// 发送统一入口：落 prjusr inbox。`insert=true` = 用户点「插话」→ 登记插话意向，
// 可插入相位时该会话全部意向一次性批量插入；缺省 = 排队下一轮。
// 返回应答 promise：{ok, id, type}；type=insert 表示已登记插话意向。
export function publishInboxAdd(sessionId, text, llmName, thinkEnabled, effort, scenarioId, insert = false) {
  return mq.emit(EventNames.llmInboxAdd, {
    session: sessionId,
    llm: llmName || '',
    content: text,
    think: thinkEnabled || '',
    effort: effort || '',
    scenario_id: scenarioId || '',
    insert: !!insert,
  }, { timeout: 10000 })
}

// 读回 inbox 项（status 缺省 = pending）：队列指示器 / 重启拉回。
export function listInbox(sessionId, status = 'pending') {
  return mq.emit(EventNames.llmInboxList, { session: sessionId, status }, { timeout: 10000 })
}

// 撤回 / 删除一条 inbox 项。
export function discardInbox(id) {
  return mq.emit(EventNames.llmInboxDiscard, { id }, { timeout: 10000 })
}

// 登记「插话」意向（next_turn → insert，pending；可传 ids 批量）；无运行轮时 = 「继续」消费。
export function promoteInbox(id) {
  return mq.emit(EventNames.llmInboxPromote, { id }, { timeout: 10000 })
}

// 取消「插话」意向（insert → next_turn，仍 pending）。
export function revertInbox(id) {
  return mq.emit(EventNames.llmInboxRevert, { id }, { timeout: 10000 })
}

// listRunningSessions 当前**在跑轮次**的会话清单（61 §4.2 `session-running-list`）：
// 服务端**只读内存快照**（busy 注册表，不落库），每项 `{session_id, turn, instance_id}`，
// 覆盖本进程全部窗口（调用方按自己的会话列表过滤）。
export async function listRunningSessions() {
  const env = await mq.emit(MsgTopics.sessionRunningList, {}, { timeout: 10000 })
  const backend = env && env.backend
  if (!backend || !backend.ok) return []
  const r = backend.result && typeof backend.result === 'object' ? backend.result : {}
  const list = r[SessionRunningListKeys.list]
  return Array.isArray(list) ? list : []
}

// 发布「同轮次继续」llm-start（continue=true）：复用既有 turn（不新开 uuid turn），
// server 侧把注入的 user 消息标 Kind=continue（非新轮边界，拼接视为同一轮）。
// turn 为空时由 server 按 session 取最近一轮（桥不再为 continue 分配 turn）。
// 返回 { turn, ack }（turn 为空表示由后端解析；ack 语义同 publishLLMStart）。
export function publishLLMContinue(sessionId, text, turnId, llmName, thinkEnabled, effort, scenarioId) {
  const ack = mq.emit(EventNames.llmStart, {
    session: sessionId,
    turn: turnId || '',
    llm: llmName || '',
    think: thinkEnabled || '',
    effort: effort || '',
    scenario_id: scenarioId || '',
    continue: true,
    q: text,
  }, { timeout: 30000 })
  return { turn: turnId || '', ack }
}
