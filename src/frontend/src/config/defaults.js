// 全局配置默认值常量：设置页各编辑弹窗的 UI 回退默认值。
// 用户已保存的配置（usr 库）优先，这些值仅在无配置时兜底。

// LLM 协议类型：openai = OpenAI 兼容 Chat Completions（默认）；
// responses = OpenAI Responses API（含 DeepSeek /responses，P1-2）。
export const DEFAULT_LLM_PROTOCOL = 'openai'
export const RESPONSES_LLM_PROTOCOL = 'responses'

// 新增 LLM 的默认值
export const DEFAULT_LLM = {
  name: '',
  protocol: DEFAULT_LLM_PROTOCOL,
  apiKey: '',
  model: 'deepseek-v4-flash',
  baseUrl: 'https://api.deepseek.com',
  temperature: 0.7,
  maxOutputToken: 4096,
  // 上下文窗口大小（口径 Z1，2026-09-25）：兜底归并的窗口来源；0 = 不启用兜底。
  // 默认 128000 = 当前主流大模型（GPT-4o/Claude 3.5/DeepSeek 等）的通用上下文窗口量级；
  // 仅作新增表单初值，不代表任何具体模型的窗口映射（不硬编码模型→窗口表）。
  maxContextToken: 128000,
  thinking: true,
  reasoningEffort: '',
  maxToolIterations: 20,
  // 模型能力（多选，声明式）：reasoning = 推理 / vision = 图形（图片输入）。
  // 未声明「vision」→ 聊天窗口禁用截图（前端按所选 provider 的该字段判定）。
  capabilities: [],
}

// 新增 MCP 服务器的默认值（默认禁用，12-数据层；字段对齐 servers.list 规范）
export const DEFAULT_MCP = {
  name: '',
  runtime: '',
  args: [],
  url: '',
  transport: '',
  enabled: false,
  description: '',
  namespace: '',
  cwd: '',
  timeout: 0,
  env: [],
  headers: {},
  hot_tools: [],
  // 按项目（workdir）隔离连接：null = 未设置（gateway 按 transport 推断：stdio → 隔离 / http|sse → 共享）
  isolate: null,
  // agentbox 沙箱（仅 stdio 的 spawn 子进程生效）：null = 未设置（缺键 = 不隔离）
  sandbox: null,
}

