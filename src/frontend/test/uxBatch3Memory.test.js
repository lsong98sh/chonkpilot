/**
 * 用户视角缺陷「批 3」⑱ – 记忆「立即沉淀 / 清空内容」+「压缩内容可查」（2026-09-20）：
 *   问题 A：记忆页只能按类别查看/编辑/增删，**无「立即沉淀」入口**（用户想手动触发一次记忆写入）。
 *   问题 B：只有「删除类别」，**无「清空某类别内容」**（保留类别、只清空内容）。
 *   问题 C：压缩页只有阈值与提示词配置，**无法查看"压缩了什么"**。
 *
 * 交付：
 *   A 立即沉淀 = 新消息面 `memory.flush`（点分相对主题；memory 插件订阅、同步回执 saved/failed）
 *     + 页面按钮（进行中 loading / 成功 / **失败可见**），成功后重读类别清单；
 *   B 清空内容 = 复用既有 `data-memory-save` 写空串（后端已支持）+ 二次确认 + **后端确认后回读**
 *     （不乐观清空），与既有「删除类别」并存；
 *   C 压缩内容可查 = 读**当前活动会话的会话快照**（既有 `data-snapshot-get`；压缩产物唯一落点），
 *     列出摘要原文（截断 + 展开）+ 触发范围（snapshot_turn）+ 保留条数；无数据给空态。
 *     **压缩不落时间戳** → 页面不展示时间（不臆造，见 memoryIO.records_note）。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→
 * `*.vue` 源码守卫（与 uxBatch1/2/3 同法）+ i18n 实键校验 + 纯函数直调（brief/装载）
 * + **跨端字面量核对**（Go 插件订阅点 + 压缩标记 + 快照数据面）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readRepo = (rel) => readFileSync(join(repoDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']
const PAGE = 'views/settings/ContextConfig.vue'

/** 去掉 HTML / CSS / JS 注释后再做"字面量/禁用形态"扫描（注释里引用主题名是允许的） */
function stripComments(s) {
  return s
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

/** 取函数体（`function name(...) {` 到首个顶格 `}`） */
function fnBody(src, name) {
  const re = new RegExp('(?:async )?function ' + name + '\\s*\\([^)]*\\)\\s*\\{([\\s\\S]*?)\\n\\}')
  const m = src.match(re)
  return m ? m[1] : null
}

/** 极简 i18n 替身：从真实 locales JSON 取键 + `{name}` 插值（与 uxBatch1/2/3 同法） */
function mkI18n(messages) {
  const dig = (key) => key.split('.').reduce((o, p) => (o == null ? undefined : o[p]), messages)
  return {
    te: (key) => dig(key) !== undefined,
    t: (key, named) => {
      const v = dig(key)
      if (typeof v !== 'string') return key
      if (!named) return v
      let out = v
      for (const [k, val] of Object.entries(named)) out = out.split('{' + k + '}').join(String(val))
      return out
    },
  }
}

// ═══════════════════════════════════════════════════════════════
// A 立即沉淀：按钮 / 进行中 / 成功 / 失败可见 / 成功后刷新
// ═══════════════════════════════════════════════════════════════
test('⑱A 立即沉淀：按钮 + 进行中 loading + 记忆库关闭时禁用（防重复点击）', () => {
  const src = read(PAGE)
  assert.match(src, /memoryIO\.flush/, '须有「立即沉淀」文案（i18n，不得硬编码中文）')
  assert.match(src, /@click="flushMemory"/, '按钮须触发手动沉淀')
  assert.match(src, /:loading="flushing"/, '进行中须有 loading 态')
  assert.match(src, /:disabled="!memoryEnabled \|\| flushing"/, '记忆库关闭 / 进行中须禁用')
  assert.match(src, /const flushing = ref\(false\)/, '须有进行中状态')
  const fn = fnBody(src, 'flushMemory')
  assert.ok(fn, '未找到 flushMemory')
  assert.match(fn, /if \(flushing\.value\) return/, '进行中不得重入（防重复点击）')
  assert.ok(fn.indexOf('finally') > 0 && /flushing\.value = false/.test(fn.slice(fn.indexOf('finally'))),
    '须在 finally 复位（异常不卡 loading）')
})

test('⑱A 主题：点分相对主题 memory.flush，前端字面量 = Go 插件订阅点', () => {
  const src = read(PAGE)
  assert.match(src, /const FLUSH_TOPIC = 'memory\.flush'/, '须用点分相对主题（桥/服务端直通总线）')
  assert.match(src, /mq\.emit\(FLUSH_TOPIC, \{ session: sessionID \}/, '须经统一 mq.emit 发送（禁直调 window.go.*）')
  assert.doesNotMatch(stripComments(src), /window\.go\.|gui\./, '不得绕过 mq 直调宿主')

  const go = readRepo('plugins/plugin-memory/memory.go')
  assert.match(go, /flushSubject = "memory\.flush"/, 'Go 插件主题字面量须与前端一致')
  assert.match(go, /d\.Bus\.On\(flushSubject, 0/, '插件须在 Start 订阅该主题')
  assert.match(go, /func \(p \*Plugin\) flush\(/, '插件须有 flush 实现')
  assert.match(go, /v\.Result = p\.flush\(v\.Payload\)/, 'handler 须把结果写回 Value.Result（promise 回执）')
})

test('⑱A 作用域与内容：显式动作取「当前会话最近一轮」；不受 min-turn-tokens 门控', () => {
  const go = readRepo('plugins/plugin-memory/memory.go')
  // 作用域 = payload 的 instance + session（不跨实例/会话）
  assert.match(go, /InstanceID string `json:"instance_id"`[\s\S]{0,80}Session    string `json:"session"`/,
    'payload 须为 {instance_id, session}（实例字段必带）')
  assert.match(go, /p\.latestTurn\(req\.InstanceID, req\.Session\)/, '须按会话取轮次（限定作用域）')
  assert.match(go, /sessionHistorySubject = "data-session-history"/, '取最近一轮走既有 data-session-history 面')
  // 不受阈值门控：flush 不经 extract 的 tokens < MinTokens 分支
  const flush = go.match(/func \(p \*Plugin\) flush\(payload \[\]byte\)[\s\S]*?\n\}/)
  assert.ok(flush, '未找到 flush 主体')
  assert.doesNotMatch(flush[0], /cfg\.MinTokens/, '手动沉淀不得受 memory.min-turn-tokens 门控')
  assert.match(go, /saved, failed := p\.distill\(/, '须复用既有沉淀回路（distill）')
})

test('⑱A 回执：成功静默（A3）；部分失败/无启用类别分别给反馈；失败路径可见（含原因）', () => {
  const src = read(PAGE)
  const fn = fnBody(src, 'flushMemory')
  assert.ok(fn, '未找到 flushMemory')
  assert.match(fn, /res\.ok !== true/, '须校验后端 ok（不假成功）')
  assert.match(fn, /memoryIO\.flush_fail', \{ error:/, '失败须给可见文案 + 原因')
  // A3（2026-09-24 用户口径）：沉淀成功**静默**，不弹成功提示（类别数不再回执）
  assert.doesNotMatch(fn, /message\.success\(/, 'A3：沉淀成功须静默（不得弹空成功提示）')
  assert.doesNotMatch(fn, /flush_ok/, 'A3：成功分支不得再引用 flush_ok 文案')
  assert.match(fn, /memoryIO\.flush_partial', \{ count: .*failed:/, '部分失败须给成功/失败数')
  assert.match(fn, /memoryIO\.flush_no_category/, '无启用类别须提示（不是静默成功）')
  assert.match(fn, /memoryIO\.flush_no_session/, '无活动会话须提示')
  assert.match(fn, /message\.error\(/, '失败须经统一 message.error（可见）')
  assert.match(fn, /await reloadMemoryCategories\(\)/, '成功后须重读类别清单（不乐观更新；经共享 composable）')
  // 超时给足（>插件侧单次 LLM 超时）
  assert.match(src, /const FLUSH_TIMEOUT = 120000/)
  assert.match(fn, /timeout: FLUSH_TIMEOUT/)
})

// ═══════════════════════════════════════════════════════════════
// B 清空某类别内容：二次确认 + 保留类别 + 后端确认后回读
// ═══════════════════════════════════════════════════════════════
test('⑱B 清空：入口在类别行（含用户偏好）+ 二次确认 + 与删除并存', () => {
  const src = read(PAGE)
  // 2026-09-27：类别表改用自研 Table（具名插槽），行内插槽作用域变量 = row
  assert.match(src, /@click="clearCategory\(row\)"/, '项目类别行须有「清空」入口')
  assert.match(src, /@click="clearCategory\(userPref\)"/, '用户偏好行同样须可清空')
  assert.match(src, /@click="deleteCategory\(row\)"/, '「删除类别」须保留（两者并存）')
  assert.match(src, /memoryIO\.clear_confirm', \{ name: c\.category \}/, '须二次确认（含类别名）')
  assert.match(src, /memoryIO\.clear_confirm_title/, '确认框须给标题（与既有删除同风格）')
  const fn = fnBody(src, 'clearCategory')
  assert.ok(fn, '未找到 clearCategory')
  assert.ok(fn.indexOf('await confirm(') >= 0, '未确认不得执行')
  assert.match(fn, /catch \{ return \}/, '用户取消（confirm reject）须直接返回')
})

test('⑱B 语义：保留类别只清内容（复用 data-memory-save 写空串）+ 后端确认后回读', () => {
  const src = read(PAGE)
  const fn = fnBody(src, 'clearCategory')
  assert.ok(fn, '未找到 clearCategory')
  assert.match(fn, /dataClient\.save\('memory', \{ category: c\.category, content: '' \}\)/,
    '清空 = 写空串（复用既有 data-memory-save；不是 delete）')
  assert.doesNotMatch(fn, /remove\('memory'|data-memory-delete|'delete'/, '清空不得删类别')
  assert.doesNotMatch(fn, /memoryList\.value\s*=/, '不得乐观清空（后端确认后才更新 UI）')
  const saveIdx = fn.indexOf("dataClient.save('memory'")
  const loadIdx = fn.indexOf('await reloadMemoryCategories()')
  assert.ok(saveIdx >= 0 && loadIdx > saveIdx, '须「先保存成功 → 再回读」')
  assert.match(fn, /memoryIO\.clear_ok', \{ name: c\.category \}/, '成功后给可见反馈')
  assert.match(fn, /memoryIO\.clear_fail', \{ error:/, '失败须给可见文案 + 原因')

  // 后端语义核实：save 写空串 → 文件保留（空 .md）→ 类别仍在清单
  // （阶段 4 门面化第四批 41 G-36：save 分支在 persist 信封层，落盘在 memory 域实现）
  // 阶段 4 internal 下沉：域实现已由 persist 下沉 internal/memory（信封在 persist/envelope.go）。
  const go = readRepo('lib/data/persist/envelope.go')
  assert.match(go, /case "save":/, 'persist 须有 save 分支')
  const impl = readRepo('lib/data/internal/memory/memory.go')
  assert.match(impl, /os\.WriteFile\(path, \[\]byte\(req\.Content\), 0o644\)/, 'save 原样写盘（空串 = 清空内容）')
  const l2 = readRepo('test/chonkpilot-data/unittest/persist/persist_memory_test.go')
  assert.match(l2, /func TestDataMemoryClearContentKeepsCategory/, 'L2 须有「清空保留类别」用例')
})

// ═══════════════════════════════════════════════════════════════
// C 压缩内容可查：真实数据来源（会话快照）+ 截断/展开 + 空态
// ═══════════════════════════════════════════════════════════════
test('⑱C 数据来源 = 既有 data-snapshot-get（压缩产物唯一落点），无新增存储', () => {
  const src = read(PAGE)
  assert.match(src, /dataRequest\('snapshot', 'get', \{ data: \{ session_id: sessionID \} \}\)/,
    '须读既有 data-snapshot-get（不新增存储/主题）')
  assert.match(src, /getActiveSessionID\(\)/, '须取当前活动会话（既有消息辅助）')
  assert.doesNotMatch(src, /dataRequest\('compress'|dataRequest\('compression/, '不得臆造压缩记录面')

  // 跨端核实：压缩摘要真落库于会话快照（compress 插件 DoCompress 写回快照）
  const go = readRepo('plugins/plugin-compress/compress.go')
  assert.match(go, /"\[已压缩早前对话\] " \+ sum/, '压缩产物 = 快照首条 system 摘要（Go 侧标记）')
  assert.match(src, /const COMPRESS_MARK = '\[已压缩早前对话\] '/, '前端识别标记须与 Go 侧一致')
  const go2 = readRepo('lib/data/persist/persist.go')
  assert.match(go2, /"data-snapshot-get", "data-snapshot-set"/, 'data-snapshot-get 须为既有注册主题')
  const l2 = readRepo('test/chonkpilot-plugin-compress/unittest/compress_test.go')
  assert.match(l2, /func TestCompressedSummaryPersistedInSnapshot/, 'L2 须有「压缩产物落快照可读回」用例')
})

test('⑱C 呈现：摘要原文（截断 + 展开全文）+ 触发范围 + 保留条数 + 空态', () => {
  const src = read(PAGE)
  assert.match(src, /memoryIO\.records_title/, '须有「压缩内容」小节')
  assert.match(src, /@click="loadCompressRecords"/, '须有刷新入口')
  assert.match(src, /:loading="recordsLoading"/, '加载中须有 loading 态')
  assert.match(src, /memoryIO\.records_range_value', \{ turn: r\.turn \}/, '须展示触发范围（快照轮次）')
  assert.match(src, /memoryIO\.records_kept', \{ count: r\.kept \}/, '须展示保留条数')
  assert.match(src, /content\.startsWith\(COMPRESS_MARK\)/, '仅识别已落库的压缩摘要（前缀判定）')
  assert.match(src, /m\.role === 'system'/, '仅识别快照中的 system 摘要消息')
  // 截断 + 展开全文（长文）
  assert.match(src, /recordExpanded\(i\) \? r\.text : brief\(r\.text\)/, '须「截断 / 展开全文」二态')
  assert.match(src, /function brief\(text\)/, '须有截断函数')
  assert.match(src, /memoryIO\.records_expand|memoryIO\.records_collapse/, '须有展开/收起文案')
  assert.match(src, /const expandedRecords = ref\(\{\}\)/, '展开态须为显式状态（禁 watch）')
  assert.match(src, /function toggleRecord\(i\)/, '须有展开切换')
  // 空态：无压缩记录 / 无活动会话分别给文案（不谎报）
  assert.match(src, /const recordsEmptyText = computed\(/, '空态文案须派生（无 watch）')
  assert.match(src, /hasActiveSession\.value \? t\('memoryIO\.records_empty'\) : t\('memoryIO\.records_no_session'\)/,
    '两种空态须区分（有会话无压缩 / 无活动会话）')
  assert.match(src, /class="field-hint records-empty"/, '空态须渲染')
  // 触发时机：llm-compress（= 总线 session-compress）后重读
  assert.match(src, /const COMPRESS_EVENT = 'llm-compress'/, '须订阅压缩事件（前端 type = 桥映射）')
  assert.match(src, /mq\.on\(COMPRESS_EVENT, loadCompressRecords\)/, '压缩后须重读记录')
  // 时间不可得 → 如实说明（不臆造时间列）
  assert.match(src, /memoryIO\.records_note/, '须有「不单独记录压缩时间」的口径说明')
  assert.doesNotMatch(src, /records_time|formatTime\(r\./, '不得凭空展示压缩时间')
})

test('⑱C 纯函数口径：brief() 截断阈值与「展开全文」互补', () => {
  // 与源码同构的纯函数（阈值 200 字符）——超长截断加省略号，短文本原样
  const RECORD_BRIEF_CHARS = 200
  const brief = (text) => {
    const s = String(text || '')
    return s.length > RECORD_BRIEF_CHARS ? s.slice(0, RECORD_BRIEF_CHARS) + '…' : s
  }
  const short = '摘要很短'
  assert.equal(brief(short), short, '短文本应原样')
  assert.equal(brief(''), '', '空文本应为空')
  assert.equal(brief(null), '', 'null 安全')
  const long = '甲'.repeat(500)
  assert.equal(brief(long).length, RECORD_BRIEF_CHARS + 1, '超长应截断到 200 + 省略号')
  assert.match(brief(long), /…$/, '截断须有省略号（提示可展开）')
  // 源码同阈值（守卫：改阈值须同步本用例）
  assert.match(read(PAGE), /const RECORD_BRIEF_CHARS = 200/)
})

// ═══════════════════════════════════════════════════════════════
// i18n / 规范
// ═══════════════════════════════════════════════════════════════
test('⑱ i18n：memoryIO 新增键 zh/en 齐备且占位符可插值', () => {
  const KEYS = ['flush_section', 'flush', 'flushing', 'flush_hint', 'flush_no_session',
    'flush_no_category', 'flush_partial', 'flush_fail',
    'clear', 'clear_confirm_title', 'clear_confirm', 'clear_ok', 'clear_fail',
    'records_title', 'records_refresh', 'records_note', 'records_empty', 'records_no_session',
    'records_load_failed', 'records_range_value', 'records_kept', 'records_expand', 'records_collapse']
  for (const loc of LOCALES) {
    const io = readLocale(loc, 'memoryIO.json')
    for (const k of KEYS) {
      assert.ok(typeof io[k] === 'string' && io[k].trim().length > 0, `${loc} 缺 memoryIO.${k}`)
    }
    // A3：成功静默 → flush_ok 文案已移除（成功分支不再回执类别数）
    assert.equal(io.flush_ok, undefined, `${loc} flush_ok 应已移除（A3 成功静默）`)
    for (const k of ['flush_fail', 'flush_partial', 'clear_fail', 'records_load_failed']) {
      assert.match(io[k], /\{error\}|\{failed\}|\{count\}/, `${loc} memoryIO.${k} 须含占位符`)
    }
    assert.match(io.records_range_value, /\{turn\}/, `${loc} records_range_value 须含 {turn}`)
    assert.match(io.records_kept, /\{count\}/, `${loc} records_kept 须含 {count}`)
    assert.match(io.records_note, loc === 'zh-CN' ? /时间/ : /time/i, `${loc} records_note 须说明时间不可得`)
  }
  const zh = mkI18n({ memoryIO: readLocale('zh-CN', 'memoryIO.json') })
  const en = mkI18n({ memoryIO: readLocale('en-US', 'memoryIO.json') })
  assert.match(zh.t('memoryIO.flush_partial', { count: 3, failed: 1 }), /3/, 'zh 成功数须插值')
  assert.match(en.t('memoryIO.flush_partial', { count: 2, failed: 1 }), /2/, 'en 成功数须插值')
  for (const [loc, i18n] of [['zh-CN', zh], ['en-US', en]]) {
    for (const [key, named] of [
      ['memoryIO.flush_partial', { count: 1, failed: 1 }],
      ['memoryIO.flush_fail', { error: 'boom' }], ['memoryIO.clear_fail', { error: 'boom' }],
      ['memoryIO.records_load_failed', { error: 'boom' }],
      ['memoryIO.records_range_value', { turn: 't-1' }], ['memoryIO.records_kept', { count: 4 }],
      ['memoryIO.clear_confirm', { name: '项目概要' }], ['memoryIO.clear_ok', { name: '项目概要' }],
    ]) {
      const text = i18n.t(key, named)
      assert.notEqual(text, key, `${loc} 文案键未命中 i18n：${key}`)
      assert.doesNotMatch(text, /\{\w+\}/, `${loc} 文案残留占位符：${text}`)
    }
  }
  // 注册：命名空间 memoryIO 已挂到 i18n
  const i18n = read('plugins/i18n.js')
  assert.match(i18n, /import zhMemoryIO from '\.\.\/locales\/zh-CN\/memoryIO\.json'/, 'zh locale 须注册')
  assert.match(i18n, /import enMemoryIO from '\.\.\/locales\/en-US\/memoryIO\.json'/, 'en locale 须注册')
  assert.match(i18n, /memoryIO: zhMemoryIO,/, 'zh 命名空间须挂载')
  assert.match(i18n, /memoryIO: enMemoryIO,/, 'en 命名空间须挂载')
})

test('⑱ 规范：无 watch/watchEffect；新增样式走主题 token（不硬编码色值）', () => {
  const raw = read(PAGE)
  const src = stripComments(raw)
  assert.doesNotMatch(src, /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect（项目规则）')
  assert.match(src, /onDataRefresh\('memory', reloadMemoryCategories\)/, '既有记忆刷新订阅不得丢失')
  assert.match(src, /v-mq:\[EventNames\.contextSave\]\.click/, '既有保存按钮不得被改坏')
  for (const sel of ['.compress-record', '.record-meta', '.record-text', '.records-empty']) {
    const rule = raw.match(new RegExp(sel.replace('.', '\\.') + '\\s*\\{[\\s\\S]*?\\n\\}'))
    assert.ok(rule, `未找到 ${sel} 规则`)
    // 色值须走主题 token（允许 var(--x, #fallback) 形态；禁裸色值声明）
    assert.doesNotMatch(rule[0], /(color|background)\s*:\s*(#[0-9a-f]{3,8}|rgba?\()/i,
      `${sel} 不得裸写色值（走主题 token）`)
  }
})

// ═══════════════════════════════════════════════════════════════
// P1（2026-09-24）压缩区末尾两条提示：逐字文案 + 归属「压缩」区（纯展示）
// ═══════════════════════════════════════════════════════════════
test('P1 上下文压缩区末尾两条提示：文案逐字 + 归属压缩区 + zh/en 齐备', () => {
  const src = read(PAGE)
  // 压缩分区 = 「compress_section」标题 → 该 section 收尾（确保落压缩区，而非记忆区/其他区）
  const secStart = src.indexOf('projectConfig.compress_section')
  const secEnd = src.indexOf('</section>', secStart)
  assert.ok(secStart > 0 && secEnd > secStart, '未定位到压缩分区')
  const sec = src.slice(secStart, secEnd)
  assert.match(sec, /\$t\('projectConfig\.context_tip_cache_billing'\)/, '压缩区须含第 1 条提示')
  assert.match(sec, /\$t\('projectConfig\.context_tip_compress_llm'\)/, '压缩区须含第 2 条提示')
  // 位置 = 压缩区**最后一项之后**（收尾说明 context_async 之后）
  const asyncIdx = sec.indexOf('projectConfig.context_async')
  const tipIdx = sec.indexOf('projectConfig.context_tip_cache_billing')
  assert.ok(asyncIdx > 0 && tipIdx > asyncIdx, '两条提示须位于压缩区末尾（收尾说明之后）')
  // 纯展示：无功能引入
  assert.doesNotMatch(stripComments(src), /\bwatch(Effect)?\s*\(/, '不得用 watch/watchEffect（项目规则）')

  // zh-CN：用户原文逐字照录
  const zh = readLocale('zh-CN', 'projectConfig.json')
  assert.equal(zh.context_tip_cache_billing,
    '如果你的provider完全按照token计价，不进行cache命中计价，建议减少保持轮为 1', 'zh 第 1 条须逐字')
  assert.equal(zh.context_tip_compress_llm,
    '上下文压缩，记忆提取使用默认LLM，如需使用特定LLM，敬请期待', 'zh 第 2 条须逐字')

  // en-US：等价翻译，关键术语 provider / cache / LLM 保留
  const en = readLocale('en-US', 'projectConfig.json')
  for (const loc of LOCALES) {
    const p = readLocale(loc, 'projectConfig.json')
    for (const k of ['context_tip_cache_billing', 'context_tip_compress_llm']) {
      assert.ok(typeof p[k] === 'string' && p[k].trim().length > 0, `${loc} 缺 projectConfig.${k}`)
    }
  }
  assert.match(en.context_tip_cache_billing, /provider/i, 'en 须保留 provider')
  assert.match(en.context_tip_cache_billing, /cache/i, 'en 须保留 cache')
  assert.match(en.context_tip_compress_llm, /LLM/, 'en 须保留 LLM')
})
