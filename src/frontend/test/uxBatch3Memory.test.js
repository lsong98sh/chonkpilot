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
import { readSrc } from './support/feSource.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const repoDir = join(here, '..', '..')
const read = (rel) => readSrc(rel)
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
// MEMORY-UI-1（2026-11-02）：记忆配置整体迁到 memory 插件配置页
//   「立即沉淀 / 清空内容 / 类别与提示词编辑」不再由主仓上下文页承载 →
//   主仓只守「迁移后不残留 + 插件侧能力面齐备」；页面实现见插件仓 webui/config.html。
// ═══════════════════════════════════════════════════════════════
test('⑱A/B 迁移：上下文页不再承载记忆配置；memory 插件配置页能力面齐备', () => {
  const src = stripComments(read(PAGE))
  // 1) 主仓页：记忆配置项 / 立即沉淀 / 清空 / 类别表 全部退场，只留迁移提示
  for (const gone of ['flushMemory', 'clearCategory', 'deleteCategory', 'addCategory',
    'memory.category.', 'memoryIO.flush', 'memoryIO.clear_confirm']) {
    assert.ok(!src.includes(gone), `上下文页不应再含记忆配置：${gone}`)
  }
  assert.match(src, /projectConfig\.context_memory_moved/, '须有迁移提示（指向插件配置页）')

  // 2) 插件 manifest：配置视图 + 动作面（页面据此调用）
  const manifest = JSON.parse(readRepo('initdata/capability/plugins/memory/manifest.json'))
  const view = (manifest.ui?.views || []).find((v) => v.id === 'config')
  assert.ok(view && view.entry === 'webui/config.html', 'memory 配置视图须指向 webui/config.html')
  for (const rpc of ['config-load', 'config-save', 'category-content', 'category-save',
    'category-delete', 'prompt-save', 'prompt-reset', 'flush']) {
    assert.ok((manifest.ui.rpc || []).includes(rpc), `插件须注册 RPC：${rpc}`)
  }
  for (const perm of ['config.prj.set', 'memory.delete', 'config.prompt.set', 'config.prompt.delete']) {
    assert.ok((manifest.permissions?.data || []).includes(perm), `插件须声明数据权限：${perm}`)
  }

  // 3) 插件 Go 侧：配置读写入参仍是既有 memory.* 键（零新增配置键；键常量在 lib 单源定义）+ 手动沉淀复用 memory.flush
  const ui = readFileSync(join(repoDir, '..', '..', 'chonkpilot-plugins', 'memory', 'src', 'lib', 'config_ui.go'), 'utf8')
  for (const key of ['memoryEnabledKey', 'memoryMinTokensKey', 'memoryCategoryMaxTokensKey',
    'memorySubsessionKey', 'memoryTriggerKey', 'memorySafetyTokensKey']) {
    assert.ok(ui.includes(key), `配置面须覆盖既有键常量：${key}`)
  }
  assert.match(ui, /data-prj-config-save/, '写回走既有 data-prj-config-save（零新增主题）')
  const lib = readFileSync(join(repoDir, '..', '..', 'chonkpilot-plugins', 'memory', 'src', 'lib', 'memory.go'), 'utf8')
  assert.match(lib, /flushSubject = "memory\.flush"/, '手动沉淀仍走 memory.flush（字面量不变）')

  // 4) 清空语义不变：仍复用 data-memory-save 写空串（不删类别）
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

  // 跨端核实：压缩摘要真落库于会话快照（内建压缩 compress.go 写回快照；原 plugin-compress 已于 2026-10-27 删除）
  const go = readRepo('lib/llm/server/compress.go')
  assert.match(go, /compressedSummaryPrefix = session\.SummaryPrefix/,
    '压缩产物标记唯一来源 = 组件 SummaryPrefix（内建压缩绑定）')
  assert.match(src, /const COMPRESS_MARK = '\[已压缩早前对话\] '/, '前端识别标记须与 Go 侧一致')
  const go2 = readRepo('lib/data/persist/persist.go')
  assert.match(go2, /TopicDataSnapshotGet, msgkeys\.TopicDataSnapshotSet/, 'data-snapshot-get 须为既有注册主题')
  const l1 = readRepo('lib/llm/server/compress_test.go')
  assert.match(l1, /func TestCompressCommitSingleFlightAndCAS/, 'L1 须有「压缩产物落快照可读回 + CAS」用例')
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
  // 触发时机：session-complete（轮末终态；压缩为内部方法面，无独立总线事件）后重读
  assert.match(src, /const COMPRESS_EVENT = EventNames\.sessionComplete/, '须订阅轮末事件（压缩无独立事件）')
  assert.match(src, /mq\.on\(COMPRESS_EVENT, loadCompressRecords\)/, '轮末后须重读记录')
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
  const KEYS = ['flush_section', 'flush', 'flush_hint', 'flush_no_session', 'flush_fail',
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
    // I-128 投递即回：不再同步等结果 → 已废弃的「部分失败 / 无类别」文案随之移除
    assert.equal(io.flush_partial, undefined, `${loc} flush_partial 应已移除（投递即回）`)
    assert.equal(io.flush_no_category, undefined, `${loc} flush_no_category 应已移除（投递即回）`)
    assert.equal(io.flushing, undefined, `${loc} flushing 应已移除（投递即回，无「沉淀中」文案）`)
    for (const k of ['flush_fail', 'clear_fail', 'records_load_failed']) {
      assert.match(io[k], /\{error\}/, `${loc} memoryIO.${k} 须含占位符`)
    }
    assert.match(io.records_range_value, /\{turn\}/, `${loc} records_range_value 须含 {turn}`)
    assert.match(io.records_kept, /\{count\}/, `${loc} records_kept 须含 {count}`)
    assert.match(io.records_note, loc === 'zh-CN' ? /时间/ : /time/i, `${loc} records_note 须说明时间不可得`)
    // I-128：statusbar 队列状态文案（记忆/压缩 × 排队/进行中/完成）zh/en 齐备
    const sb = readLocale(loc, 'statusBar.json')
    for (const k of ['queue_memory_queued', 'queue_memory_running', 'queue_memory_done',
      'queue_compress_queued', 'queue_compress_running', 'queue_compress_done']) {
      assert.ok(typeof sb[k] === 'string' && sb[k].trim().length > 0, `${loc} 缺 statusBar.${k}`)
    }
  }
  const zh = mkI18n({ memoryIO: readLocale('zh-CN', 'memoryIO.json') })
  const en = mkI18n({ memoryIO: readLocale('en-US', 'memoryIO.json') })
  for (const [loc, i18n] of [['zh-CN', zh], ['en-US', en]]) {
    for (const [key, named] of [
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
  assert.match(src, /v-mq:\[EventNames\.contextSave\]\.click/, '既有保存按钮不得被改坏')
  // MEMORY-UI-1：记忆配置已迁到 memory 插件配置页 → 本页不得再出现记忆配置项
  assert.doesNotMatch(src, /memory\.enabled|memory\.min-turn-tokens|memory\.category-max-tokens/,
    '上下文页不得再写记忆配置键（已迁移到插件配置页）')
  assert.match(src, /projectConfig\.context_memory_moved/, '须保留迁移提示（指向插件配置页）')
  for (const sel of ['.compress-record', '.record-meta', '.record-text', '.records-empty']) {
    const rule = raw.match(new RegExp(sel.replace('.', '\\.') + '\\s*\\{[\\s\\S]*?\\n\\}'))
    assert.ok(rule, `未找到 ${sel} 规则`)
    // 色值须走主题 token（允许 var(--x, #fallback) 形态；禁裸色值声明）
    assert.doesNotMatch(rule[0], /(color|background)\s*:\s*(#[0-9a-f]{3,8}|rgba?\()/i,
      `${sel} 不得裸写色值（走主题 token）`)
  }
  // 新归属：memory 插件 manifest 声明配置页（插件维护 → 记忆沉淀 → 配置）
  const manifest = JSON.parse(readRepo('initdata/capability/plugins/memory/manifest.json'))
  assert.equal(manifest.ui.selfServe, true, 'memory 插件须自持 webui 资源')
  const view = (manifest.ui.views || []).find((v) => v.id === 'config')
  assert.ok(view, 'memory 插件须声明 id=config 的配置视图')
  assert.match(view.entry, /^webui\//, '配置视图 entry 须指向 webui 资源')
  for (const rpc of ['config-load', 'config-save', 'category-save', 'prompt-save', 'flush']) {
    assert.ok((manifest.ui.rpc || []).includes(rpc), `memory 插件须注册 RPC：${rpc}`)
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
