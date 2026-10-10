/**
 * gui.show-dialog（服务端下行事件）→ 目录选择器视图 + 结果事件载荷（纯逻辑，无 Vue / 无 DOM；
 * `node --test` 可直接导入）。
 *
 * 契约（61 §1）：`gui.show-dialog{widget_id, kind, options}` —— **仅当处理方无法自行弹出原生框时**
 * （browser/服务端场景）由服务端下发、请 UI 渲染选择器；`kind="select-dir"` 时期望 UI 弹目录选择器，
 * 用户选定 / 取消后 UI 自行发 `gui.selected-dir{widget_id, status, path?}`（`status = ok|cancelled`）。
 *
 * `options` 口径 = 服务端能提供的允许目录清单（**该用户的允许根**：`auth.projectRoots`，缺省用户
 * 数据根；+ 其子目录；深≤3 / ≤200 项 / 跳隐藏与重型目录，与 `httpapi.allowedDirs()` 同源）。
 * UI 只在该清单内选择，不拼接任意绝对路径。
 */
import { normPath, baseName } from './dirPicker.js'
import { GuiShowDialogKeys, GuiSelectedDirKeys } from '../events/msgkeys.js'

/** gui.show-dialog 的 kind：请 UI 弹目录选择器。 */
export const SHOW_DIALOG_KIND_SELECT_DIR = 'select-dir'

/**
 * 校验并归一 gui.show-dialog 事件载荷（仅认 kind="select-dir"）。
 * @param {object} payload 事件载荷 `{widget_id, kind, options}`
 * @returns {null | {widgetId: string, root: string, options: Array<{path,name,depth}>}}
 *   非 select-dir / 非法载荷 → null（调用方忽略）。
 */
export function parseShowDialog(payload) {
  const p = payload || {}
  if (p[GuiShowDialogKeys.kind] !== SHOW_DIALOG_KIND_SELECT_DIR) return null
  const raw = Array.isArray(p[GuiShowDialogKeys.options]) ? p[GuiShowDialogKeys.options] : []
  const options = []
  for (const d of raw) {
    if (!d || typeof d.path !== 'string' || d.path === '') continue
    const path = normPath(d.path)
    options.push({ path, name: d.name || baseName(path), depth: Number(d.depth) || 0 })
  }
  // root = depth 最小项（服务端清单恒以 work_dir 为首、depth=0）；空清单 → ''。
  let root = ''
  let best = Infinity
  for (const d of options) {
    if (d.depth < best) {
      best = d.depth
      root = d.path
    }
  }
  const wid = p[GuiShowDialogKeys.widget_id]
  return {
    widgetId: wid == null ? '' : String(wid),
    root,
    options,
  }
}

/**
 * 由用户选择构造 `gui.selected-dir` 事件载荷（纯函数）。
 * path 非空 → `status:"ok"` + `path`；否则（取消 / 关闭）→ `status:"cancelled"`（不带 path）。
 */
export function selectedDirEvent(widgetId, path) {
  const id = widgetId == null ? '' : String(widgetId)
  if (path) {
    return {
      [GuiSelectedDirKeys.widget_id]: id,
      [GuiSelectedDirKeys.status]: 'ok',
      [GuiSelectedDirKeys.path]: path,
    }
  }
  return {
    [GuiSelectedDirKeys.widget_id]: id,
    [GuiSelectedDirKeys.status]: 'cancelled',
  }
}
