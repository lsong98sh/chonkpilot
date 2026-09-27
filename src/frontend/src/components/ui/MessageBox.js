import { h, ref, defineComponent, onUnmounted } from 'vue'
import Button from './Button.vue'
import Input from './Input.vue'
import Textarea from './Textarea.vue'
import KeyValueEditor from './KeyValueEditor.vue'
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
                  i18n.global.t('common.ok'),
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
        title: title || i18n.global.t('dialog.confirm_title'),
        width: 360,
        // 瞬时确认类：高度随内容自适应（不套用弹窗默认固定高）
        height: 'auto',
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
 * options.multiline = true → 多行 Textarea（Enter 换行、仅 Esc 取消）；缺省单行 Input。
 */
export function promptInput(placeholder, initialValue = '', options = {}) {
  return new Promise((resolve, reject) => {
    let resolved = false
    let handle = null
    const multiline = !!options.multiline
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
            multiline
              ? h(Textarea, {
                modelValue: value.value,
                placeholder,
                rows: 6,
                autofocus: true,
                style: { width: '100%' },
                'onUpdate:modelValue': (v) => { value.value = v },
                onKeydown: (e) => { if (e.key === 'Escape') finish(null) },
              })
              : h(Input, {
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
                  i18n.global.t('common.ok'),
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
      // 瞬时输入类：高度随内容自适应（不套用弹窗默认固定高）
      height: 'auto',
      minimizable: false,
      collapsible: false,
      resizable: false,
      closable: true,
    })
  })
}

// splitListRows 行文本 → 去空行/去首尾空白的字符串数组。
function splitListRows(text) {
  return String(text || '').split('\n').map((s) => s.trim()).filter(Boolean)
}

/**
 * promptList opens a row-editor dialog (KeyValueEditor mode="list") and resolves with
 * the array of non-empty rows. Resolves with `null` when cancelled. 供「枚举值」等
 * 「每行一条」的行编辑使用（与 promptInput 同一弹窗/收口链路，零新增 MQ 主题）。
 * options: valuePlaceholder / addLabel / deleteLabel。
 */
export function promptList(title, values, options = {}) {
  return new Promise((resolve, reject) => {
    let resolved = false
    let handle = null
    const text = ref((Array.isArray(values) ? values : []).map((v) => String(v)).join('\n'))

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
            h(KeyValueEditor, {
              modelValue: text.value,
              mode: 'list',
              valuePlaceholder: options.valuePlaceholder || '',
              addLabel: options.addLabel || '',
              deleteLabel: options.deleteLabel || '',
              'onUpdate:modelValue': (v) => { text.value = v },
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
                    onClick: () => finish(splitListRows(text.value)),
                  },
                  i18n.global.t('common.ok'),
                ),
              ],
            ),
          ],
        )
      },
    })

    handle = dialog.show(content, {
      title,
      width: 420,
      // 瞬时行编辑类：高度随内容自适应（超出由 .dialog-body 内滚动）
      height: 'auto',
      minimizable: false,
      collapsible: false,
      resizable: false,
      closable: true,
    })
  })
}
