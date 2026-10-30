/**
 * 打开一个 view（UILAYOUT-1 §3.5 后续修订；2026-10-30 定稿）。
 *
 * 口径（用户裁决）：
 *   - 组件只提供**注册 view**：`viewKey` ↔ 组件实例/module 的关联由 `view-registry.js#registerViews` 做，
 *     **不存在**第二份「viewKey → 具体 Vue / 目标页」的翻译表（原 `layout/commands.js` 及
 *     `PREVIEW_TAB_TARGETS` 白名单已删除）。
 *   - 「怎么打开」由该 view 的**定义自己声明**（`view-definitions.js` 的 `host`，插件 view 另有
 *     `containers`），本模块只按声明分发。
 *
 * 承载方式（`host`）：
 *   - `preview`  → 预览区功能页：`preview-tab-open{kind:viewKey}`（跨模块，走 MQ 合法）
 *   - `dialog`   → 对话框（当前无 view 声明，拒绝；后续由 dialog 服务承载）
 *   - `layout`   → 已在布局区/页签区里渲染，不在此"打开"
 *   - `bar`      → 栏内嵌 view，不可在此打开
 * 插件 view：定义 `containers` 含 `preview` → `preview-tab-open{kind:'plugin-page', plugin, page}`。
 */
import mq from '@ckfw/utils/mq.js'
import { EventNames } from '../events/event-names'
import { definitionOf } from './view-definitions.js'

function emit(bus, topic, payload) {
  try {
    bus.emit(topic, payload)
  } catch (_) {
    return { ok: false, reasonKey: 'emitFailed' }
  }
  return { ok: true, topic, payload }
}

/**
 * 打开 view（按注册表声明分发）。
 * @param {string} viewKey 视图 key（内置或插件 `plugin:<id>:<view>`）
 * @param {{title?: string, bus?: object}} [opts]
 * @returns {{ok:boolean, topic?:string, payload?:object, reasonKey?:string}}
 */
export function openView(viewKey, { title = '', bus = mq } = {}) {
  const key = String(viewKey || '')
  const def = definitionOf(key)
  if (!def) return { ok: false, reasonKey: 'unknownTarget' }

  // 插件 iframe view：声明 preview 容器 → 预览区插件页（CodeView 自管页签）
  if (def.plugin && Array.isArray(def.containers) && def.containers.includes('preview')) {
    return emit(bus, EventNames.previewTabOpen, {
      kind: 'plugin-page',
      plugin: def.plugin.id,
      page: def.plugin.view,
      title: def.label || title,
    })
  }

  if (def.host === 'preview') {
    return emit(bus, EventNames.previewTabOpen, { kind: key })
  }
  if (def.host === 'bar') return { ok: false, reasonKey: 'barViewNotInTabs' }
  if (def.host === 'dialog') return { ok: false, reasonKey: 'noDialogService' }
  return { ok: false, reasonKey: 'notOpenable' }
}
