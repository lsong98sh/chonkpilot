/**
 * useToolSandbox — agentbox 沙箱「executor」页签（usr 键 `tool_sandbox`）
 *
 * 决策（用户口径，2026-09-26）：沙箱**不是工具级、而是 executor 级** —— 开关按工具所属
 * **executor 类别**（= 契约 `_meta.category`：core / desktop / browser，即 chonkpilot-mcp-tools
 * 的三个执行器能力目录）生效；一个开关管制该 executor 下的**全部工具**。仅 **self 的 executor
 * 工具**（本仓 spawn 子进程执行）可隔离；第三方 MCP 的沙箱在 MCP 对话框「运行信息」页签（
 * usr `mcps[].sandbox`）设置，http/sse 无法施加沙箱。
 *
 * 读写面（**零新增 MQ 主题**）：
 *   - 工具清单 = 既有能力面 `tools-list`（每项 `_meta.category` / `_meta.server`）。
 *   - 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（api/config.js）。
 * 存储形态：usr 自由键 `tool_sandbox` = JSON 对象 `{"core":true|false,"desktop":...,"browser":...}`
 *   - 未配置 / `false` = **不隔离**（缺省与引入前行为一致）；**旧形态（按工具暴露名）不再生效**。
 *
 * 手动保存（用户口径「不做 change 就保存」）：拨动 Switch / 「恢复未设置」只改**本地待保存态**
 * （`workMap`），**不立即落库**；点【保存】才写库，**无改动时保存按钮禁用**（`dirty === false`）。
 *
 * 本 composable 只做数据与读写，不弹提示（提示由页面按 i18n 决定）。
 */
import { ref, computed } from 'vue'
import { getUserConfig, saveUserConfig, resetUserKey, getProjectSecurity } from '../api/config'
import { countServerSandboxOn } from '../utils/sandboxSummary'
import { countTrustDirs, needsTrustDirsWarning } from '../utils/sandboxTrust'
import mq from '../utils/mq'

// usr 配置键（自由键通道）：值与 tool_async 同形态 = JSON 对象文本。
export const TOOL_SANDBOX_KEY = 'tool_sandbox'
// executor 类别（= 契约 `_meta.category`）——三个执行器各一行，共三个 Switch。
export const EXECUTOR_CATEGORIES = ['core', 'desktop', 'browser']

// 解析 usr tool_sandbox：允许对象形态与 JSON 字符串形态（自由键按字符串回读）。
function parseToolSandbox(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return {} }
  }
  return obj && typeof obj === 'object' && !Array.isArray(obj) ? obj : {}
}

// 只保留三个 executor 类别键（旧形态 / 其它键忽略 → 不参与 diff、不落库）。
function pickExecutorMap(m) {
  const out = {}
  for (const c of EXECUTOR_CATEGORIES) {
    if (m && (m[c] === true || m[c] === false)) out[c] = m[c]
  }
  return out
}

