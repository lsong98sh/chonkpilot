import { h } from 'vue'
import { useI18n } from 'vue-i18n'
import { dialog } from '../components/dialog'
import DirPickerDialog from '../views/common/DirPickerDialog.vue'
import { openDirDialog } from '../api/config'
import { isBrowserForm } from '../utils/runtimeForm'
import { dispatchDirPick } from '../utils/dirPicker'

/**
 * 目录选择（统一入口，browser/GUI 双路径；2026-09-19）。
 *
 *   - GUI / native（WebView2）→ 既有 `gui.dir.open-dialog`（**行为不变**）；
 *   - browser（服务端 HTTP 入口托管）→ 服务端等价面 `GET /dirs` 的**只读**选择器
 *     （层级限本 instance 的 work_dir 子树；`work_dir` 由服务端绑定，前端不拼接任意绝对路径）。
 *
 * 用法（在 setup 中调用一次，事件处理器里 await）：
 *   const { pickDir } = useDirPicker()
 *   const path = await pickDir()   // 选中路径；取消 / 未选 = ''
 *
 * 注：native 分支的错误向上抛出（调用方可回退到既有 prompt 逻辑）；browser 分支的
 * 拉取失败在选择器内**明确提示**（不静默），取消/关闭返回 ''。
 */
export function useDirPicker() {
  const { t } = useI18n()

  function browserPick() {
    return new Promise((resolve) => {
      let handle = null
      let done = false
      const finish = (v) => {
        if (done) return
        done = true
        resolve(v || '')
      }
      const close = () => { try { handle && handle.close() } catch (_) {} }
      handle = dialog.show(h(DirPickerDialog, {
        onConfirm: (path) => { finish(path); close() },
        onCancel: () => { finish(''); close() },
        // 标题栏 X / 卸载等外部关闭：仅收口 Promise（不再 close，避免重入）
        onDismiss: () => finish(''),
      }), {
        title: t('common.dir_picker_title'),
        width: 520,
        height: 460,
        closable: true,
      })
    })
  }

  function pickDir() {
    return dispatchDirPick({
      isBrowser: isBrowserForm(),
      // native 分支 = 纯目录选择（gui.dir.open-dialog 无副作用）；「以新窗口打开」由调用方
      // 另走 gui.dir.open（见 Toolbar.handleOpenClick）。
      nativePick: () => openDirDialog().then((res) => (res && res.path) || ''),
      browserPick,
    })
  }

  return { pickDir }
}
