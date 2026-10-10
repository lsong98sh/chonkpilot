/**
 * 项目就地切换（去整页重载）纯逻辑单测（2026-10-10）。
 *
 * `utils/projectReinit.js` = browser「打开目录」（`gui.dir.open` 服务端切本实例 work_dir）
 * 成功后的**就地重初始化**：自增项目纪元（驱动主布局整树重挂载）+ 执行 App 注册的
 * 重初始化序列（清 init-data 缓存 → 重读活动会话 → 广播 session-changed）。
 * 本模块为叶子（仅依赖 vue 的 ref）→ 可直测。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { projectEpoch, setProjectReinitHandler, reinitProjectState } from '../src/utils/projectReinit.js'

test('reinitProjectState：自增项目纪元 + 执行（await）已注册的重初始化序列', async () => {
  const before = projectEpoch.value
  let calls = 0
  let awaited = false
  setProjectReinitHandler(async () => {
    calls += 1
    await Promise.resolve()
    awaited = true
  })
  await reinitProjectState()
  assert.equal(projectEpoch.value, before + 1, '每次切换项目纪元 +1（驱动主布局重挂载）')
  assert.equal(calls, 1, '须执行已注册的重初始化序列')
  assert.equal(awaited, true, 'reinitProjectState 须 await 重初始化序列完成')
})

test('reinitProjectState：未注册处理器时不报错（纪元仍自增）', async () => {
  setProjectReinitHandler(null)
  const before = projectEpoch.value
  await reinitProjectState()
  assert.equal(projectEpoch.value, before + 1, '无处理器也须自增纪元（重挂主布局）')
})
