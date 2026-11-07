/**
 * 删除会话前取消**全部**运行轮次（前端守卫）。
 *
 * 口径：删除会话时不再只取消「当前 ChatPanel 的当前轮」，而是读 `session-running-list` 快照，
 * 对该 session 的每个 running turn 逐项 await `llm-cancel {session, turn, instance_id}`；
 * 非当前会话同样取消；快照失败不阻断删除；当前会话仍保留 `session-cancel-llm`（Ask 本地状态）。
 */
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { EventNames } from '../src/events/event-names.js'
import { cancelRunningTurnsOf, CANCEL_RUNNING_TURN_TIMEOUT_MS } from '../src/api/chat.js'
import { readSrc } from './support/feSource.mjs'

test('cancelRunningTurnsOf：只取该会话的轮次，逐项 llm-cancel 且全部 await', async () => {
  const calls = []
  const resolvers = []
  let listCalls = 0
  const deps = {
    listRunningSessions: async () => {
      listCalls++
      return [
        { session_id: 's1', turn: 't1', instance_id: 'i1' },
        { session_id: 's2', turn: 't9', instance_id: 'i1' },
        { session_id: 's1', turn: 't2', instance_id: 'i2' },
      ]
    },
    emit: (topic, payload, ctx) => {
      calls.push({ topic, payload, ctx })
      return new Promise((resolve) => resolvers.push(resolve))
    },
  }

  let done = false
  const p = cancelRunningTurnsOf('s1', deps).then((r) => { done = true; return r })
  await new Promise((r) => setTimeout(r, 10))

  assert.equal(listCalls, 1, '删除前应读一次运行态快照')
  assert.equal(calls.length, 2, '只取消目标会话的 2 个轮次（s2 不碰）')
  assert.ok(calls.every((c) => c.topic === EventNames.llmCancel), '主题应为 llm-cancel')
  assert.deepEqual(calls.map((c) => c.payload), [
    { session: 's1', turn: 't1', instance_id: 'i1' },
    { session: 's1', turn: 't2', instance_id: 'i2' },
  ], 'payload 精确到 session+turn+instance_id')
  assert.ok(calls.every((c) => c.ctx && c.ctx.timeout === CANCEL_RUNNING_TURN_TIMEOUT_MS),
    'llm-cancel 应带 5s 等待上限')
  assert.equal(done, false, '必须 await 全部取消回执后才返回')

  resolvers.forEach((r) => r({ ok: true }))
  const turns = await p
  assert.equal(done, true)
  assert.equal(turns.length, 2)
})

test('cancelRunningTurnsOf：快照失败 → 返回 []、不发取消、不抛出（不阻断删除）', async () => {
  const warn = console.warn
  console.warn = () => {}
  try {
    let emits = 0
    const result = await cancelRunningTurnsOf('s1', {
      listRunningSessions: async () => { throw new Error('running-list down') },
      emit: () => { emits++; return Promise.resolve() },
    })
    assert.deepEqual(result, [])
    assert.equal(emits, 0)
  } finally {
    console.warn = warn
  }
})

test('cancelRunningTurnsOf：单项取消失败/超时不影响其它轮次', async () => {
  const warn = console.warn
  console.warn = () => {}
  try {
    const seen = []
    const turns = await cancelRunningTurnsOf('s1', {
      listRunningSessions: async () => [
        { session_id: 's1', turn: 't1', instance_id: 'i1' },
        { session_id: 's1', turn: 't2', instance_id: 'i1' },
      ],
      emit: async (topic, payload) => {
        seen.push(payload.turn)
        if (payload.turn === 't1') throw new Error('timeout')
      },
    })
    assert.deepEqual(seen, ['t1', 't2'], '失败后仍继续取消下一轮')
    assert.equal(turns.length, 2)
  } finally {
    console.warn = warn
  }
})

test('cancelRunningTurnsOf：空 session id → 不读快照、不发取消', async () => {
  let touched = 0
  const result = await cancelRunningTurnsOf('', {
    listRunningSessions: async () => { touched++; return [] },
    emit: () => { touched++; return Promise.resolve() },
  })
  assert.deepEqual(result, [])
  assert.equal(touched, 0)
})

test('SessionsPane.handleDelete：删除前 await cancelRunningTurnsOf（非当前会话也取消），并保留当前会话 sessionCancelLlm', () => {
  const src = readSrc('views/sessions/SessionsPane.vue')
  const start = src.indexOf('async function handleDelete')
  assert.ok(start >= 0, '未找到 handleDelete')
  const next = src.indexOf('// fork（P4 已落地）', start)
  const body = src.slice(start, next > start ? next : undefined)

  const idxCancel = body.indexOf('await cancelRunningTurnsOf(session.session_id)')
  const idxDelete = body.indexOf('await sessionApi.deleteSession(session.session_id)')
  assert.ok(idxCancel >= 0, '删除前必须 await cancelRunningTurnsOf')
  assert.ok(idxDelete >= 0, '未找到 deleteSession 调用')
  assert.ok(idxCancel < idxDelete, '取消全部运行轮次必须发生在 deleteSession 之前')
  // 位于函数体层级（非 wasCurrent 分支内）→ 非当前会话也取消
  assert.match(body, /^  await cancelRunningTurnsOf\(session\.session_id\)$/m,
    'cancelRunningTurnsOf 不得包在 wasCurrent 条件内')
  assert.match(body, /if \(wasCurrent\) \{\s*mq\.emit\(EventNames\.sessionCancelLlm\)/,
    '当前会话仍发 session-cancel-llm（Ask 本地状态；与按轮取消重复可接受）')
})