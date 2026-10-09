/**
 * E 区修复守卫（E-20/21/22/23/24/25/26/27/28/29，2026-10-08/09）。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑）→ 以「源码守卫 + 纯模块行为单测」锁定：
 *   - E-20 MessageList：`await ack` 后校验受理信封（backend 缺失 / ok=false / errors 非空），
 *     未受理 → handleError（错误气泡）+ cleanupAndFinish（复位加载态），防 isLoading 死锁；
 *   - E-21 file.js：init-data 单飞缓存一次性化（resolve/reject 后 finally 清槽——在飞期间共享
 *     保住 E-13 启动去重，完成后不再缓存旧快照）+ 显式失效入口 invalidateInitDataCache，
 *     FileTree 卸载即失效（pane 关闭重挂拿新数据）；行为单测直载 api/file.js（fetch 桩）验证；
 *   - E-22 CodeView：PDF iframe sandbox 豁免注释（不改行为）；
 *   - E-23 DialogShell：capture 监听移除参数匹配（mousemove capture:true ↔ remove 带 true）；
 *   - E-24 FileTree：watcher 错误提示走 i18n（zh/en fileTree.watcher_error 词条齐备）；
 *   - E-25 请求类 emit 统一 30s 超时（session / knowledge / config / chat ack；对齐 file.js 口径）；
 *   - E-26 MessageItem：流式渲染 100ms 时间闸 + 尾随刷新（renderTick 闸门，最终内容必渲染），
 *     DOMPurify 保持最外层净化收口，卸载清尾随 timer，不引入 watch。
 *   - E-27 优化订阅：4 个 optimizeAgentPrompt 调用方保存 {abort}，onUnmounted 主动 abort
 *     （流进行中关闭组件不再残留 3 个订阅）；
 *   - E-28 MessageList：onUpdated 圆圈高亮走时间闸（turn 数变化即时刷新），滚动路径仍实时；
 *   - E-29 MessageList：sendSameTurnContinue 拒绝分支复位「继续」按钮（不再永久消失）。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const readSrc = (rel) => readFileSync(join(srcDir, rel), 'utf8')

/** 按 header 定位函数体（花括号配平），与 chatRenderConfirmed.test.js 同法。 */
function fnBody(src, header) {
  const i = src.indexOf(header)
  assert.ok(i >= 0, `未找到 ${header}`)
  const start = src.indexOf('{', i)
  assert.ok(start >= 0, `未找到 ${header} 的函数体起点`)
  let depth = 0
  for (let j = start; j < src.length; j++) {
    const ch = src[j]
    if (ch === '{') depth++
    else if (ch === '}') {
      depth--
      if (depth === 0) return src.slice(start, j + 1)
    }
  }
  assert.fail(`未解析 ${header} 函数体`)
}

