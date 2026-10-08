// 提示词变量插入（OP-12）：在文本光标处插入占位符整串（如 {{toolchain.java}}）。
//
// 纯函数：给定 textarea 元素 + 当前文本 + 待插入串，返回新文本与插入后光标位置；
// 不触碰 DOM 选区（由调用方在下一帧恢复焦点/光标，避免与 v-model 回写竞态）。

/**
 * insertAtCursor 计算插入后的文本与光标位置。
 * - 有 textarea 选区（selectionStart/End）→ 在选区处插入（选中则替换）；
 * - 无 DOM（弹框未聚焦 / 非 textarea）→ 追加到末尾。
 * @param {HTMLTextAreaElement|null} el 目标 textarea（可为 null）
 * @param {string} current 当前文本
 * @param {string} insert 待插入文本
 * @returns {{value: string, caret: number}}
 */
export function insertAtCursor(el, current, insert) {
  const text = current == null ? '' : String(current)
  const token = String(insert == null ? '' : insert)
  let start = text.length
  let end = text.length
  if (el && typeof el.selectionStart === 'number') {
    start = el.selectionStart
    end = typeof el.selectionEnd === 'number' ? el.selectionEnd : start
    if (start > text.length) start = text.length
    if (end > text.length) end = text.length
    if (end < start) end = start
  }
  const value = text.slice(0, start) + token + text.slice(end)
  return { value, caret: start + token.length }
}
