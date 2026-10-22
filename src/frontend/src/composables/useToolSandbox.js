/**
 * useToolSandbox — agentbox 沙箱配置页（usr 键 `tool_sandbox`）
 *
 * 管理单位（用户口径，2026-10-12 重整）：沙箱开关**按「被 spawn 的执行体」为单位**——
 *   - 内置 executor：按 **exe** 一行（2026-10-21 GATEWAY-2/L1 起只剩 tools / script / web 三个；
 *     browser / desktop 已改为常驻插件，不受本开关约束）；
 *   - 外部 MCP：按 **MCP 条目**一行（仅 stdio 可施加沙箱，http/sse 不可）。
 *
 * 实施位置（2026-10-21 GATEWAY-2/L7）：开关下发后由 **gateway 在 spawn stdio 进程时**实施
 * **进程级写隔离**（`src/lib/core/sandbox`：Windows Low IL 令牌 + Job Object，只限写、读不受限、
 * 不改 DACL）；原「执行体内 agentbox 拦截」已删除。
 *
 * 存储形态：usr 自由键 `tool_sandbox` = JSON 对象
 *   `{"executors":{"<exe基名>":bool},"mcps":{"<mcp名>":bool}}`
 *   - 未配置 / `false` = **不隔离**（缺省与引入前行为一致）；
 *   - 兼容旧形状 `{"core":bool,"desktop":bool,"browser":bool}`（读取时迁移，见 sandboxSummary）。
 *
 * 读写面（**零新增 MQ 主题**）：
 *   - 工具清单 = 既有能力面 `tools-list`（每项 `_meta.category` / `_meta.server`）。
 *   - 外部 MCP 清单 = 既有 `data-mcp-list`（listMcpServers）。
 *   - 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（api/config.js）。
 *
 * 手动保存（用户口径「不做 change 就保存」）：拨动 Switch / 「恢复未设置」只改**本地待保存态**
 * （`workMap`），**不立即落库**；点【保存】才写库，**无改动时保存按钮禁用**（`dirty === false`）。
 *
 * 本 composable 只做数据与读写，不弹提示（提示由页面按 i18n 决定）。
 */
import { ref, computed } from 'vue'
import { getUserConfig, saveUserConfig, resetUserKey, getProjectSecurity, listMcpServers } from '../api/config'
import { countMCPsOn, isStdioServer, parseToolSandboxMap } from '../utils/sandboxSummary'
import { countTrustDirs, needsTrustDirsWarning } from '../utils/sandboxTrust'
import mq from '../utils/mq'
import { MsgClientTopics } from '../events/msgkeys.js'

// usr 配置键（自由键通道）：值与 tool_async 同形态 = JSON 对象文本。
export const TOOL_SANDBOX_KEY = 'tool_sandbox'

// 内置 executor 单位：unit = executor exe 基名（写入 tool_sandbox.executors 的键），
// category = 该执行体工具契约的 `_meta.category`（tools-list 归组用）。
// 2026-10-21（GATEWAY-2/L1）：DSL 退役 —— 只剩 tools / script / web 三个 executor；
// browser / desktop 已改为常驻插件（UDS，不接 gateway），不受本开关约束。
export const EXECUTOR_UNITS = [
  { unit: 'chonkpilot-tools-executor.exe', category: 'file' },
  { unit: 'chonkpilot-script-executor.exe', category: 'script' },
  { unit: 'chonkpilot-web-executor.exe', category: 'web' },
]

// isSet：该键是否显式设置（区分 true / false 与「未设置」）。
function isSet(v) { return v === true || v === false }

// pickMap：从（本地 / 已保存）态中取归一后的 `{ executors, mcps }`（仅保留布尔值键）。
function pickMap(m) {
  const ex = {}, mc = {}
  const e = (m && m.executors) || {}
  const c = (m && m.mcps) || {}
  for (const [k, v] of Object.entries(e)) if (isSet(v)) ex[k] = v
  for (const [k, v] of Object.entries(c)) if (isSet(v)) mc[k] = v
  return { executors: ex, mcps: mc }
}

// mapsEqual：两张 `{ executors, mcps }` 是否等价。
function mapsEqual(a, b) {
  const keys = (x) => [...Object.keys(x)].sort()
  const eqEx = keys(a.executors), eqMc = keys(a.mcps)
  if (eqEx.length !== keys(b.executors).length || eqMc.length !== keys(b.mcps).length) return false
  for (const k of eqEx) if (a.executors[k] !== b.executors[k]) return false
  for (const k of eqMc) if (a.mcps[k] !== b.mcps[k]) return false
  return true
}

