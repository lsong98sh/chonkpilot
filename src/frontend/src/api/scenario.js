import { dataClient, dataRequest } from '../utils/dataClient'
import { DataScenarioListKeys } from '../events/msgkeys.js'

// 场景 CRUD 走 data-scenario 消息面（20-gui）：list → data-scenario-list，
// save → data-scenario-save（有 id 更新 / 无 id 新建），delete → data-scenario-delete。
// 返回结构保持旧 /call 契约（{scenarios} / reply {ok, id}），组件消费字段不变。

export async function getScenarioList() {
  const reply = await dataClient.list('scenario')
  const list = reply[DataScenarioListKeys.list] !== undefined ? reply[DataScenarioListKeys.list] : reply
  return { scenarios: Array.isArray(list) ? list : [] }
}

export function saveScenario(data) {
  return dataClient.save('scenario', data)
}

// 删除场景（data-scenario-delete）：level 非空限定级别（persist 读 data.level；
// app 级只读被拒绝）；不传 level 时 persist 按 具体级优先（project → user）定位可写副本。
export function deleteScenario(id, level) {
  return dataRequest('scenario', 'delete', { data: level ? { id, level } : { id } })
}
