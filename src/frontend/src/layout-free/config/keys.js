/**
 * 预置变量（key）列表。
 * 变量由 store 管理，响应式；多实例 view 必须从中选择一个配套 key。
 */
export const KEY_DEFINITIONS = [
  { key: 'project', label: '当前项目', description: '当前打开的项目标识', defaultValue: 'layout-free' },
  { key: 'taskId', label: '任务 ID', description: '任务详情视图使用的任务标识', defaultValue: 'T-1001' },
  { key: 'filePath', label: '文件路径', description: '文件浏览器 / 编辑器定位的文件', defaultValue: 'src/main.js' },
  { key: 'sessionId', label: '会话 ID', description: '对话会话标识', defaultValue: 'S-01' },
  { key: 'mcpServer', label: 'MCP 服务', description: 'MCP 配置页定位的服务名', defaultValue: 'filesystem' },
  { key: 'model', label: '模型', description: 'LLM 配置页定位的模型', defaultValue: 'deepseek-chat' },
  { key: 'tabGroup', label: '标签组', description: '自由 tabs 区的分组标识', defaultValue: 'main' },
  { key: 'searchBox', label: '搜索框', description: '内嵌搜索框（视图条目）的实例标识', defaultValue: 'main' },
]
