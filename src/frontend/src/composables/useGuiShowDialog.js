import { h } from 'vue'
import { useI18n } from 'vue-i18n'
import { dialog } from '../components/dialog'
import DirPickerDialog from '../views/common/DirPickerDialog.vue'
import mq from '../utils/mq'
import { MsgTopics } from '../events/msgkeys.js'
import { parseShowDialog, selectedDirEvent } from '../utils/guiShowDialog.js'

/**
 * gui.show-dialog 消费（🆕 2026-10-10，browser 形态）：服务端无法自行弹原生框时下发本事件，
 * 请 UI 渲染选择器；用户选定 / 取消后由 UI 自行发 `gui.selected-dir{widget_id, status, path?}`
 * （前端 `utils/guiAsync.js::selectDir` 的 Promise 据此按 `widget_id` 关联收口）。
 *
 * 入口：`App.vue` setup 调用一次（全局单例，模块级只订阅一次）。用 `mq.on` 订阅下行事件
 * （**不用 watch**，见 .trae/rules「状态管理」）；`kind="select-dir"` 时弹自研 `DialogShell`
 * 承载的目录选择器（`DirPickerDialog`，options 驱动）。防二次弹框：同一 `widget_id` 已在弹框中
 * 则忽略重复事件（控件维度防二次点击另由 `useDirPicker` 的在飞 Promise 保证）。
 */
let subscribed = false
const openDialogs = new Map() // widget_id → 弹框句柄（在飞去重）

export function useGuiShowDialog() {
  const { t } = useI18n()
  if (subscribed) return
  subscribed = true
  mq.on(MsgTopics.guiShowDialog, (payload) => {
    const view = parseShowDialog(payload)
    if (!view) return
    const widgetId = view.widgetId
    if (openDialogs.has(widgetId)) return // 已在弹框中 → 忽略重复（防二次弹框）

    let handle = null
    let done = false
    const close = () => { try { handle && handle.close() } catch (_) {} }
    // answer：收口一次即可 —— 发 gui.selected-dir（选中 / 取消），并解除本 widget 的登记。
    const answer = (path) => {
      if (done) return
      done = true
      openDialogs.delete(widgetId)
      mq.emit(MsgTopics.guiSelectedDir, selectedDirEvent(widgetId, path))
    }
    handle = dialog.show(h(DirPickerDialog, {
      options: view.options,
      root: view.root,
      onConfirm: (path) => { answer(path); close() },
      onCancel: () => { answer(''); close() },
      // 标题栏 X / 外部卸载：仅收口事件（不再 close，避免重入）；等价于「取消」。
      onDismiss: () => answer(''),
    }), {
      title: t('common.dir_picker_title'),
      width: 520,
      height: 460,
      closable: true,
    })
    openDialogs.set(widgetId, handle)
  })
}