export function useToolSandbox() {
  const loading = ref(false)
  const saving = ref(false)
  // 加载失败态（E-32）：各路加载（tools-list / usr 配置 / mcp 列表 / 信任目录）任一失败即置位。
  // ① 页面据此给可见提示（否则空态 /「未设置」会误导）；
  // ② 失败态禁止保存：此时保存会以空态整键覆盖 `tool_sandbox`，丢失未读到的既有配置。
  const loadFailed = ref(false)
  // 最近一次加载失败原因（供页面 loadFailedText 拼「失败原因摘要」）。
  const loadError = ref(null)
  // 工具归组：toolsByCategory（内置 executor，key = _meta.category）/ toolsByServer（外部 MCP，key = _meta.server.alias）。
  const toolsByCategory = ref({})
  const toolsByServer = ref({})
  // 外部 MCP 清单（data-mcp-list）：[{ name, applicable }]。
  const mcpList = ref([])
  // workMap = 本地待保存态（`{executors,mcps}`）；savedMap = 上次落库态（用于 dirty 判定）。
  const workMap = ref({ executors: {}, mcps: {} })
  const savedMap = ref({ executors: {}, mcps: {} })
  // 外部 MCP 开关（mcps 子表）已开启数量 —— 供「两页状态互见」摘要。
  const serverSandboxOn = ref(0)
  // 项目信任目录有效条目数（prj `security-*`）——供「空目录 → 不施加隔离」内联预警。
  const trustDirCount = ref(0)

  // failLoad：记录任一路加载失败（置位 + 留因由供页面文案）。整体在 reload() 开头复位。
  function failLoad(e) {
    loadFailed.value = true
    loadError.value = e
  }

  // rows = 内置 4 个 exe 单位 + 外部 MCP 单位（每单位一行）。
  const rows = computed(() => {
    const wm = workMap.value || {}
    const ex = wm.executors || {}
    const mc = wm.mcps || {}
    const out = EXECUTOR_UNITS.map((u) => {
      const v = ex[u.unit]
      return {
        kind: 'executor', key: u.unit, unit: u.unit, category: u.category,
        tools: toolsByCategory.value[u.category] || [],
        applicable: true, userSet: isSet(v), on: v === true,
      }
    })
    // 外部 MCP：清单并集（含已配置但当前未列出者，避免配置「隐身」）+ 按名排序。
    const applicableByName = Object.fromEntries(mcpList.value.map((m) => [m.name, m.applicable]))
    const names = new Set()
    for (const m of mcpList.value) names.add(m.name)
    for (const k of Object.keys(mc)) names.add(k)
    for (const name of [...names].sort()) {
      const v = mc[name]
      out.push({
        kind: 'mcp', key: name, unit: name, category: '',
        tools: toolsByServer.value[name] || [],
        applicable: applicableByName[name] !== false, // 未在清单（旧配置残留）→ 仍可操作
        userSet: isSet(v), on: v === true,
      })
    }
    return out
  })

  // 是否扫描到任何单位工具（tools-list 为空 / 未构建时给出空态提示）。
  const totalTools = computed(() => rows.value.reduce((n, r) => n + r.tools.length, 0))

  // 是否有未保存改动（只比较归一后的 executors + mcps）。
  const dirty = computed(() => !mapsEqual(pickMap(workMap.value), pickMap(savedMap.value)))
  // 是否至少有一个开关已开启（含待保存态，"已开启沙箱"）。
  const anySandboxOn = computed(() => {
    const wm = workMap.value || {}
    return Object.values(wm.executors || {}).some((v) => v === true) ||
      Object.values(wm.mcps || {}).some((v) => v === true)
  })
  // 已开启沙箱 且 信任目录为空 → 显著预警（空允许目录 = **不施加目录隔离**；
  // 内建 executor 的进程级沙箱待 L6，见 gateway/serverslist.go · llm/server/server.go）。
  // 目录变更经 loadTrustDirs（订阅 data-prj-security-refresh）即时重算 → 警告自动消失。
  const trustWarning = computed(() =>
    needsTrustDirsWarning({ sandboxOn: anySandboxOn.value, trustDirCount: trustDirCount.value })
  )

  async function loadTools() {
    loading.value = true
    try {
      // tools-list：客户端能力面 type（schema clientTopic，桥映射 mcp-tools-list），常量见 MsgClientTopics。
      const env = await mq.emit(MsgClientTopics.toolsList, {})
      const res = env && env.backend && env.backend.result
      const tools = res && Array.isArray(res.tools) ? res.tools : []
      const byCat = {}
      for (const u of EXECUTOR_UNITS) byCat[u.category] = []
      const byServer = {}
      for (const tl of tools) {
        const meta = (tl && tl._meta) || {}
        const category = String(meta.category || '').toLowerCase()
        const srv = meta.server && typeof meta.server === 'object' ? meta.server : null
        const alias = String((srv && (srv.alias || srv.node)) || '')
        const item = {
          name: tl.name,
          description: tl.description || '',
          // 展示用：网关为暴露名加的前缀（self_ 等）剥掉后显示（`name` 仍作完整暴露名 / title）
          server: srv || {},
        }
        // 内置 executor 工具：按契约 category 归组（self 节点工具 category = core/desktop/browser；dsl = server）。
        if (category === 'core' || category === 'desktop' || category === 'browser') {
          byCat[category].push(item)
          continue
        }
        if (category === 'server') { // dsl 执行器（dsl_run）
          byCat['server'].push(item)
          continue
        }
        if (category === 'dir') continue // dir 节点工具（第三方目录），非本页范围
        // 外部 MCP 工具：按 `_meta.server.alias`（= MCP 条目名）归组。
        if (alias && alias !== 'self') {
          if (!byServer[alias]) byServer[alias] = []
          byServer[alias].push(item)
        }
      }
      toolsByCategory.value = byCat
      toolsByServer.value = byServer
    } catch (e) {
      console.warn('[useToolSandbox] load tools failed:', e)
      toolsByCategory.value = {}
      toolsByServer.value = {}
      failLoad(e)
    } finally {
      loading.value = false
    }
  }

  async function loadUserConfig() {
    try {
      const res = await getUserConfig()
      const uc = res.config || res
      const parsed = parseToolSandboxMap(uc[TOOL_SANDBOX_KEY])
      savedMap.value = parsed
      workMap.value = { executors: { ...parsed.executors }, mcps: { ...parsed.mcps } }
    } catch (e) {
      console.warn('[useToolSandbox] load user config failed:', e)
      savedMap.value = { executors: {}, mcps: {} }
      workMap.value = { executors: {}, mcps: {} }
      failLoad(e)
    }
  }

  // 读 MCP 四级文件化列表（data-mcp-list）→ 外部 MCP 行清单（name + 是否 stdio 可施加沙箱）。
  async function loadMcpList() {
    try {
      const list = await listMcpServers()
      mcpList.value = (Array.isArray(list) ? list : [])
        .map((s) => ({ name: String((s && s.name) || '').trim(), applicable: isStdioServer(s) }))
        .filter((m) => m.name)
    } catch (e) {
      console.warn('[useToolSandbox] load mcp list failed:', e)
      mcpList.value = []
      failLoad(e)
    }
    serverSandboxOn.value = countMCPsOn(workMap.value)
  }

  // 读项目信任目录（prj `security-*`，既有 data-prj-security-list）→ 有效条目数。
  // 失败保持现值（不误报/误清）：仅在成功时更新计数。
  async function loadTrustDirs() {
    try {
      const res = await getProjectSecurity()
      trustDirCount.value = countTrustDirs(res.entries)
    } catch (e) {
      console.warn('[useToolSandbox] load trust dirs failed:', e)
      failLoad(e) // 计数保持现值（不误报/误清），但失败态须可见且禁止保存
    }
  }

  async function reload() {
    // 整体加载前复位失败态：本轮任何一路失败会重新置位。
    loadFailed.value = false
    loadError.value = null
    await loadUserConfig()
    await loadMcpList()
    await loadTools()
    await loadTrustDirs()
  }

  // 拨动 Switch = 只改本地待保存态（不落库）。scope = 'executors' | 'mcps'。
  function setSandbox(scope, key, on) {
    const wm = workMap.value
    workMap.value = { ...wm, [scope]: { ...(wm[scope] || {}), [key]: !!on } }
    serverSandboxOn.value = countMCPsOn(workMap.value)
  }

  // 「恢复未设置」= 从待保存态移除该单位键（不落库；末项清空后整键删除由 save() 决定）。
  function restore(scope, key) {
    const wm = workMap.value
    const next = { ...(wm[scope] || {}) }
    delete next[key]
    workMap.value = { ...wm, [scope]: next }
    serverSandboxOn.value = countMCPsOn(workMap.value)
  }

  // 手动保存：把待保存态写库。无改动 → 不写（返回 false）。全部未设置 → 删整键。
  // 加载失败态（loadFailed）→ 拒绝写库：此时待保存态是空态，写入会整键覆盖、丢失既有配置。
  async function save() {
    if (!dirty.value) return false
    if (loadFailed.value) return false
    const next = pickMap(workMap.value)
    saving.value = true
    try {
      if (Object.keys(next.executors).length === 0 && Object.keys(next.mcps).length === 0) {
        await resetUserKey(TOOL_SANDBOX_KEY)
      } else {
        await saveUserConfig({ [TOOL_SANDBOX_KEY]: next })
      }
      savedMap.value = next
      workMap.value = { executors: { ...next.executors }, mcps: { ...next.mcps } }
      return true
    } finally {
      saving.value = false
    }
  }

  return {
    loading, saving, loadFailed, loadError, rows, totalTools, workMap, dirty, anySandboxOn,
    serverSandboxOn, trustDirCount, trustWarning, loadTrustDirs,
    reload, setSandbox, restore, save,
  }
}
