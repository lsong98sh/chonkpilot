import { dataClient } from '../utils/dataClient'
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'

// 配置 CRUD 走 data-<domain> 消息面（20-gui）：
//   - user-config → data-user-config-{load,save}
//   - prj-config  → data-prj-config-{list,save}
//   - prj-security→ data-prj-security-{list,save}
//   - prompt      → data-prompt-{load,save}
// 返回结构保持旧 /call 契约（{config}/{entries}/{value}），组件消费字段不变。
// 目录/最近项目等桌面能力走 gui.* 本地面（61-消息一览 §1）。

// gui.* 消息辅助：publish + await 收集回复（与 data-* 同模式）。
function guiReq(action, body = {}) {
  const topic = 'gui.' + action
  return mq.emit(topic, body).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(topic + ': backend unreachable')
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || (topic + ' failed'))
      e.action = action
      throw e
    }
    return p
  })
}

// prj-config（项目配置 key-value 表）
export async function getAllConfig() {
  const reply = await dataClient.list('prj-config')
  const cfg = reply.list !== undefined ? reply.list : reply
  return { config: cfg || {} }
}

export function setConfig(key, value) {
  return dataClient.save('prj-config', { key, value })
}

// 批量写多个 prj-config 键：一次请求 = 1 条 `data-prj-config-save`（报文 `data.entries` =
// `{key: value}`）→ 后端一次写入并**只广播 1 条** `data-prj-config-refresh`（载荷带 `ids`
// 全组键；61-消息一览 §3.1）。单键 `setConfig` 保留（既有调用方不受影响）。
export function setConfigs(entries) {
  return dataClient.save('prj-config', { entries })
}

// 读取单个 prj-config key（load 按 key 返回 {data:{value}}；缺失返回空串）
export async function getConfigValue(key) {
  try {
    const reply = await dataClient.load('prj-config', key)
    const data = reply && reply.data !== undefined ? reply.data : reply
    if (data && typeof data === 'object' && data.value !== undefined) return data.value
    return data === undefined ? '' : data
  } catch (_) {
    return ''
  }
}

// 删除单个 prj-config key（= 重置继承/恢复上级）
export function deleteConfig(key) {
  return dataClient.remove('prj-config', key)
}

export function getRecentDirs() {
  return guiReq('recent.list', {})
}

export function saveRecentDir() {
  // Recent manager auto-records on startup; no-op needed
  return Promise.resolve()
}

export function openDir(path) {
  return guiReq('dir.open', { path })
}

// 探测工作目录 VCS（gui.vcs.info）→ {git, svn, gitInstalled}
// git = work-dir 为 git 仓库；gitInstalled = 系统可执行 git（文件历史双门槛）
export function getVCSInfo() {
  return guiReq('vcs.info', {})
}

export function openDirDialog() {
  return guiReq('dir.open-dialog', {})
}

// user-config（用户配置整体对象，usr config 表）
export async function getUserConfig() {
  const reply = await dataClient.load('user-config')
  const data = reply.data !== undefined ? reply.data : (reply.config !== undefined ? reply.config : reply)
  return { config: data || {} }
}

export function saveUserConfig(data) {
  return dataClient.save('user-config', data)
}

// 删除用户级单个标量 key（继承控件「重置继承」→ 回落系统/上级）
export function resetUserKey(key) {
  return dataClient.remove('user-config', key)
}

// usr 主库视图（批 3 · ⑯ 导出/备份数据源）：走 data-user-config-list ——
// list 返回 **usr 主库视图**（不含 prj/prjusr 项目层覆盖，见 61-消息一览 §3.1 语义要点；
// load 才是合并后有效值），导出/备份要的就是「这份 usr 配置本体」。
export async function getUserConfigMain() {
  const reply = await dataClient.list('user-config')
  const list = reply.list !== undefined ? reply.list : reply
  const first = Array.isArray(list) ? list[0] : list
  return first && typeof first === 'object' ? { ...first } : {}
}

// 清空整份 usr 全局配置（恢复出厂）：data-user-config-delete 不带 key（persist 无 key 分支
// = 清标量 + 自由键 + llms/mcps 集合 + legacy 整块；未知键分支不受影响）。
export function clearUserConfig() {
  return dataClient.remove('user-config')
}

// 配置快照落盘（gui.file.save，61-消息一览 §1）：payload {name, content, mode?}
//   mode 缺省 → 系统「另存为」对话框（GUI）→ {path}（取消 = ""）；mode='backup' → 直接落
//   **prjusr 数据根** backup/（`~/.chonkpilot/data/<prj-id>/backup/<name>`；个人数据不进项目目录，
//   24 §3.2 MW-8；未注入 prjusr 根时回落 `<workDir>/.chonkpilot/backup/`）——导入 / 恢复出厂前
//   自动备份。宿主不可用（browser 形态）→ 抛错，调用方降级为浏览器下载。
export function saveConfigFile(name, content, mode) {
  const body = { name, content }
  if (mode) body.mode = mode
  return guiReq('file.save', body)
}

// mcp（MCP server 四级文件化配置，2026-10-01）：`<级别>/capability/mcps/<名>.json`
//   - list：四级合并**生效**视图（同名最具体级优先、整条覆盖；每项带 level）。
//   - save：按 server.level 落对应级文件（改名/移级 → 传 old_name/old_level 先删旧文件）。
//   - delete：按名删（level 空 = 删最具体级副本，与列表所示一致）。
// 变更广播 data-mcp-refresh 由后端在 save/delete 后发出（前端 onDataRefresh('mcp', …) 刷新）。
export async function listMcpServers() {
  const reply = await dataClient.list('mcp')
  const list = reply.list !== undefined ? reply.list : reply
  return Array.isArray(list) ? list : []
}

