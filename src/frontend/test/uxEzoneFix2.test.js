/**
 * E 区修复守卫（第二轮：E-30 ~ E-34，2026-10-09）。
 *
 * 前端暂无组件级测试运行器（`npm test` = node:test 直跑）→ 以「源码守卫」锁定修复口径：
 *   - E-30 SettingsLLMPage：增/改/删先按快照改本地 `llms`，落库失败即回滚本地数组且**不关框**
 *     （成功才关框）→ 列表与后端不再不一致；
 *   - E-31 CodegraphConfig / VftsConfig：启用开关 `handleChange` 落库失败回滚开关本地态
 *     （口径同 HistoryConfig 既有 `historyEnabled.value = prev`）；
 *   - E-32 useToolSandbox：四路加载任一失败置 `loadFailed`（页面给可见提示），且失败态**禁止保存**
 *     （避免空态整键覆盖 `tool_sandbox` 丢失既有配置）；
 *   - E-33 SettingsPathsPage / SettingsParamsPage：usr 保存收成**一次** `saveUserConfig(patch)`
 *     （替代逐键 N 次往返），清空项逐键 resetUserKey，语义不变；
 *   - E-34 FileTree：新建后取消 / 改名失败清理 / 空名取消 三处删除失败改 `message.error`（可见），
 *     并刷新目录暴露残留（不再仅 console.error 静默）。
 */
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const readSrc = (rel) => readFileSync(join(srcDir, rel), 'utf8')

/** 按 header 定位函数体（花括号配平），与 uxEzoneFix.test.js 同法。 */
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

