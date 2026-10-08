/**
 * 认证域前端（61 §4.6；阶段 2b-2）：登录 / 注册 / 登出 + 「登录 → 选目录 → instance-claim」时序。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑，见 package.json）→ 本文件三位一体：
 *   ① **MQ 驱动**的行为测试：`mq.emit` 经 `fetch /publish` 上行 → 断言 topic 与 payload
 *      （`login-in` / `login-register` / `login-out` / `instance-claim`；**payload 与 61 §4.6/§4.1
 *      逐字一致，零新增字段，且前端**从不携带令牌**）；
 *   ② `*.vue` **源码守卫**（与 uxBatch1/2/3 同法）：视图分派 / 错误分派 / 登出入口 / 不硬编码中文；
 *   ③ i18n **实键校验**（zh-CN 与 en-US 双侧齐备）。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
const readLocale = (loc, name) => JSON.parse(read('locales/' + loc + '/' + name))
const LOCALES = ['zh-CN', 'en-US']

const sent = []
let queue = []

/** 去 HTML / CSS / JS 注释（注释里引用错误码/函数名是允许的，不计入"落点"断言）。 */
function stripComments(s) {
  return s
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^[ \t]*\/\/.*$/gm, '')
}

/** 装最小浏览器环境：`window.__ck`（首屏注入）+ `fetch /publish` 桩（记录上行 topic/payload）。 */
function setupEnv(ck) {
  const win = {}
  if (ck) win.__ck = ck
  globalThis.window = win
  sent.length = 0
  queue = []
  globalThis.fetch = async (_url, opts) => {
    sent.push(JSON.parse(opts.body)) // {type, payload(JSON 字符串)}
    const env = queue.length ? queue.shift() : { ok: true, result: null, errors: [] }
    return { json: async () => env }
  }
  return win
}

/** 排一条后端应答（请求-响应：result 为服务端写回的 v.Result）。 */
function nextBackend({ ok = true, result = null, errors = [] } = {}) {
  queue.push({ ok, result, errors })
}

/** 取最近一次上行的载荷（解析 JSON 字符串）。 */
function lastPayload() {
  const body = sent[sent.length - 1]
  return body && body.payload ? JSON.parse(body.payload) : {}
}

// 认证形态（desktop/browser）首屏：requireAuth=true、未认证 → 登录视图
setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
const auth = await import('../src/composables/useAuth.js')
const claimMod = await import('../src/composables/useInstanceClaim.js')
const { claimedInstanceId, setClaimedInstance, currentInstanceId } = await import('../src/utils/instanceState.js')

// ═══════════════════════════════════════════════════════════════
// ① 首屏分派读数 + 登录 / 注册（MQ 驱动）
// ═══════════════════════════════════════════════════════════════

test('① 首屏：requireAuth=true 且 authed=false → 未登录（需登录视图）', () => {
  assert.equal(auth.useAuth().signedIn.value, false)
  assert.equal(auth.useAuth().user.value, null)
})

test('② login-in：payload 逐字为 {username,password}（**不含令牌**）→ 成功后置已登录', async () => {
  setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
  nextBackend({ ok: true, result: { ok: true, user: { uid: 'u-1', username: 'alice' } } })
  const ok = await auth.useAuth().login('alice', 'p@ss1')
  assert.equal(ok, true)
  assert.equal(sent.length, 1)
  assert.equal(sent[0].type, 'login-in')
  const p = lastPayload()
  assert.equal(p.username, 'alice')
  assert.equal(p.password, 'p@ss1')
  // 前端**不接触令牌**：不得携带 token / token_kind（由入口承载，61 §4.6）
  assert.equal(p.token, undefined)
  assert.equal(p.token_kind, undefined)
  assert.equal(p.instance_id, 'ins-injected') // mq 统一注入（61 §0）
  assert.equal(auth.useAuth().signedIn.value, true)
  assert.equal(auth.useAuth().user.value.uid, 'u-1')
})

