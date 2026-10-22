/**
 * INPUTVAL-1 前端校验规则（纯函数 + 关键接入点 source-guard）。
 *
 * 口径：docs/input-validation/输入校验专题.md §3（v1 · 2026-10-21 已拍板）。
 * 运行器 = node:test（与既有前端测试同法；`npm test`）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  MAX_NAME, MAX_TITLE, MAX_URL, MAX_PATH, MAX_TEXT, MAX_CHAT_CHARS,
  MAX_ATTACHMENT_BYTES, MAX_ATTACHMENTS,
  charLen, tooLong, isHttpUrl, isAbsolutePath, isIdentifier, isDirId,
  hasControlChar, intInRange, attachmentTooLarge, attachmentCountExceeded,
} from '../src/utils/fieldRules.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')

test('① 基线：与专题 §3 的长度 / 数值 / 附件口径一致', () => {
  assert.equal(MAX_NAME, 64)
  assert.equal(MAX_TITLE, 128)
  assert.equal(MAX_URL, 2048)
  assert.equal(MAX_PATH, 512)
  assert.equal(MAX_TEXT, 262144)
  assert.equal(MAX_CHAT_CHARS, 16384)
  assert.equal(MAX_ATTACHMENT_BYTES, 20 * 1024 * 1024)
  assert.equal(MAX_ATTACHMENTS, 9) // 单条 < 10
})

test('② charLen 按 Unicode 码点计数（代理对算 1）', () => {
  assert.equal(charLen('abc'), 3)
  assert.equal(charLen('中文🙂'), 3)
  assert.equal(tooLong('x'.repeat(65), MAX_NAME), true)
  assert.equal(tooLong('x'.repeat(64), MAX_NAME), false)
})

test('③ URL / 路径 / 标识符', () => {
  assert.equal(isHttpUrl('https://api.example.com/v1'), true)
  assert.equal(isHttpUrl('http://x'), true)
  assert.equal(isHttpUrl('ftp://x'), false)
  assert.equal(isHttpUrl('api.example.com'), false)

  assert.equal(isAbsolutePath('E:\\work\\proj'), true)
  assert.equal(isAbsolutePath('\\\\server\\share'), true)
  assert.equal(isAbsolutePath('/opt/app'), true)
  assert.equal(isAbsolutePath('relative/dir'), false)
  assert.equal(isAbsolutePath(''), false)

  assert.equal(isIdentifier('mcp_server1'), true)
  assert.equal(isIdentifier('1bad'), false)
  assert.equal(isDirId('scen-01_x'), true)
  assert.equal(isDirId('bad id'), false)
})

test('④ 控制字符 / 数值范围 / 附件上限', () => {
  assert.equal(hasControlChar('a\u0000b'), true)
  assert.equal(hasControlChar('正常文本'), false)
  assert.equal(intInRange('5', 1, 10), true)
  assert.equal(intInRange('0', 1, 10), false)
  assert.equal(intInRange('', 1, 10), true)
  assert.equal(attachmentTooLarge(20 * 1024 * 1024 + 1), true)
  assert.equal(attachmentTooLarge(1024), false)
  assert.equal(attachmentCountExceeded(10), true)
  assert.equal(attachmentCountExceeded(9), false)
})

test('⑤ 接入点：Input / Textarea 透传原生约束', () => {
  const input = read('components/ui/Input.vue')
  for (const p of ['maxlength', 'minlength', 'pattern', 'required']) {
    assert.match(input, new RegExp(`:${p}="${p}"`), `Input 须透传 ${p}`)
  }
  const ta = read('components/ui/Textarea.vue')
  assert.match(ta, /:maxlength="maxlength"/)
  assert.match(ta, /:required="required"/)
})

test('⑥ 接入点：LLM / MCP 配置弹窗', () => {
  const llm = read('views/config/EditLLMDialog.vue')
  assert.match(llm, /:maxlength="MAX_NAME"/)
  assert.match(llm, /:maxlength="MAX_URL"/)
  assert.match(llm, /warn\('tooLong'/)
  const mcp = read('views/config/EditMCPDialog.vue')
  assert.match(mcp, /:maxlength="MAX_URL"/)
  assert.match(mcp, /fieldRules\.invalidUrl/)
})

test('⑦ 接入点：聊天输入 16K / 附件上限', () => {
  const box = read('views/chat/InputBox.vue')
  assert.match(box, /MAX_CHAT_CHARS/)
  assert.match(box, /attachmentTooLarge/)
  assert.match(box, /attachmentCountExceeded/)
  assert.match(box, /fieldRules\.messageTooLong/)
})

test('⑧ 接入点：安全页 / 路径页 / 场景页', () => {
  assert.match(read('views/settings/SecurityConfig.vue'), /isAbsolutePath/)
  assert.match(read('views/config/SettingsPathsPage.vue'), /MAX_PATH/)
  const sc = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(sc, /:maxlength="MAX_NAME"/)
  assert.match(sc, /agent_duplicate/)
})
