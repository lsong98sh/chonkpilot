/**
 * 智能体「工具级别矩阵」（P4，2026-10-01，25-MCP与场景分层模型 §4）前端守卫。
 *
 * 前端无组件级测试运行器（`npm test` = node:test 直跑）→ 本文件 = 纯逻辑断言（直接 import
 * 共享单源 `utils/agentAssets.js`）+ 源码守卫（装载/过滤/标签接线）。
 *
 * 覆盖：
 *  1) 矩阵**单源**：AGENT_LEVEL_MATRIX 与后端 `capfs.AgentToolLevels`（capfs.go）**逐字一致**；
 *  2) 级别映射：nodeLevel（self=app / <inst>-user/-project/-prjusr；无法判定 → ''）；
 *  3) 编辑期过滤：4 个级别各一例（同级+更高级放行、越权项**不出现**、无法判定放行）；
 *  4) 接线：ScenarioEditDialog（场景级别）/ PrimitivePanel（原语级别）按矩阵装载工具候选；
 *     loadToolGroups 用级别 i18n 文案做分组标签。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  AGENT_LEVEL_MATRIX, allowedLevels, levelAllowed, nodeLevel, toolLevelOf, toolAllowedForLevel,
} from '../src/utils/agentLevelMatrix.js'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, '..', 'src')
const read = (rel) => readFileSync(join(srcDir, rel), 'utf8')
// 后端矩阵单源（仓库 src/lib/data/internal/capfs/capfs.go）；frontend/test → ../../lib
const capfsSrc = readFileSync(join(here, '..', '..', 'lib', 'data', 'internal', 'capfs', 'capfs.go'), 'utf8')

// 把级别数组渲染成 Go 字面量（用于与 capfs.go 逐字比对）。
const goLiteral = (arr) => '[]string{' + arr.map((x) => '"' + x + '"').join(', ') + '}'

test('矩阵单源：AGENT_LEVEL_MATRIX 与后端 capfs.AgentToolLevels 逐字一致', () => {
  assert.deepEqual(AGENT_LEVEL_MATRIX, {
    prjusr: ['prjusr', 'user', 'project', 'app'],
    project: ['project', 'app'],
    user: ['user', 'app'],
    app: ['app'],
  })
  for (const [kind, arr] of Object.entries(AGENT_LEVEL_MATRIX)) {
    assert.ok(
      capfsSrc.includes(goLiteral(arr)),
      `capfs.go 应含 ${goLiteral(arr)}（与前端 ${kind} 一致）`,
    )
  }
})

test('allowedLevels / levelAllowed：未知级别回落用户级；level 空保守放行', () => {
  assert.deepEqual(allowedLevels('prjusr'), ['prjusr', 'user', 'project', 'app'])
  assert.deepEqual(allowedLevels('project'), ['project', 'app'])
  assert.deepEqual(allowedLevels('user'), ['user', 'app'])
  assert.deepEqual(allowedLevels('app'), ['app'])
  assert.deepEqual(allowedLevels('ghost'), ['user', 'app']) // 未知 → 用户级
  assert.deepEqual(allowedLevels(''), ['user', 'app'])

  assert.equal(levelAllowed('project', 'app'), true)
  assert.equal(levelAllowed('project', 'user'), false) // 越权
  assert.equal(levelAllowed('app', ''), true) // 无法判定 → 放行
})

test('nodeLevel / toolLevelOf：节点名 → 级别；无法判定 → 空串', () => {
  assert.equal(nodeLevel('self'), 'app')
  assert.equal(nodeLevel('ins-1-user'), 'user')
  assert.equal(nodeLevel('ins-1-project'), 'project')
  assert.equal(nodeLevel('ins-1-prjusr'), 'prjusr')
  assert.equal(nodeLevel('ins-with-dash-user'), 'user') // instanceID 含连字符仍按末段后缀
  assert.equal(nodeLevel('third-party'), '')            // 第三方 → 无法判定
  assert.equal(nodeLevel(''), '')
  assert.equal(toolLevelOf({ _meta: { server: { node: 'self' } } }), 'app')
  assert.equal(toolLevelOf({ _meta: { server: { node: 'ins-1-prjusr' } } }), 'prjusr')
  assert.equal(toolLevelOf({}), '')
  assert.equal(toolLevelOf(null), '')
})

test('编辑期候选按矩阵过滤：4 个级别各一例（越权项不出现）', () => {
  const tool = (node) => ({ _meta: { server: { node } } })
  const cases = [
    { kind: 'prjusr', allow: ['self', 'a-user', 'a-project', 'a-prjusr'], deny: [] },
    { kind: 'project', allow: ['self', 'a-project'], deny: ['a-user', 'a-prjusr'] },
    { kind: 'user', allow: ['self', 'a-user'], deny: ['a-project', 'a-prjusr'] },
    { kind: 'app', allow: ['self'], deny: ['a-user', 'a-project', 'a-prjusr'] },
  ]
  for (const c of cases) {
    for (const n of c.allow) {
      assert.equal(toolAllowedForLevel(tool(n), c.kind), true, `${c.kind} 应允许 ${n}`)
    }
    for (const n of c.deny) {
      assert.equal(toolAllowedForLevel(tool(n), c.kind), false, `${c.kind} 应剔除越权 ${n}`)
    }
  }
  // 无法判定（无 _meta.server / 第三方节点）→ 放行（宁全勿误删）
  assert.equal(toolAllowedForLevel(tool(''), 'app'), true)
  assert.equal(toolAllowedForLevel({}, 'app'), true)
})

test('接线：场景编辑 / 原语编辑按级别装载工具候选；loadToolGroups 过滤 + 级别标签', () => {
  const assets = read('utils/agentAssets.js')
  assert.match(assets, /toolAllowedForLevel\(tl, kind\)\)\s*continue/, 'loadToolGroups 按级别过滤越权项')
  assert.match(assets, /label = g\.level && t \? t\('scenario\.level\.' \+ g\.level\) : key/, '分组标签用级别 i18n')

  const dlg = read('views/scenario/ScenarioEditDialog.vue')
  assert.match(dlg, /allowedLevels[\s\S]{0,40}from '\.\.\/\.\.\/utils\/agentLevelMatrix'/, '场景编辑复用级别矩阵单源')
  assert.match(dlg, /loadToolGroupsShared\(form\.value\.level \|\| 'user', t\)/, '场景编辑按场景级别装载工具')
  assert.match(dlg, /function onLevelChange[\s\S]{0,320}loadToolGroups\(\)/, '级别变 → 重载工具候选')
  assert.doesNotMatch(dlg, /const AGENT_LEVEL_MATRIX/, '矩阵不得在场景编辑内重复定义（单源 agentLevelMatrix）')

  const panel = read('views/knowledge/PrimitivePanel.vue')
  assert.match(panel, /loadToolGroups\(curLevel\.value, t\)/, '原语编辑按原语级别装载工具')
})