test('③ login-failed / login-username-taken：错误码原样带出且不置已登录', async () => {
  setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
  auth.markSignedOut() // 复位到「未登录」（模块级单例；上一用例可能已置位）
  nextBackend({ ok: false, result: { ok: false, error: 'login-failed' }, errors: ['login-failed'] })
  assert.equal(await auth.useAuth().login('alice', 'bad'), false)
  assert.equal(auth.useAuth().errorCode.value, 'login-failed')
  assert.equal(auth.useAuth().signedIn.value, false)

  nextBackend({ ok: false, result: { ok: false, error: 'login-username-taken' }, errors: ['login-username-taken'] })
  assert.equal(await auth.useAuth().register('alice', 'p@ss1'), false)
  assert.equal(auth.useAuth().errorCode.value, 'login-username-taken')
  assert.equal(sent[1].type, 'login-register')
  assert.equal(lastPayload().username, 'alice')
})

// ═══════════════════════════════════════════════════════════════
// ② 认领时序：登录 → 选目录 → instance-claim（61 §4.6「流程」）
// ═══════════════════════════════════════════════════════════════

test('④ claim：payload {user, work_dir}（提议；+注入 instance_id）→ 成功存实例 id', async () => {
  setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
  nextBackend({ ok: true, result: { ok: true, user: { uid: 'u-1', username: 'alice' } } })
  assert.equal(await auth.useAuth().login('alice', 'p@ss1'), true)
  sent.length = 0

  nextBackend({ ok: true, result: { instance_id: 'ins-claimed', work_dir: 'D:\\projects\\app', data_dir: 'D:\\data\\u-1' } })
  const ok = await claimMod.claim({ user: 'alice', work_dir: 'D:\\projects\\app' })
  assert.equal(ok, true)
  assert.equal(sent[0].type, 'instance-claim')
  const p = lastPayload()
  assert.equal(p.user, 'alice')
  assert.equal(p.work_dir, 'D:\\projects\\app')
  assert.equal(p.token, undefined) // 令牌不入前端 payload
  assert.equal(claimMod.useInstanceClaim().state.value, claimMod.CLAIM_OK)
  assert.equal(claimedInstanceId(), 'ins-claimed')
  assert.equal(currentInstanceId(), 'ins-claimed')
  claimMod.resetClaim() // 复位（含停心跳；避免测试进程被 30s 定时器挂住）
})

test('⑤ claim 失败分派：unauthorized → 回登录视图；forbidden → 不跳登录', async () => {
  setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
  nextBackend({ ok: true, result: { ok: true, user: { uid: 'u-1', username: 'alice' } } })
  await auth.useAuth().login('alice', 'p@ss1')

  // instance-unauthorized（令牌失效）→ 状态入 UNAUTHORIZED → 视图回登录（markSignedOut）
  nextBackend({ ok: false, result: { ok: false, error: 'instance-unauthorized' }, errors: ['instance-unauthorized'] })
  assert.equal(await claimMod.claim({ user: 'alice', work_dir: 'D:\\w' }), false)
  assert.equal(claimMod.useInstanceClaim().state.value, claimMod.CLAIM_UNAUTHORIZED)
  assert.equal(claimMod.useInstanceClaim().errorCode.value, 'instance-unauthorized')
  auth.markSignedOut()
  assert.equal(auth.useAuth().signedIn.value, false)

  // instance-forbidden（无权访问该目录）→ 状态入 FORBIDDEN，**保持已登录**
  setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
  nextBackend({ ok: true, result: { ok: true, user: { uid: 'u-1', username: 'alice' } } })
  await auth.useAuth().login('alice', 'p@ss1')
  nextBackend({ ok: false, result: { ok: false, error: 'instance-forbidden' }, errors: ['instance-forbidden'] })
  assert.equal(await claimMod.claim({ user: 'alice', work_dir: 'D:\\denied' }), false)
  assert.equal(claimMod.useInstanceClaim().state.value, claimMod.CLAIM_FORBIDDEN)
  assert.equal(auth.useAuth().signedIn.value, true) // 不回登录
  claimMod.resetClaim()
})

// ═══════════════════════════════════════════════════════════════
// ③ 登出：login-out {} + 认领复位
// ═══════════════════════════════════════════════════════════════

