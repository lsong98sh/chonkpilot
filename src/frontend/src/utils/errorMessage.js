/**
 * 错误「人话化」分类映射（用户视角缺陷批 2 – 错误呈现面，2026-09-20）。
 *
 * 会话错误气泡原先直出 `Error: <原始串>`（`utils/sessionMessages.js:252`），
 * 工具失败卡片里也直出 `错误: <原始串>`（`chonkpilot-llm/server/turn.go:548/583/614`）
 * → 用户会看到 `context deadline exceeded`、`-32602`、`exit status 1` 这类不可理解的串。
 *
 * 本模块只做「原始错误串 → i18n key + 原始详情」的**纯映射**：
 * **不丢弃原始串**（诊断需要）——展示层以人话文案为主，原始错误折叠可查、可复制。
 *
 * 类别判定依据 = 仓库实际会出现的错误形态（grep 核实）：
 *   - LLM 侧：`*LLMError.Error()` = `[<kind>] <msg>`，kind ∈ network/timeout/rate_limit/
 *     server/auth/protocol（`chonkpilot-llm/server/llm.go:44-64`），msg 形如
 *     `llm http 401: …` / `Post "http://…": context deadline exceeded`；
 *   - gateway：JSON-RPC 码 -32700/-32602/-32601/-32000
 *     （`chonkpilot-mcp-gateway/gateway/mcpgateway.go`）；
 *   - 进程退出：`exit status N`（Go `exec.ExitError`；
 *     `chonkpilot-mcp-tools/internal/scriptrun/exec.go:176`）；
 *   - 轮次错误码：DB_ERROR / TOOL_LOOP_LIMIT / EMPTY_REPLY
 *     （`chonkpilot-llm/server/turn.go:384,467`、`server.go:1794`）。
 */

/**
 * 分类判定表（**数组顺序 = 优先级**，先匹配先返回）：
 * 超时先于网络（超时串常同时含 dial 信息）、磁盘/权限先于协议（避免误吃数字）。
 * `pick` 可选：从原文提取展示参数（提取不到给有意义的兜底，不留空占位）。
 */
const RULES = [
  {
    key: 'chat.error_timeout',
    re: /context deadline exceeded|i\/o timeout|timeout exceeded|request timed out|\btimed out\b|\btimeout\b|\[timeout\]|超时/i,
  },
  {
    key: 'chat.error_network',
    re: /ECONNREFUSED|ECONNRESET|EHOSTUNREACH|ENETUNREACH|connection refused|connection reset|no such host|network is unreachable|dial tcp|unexpected EOF|broken pipe|\[network\]/i,
  },
  {
    key: 'chat.error_auth',
    re: /\[auth\]|invalid api key|incorrect api key|unauthorized|forbidden|\b40[13]\b/i,
    pick: (s) => ({ status: firstGroup(s, /\b(401|403)\b/, '401/403') }),
  },
  {
    key: 'chat.error_rate_limit',
    re: /\[rate_limit\]|rate ?limit|too many requests|\b429\b/i,
    pick: () => ({ status: '429' }),
  },
  {
    key: 'chat.error_disk',
    re: /no space left|ENOSPC|disk full|磁盘空间/i,
  },
  {
    key: 'chat.error_permission',
    re: /permission denied|access is denied|EPERM|EACCES|拒绝访问|没有权限/i,
  },
  {
    key: 'chat.error_process',
    re: /exit status\s+\d+|（exit\s+\d+）|exit code\s+\d+/i,
  },
  {
    key: 'chat.error_protocol',
    re: /-3\d{4}\b|\[protocol\]|parse error|invalid params|method not found|\bjsonrpc\b/i,
  },
  {
    key: 'chat.error_server',
    re: /\[server\]|llm http 5\d\d|\b50[0-4]\b/i,
    pick: (s) => ({ status: firstGroup(s, /llm http (5\d\d)/i, '5xx') }),
  },
]

/** 取首个捕获组；无匹配 → fallback（避免文案里留空占位） */
function firstGroup(s, re, fallback) {
  const m = s.match(re)
  return (m && m[1]) || fallback
}

/** 轮次错误码（无 message 时按 code 归类；`chonkpilot-llm/server/turn.go`、`server.go`） */
const CODE_KEYS = {
  DB_ERROR: 'chat.error_db',
  TOOL_LOOP_LIMIT: 'chat.error_tool_loop',
  EMPTY_REPLY: 'chat.empty_reply', // 复用既有空回复文案（同样提示「继续」）
}

/** 本模块可能返回的全部 i18n key（供 i18n 齐备性校验 / 维护清点） */
export const ERROR_KEYS = [
  ...RULES.map((r) => r.key),
  ...Object.values(CODE_KEYS),
  'chat.error_unknown',
]

/** 取原始错误串（string / 其它原始值 / Error / `{message, code}` 四种入参形态）。 */
function rawDetail(input) {
  if (input == null) return ''
  if (typeof input === 'string' || typeof input !== 'object') return String(input)
  if (input instanceof Error) return input.message || String(input)
  return String(input.message || input.code || '')
}

/**
 * 把原始错误归类为「人话 i18n key + 原始详情」。
 * @param {string|Error|{message?: string, code?: string}} input - 原始错误（串 / Error / llm-complete 事件）
 * @returns {{key: string, params: object, detail: string}} key = i18n 键（`chat.error_*`）；
 *   params = 文案占位参数（可能为空对象）；detail = **原样保留**的原始错误（不裁剪、不丢弃）
 */
export function classifyError(input) {
  const detail = rawDetail(input)
  const text = detail.trim()
  const code = (typeof input === 'object' && input) ? String(input.code || '').trim() : ''

  if (text) {
    for (const r of RULES) {
      if (r.re.test(text)) return { key: r.key, params: r.pick ? r.pick(text) : {}, detail }
    }
  }
  if (code && CODE_KEYS[code]) return { key: CODE_KEYS[code], params: {}, detail: detail || code }
  return { key: 'chat.error_unknown', params: {}, detail: detail || code }
}