// ── E-30 ──────────────────────────────────────────────────────────────
test('E-30 SettingsLLMPage：增改删落库失败回滚本地 llms、成功才关框', () => {
  const src = readSrc('views/config/SettingsLLMPage.vue')
  // saveNow 仍以布尔表达落库结果（供回滚判定）
  const sn = fnBody(src, 'async function saveNow(tip)')
  assert.match(sn, /return true/, 'saveNow 成功返回 true')
  assert.match(sn, /return false/, 'saveNow 失败返回 false')

  // 新增 / 编辑：先备份快照 → 失败回滚，且只在成功时关框（失败保留编辑内容）
  const oe = fnBody(src, 'function openEditor(data, index)')
  assert.match(oe, /const snapshot = llms\.value\.slice\(\)/, '改本地列表前须先备份快照')
  assert.match(oe, /const ok = await saveNow\(/, '须判定落库结果')
  assert.match(oe, /if \(ok\) handle\.close\(\)/, '成功才关框')
  assert.match(oe, /else llms\.value = snapshot/, '失败回滚本地列表（弹框保持打开，保留编辑内容）')
  assert.ok(oe.indexOf('const ok = await saveNow(') < oe.indexOf('handle.close()'),
    '关框须在落库结果判定之后（不再先关框再落库）')

  // 删除：失败回滚
  const del = fnBody(src, 'async function deleteLLM(index)')
  assert.match(del, /const snapshot = llms\.value\.slice\(\)/, '删除前须备份快照')
  assert.match(del, /if \(!ok\) llms\.value = snapshot/, '删除落库失败即回滚')
})

// ── E-31 ──────────────────────────────────────────────────────────────
test('E-31 CodegraphConfig / VftsConfig：启用开关落库失败回滚本地态', () => {
  const cg = fnBody(readSrc('views/settings/CodegraphConfig.vue'), 'async function handleChange(val)')
  assert.match(cg, /const prev = !val/, '须记录前值（Switch v-model 已先行改本地态）')
  assert.match(cg, /cgEnabled\.value = prev/, '落库失败回滚开关本地态')

  const vf = fnBody(readSrc('views/settings/VftsConfig.vue'), 'async function handleChange(val)')
  assert.match(vf, /const prev = !val/, '须记录前值')
  assert.match(vf, /vfEnabled\.value = prev/, '落库失败回滚开关本地态')

  // 口径一致：既有 HistoryConfig 回滚仍在（对照口径）
  assert.match(readSrc('views/settings/HistoryConfig.vue'), /historyEnabled\.value = prev/)
})

// ── E-32 ──────────────────────────────────────────────────────────────
test('E-32 useToolSandbox：四路加载失败置 loadFailed，重载复位，失败态禁止保存', () => {
  const js = readSrc('composables/useToolSandbox.js')
  assert.match(js, /const loadFailed = ref\(false\)/, '须暴露加载失败态')
  assert.doesNotMatch(js, /\bwatch\(|\bwatchEffect\(/, 'composable 禁止 watch')
  // 四路加载失败均置位
  for (const h of [
    'async function loadTools()',
    'async function loadUserConfig()',
    'async function loadServerSandboxCount()',
    'async function loadTrustDirs()',
  ]) {
    assert.match(fnBody(js, h), /failLoad\(e\)/, `${h} 失败须置 loadFailed`)
  }
  // 重载开头整体复位
  const rl = fnBody(js, 'async function reload()')
  assert.match(rl, /loadFailed\.value = false/, 'reload 开头须复位失败态')
  // 失败态禁止保存（避免空态整键覆盖既有配置）
  const sv = fnBody(js, 'async function save()')
  assert.match(sv, /if \(!dirty\.value\) return false/, '无改动短路（既有）')
  assert.match(sv, /if \(loadFailed\.value\) return false/, '失败态拒绝落库')
})

test('E-32 SettingsToolSandboxPage：失败可见提示 + 保存前拒绝', () => {
  const pg = readSrc('views/config/SettingsToolSandboxPage.vue')
  assert.match(pg, /loadFailedText\(t, t\('config\.page\.toolSandbox'\)/, '须用统一加载失败文案给可见提示')
  assert.match(pg, /const \{ loading, saving, loadFailed, loadError,/, '须从 composable 取失败态')
  assert.match(pg, /if \(loadFailed\.value\) \{ showLoadFailure\(\); return \}/, '保存前失败态须拒绝并提示')
  assert.doesNotMatch(pg, /\bwatch\(/, '禁止 watch')
})

// ── E-33 ──────────────────────────────────────────────────────────────
test('E-33 SettingsPathsPage：usr 改动一次批量写', () => {
  const pu = fnBody(readSrc('views/config/SettingsPathsPage.vue'), 'async function saveUserTab()')
  assert.match(pu, /const patch = \{\}/, '须收集全部改动为单个对象')
  assert.match(pu, /await saveUserConfig\(patch\)/, '一次批量写')
  assert.doesNotMatch(pu, /await saveUserConfig\(\{/, '不得再逐键写库')
  assert.match(pu, /savedText\(t, APPLY_RESTART\)/, '成功反馈语义不变（需重启）')
})

test('E-33 SettingsParamsPage：usr 改动一次批量写（commitUser 变同步规划器）', () => {
  const src = readSrc('views/config/SettingsParamsPage.vue')
  const qu = fnBody(src, 'async function saveUserTab()')
  assert.match(qu, /commitUser\(f, patch, clears\)/, '须逐项规划（校验 + 收集）')
  assert.match(qu, /await saveUserConfig\(patch\)/, '一次批量写')
  assert.match(qu, /await resetUserKey\(k\)/, '清空项逐键删键（回落默认）')

  const cu = fnBody(src, 'function commitUser(f, patch, clears)')
  assert.doesNotMatch(cu, /await/, 'commitUser 须为同步规划器（不再落库）')
  assert.match(cu, /patch\[key\] = r\.value/, '待写值入 patch')
  assert.match(cu, /clears\.push\(key\)/, '清空项入 clears')
})

// ── E-34 ──────────────────────────────────────────────────────────────
test('E-34 FileTree：新建后取消 / 改名失败清理 / 空名取消 的删除失败须用户可见', () => {
  const ft = readSrc('views/filetree/FileTree.vue')
  assert.doesNotMatch(ft, /console\.error\('\[FileTree\] deleteFilePath error:'/,
    '删除清理失败不得仅 console.error（静默）')
  // 三处同型分支均给可见提示（含原因）
  const n = (ft.match(/fileTree\.delete_failed_detail/g) || []).length
  assert.ok(n >= 3, 'confirmEdit 两处 + cancelEdit 一处删除失败均须可见提示')

  const ce = fnBody(ft, 'async function confirmEdit()')
  assert.match(ce, /fileTree\.delete_failed_detail/, 'confirmEdit 清理失败须可见')
  const cx = fnBody(ft, 'async function cancelEdit()')
  assert.match(cx, /fileTree\.delete_failed_detail/, 'cancelEdit 清理失败须可见')
  assert.match(cx, /await refreshDirInTree\(getParentPath\(origPath\)\)/,
    '取消新建后无论清理成败都刷新目录，暴露可能残留的空文件')
})