test('⑥ 登出：发 login-out {}（无身份字段）→ 未登录；resetClaim 清旧实例上下文', async () => {
  setupEnv({ form: 'browser', requireAuth: true, authed: false, instanceId: 'ins-injected' })
  nextBackend({ ok: true, result: { ok: true, user: { uid: 'u-1', username: 'alice' } } })
  await auth.useAuth().login('alice', 'p@ss1')
  setClaimedInstance({ instance_id: 'ins-old', work_dir: 'D:\\w', data_dir: 'D:\\d' })
  sent.length = 0

  nextBackend({ ok: true, result: { ok: true } })
  await auth.useAuth().signOut()
  assert.equal(sent[0].type, 'login-out')
  assert.deepEqual(Object.keys(lastPayload()).sort(), ['instance_id'], '登出载荷除 mq 统一注入的 instance_id 外无字段')
  assert.equal(auth.useAuth().signedIn.value, false)

  claimMod.resetClaim()
  assert.equal(claimMod.useInstanceClaim().state.value, claimMod.CLAIM_IDLE)
  assert.equal(claimedInstanceId(), '')
})

// ═══════════════════════════════════════════════════════════════
// ④ 视图 / 工具栏源码守卫（分派与错误映射落点）
// ═══════════════════════════════════════════════════════════════

