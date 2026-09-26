import { h, ref, defineComponent, onUnmounted } from 'vue'
import Button from './Button.vue'
import Input from './Input.vue'
import { dialog } from '../dialog'
import { i18n } from '../../plugins/i18n'

export function confirm(message, title) {
  return new Promise((resolve, reject) => {
    let resolved = false
    // 保存 dialog 句柄，用于点击确定/取消时关闭弹窗
    let handle = null

    const content = defineComponent({
      setup() {
        onUnmounted(() => {
          if (!resolved) {
            // ── 诊断日志（临时）：弹窗未点击即被卸载，打印调用栈定位谁 close 的 ──
            console.warn('[MessageBox] confirm content unmounted before resolution, stack =', new Error().stack)
            reject('cancel')
          }
        })

        return () => h(
          'div',
          { style: { padding: '8px 0' } },
          [
            h(
              'p',
              {
                style: {
                  margin: 0,
                  color: 'var(--text-secondary)',
                  lineHeight: '1.6',
                  fontSize: '14px',
                },
              },
              message,
            ),
            h(
              'div',
              {
                style: {
                  display: 'flex',
                  justifyContent: 'flex-end',
                  gap: '8px',
                  marginTop: '20px',
                },
              },
              [
                h(
                  Button,
                  {
                    size: 'small',
                    onClick: () => {
                      resolved = true
                      reject('cancel')
                      handle?.close()
                    },
                  },
                  i18n.global.t('common.cancel'),
                ),
                h(
                  Button,
                  {
                    size: 'small',
                    type: 'primary',
                    onClick: () => {
                      resolved = true
                      resolve(true)
                      handle?.close()
                    },
                  },
                  i18n.global.t('common.confirm'),
                ),
              ],
            ),
          ],
        )
      },
    })

    // ── 诊断日志（临时）：捕获 dialog.show 抛错，区分「渲染异常」与「被卸载」──
    try {
      handle = dialog.show(content, {
        title: title || i18n.global.t('common.confirm'),
        width: 360,
        minimizable: false,
        collapsible: false,
        resizable: false,
        closable: true,
      })
    } catch (e) {
      console.error('[MessageBox] dialog.show threw:', e)
      reject(e)
    }
  })
}

/**
 * promptInput opens an input dialog and resolves with the entered value.
 * Resolves with `null` when cancelled. Replaces window.prompt(), which is
 * not supported by WebView2 (it returns null immediately without showing).
 */
export function promptInput(placeholder, initialValue = '') {
  return new Promise((resolve, reject) => {
    let resolved = false
    let handle = null
    const value = ref(initialValue)

    const finish = (val) => {
      if (resolved) return
      resolved = true
      resolve(val)
      handle?.close()
    }

    const content = defineComponent({
      setup() {
        onUnmounted(() => {
          if (!resolved) reject('cancel')
        })

        return () => h(
          'div',
          { style: { padding: '8px 0' } },
          [
            h(Input, {
              modelValue: value.value,
              placeholder,
              'onUpdate:modelValue': (v) => { value.value = v },
              autofocus: true,
              style: { width: '100%' },
              onKeydown: (e) => {
                if (e.key === 'Enter') finish(value.value)
                if (e.key === 'Escape') finish(null)
              },
            }),
            h(
              'div',
              {
                style: {
                  display: 'flex',
                  justifyContent: 'flex-end',
                  gap: '8px',
                  marginTop: '16px',
                },
              },
              [
                h(
                  Button,
                  { size: 'small', onClick: () => finish(null) },
                  i18n.global.t('common.cancel'),
                ),
                h(
                  Button,
                  {
                    size: 'small',
                    type: 'primary',
                    onClick: () => finish(value.value),
                  },
                  i18n.global.t('common.confirm'),
                ),
              ],
            ),
          ],
        )
      },
    })

    handle = dialog.show(content, {
      title: placeholder,
      width: 360,
      minimizable: false,
      collapsible: false,
      resizable: false,
      closable: true,
    })
  })
}
