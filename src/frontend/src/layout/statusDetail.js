/**
 * 长任务状态栏条目 → preview 详情页（STB-1）。
 *
 * 复用 UILAYOUT-1 的**统一命令模型** `open-view{viewKey,target}`（`layout/commands.js`）：
 * viewKey = 详情 view（`status-memory` / `status-compress` / `status-index`，见 `view-definitions.js`），
 * 落点 = 既有 `preview-tab-open{kind}`（CodeView 功能页页签）——**零新增 MQ 主题**。
 */
import { executeCommand } from './commands.js'

/** 长任务类 → preview 详情 view（同时登记在 view-definitions / views.registry / CodeView 功能页） */
export const STATUS_DETAIL_VIEWS = {
  memory: 'status-memory',
  compress: 'status-compress',
  index: 'status-index',
}

/**
 * 打开某类长任务的详情页。
 * @param {'memory'|'compress'|'index'} kind
 * @param {{bus?: object}} [opts] 注入 mq（单测用；缺省 = 宿主 mq）
 * @returns {{ok:boolean, topic?:string, payload?:object, reasonKey?:string}}
 */
export function openStatusDetail(kind, opts) {
  const viewKey = STATUS_DETAIL_VIEWS[kind]
  if (!viewKey) return { ok: false, reasonKey: 'unknownTarget' }
  return executeCommand({ type: 'open-view', viewKey, target: 'auto' }, opts)
}
