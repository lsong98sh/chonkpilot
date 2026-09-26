/**
 * useToolSandbox — agentbox 沙箱「扫描到的工具」页签（usr 键 `tool_sandbox`）
 *
 * 决策 [42 §2 (104)/(109)]：agentbox = 内存 MCP + 执行层，**仅对「扫描到的 runtime」**
 * （chonkpilot-mcp-tools 的 core / desktop / browser 执行器工具）施加限制。
 *
 * 读写面（**零新增 MQ 主题**）：
 *   - 工具清单 = 既有客户端能力面 `tools-list`（与 SettingsToolAsyncPage 同一消费点）；每项
 *     `_meta.category` = 契约 [meta] category（core/desktop/browser/server/codegraph/vfts/meta…），
 *     `tools-list` 的 `name` = 工具暴露名（内嵌 self 节点为 `self_<契约名>`）。
 *   - 配置读写 = 既有 usr 配置面 `data-user-config-{load,save,delete}`（api/config.js）。
 * 存储形态：usr 自由键 `tool_sandbox` = JSON 对象 `{"<工具暴露名>": true|false}`
 *   - 未配置 / `false` = **不隔离**（缺省与引入前行为一致）；
 *   - 「恢复未设置」= 删除该工具键项（最后一项删除后整键删除，走 `data-user-config-delete`）。
 *
 * 本 composable 只做数据与读写，不弹提示（提示由页面按 i18n 决定）。
 */
import { ref, computed } from 'vue'
import { getUserConfig, saveUserConfig, resetUserKey, getProjectSecurity } from '../api/config'
import { countServerSandboxOn } from '../utils/sandboxSummary'
import { countTrustDirs, needsTrustDirsWarning } from '../utils/sandboxTrust'
import { isThirdPartyProvider } from '../utils/toolSource'
import mq from '../utils/mq'

// usr 配置键（自由键通道）：值与 tool_async 同形态 = JSON 对象文本。
export const TOOL_SANDBOX_KEY = 'tool_sandbox'
// 「扫描到的 runtime 工具」判据 = 契约 meta.category ∈ {core, desktop, browser}
// （core/desktop/browser = chonkpilot-mcp-tools 的三个执行器能力目录；其余类别不属 agentbox
//  约束面 → 不列出，避免误配）。
export const RUNTIME_CATEGORIES = ['core', 'desktop', 'browser']

// 解析 usr tool_sandbox：允许对象形态与 JSON 字符串形态（自由键按字符串回读）。
function parseToolSandbox(v) {
  let obj = v
  if (typeof obj === 'string') {
    try { obj = JSON.parse(obj) } catch (_) { return {} }
  }
  return obj && typeof obj === 'object' && !Array.isArray(obj) ? obj : {}
}

export function useToolSandbox() {
  const loading = ref(false)
  const groups = ref([]) // [{ key, tools: [{ name, description, category, on, userSet }] }]
  const userMap = ref({}) // 工具名 → true|false（显式设置项）
  // server 级（mcps[].sandbox，仅 stdio）已开启数量 —— 供「两页状态互见」摘要（读 usr 同一次加载）。
  const serverSandboxOn = ref(0)
  // 项目信任目录有效条目数（prj `security-*`）——供 ②「空目录 = 全拒」内联预警。
  const trustDirCount = ref(0)

  // 是否至少有一个工具级开关已开启（"已开启沙箱"）。
  const anySandboxOn = computed(() =>
    Object.values(userMap.value).some((v) => v === true)
  )
  // ② 已开启沙箱 且 信任目录为空 → 显著预警（空允许集 = 全拒）。
  // 目录变更经 loadTrustDirs（订阅 data-prj-security-refresh）即时重算 → 警告自动消失。
  const trustWarning = computed(() =>
    needsTrustDirsWarning({ sandboxOn: anySandboxOn.value, trustDirCount: trustDirCount.value })
  )

  // 用户配置 → 行生效值（未配置的行 = 未设置 / 不隔离）
  function applyUserConfig() {
    for (const g of groups.value) {
      for (const row of g.tools) {
        const v = userMap.value[row.name]
        row.userSet = v === true || v === false
        row.on = v === true
      }
    }
  }

  async function loadTools() {
    loading.value = true
    try {
      const env = await mq.emit('tools-list', {})
      const res = env && env.backend && env.backend.result
      const tools = res && Array.isArray(res.tools) ? res.tools : []
      const byServer = new Map()
      for (const tl of tools) {
        const meta = (tl && tl._meta) || {}
        const category = String(meta.category || '').toLowerCase()
        if (!RUNTIME_CATEGORIES.includes(category)) continue
        const srv = meta.server || {}
        const key = srv.alias || srv.node || 'global'
        if (!byServer.has(key)) byServer.set(key, [])
        byServer.get(key).push({
          name: tl.name,
          description: tl.description || '',
          category,
          // 展示用：网关为暴露名加的前缀（self_/dir_ 等）剥掉后显示（`row.name` 仍作配置键）
          server: srv,
          // 来源判定（I-109）：第三方（spawned/proxied）不可施加 agentbox 沙箱 → 页面标注并禁用。
          // 无 `_meta.server` → null（无法判定，不标注；宁缺勿错）。
          thirdParty: isThirdPartyProvider(meta),
          on: false,
          userSet: false,
        })
      }
      groups.value = [...byServer.entries()].map(([key, list]) => ({ key, tools: list }))
      applyUserConfig()
    } catch (e) {
      console.warn('[useToolSandbox] load tools failed:', e)
      groups.value = []
    } finally {
      loading.value = false
    }
  }

  async function loadUserConfig() {
    try {
      const res = await getUserConfig()
      const uc = res.config || res
      userMap.value = parseToolSandbox(uc[TOOL_SANDBOX_KEY])
      // 同一次加载顺带取 server 级沙箱开启数（两页状态互见摘要）
      serverSandboxOn.value = countServerSandboxOn(uc.mcpServers)
    } catch (e) {
      console.warn('[useToolSandbox] load user config failed:', e)
      userMap.value = {}
      serverSandboxOn.value = 0
    }
    applyUserConfig()
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

  // saveUserConfig 是**增量合并**（只写本键，不影响其它配置）。
  function persist() {
    return saveUserConfig({ [TOOL_SANDBOX_KEY]: userMap.value })
  }

  // 拨动即写库（true/false）
  async function setSandbox(row, on) {
    row.on = !!on
    row.userSet = true
    userMap.value = { ...userMap.value, [row.name]: !!on }
    await persist()
  }

  // 恢复未设置 = 删除该工具键项；最后一项删除后整键删除（不留空对象配置）。
  async function restore(row) {
    const next = { ...userMap.value }
    delete next[row.name]
    userMap.value = next
    if (Object.keys(next).length === 0) await resetUserKey(TOOL_SANDBOX_KEY)
    else await persist()
    row.on = false
    row.userSet = false
  }

  return {
    loading, groups, userMap, serverSandboxOn,
    trustDirCount, anySandboxOn, trustWarning, loadTrustDirs,
    reload, setSandbox, restore,
  }
}
