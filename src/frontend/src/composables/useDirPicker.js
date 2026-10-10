import { openDirDialog } from '../api/config'

/**
 * 目录选择（统一入口；2026-10-10 收敛为**单一两段式路径**，双形态共用同一消息面）。
 *
 * 契约（61 §1）：请求 `gui.select-dir{widget_id}` → 结果事件 `gui.selected-dir{widget_id, status, path?}`。
 *   - GUI / native（WebView2）：壳弹系统原生目录框后发结果；
 *   - browser（服务端 HTTP 入口托管）：**服务端**无原生框 → 下发 `gui.show-dialog{widget_id,
 *     kind:"select-dir", options}`；由 `useGuiShowDialog`（App.vue 全局订阅）弹自研 DialogShell
 *     选择器，用户选定 / 取消后发 `gui.selected-dir`。两形态**同消息面、同关联键**（无双路径）。
 *
 * 用法（在 setup 中调用一次，事件处理器里 await）：
 *   const { pickDir } = useDirPicker('toolbar-open')   // 参数 = 控件标识（widget_id）
 *   const path = await pickDir()   // 选中路径；取消 / 未选 = ''
 *
 * widget_id：两段式 select-dir/selected-dir 的关联键，**每个选择控件须不同**；同一 composable 内
 * 并发调用同一控件 → 复用同一在飞 Promise（**防二次点击**：不重复弹框）。失败向上抛出（调用方可回退）。
 */
export function useDirPicker(widgetId = 'dir') {
  // 在飞 Promise（本控件维度）：防二次点击 —— 重复点击复用同一 Promise，不重复弹框。
  let inflight = null

  function pickDir() {
    if (inflight) return inflight
    inflight = openDirDialog(widgetId)
      .then((res) => (res && res.path) || '')
      .finally(() => { inflight = null })
    return inflight
  }

  return { pickDir }
}