export function saveMcpServer(server, oldName, oldLevel) {
  const data = { ...server }
  if (oldName) data.old_name = oldName
  if (oldLevel) data.old_level = oldLevel
  return dataClient.save('mcp', data)
}

export function deleteMcpServer(name) {
  return dataClient.remove('mcp', name)
}

// 探测工具链（gui.toolchain.detect）→ {tools:[{id,name,path,version}]}（系统级候选，不落库）
export async function detectToolchains() {
  const res = await guiReq('toolchain.detect', {})
  return Array.isArray(res.tools) ? res.tools : []
}

// 系统级只读内置项（gui.system.builtins；OEM/发布资源）→ {mcpServers}
// 系统级 MCP 定义（exe 同目录 config.json 的 mcpServers 段）：仅展示，禁止编辑/删除；
// 与四级文件化 MCP 配置无关（那是 data-mcp-* 面）。
export async function getSystemBuiltins() {
  const res = await guiReq('system.builtins', {})
  return {
    mcpServers: Array.isArray(res.mcpServers) ? res.mcpServers : [],
  }
}

// 选择可执行文件（gui.pick-executable）→ {path}（取消返回空串）
export async function pickExecutable() {
  const res = await guiReq('pick-executable', {})
  return res.path || ''
}

// prj-security（项目安全白名单条目）
// 契约（对齐 persist，见 14-安全域 §2.3）：**一条信任目录 = 一个 persist key**
// （prj config 表 `security-<key>`），value = JSON 字符串 {"dir","writable"}；
// list 返回平铺 map（key 已剥 `security-` 前缀）→ 此处还原为条目数组（条目保序依赖 map 键序）。
// key = 前端生成的不透明稳定 id：改目录/勾选只更新 value，key 不变（无孤儿键）。

// genSecurityKey 生成条目稳定键（base36 时间戳 + 随机后缀；同毫秒并发亦不撞，且键序 ≈ 创建序）。
function genSecurityKey() {
  return 'dir-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8)
}

// parseSecurityEntry 把 map 的 (key, value) 还原为条目：新形态 value = `{"dir","writable"}` JSON；
// 旧形态 value 为标量（如 "true"）且 key 即目录（历史写法，兼容读回）。
function parseSecurityEntry(key, value) {
  let obj = null
  if (typeof value === 'string') {
    try {
      const p = JSON.parse(value)
      if (p && typeof p === 'object') obj = p
    } catch (_) { /* 非 JSON → 旧形态 */ }
  } else if (value && typeof value === 'object') {
    obj = value
  }
  if (obj) {
    return {
      _key: key,
      dir: typeof obj.dir === 'string' ? obj.dir : '',
      writable: obj.writable === true || obj.writable === 'true',
    }
  }
  return { _key: key, dir: key, writable: String(value) === 'true' }
}

export async function getProjectSecurity() {
  const reply = await dataClient.list('prj-security')
  const list = reply.list !== undefined ? reply.list : reply
  if (Array.isArray(list)) return { entries: list } // 兼容数组载荷
  const entries = []
  if (list && typeof list === 'object') {
    for (const [key, value] of Object.entries(list)) entries.push(parseSecurityEntry(key, value))
  }
  return { entries }
}

// saveProjectSecurity 逐条 upsert（`{data:{key, value}}`）+ 删除已移除条目（removedKeys）；
// 条目 _key 缺失时现场生成（新行首次落库）。返回后调用方可清空 removedKeys。
export async function saveProjectSecurity(entries, removedKeys = []) {
  const tasks = []
  for (const it of entries || []) {
    if (!it._key) it._key = genSecurityKey()
    const value = JSON.stringify({ dir: it.dir || '', writable: !!it.writable })
    tasks.push(dataClient.save('prj-security', { key: it._key, value }))
  }
  for (const k of removedKeys) {
    if (k) tasks.push(dataClient.remove('prj-security', k))
  }
  await Promise.all(tasks)
}

// prompt（提示词，prj config 表；load 以 key 为 id，save 带 {key, value}）
export async function getPrompt(key) {
  const reply = await dataClient.load('prompt', key)
  const data = reply.data !== undefined ? reply.data : reply
  return { value: data && typeof data === 'object' && data.value !== undefined ? data.value : data }
}

export function setPrompt(key, value) {
  return dataClient.save('prompt', { key, value })
}

/**
 * 流式优化 Agent Prompt — 经 gui.prompt-optimise 消息面触发，流式结果经事件接收
 * - onToken: (content) => void
 * - onDone: (prompt) => void
 * - onError: (message) => void
 */
export function optimizeAgentPrompt(data, onToken, onDone, onError) {
  const unsubs = []
  unsubs.push(mq.on(EventNames.optimizeToken, (payload) => {
    if (onToken) onToken(payload.content)
  }))
  unsubs.push(mq.on(EventNames.optimizeDone, (payload) => {
    unsubs.forEach(fn => fn())
    if (onDone) onDone(payload.prompt || '')
  }))
  unsubs.push(mq.on(EventNames.optimizeError, (payload) => {
    unsubs.forEach(fn => fn())
    if (onError) onError(payload.message || 'Unknown error')
  }))

  // payload {title, useCase, prompt} 原样透传（对齐旧 call('OptimizeAgentPrompt', data)）。
  guiReq('prompt-optimise', data).catch(err => {
    unsubs.forEach(fn => fn())
    if (onError) onError(err.message || 'Request failed')
  })

  return { abort: () => unsubs.forEach(fn => fn()) }
}