// ── E-20 ──────────────────────────────────────────────────────────────
test('E-20 MessageList：await ack 后校验受理信封，未受理即复位加载态', () => {
  const vue = readSrc('views/chat/MessageList.vue')
  assert.match(vue, /function ackRejectedReason\(ack\)/, '须有受理校验函数')
  for (const hdr of ['async function doSend(batch)', 'async function sendSameTurnContinue()']) {
    const body = fnBody(vue, hdr)
    const iAck = body.indexOf('await ack')
    const iChk = body.indexOf('ackRejectedReason(ack)')
    assert.ok(iChk > iAck, `${hdr} 须在 await ack 之后校验信封`)
    const branch = body.slice(iChk)
    assert.match(branch, /handleError\(/, '未受理须走既有错误处理（错误气泡，用户可见）')
    assert.match(branch, /cleanupAndFinish\(\)/, '未受理须复位加载态（isLoading/pending 收起）')
    assert.match(branch, /return/, '未受理须终止后续渲染（不 pushUserBubble）')
  }
  // 发送 payload 零变更：ack 校验为纯前端检查
  const chat = readSrc('api/chat.js')
  assert.match(chat, /q: text,/, 'llm-start 载荷保持不变')
})

// ── E-21 ──────────────────────────────────────────────────────────────
test('E-21 file.js：init-data 缓存一次性化 + 显式失效入口；FileTree 卸载即失效', () => {
  const f = readSrc('api/file.js')
  assert.match(f, /_initDataPromise = run\(\)\.finally\(\(\) => \{ _initDataPromise = null \}\)/,
    'resolve/reject 后 finally 清槽（一次性）')
  assert.match(f, /export function invalidateInitDataCache\(\)/, '须有显式失效入口')
  // E-13 启动去重接线不变：预取状态机复用 gui.init-data（在飞期间共享同一请求）
  assert.match(f, /createInitDataPrefetch\(\(\) => guiReq\('init-data'/, '预取 load 仍走 guiReq 单飞')
  const ft = readSrc('views/filetree/FileTree.vue')
  const un = fnBody(ft, 'onUnmounted(() => {')
  assert.match(un, /invalidateInitDataCache\(\)/, 'FileTree 卸载须失效 init-data 缓存')
})

test('E-21 行为：init-data 在飞共享（启动去重）、完成后一次性（重挂新数据）', async () => {
  const fileApi = await import('../src/api/file.js')
  const realFetch = globalThis.fetch
  let fetchCount = 0
  globalThis.fetch = async () => {
    fetchCount++
    return { json: async () => ({ ok: true, result: { treeData: [], workDir: 'X:/' }, errors: [] }) }
  }
  try {
    fileApi.invalidateInitDataCache()
    const [a, b] = await Promise.all([fileApi.loadInitData(), fileApi.loadInitData()])
    assert.ok(a && b, '两个消费点都拿到结果')
    assert.equal(fetchCount, 1, '在飞期间并发消费共享同一次请求（E-13 启动去重）')
    await new Promise((r) => setTimeout(r, 0)) // 让 finally 清槽执行
    await fileApi.loadInitData()
    assert.equal(fetchCount, 2, '完成后缓存一次性：再次读取发新请求（E-21 重挂不回退旧快照）')
    fileApi.invalidateInitDataCache()
    await fileApi.loadInitData()
    assert.equal(fetchCount, 3, '显式失效后重新拉取')
  } finally {
    globalThis.fetch = realFetch
    fileApi.invalidateInitDataCache()
  }
})

test('E-21 行为：启动期预取与 FileTree 首次加载共享同一次在飞请求', async () => {
  const fileApi = await import('../src/api/file.js')
  const realFetch = globalThis.fetch
  let fetchCount = 0
  globalThis.fetch = async () => {
    fetchCount++
    return { json: async () => ({ ok: true, result: {}, errors: [] }) }
  }
  try {
    fileApi.invalidateInitDataCache()
    await Promise.all([fileApi.prefetchInitData(), fileApi.loadInitData()])
    assert.equal(fetchCount, 1, '预取（prefetchInitData）与消费点（loadInitData）共享同一次请求')
    await fileApi.loadInitDataPrefetched() // consume 预取（一次性），不新增请求
    assert.equal(fetchCount, 1, 'consume 消费同一预取结果，不放大往返')
  } finally {
    globalThis.fetch = realFetch
    fileApi.invalidateInitDataCache()
  }
})

// ── E-22 ──────────────────────────────────────────────────────────────
test('E-22 CodeView：PDF iframe 保留 sandbox 豁免注释（不改行为）', () => {
  const src = readSrc('views/codeview/CodeView.vue')
  const iComment = src.indexOf('PDF 预览 sandbox 豁免（用户 2026-10-08）')
  assert.ok(iComment > 0, '须有豁免注释（含豁免依据与日期）')
  const iIframe = src.indexOf("renderType === 'pdf'")
  assert.ok(iIframe > iComment, '注释紧邻 PDF iframe')
  // 行为不变：PDF iframe 仍无 sandbox 属性
  const pdfBlock = src.slice(iComment, iIframe + 200)
  assert.ok(!/sandbox=/.test(pdfBlock), 'PDF iframe 不加 sandbox（豁免）')
})

// ── E-23 ──────────────────────────────────────────────────────────────
test('E-23 DialogShell：capture 监听移除参数匹配（不再泄漏）', () => {
  const src = readSrc('components/dialog/DialogShell.vue')
  assert.match(src, /addEventListener\('mousemove', onResizeMove, \{ passive: false, capture: true \}\)/,
    '添加时 capture: true（现状）')
  assert.match(src, /removeEventListener\('mousemove', onResizeMove, true\)/,
    'onUnmounted 移除须带 true（capture 匹配）')
  // 其余成对监听不误伤：drag（无 capture）移除仍不带第三参
  assert.match(src, /removeEventListener\('mousemove', onDragMove\)\r?\n/)
})

// ── E-24 ──────────────────────────────────────────────────────────────
test('E-24 FileTree：watcher 错误提示走 i18n（zh/en 词条齐备）', () => {
  const ft = readSrc('views/filetree/FileTree.vue')
  assert.match(ft, /t\('fileTree\.watcher_error', \{ msg:/, '提示文案走 i18n')
  assert.doesNotMatch(ft, /File watcher error: \$\{/, '硬编码英文须移除')
  for (const loc of ['zh-CN', 'en-US']) {
    const json = JSON.parse(readSrc(`locales/${loc}/fileTree.json`))
    assert.ok(json.watcher_error && json.watcher_error.includes('{msg}'),
      `${loc} fileTree.watcher_error 词条存在且含占位符`)
  }
})

// ── E-25 ──────────────────────────────────────────────────────────────
test('E-25 请求类 emit 统一 30s 超时（session / knowledge / config / chat ack）', () => {
  assert.match(readSrc('api/session.js'),
    /mq\.emit\(`data-session-\$\{action\}`, body, \{ timeout: 30000 \}\)/)
  assert.match(readSrc('api/knowledge.js'),
    /mq\.emit\(`data-knowledge-\$\{action\}`, body, \{ timeout: 30000 \}\)/)
  assert.match(readSrc('api/config.js'),
    /mq\.emit\(topic, body, \{ timeout: 30000 \}\)/)
  const chat = readSrc('api/chat.js')
  const n = (chat.match(/\{ timeout: 30000 \}\)/g) || []).length
  assert.ok(n >= 2, 'publishLLMStart / publishLLMContinue 的 ack 均带超时')
  // 载荷零变更：opts 第三参不影响 payload（消息面不变）
  assert.match(chat, /q: text,/, 'llm-start 载荷保持不变')
})

// ── E-26 ──────────────────────────────────────────────────────────────
test('E-26 MessageItem：流式渲染 100ms 时间闸 + 尾随刷新（最终内容完整渲染）', () => {
  const src = readSrc('views/chat/MessageItem.vue')
  assert.match(src, /const RENDER_THROTTLE_MS = 100/, '节流阈值常量（命名清晰）')
  assert.match(src, /const renderTick = ref\(0\)/, '尾随刷新闸门 ref')
  const computed = fnBody(src, 'const renderedContent = computed(')
  assert.match(computed, /renderTick\.value/, 'computed 依赖闸门（尾随递增触发重算）')
  assert.match(computed, /DOMPurify\.sanitize\(withLocalImageUrls\(marked\(/,
    'DOMPurify 保持最外层净化收口')
  assert.match(computed, /renderTrailingTimer = setTimeout/, '被闸时安排尾随刷新')
  assert.match(computed, /renderTick\.value\+\+/, '尾随刷新递增闸门 → 最后一块内容必渲染')
  assert.match(src, /if \(renderTrailingTimer\) \{ clearTimeout\(renderTrailingTimer\)/,
    '卸载清尾随 timer')
  // 项目硬性规范：不引入 watch/watchEffect
  assert.doesNotMatch(src, /\bwatch\(|\bwatchEffect\(/, '不引入 watch/watchEffect')
})

// ── E-27 ──────────────────────────────────────────────────────────────
test('E-27 优化订阅：4 个调用方保存 abort 句柄并在 onUnmounted 调用', () => {
  const files = [
    'components/common/TextEditDialog.vue',
    'views/knowledge/PrimitivePanel.vue',
    'views/scenario/ScenarioEditDialog.vue',
    'views/scenario/ScenarioWizardDialog.vue',
  ]
  for (const f of files) {
    const src = readSrc(f)
    assert.match(src, /let optimizeAbort = null/, `${f}: 须保存 abort 句柄`)
    assert.match(src, /optimizeAbort = optimizeAgentPrompt\(/, `${f}: 调用须保存返回的 {abort}`)
    const un = fnBody(src, 'onUnmounted(')
    assert.match(un, /optimizeAbort\(\)/, `${f}: onUnmounted 须调 abort()`)
  }
  // API 契约：abort 仍返回（签名语义不变，仅调用方接入）
  const cfg = readSrc('api/config.js')
  assert.match(cfg, /return \{ abort: \(\) => unsubs\.forEach\(fn => fn\(\)\) \}/,
    'optimizeAgentPrompt 仍返回 {abort}（签名语义未改）')
})

// ── E-28 ──────────────────────────────────────────────────────────────
test('E-28 MessageList：onUpdated 圆圈高亮时间闸限流（turn 数变化即时刷新）', () => {
  const src = readSrc('views/chat/MessageList.vue')
  assert.match(src, /const CIRCLE_UPDATE_THROTTLE_MS = 100/, '节流阈值常量（同 E-26 口径）')
  const throttle = fnBody(src, 'function updateCurrentCircleThrottled()')
  assert.match(throttle, /turnStarts\.value\.length/, '按 turn 数判断（新增 turn 即时刷新）')
  assert.match(throttle, /Date\.now\(\)/, '时间闸')
  assert.match(throttle, /updateCurrentCircle\(\)/, '闸门开启时调用实际全量更新')
  const upd = fnBody(src, 'onUpdated(() => {')
  assert.match(upd, /updateCurrentCircleThrottled\(\)/, 'onUpdated 走节流入口')
  assert.doesNotMatch(upd, /updateCurrentCircle\(\)/, 'onUpdated 不再直调全量更新')
  const onScrollBody = fnBody(src, 'function onScroll()')
  assert.match(onScrollBody, /updateCurrentCircle\(\)/, 'onScroll（滚动路径）仍实时更新高亮')
  assert.doesNotMatch(src, /\bwatch\(|\bwatchEffect\(/, '不引入 watch/watchEffect')
})

// ── E-29 ──────────────────────────────────────────────────────────────
test('E-29 MessageList：sendSameTurnContinue 拒绝分支复位 showContinue', () => {
  const src = readSrc('views/chat/MessageList.vue')
  const body = fnBody(src, 'async function sendSameTurnContinue()')
  const iChk = body.indexOf('ackRejectedReason(ack)')
  assert.ok(iChk >= 0, '须有受理校验')
  const branch = body.slice(iChk)
  assert.match(branch, /handleError\(/, '拒绝 → 错误气泡')
  assert.match(branch, /cleanupAndFinish\(\)/, '拒绝 → 复位加载态')
  assert.match(branch, /showContinue\.value = true/, '拒绝 → 复位「继续」按钮（否则入口置 false 后永久消失）')
})
