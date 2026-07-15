import { GetAllConfig, SetConfig, GetRecentDirs, OpenDir, OpenDirDialog, GetScenarios, ApplyScenario, GetUserConfig, SaveUserConfig, GetProjectTools, SaveProjectTools, GetProjectAgents, SaveProjectAgents, GetProjectSecurity, SaveProjectSecurity, AnalyzeProject, GetTechInfo, GeneratePrompts, GetPrompt, OptimizeAgentPrompt, RestoreAgent, LoadMissingAgentsFromResource } from '../../wailsjs/go/main/App'
import bridge from '../utils/bridge'

export function getAllConfig() {
  return GetAllConfig()
}

export function setConfig(key, value) {
  return SetConfig(key, value)
}

export function getRecentDirs() {
  return GetRecentDirs()
}

export function saveRecentDir() {
  // Recent manager auto-records on startup; no-op needed
  return Promise.resolve()
}

export function openDir(path) {
  return OpenDir(path)
}

export function openDirDialog() {
  return OpenDirDialog()
}

export function getScenarios() {
  return GetScenarios()
}

export function applyScenario(scenarioId) {
  return ApplyScenario({ scenario_id: scenarioId })
}

export function getUserConfig() {
  return GetUserConfig()
}

export function saveUserConfig(data) {
  return SaveUserConfig(data)
}

export function getProjectTools() {
  return GetProjectTools()
}

export function saveProjectTools(data) {
  return SaveProjectTools({ tools: data })
}

export function getProjectAgents() {
  return GetProjectAgents()
}

export function saveProjectAgents(data) {
  return SaveProjectAgents({ agents: data })
}

export function getProjectSecurity() {
  return GetProjectSecurity()
}

export function saveProjectSecurity(data) {
  return SaveProjectSecurity({ entries: data })
}

export function analyzeProject() {
  return AnalyzeProject()
}

export function getTechInfo() {
  return GetTechInfo()
}

/**
 * 流式生成 Prompts — 通过 Bridge Push 接收事件
 * - onToken: (content) => void
 * - onDone: (prompts) => void
 * - onError: (message) => void
 */
export function generatePrompts(data, onToken, onDone, onError) {
  const unsubs = []
  unsubs.push(bridge.on('generate:token', (payload) => {
    if (onToken) onToken(payload.content)
  }))
  unsubs.push(bridge.on('generate:done', (payload) => {
    unsubs.forEach(fn => fn())
    if (onDone) onDone(payload.prompts || [])
  }))
  unsubs.push(bridge.on('generate:error', (payload) => {
    unsubs.forEach(fn => fn())
    if (onError) onError(payload.message || 'Unknown error')
  }))

  // 启动生成（异步，结果通过 Push 事件返回）
  GeneratePrompts(data).catch(err => {
    unsubs.forEach(fn => fn())
    if (onError) onError(err.message || 'Request failed')
  })

  return { abort: () => unsubs.forEach(fn => fn()) }
}

export function restoreAgent(id) {
  return RestoreAgent(id)
}

export function loadMissingAgentsFromResource() {
  return LoadMissingAgentsFromResource()
}

export function getPrompt(key) {
  return GetPrompt(key)
}

export function setPrompt(key, value) {
  return SetConfig(key, value)
}

/**
 * 全局 MCP Server 配置（存储在 ~/.chonkpilot/config.json）
 */
export async function getUserMCP() {
  const res = await GetUserConfig()
  return (res && res.config && res.config.mcpServers) || []
}

export async function saveUserMCP(servers) {
  return SaveUserConfig({ mcpServers: servers })
}

/**
 * 流式优化 Agent Prompt — 通过 Bridge Push 接收事件
 * - onToken: (content) => void
 * - onDone: (prompt) => void
 * - onError: (message) => void
 */
export function optimizeAgentPrompt(data, onToken, onDone, onError) {
  const unsubs = []
  unsubs.push(bridge.on('optimize:token', (payload) => {
    if (onToken) onToken(payload.content)
  }))
  unsubs.push(bridge.on('optimize:done', (payload) => {
    unsubs.forEach(fn => fn())
    if (onDone) onDone(payload.prompt || '')
  }))
  unsubs.push(bridge.on('optimize:error', (payload) => {
    unsubs.forEach(fn => fn())
    if (onError) onError(payload.message || 'Unknown error')
  }))

  OptimizeAgentPrompt(data).catch(err => {
    unsubs.forEach(fn => fn())
    if (onError) onError(err.message || 'Request failed')
  })

  return { abort: () => unsubs.forEach(fn => fn()) }
}
