import { dataClient, dataRequest } from '../utils/dataClient'

// 场景 CRUD 走 data-scenario 消息面（20-gui）：list → data-scenario-list，
// save → data-scenario-save（有 id 更新 / 无 id 新建），delete → data-scenario-delete。
// 返回结构保持旧 /call 契约（{scenarios} / reply {ok, id}），组件消费字段不变。

export async function getScenarioList() {
  const reply = await dataClient.list('scenario')
  const list = reply.list !== undefined ? reply.list : reply
  return { scenarios: Array.isArray(list) ? list : [] }
}

export function saveScenario(data) {
  return dataClient.save('scenario', data)
}

// 读取指定级别的场景（data-scenario-load）：level 非空限定级别（app|user|project）；
// 用于「恢复默认」按上一级回填。缺失/越级 → 抛错（调用方按序降级）。
export function loadScenario(id, level) {
  return dataRequest('scenario', 'load', { data: { id, level } }).then(r => r.data)
}

// 删除场景（data-scenario-delete）：level 非空限定级别（persist 读 data.level；
// app 级只读被拒绝）；不传 level 时 persist 按 具体级优先（project → user）定位可写副本。
export function deleteScenario(id, level) {
  return dataRequest('scenario', 'delete', { data: level ? { id, level } : { id } })
}

// 还原默认场景（data-scenario-restore 消息面）。
// 返回 reply {req_id, ok}（与 data-scenario-save.reply 同构）。
export function restoreScenario(id) {
  return dataRequest('scenario', 'restore', { id })
}
