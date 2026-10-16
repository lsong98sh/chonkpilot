/**
 * 区域内容的「设计态 / 运行态」判定。
 *
 * 设计态（preview）：view 渲染为只读样式（design prop = true），可以看到效果但无法操作；
 *   区域标题由布局容器绘制在区域顶部（仅设计态显示）。
 * 运行态：view 提供完整交互能力，且不绘制任何区域标题。
 *
 * 设计态由 props 显式向下传递（LfFreeLayoutContainer → LfLayoutNodeRenderer → LfLayoutSplit/Leaf），
 * 同时 provide 一份上下文，便于 view 内部（useDesign）读取。
 */
import { inject, computed } from 'vue'

export const LAYOUT_CONTEXT = Symbol('layout-free-context')

/** view 组件内部使用：是否为设计态（只读渲染）。组件亦可用自己的 design prop。 */
export function useDesign() {
  const design = inject(LAYOUT_CONTEXT, null)
  return computed(() => design?.value === true)
}
