/**
 * 输入框 IME 守卫（2026-11-17）：
 *   - contenteditable 绑定 compositionstart / compositionend，记录组合输入状态；
 *   - Enter keydown 在 `isComposing` / `keyCode 229` / 显式 composing 时直接返回；
 *   - 守卫必须位于 `handleSend()` 之前，避免中文选词 Enter 误发送。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readSrc } from './support/feSource.mjs'

const src = readSrc('views/chat/InputBox.vue')

function fnBody(name) {
  const start = src.indexOf('function ' + name + '(')
  assert.ok(start >= 0, `未找到 ${name}`)
  const end = src.indexOf('\nfunction ', start + 1)
  return src.slice(start, end < 0 ? undefined : end)
}

test('contenteditable 绑定 composition 生命周期', () => {
  assert.match(src, /@compositionstart="onCompositionStart"/)
  assert.match(src, /@compositionend="onCompositionEnd"/)
  assert.match(src, /@blur="onCompositionEnd"/)
  assert.match(src, /const composing = ref\(false\)/)
})

test('Enter 在 IME 组合输入中不发送', () => {
  const body = fnBody('handleKeydown')
  assert.match(body, /if \(e\.isComposing \|\| e\.keyCode === 229 \|\| composing\.value\) return/)
  const guard = body.indexOf('isComposing')
  const send = body.indexOf('handleSend()')
  assert.ok(guard >= 0 && send > guard, 'IME 守卫必须先于 handleSend()')
})

test('composition 开始/结束正确切换状态', () => {
  assert.match(fnBody('onCompositionStart'), /composing\.value = true/)
  assert.match(fnBody('onCompositionEnd'), /composing\.value = false/)
})