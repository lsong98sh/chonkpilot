/**
 * useToolAsyncMode — 工具 async 模式查询（G-17：仅 manual 工具显示「转后台」按钮）
 *
 * 数据源 = **客户端能力面** tools-list（61-消息一览 §4.5；桥直取 gateway `mcp-tools-list`，
 * 复用既有消息/字段，非消息面变更）：每项带 `_meta.async`（契约 [meta] async，
 * 缺省 auto，见 72-工具开发规范）。
 *
 * 本 composable 是**模块级单例**：全应用懒加载一次并缓存；manual 工具名集合以 ref 暴露，
 * computed 中调用 isManualTool 会读取该 ref → 加载完成后自动刷新（无需 watch）。
 */
import { ref } from 'vue'
import mq from '../utils/mq'

// manualTools：async=manual 的工具名集合（加载前为空 → 按钮不显示；server 侧另有硬门控兜底）
const manualTools = ref(new Set())
let loading = null // 进行中的加载 promise（并发调用去重）

async function loadManualTools() {
  try {
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const tools = res && Array.isArray(res.tools) ? res.tools : []
    const names = new Set()
    for (const tl of tools) {
      const meta = (tl && tl._meta) || {}
      if (meta.async === 'manual' && tl.name) names.add(tl.name)
    }
    manualTools.value = names
  } catch (_) {
    // 拉取失败保持空集：按钮不显示（server 侧 onTaskBackground 硬门控兜底）
  }
}

// useToolAsyncMode 返回 { isManualTool }；isManualTool 读模块级 manualTools ref（可响应式追踪）。
export function useToolAsyncMode() {
  if (!loading) loading = loadManualTools()
  return {
    isManualTool: (name) => !!name && manualTools.value.has(name),
  }
}