test('⑦ App.vue：认证形态分派登录视图 / 选目录视图；desktop 启动即认领', () => {
  const src = stripComments(read('App.vue'))
  assert.match(src, /<AuthView v-if="showAuth"/, 'auth 前置视图须在 App 分派')
  assert.match(src, /const showAuth = computed\(/, '分派须为派生状态（composable 状态驱动）')
  assert.match(src, /requireAuth\(\) && !signedIn\.value/, '未认证 → 登录步骤')
  assert.match(src, /requireAuth\(\) && signedIn\.value && !ready\.value/, '已认证未认领 → 选目录步骤')
  assert.match(src, /if \(!requireAuth\(\)\) \{[\s\S]*claim\(\)/, 'desktop 免鉴权 → 启动即认领（行为不变）')
  assert.match(src, /@ready="onAuthReady"/, '认领完成后须触发既有初始化')
  assert.doesNotMatch(src, /window\.go\./, '禁止直调 window.go.*')
})

test('⑧ AuthView.vue：登录/注册 + 选目录 → claim；错误分派符合 61 §4.6', () => {
  const src = stripComments(read('views/auth/AuthView.vue'))
  assert.match(src, /useAuth\.js/, '须用 useAuth composable')
  assert.match(src, /useInstanceClaim\.js/, '须用 useInstanceClaim composable')
  assert.match(src, /claim\(\{ user: username\.value\.trim\(\) \|\| undefined, work_dir: dir \}\)/, '选目录后须发 instance-claim{user?, work_dir}')
  assert.match(src, /code === 'instance-unauthorized'\)[\s\S]{0,200}markSignedOut\(\)/, 'unauthorized → 回登录视图')
  assert.match(src, /code === 'instance-forbidden' \? 'common\.auth_err_forbidden'/, 'forbidden → 提示换目录（不跳登录）')
  assert.equal((src.match(/markSignedOut\(\)/g) || []).length, 1, 'markSignedOut 只允许在 unauthorized 分支调用')
  assert.doesNotMatch(src, /window\.go\./, '禁止直调 window.go.*')
  assert.match(src, /isBrowserForm\(\)/, '目录选择器仅在 GUI/native 形态提供')
})

test('⑨ 登出入口：工具栏认证形态显示 + 走 login-out + 复位认领；类名不占用 tb-btn 下标', () => {
  const src = stripComments(read('views/toolbar/Toolbar.vue'))
  assert.match(src, /const needsAuth = requireAuth\(\)/, '登出按钮须按形态（requireAuth）显隐')
  assert.match(src, /class="auth-btn"/, '登出按钮类名不用 tb-btn（避免 test_toolbar 固定下标错位）')
  assert.match(src, /await signOut\(\)[\s\S]{0,80}resetClaim\(\)/, '登出后须复位实例认领')
  assert.match(src, /title="\$t\('toolbar\.sign_out'\)"/, '登出文案入 i18n')
})

test('⑩ 无硬编码中文（模板文案一律 i18n）', () => {
  const strip = (s) => s.replace(/<!--[\s\S]*?-->/g, '').replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
  for (const rel of ['views/auth/AuthView.vue', 'App.vue']) {
    const body = strip(read(rel))
    const template = body.slice(body.indexOf('<template>'), body.indexOf('</template>'))
    assert.doesNotMatch(template, /[\u4e00-\u9fa5]/, `${rel} 模板不得出现硬编码中文`)
  }
})

test('⑪ i18n 实键齐备（zh-CN / en-US 双侧）', () => {
  const keys = [
    'auth_login_title', 'auth_register_title', 'auth_username', 'auth_password', 'auth_login', 'auth_register',
    'auth_switch_to_login', 'auth_switch_to_register', 'auth_dir_title', 'auth_dir_hint', 'auth_dir_placeholder',
    'auth_enter', 'auth_switch_account', 'auth_err_login_failed', 'auth_err_username_taken',
    'auth_err_unauthorized', 'auth_err_forbidden', 'auth_err_claim_failed', 'auth_err_unknown',
    'input_required', 'select', 'dir_picker_title',
  ]
  for (const loc of LOCALES) {
    const common = readLocale(loc, 'common.json')
    const toolbar = readLocale(loc, 'toolbar.json')
    for (const k of keys) assert.equal(typeof common[k], 'string', `${loc}/common.json 缺 ${k}`)
    assert.equal(typeof toolbar.sign_out, 'string', `${loc}/toolbar.json 缺 sign_out`)
  }
})

// ═══════════════════════════════════════════════════════════════
// ⑤ 跨端契约核对（主题名与错误码字面量与后端一致；61 §4.6 / §4.1 为唯一准则）
// ═══════════════════════════════════════════════════════════════

test('⑫ 跨端：主题名 / 错误码与 Go 侧一致（零新增主题）', () => {
  const repo = join(here, '..', '..')
  const goClaim = readFileSync(join(repo, 'lib/llm/server/claim.go'), 'utf8')
  const goLogin = readFileSync(join(repo, 'lib/llm/server/login.go'), 'utf8')
  // 错误码（61 §4.6 两码 + login 两码）
  for (const code of ['instance-unauthorized', 'instance-forbidden']) {
    assert.ok(goClaim.includes(code), `claim.go 应含错误码 ${code}`)
  }
  for (const code of ['login-failed', 'login-username-taken']) {
    assert.ok(goLogin.includes(code), `login.go 应含错误码 ${code}`)
  }
  // 主题名（前端 type = 相对主题同名；无新增主题）——Go 侧已改引生成键常量（msgkeys），
  // 值由 genmsg 从契约（61 schema）生成，故此处断言**常量引用 + 生成值**双一致。
  assert.ok(goClaim.includes('SubjectInstanceClaim = msgkeys.TopicInstanceClaim'))
  assert.ok(read('events/msgkeys.js').includes("instanceClaim: 'instance-claim'"))
  assert.ok(goLogin.includes('SubjectLoginIn       = msgkeys.TopicLoginIn'))
  assert.ok(goLogin.includes('SubjectLoginRegister = msgkeys.TopicLoginRegister'))
  assert.ok(goLogin.includes('SubjectLoginOut      = msgkeys.TopicLoginOut'))
  // 前端视图/组件**不直发** MQ 主题（统一经 composable，composable 走 mq.emit）
  const view = read('views/auth/AuthView.vue')
  assert.doesNotMatch(view, /mq\.emit\(/, '视图不得直发 MQ（状态逻辑一律走 composable）')
  const composable = read('composables/useAuth.js')
  for (const t of ['MsgTopics.loginIn', 'MsgTopics.loginRegister', 'MsgTopics.loginOut']) {
    assert.ok(composable.includes(t), `useAuth 应发 ${t}（拟消息常量引用，禁字符串字面量）`)
  }
  assert.ok(read('composables/useInstanceClaim.js').includes('mq.emit(MsgTopics.instanceClaim'))
})
