/**
 * useUnsavedMark — 设置面「未保存」可视标记（仅显示，不改保存时机、不拦截）。
 *
 * 背景（用户视角缺陷批 2 · 设置面 ⑤）：全仓无 dirty 提示。本 composable 只提供
 * `dirty` 状态与两个动作，供页面在页头渲染小圆点/「未保存」文字：
 *   - markDirty()：有尚未落库的改动（失焦即存页 = 编辑中；按钮/对话框页 = 与已保存值不一致）；
 *   - markSaved()：落库成功（或撤销回已保存值）后清除。
 * **不改变任何页面的保存时机、不做离开拦截弹窗**（约束见任务 ⑤）。
 */
import { ref } from 'vue'

export function useUnsavedMark() {
  const dirty = ref(false)
  return {
    dirty,
    markDirty: () => { dirty.value = true },
    markSaved: () => { dirty.value = false },
  }
}
