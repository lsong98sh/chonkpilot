/**
 * 长任务状态栏条目 → preview 详情页（STB-1）。
 *
 * 详情 view（`status-memory` / `status-compress`）在 `view-definitions.js` 里声明 `host:'preview'`，
 * 打开由 `layout/openView.js#openView` 按注册表声明分发（落既有 `preview-tab-open{kind}`，
 * CodeView 功能页）——**零新增 MQ 主题**，也不再经过任何「viewKey → 目标页」翻译表。
 *
 * 注：**索引进度不在此列**（PLUGIN-UI-2）——codegraph 插件自带 `badge`（状态栏紧凑视图）与
 * `status`（完整页），宿主不再持有插件业务详情页。
 */
import { openView } from './openView.js'

/** 长任务类 → preview 详情 view（同时登记在 view-definitions / views.registry / CodeView 功能页） */
export const STATUS_DETAIL_VIEWS = {
  memory: 'status-memory',
  compress: 'status-compress',
}

/**
 * 打开某类长任务的详情页。
 * @param {'memory'|'compress'} kind
 * @param {{bus?: object}} [opts] 注入 mq（单测用；缺省 = 宿主 mq）
 * @returns {{ok:boolean, topic?:string, payload?:object, reasonKey?:string}}
 */
export function openStatusDetail(kind, opts) {
  const viewKey = STATUS_DETAIL_VIEWS[kind]
  if (!viewKey) return { ok: false, reasonKey: 'unknownTarget' }
  return openView(viewKey, opts)
}
