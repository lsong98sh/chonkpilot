/**
 * gui.* 两段式异步能力**调用侧适配**（🆕 2026-10-09）。
 *
 * 契约（61 §1）：请求与结果拆两条消息——
 *   请求 `gui.select-dir{widget_id}` / `gui.capture{widget_id}`（壳/server 受理，result={ok}）；
 *   结果 `gui.selected-dir{widget_id, status, path?}` / `gui.captured{widget_id, status, ...}`（事件）。
 * 本模块把「请求 + 结果」按 `widget_id`（+ 请求的 `instance_id`）关联包成 Promise：
 *   - status=ok → resolve（带结果字段）；status=cancelled → resolve（空结果）；
 *   - status=error / 传输失败（后端不可达）→ reject → 调用方 `catch`。
 *
 * **防二次点击**由调用方负责（等待态期间禁用控件）；本模块只保证同一 `widget_id` 的
 * 结果唯一收口（首个匹配结果即解除监听）。
 */
import mq from './mq'
import { resolvePublishError } from './publishError.js'
import { currentInstanceId } from './instanceState.js'
import {
  MsgTopics,
  GuiSelectedDirKeys,
  GuiCapturedKeys,
} from '../events/msgkeys.js'

/** 事件是否属本调用（同 widget_id；事件回带 instance_id 时须同实例）。 */
function matches(d, widgetId, instanceId) {
  if (!d) return false
  if (d.instance_id !== undefined && instanceId && String(d.instance_id) !== String(instanceId)) return false
  return String(d.widget_id) === String(widgetId)
}

/**
 * 通用两段式：发 `reqTopic`（载荷含 widget_id），等 `resTopic` 结果事件（按 widget_id 关联）。
 * 传输/受理失败 → reject；结果事件到达 → resolve（原样返回 event）。
 */
function requestWithResult(reqTopic, resTopic, widgetId) {
  return new Promise((resolve, reject) => {
    let unsub = () => {}
    const iid = currentInstanceId()
    const key = String(widgetId)
    const onRes = (d) => {
      if (!matches(d, key, iid)) return
      unsub()
      resolve(d)
    }
    unsub = mq.on(resTopic, onRes)
    mq.emit(reqTopic, { widget_id: key }, { timeout: 30000 })
      .then((env) => {
        const backend = env && env.backend
        if (!backend || !backend.ok) {
          unsub()
          const emsg = resolvePublishError(backend) || (backend && backend.errors && backend.errors[0]) || 'backend unreachable'
          reject(new Error(reqTopic + ': ' + emsg))
        }
      })
      .catch((e) => { unsub(); reject(e) })
  })
}

/**
 * 选目录（两段式）：`gui.select-dir{widget_id}` → `gui.selected-dir{widget_id, status, path?}`。
 * 返回 `{path}`（取消 = `{path:''}`）；失败/取消以外的错误 → reject。
 */
export async function selectDir(widgetId = 'dir') {
  const d = await requestWithResult(MsgTopics.guiSelectDir, MsgTopics.guiSelectedDir, widgetId)
  const st = d[GuiSelectedDirKeys.status]
  if (st === 'ok') return { path: d[GuiSelectedDirKeys.path] || '' }
  if (st === 'cancelled') return { path: '' }
  throw new Error(MsgTopics.guiSelectedDir + ': ' + st)
}

/**
 * 截图（两段式）：`gui.capture{widget_id}` → `gui.captured{widget_id, status, b64?}`。
 * 返回 `{b64}`（截图字节 base64，供本地预览 + 随后 `gui.upload{...,dir:"screenshot"}`
 * 落盘）；失败 → reject。
 */
export async function captureWindow(widgetId = 'capture') {
  const d = await requestWithResult(MsgTopics.guiCapture, MsgTopics.guiCaptured, widgetId)
  if (d[GuiCapturedKeys.status] !== 'ok') throw new Error(MsgTopics.guiCaptured + ': ' + d[GuiCapturedKeys.status])
  return { b64: d[GuiCapturedKeys.b64] }
}
