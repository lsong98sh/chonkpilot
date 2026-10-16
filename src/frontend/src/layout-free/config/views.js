/**
 * view 定义读取（机制层不持有任何区块定义）。
 *
 * UILAYOUT-1 §3.4：区块注册表由**宿主**提供，机制层只按 `viewKey` 解析。
 * 宿主经 `createLayoutEngine({ views: { definitions, resolve } })` 注入；
 * 参考工程的 demo 预置视图（`views/presets/*`、`views/registry.js`）已随 P0 移除。
 *
 * 定义字段：`key / label / icon / mode: singleton|multi / host: layout|dialog|bar / designable`
 * （+ 可选 `defaultWidth` / `defaultHeight`：栏内「视图条目」的默认尺寸）
 */
import { viewDefinitions } from '../deps.js'

export function allViews() {
  return viewDefinitions()
}

export function getView(key) {
  if (!key) return undefined
  return viewDefinitions().find((v) => v.key === key)
}

export function viewLabel(key) {
  return getView(key)?.label ?? (key ? `未知视图(${key})` : '空白区域')
}

/** 图标名（可能是名称 / svg 串 / 图片地址 / emoji），交给 <LfIcon> 渲染 */
export function viewIcon(key) {
  return getView(key)?.icon ?? 'square-dashed'
}
