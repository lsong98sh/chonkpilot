/**
 * agent 工具过滤开关语义（G-45 ①，2026-09-25 拍板 A）。
 *
 * 背景（见 41 G-45 ① / 42 §2 (165)）：`filterTools` **不持久化** —— 门面 DTO
 * （`facade/scenario.go` `ScenarioAgent`）与 `capfs/scenario.go` **均无该字段**，
 * 后端只能以「**`tools` 非空 = 白名单**」为判据（`tools` 空 = **不限制**）。
 * ⇒ 若用户关掉过滤开关而 `tools` 数组残留 → 后端**仍静默限制**，与用户意图不符。
 *
 * 收口口径：**关闭时清空 `tools`**（不改 DTO / 不改消息契约）；**打开时不清空**
 * （保留用户既有选择供继续编辑）。
 *
 * G-45 ⑤c（同日）：因 `filterTools` 不持久化，「**过滤开启且 `tools` 非空**」保存后重新载入时
 * 勾选框会显示为**关**（提示「所有工具均可使用」）而后端**仍按白名单限制** → **显示面误导**。
 * ⇒ **载入时按 `tools` 反推回填** `filterTools`（`filterToolsLoadPatch`），使显示态与后端行为一致。
 *
 * 两个纯函数互为反向、必须成对使用：
 *   - 载入（持久化 → 表单）：`filterToolsLoadPatch`（`tools` 非空 → `true`）
 *   - 交互（表单 → 存储）：`filterToolsTogglePatch`（关闭 → `tools: []`）
 */

/**
 * filterToolsTogglePatch 计算「切换工具过滤开关」应施加到 agent 上的**字段补丁**
 * （纯函数，**不改入参**）：
 *   - 关闭：`{ filterTools: false, tools: [] }` —— 清空白名单 = 后端「不限制」语义
 *   - 打开：`{ filterTools: true }` —— **不含 `tools` 键**，保留既有选择
 *
 * @param {{filterTools?: boolean, tools?: unknown}} agent 当前 agent
 * @returns {{filterTools: boolean, tools?: string[]}} 字段补丁
 */
export function filterToolsTogglePatch(agent) {
  const enabling = !(agent && agent.filterTools)
  return enabling ? { filterTools: true } : { filterTools: false, tools: [] }
}

/**
 * normalizeTools 把 `agent.tools` 归一为数组（纯函数）：数组原样返回；兼容 legacy
 * JSON 字符串（解析后仍为数组才采纳）；其余（`undefined`/`null`/数字/对象/非法 JSON）→ `[]`。
 *
 * @param {unknown} tools
 * @returns {string[]} 工具名数组
 */
export function normalizeTools(tools) {
  if (Array.isArray(tools)) return tools
  if (typeof tools === 'string') {
    try {
      const parsed = JSON.parse(tools)
      return Array.isArray(parsed) ? parsed : []
    } catch {
      return []
    }
  }
  return []
}

/**
 * filterToolsLoadPatch 计算「**载入** agent」应施加到 agent 上的**字段补丁**
 * （纯函数，**不改入参**）：`filterTools` **不持久化** → 按 `tools` **反推回填**，
 * 判据 = `tools.length > 0`（与后端「`tools` 非空 = 白名单」判据、及
 * `filterToolsTogglePatch`「关闭即清空 `tools`」自洽）。一并归一 `tools` 为数组。
 *
 * 注：**只在载入时调用一次**（不做持续派生）—— 否则「打开开关但尚未勾选任何工具」
 * （`tools: []`）会被立刻反推成关闭，破坏 `filterToolsTogglePatch`「打开不清空」语义。
 *
 * @param {{tools?: unknown}} agent 载入自持久化的 agent
 * @returns {{tools: string[], filterTools: boolean}} 字段补丁
 */
export function filterToolsLoadPatch(agent) {
  const tools = normalizeTools(agent && agent.tools)
  return { tools, filterTools: tools.length > 0 }
}