export function useToolSandbox() {
  const loading = ref(false)
  const saving = ref(false)
  // 三行固定的 executor 行：{ category, tools: [{name, description, server}], on, userSet }
  const toolsByCategory = ref({})
  // workMap = 本地待保存态（拨动/恢复只改它）；savedMap = 上次落库态（用于 dirty 判定）。
  const workMap = ref({})
  const savedMap = ref({})
  // server 级（mcps[].sandbox，仅 stdio）已开启数量 —— 供「两页状态互见」摘要（读 usr 同一次加载）。
  const serverSandboxOn = ref(0)
  // 项目信任目录有效条目数（prj `security-*`）——供 ②「空目录 = 全拒」内联预警。
  const trustDirCount = ref(0)

  // 三个 executor 行（恒定三行；类别下无工具时 tools 为空数组）。
  const rows = computed(() => EXECUTOR_CATEGORIES.map((category) => {
    const v = workMap.value[category]
    return {
      category,
      tools: toolsByCategory.value[category] || [],
      userSet: v === true || v === false,
      on: v === true,
    }
  }))
  // 是否扫描到任何 executor 工具（tools-list 为空 / 未构建时给出空态提示）。
  const totalTools = computed(() =>
    EXECUTOR_CATEGORIES.reduce((n, c) => n + ((toolsByCategory.value[c] || []).length), 0)
  )

  // 是否有未保存改动（只比较三个 executor 类别键）。
  const dirty = computed(() => {
    const a = pickExecutorMap(workMap.value)
    const b = pickExecutorMap(savedMap.value)
    const keys = new Set([...Object.keys(a), ...Object.keys(b)])
    for (const k of keys) {
      if (a[k] !== b[k]) return true
    }
    return false
  })
  // 是否至少有一个 executor 开关已开启（含待保存态，"已开启沙箱"）。
  const anySandboxOn = computed(() =>
    Object.values(workMap.value).some((v) => v === true)
  )
  // ② 已开启沙箱 且 信任目录为空 → 显著预警（空允许集 = 全拒）。
  // 目录变更经 loadTrustDirs（订阅 data-prj-security-refresh）即时重算 → 警告自动消失。
  const trustWarning = computed(() =>
    needsTrustDirsWarning({ sandboxOn: anySandboxOn.value, trustDirCount: trustDirCount.value })
  )

  async function loadTools() {
    loading.value = true
    try {
      const env = await mq.emit('tools-list', {})
      const res = env && env.backend && env.backend.result
      const tools = res && Array.isArray(res.tools) ? res.tools : []
      const byCat = {}
      for (const c of EXECUTOR_CATEGORIES) byCat[c] = []
      for (const tl of tools) {
        const meta = (tl && tl._meta) || {}
        const category = String(meta.category || '').toLowerCase()
        if (!EXECUTOR_CATEGORIES.includes(category)) continue
        byCat[category].push({
          name: tl.name,
          description: tl.description || '',
          // 展示用：网关为暴露名加的前缀（self_ 等）剥掉后显示（`name` 仍作完整暴露名 / title）
          server: meta.server || {},
        })
      }
      toolsByCategory.value = byCat
    } catch (e) {
      console.warn('[useToolSandbox] load tools failed:', e)
      toolsByCategory.value = {}
    } finally {
      loading.value = false
    }
  }

  async function loadUserConfig() {
    try {
      const res = await getUserConfig()
      const uc = res.config || res
      const parsed = parseToolSandbox(uc[TOOL_SANDBOX_KEY])
      savedMap.value = { ...parsed }
      workMap.value = { ...parsed }
      // 同一次加载顺带取 server 级沙箱开启数（两页状态互见摘要）
      serverSandboxOn.value = countServerSandboxOn(uc.mcpServers)
    } catch (e) {
      console.warn('[useToolSandbox] load user config failed:', e)
      savedMap.value = {}
      workMap.value = {}
      serverSandboxOn.value = 0
    }
  }

  // 读项目信任目录（prj `security-*`，既有 data-prj-security-list）→ 有效条目数。
  // 失败保持现值（不误报/误清）：仅在成功时更新计数。
  async function loadTrustDirs() {
    try {
      const res = await getProjectSecurity()
      trustDirCount.value = countTrustDirs(res.entries)
    } catch (e) {
      console.warn('[useToolSandbox] load trust dirs failed:', e)
    }
  }

  async function reload() {
    await loadUserConfig()
    await loadTools()
    await loadTrustDirs()
  }

  // 拨动 Switch = 只改本地待保存态（不落库）。
  function setSandbox(category, on) {
    workMap.value = { ...workMap.value, [category]: !!on }
  }

  // 「恢复未设置」= 从待保存态移除该类别键（不落库；末项清空后整键删除由 save() 决定）。
  function restore(category) {
    const next = { ...workMap.value }
    delete next[category]
    workMap.value = next
  }

  // 手动保存：把待保存态写库。无改动 → 不写（返回 false）。全部类别未设置 → 删整键。
  async function save() {
    if (!dirty.value) return false
    const next = pickExecutorMap(workMap.value)
    saving.value = true
    try {
      if (Object.keys(next).length === 0) await resetUserKey(TOOL_SANDBOX_KEY)
      else await saveUserConfig({ [TOOL_SANDBOX_KEY]: next })
      savedMap.value = { ...next }
      workMap.value = { ...next }
      return true
    } finally {
      saving.value = false
    }
  }

  return {
    loading, saving, rows, totalTools, workMap, dirty, anySandboxOn,
    serverSandboxOn, trustDirCount, trustWarning, loadTrustDirs,
    reload, setSandbox, restore, save,
  }
}
