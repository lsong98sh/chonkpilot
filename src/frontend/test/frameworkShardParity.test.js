/**
 * TOPO-1 §2.22.3 · framework 分片与主仓契约的**逐键等值**守卫。
 *
 * 前端机制包（`chonkpilot-framework/host/web`）只保留**平台自用**的事件名 / 消息键；
 * 主仓是全量契约（`events/event-names.js` 合成 + `events/msgkeys.js` 由 genmsg 生成）。
 * 两处若漂移，就会出现「framework 发的主题没人听」或「听的主题不是契约值」的静默故障，
 * 故在此逐键断言：
 *   1. framework `FrameworkEventNames` 的每个键，主仓 `EventNames` 同键**同值**；
 *   2. framework `GuiWindowResizeKeys` 与主仓同名常量**逐键等值**。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { EventNames } from '../src/events/event-names.js'
import { GuiWindowResizeKeys } from '../src/events/msgkeys.js'
import {
  FrameworkEventNames,
} from '../../../../chonkpilot-framework/host/web/src/events/event-names.js'
import {
  GuiWindowResizeKeys as FwGuiWindowResizeKeys,
} from '../../../../chonkpilot-framework/host/web/src/events/msgkeys.js'

test('framework 分片：FrameworkEventNames 每个键在主仓 EventNames 中同值', () => {
  for (const [k, v] of Object.entries(FrameworkEventNames)) {
    assert.ok(k in EventNames, `主仓 EventNames 缺 framework 键：${k}`)
    assert.equal(EventNames[k], v, `${k} 值不一致：framework=${v} 主仓=${EventNames[k]}`)
  }
})

test('framework 分片：GuiWindowResizeKeys 与主仓逐键等值', () => {
  assert.deepEqual(FwGuiWindowResizeKeys, GuiWindowResizeKeys)
})
