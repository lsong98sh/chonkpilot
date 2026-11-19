/**
 * 工作区项目注册（SESSION-SCOPE-1）：一个窗口内可同时打开多个 workdir。
 *
 * 与 `gui.dir.open` 的区别：本接口只把项目登记到宿主工作区注册表（数据根/文件监听/事件订阅），
 * **不打开新窗口**；页面由当前窗口的 workdir 树承载。
 */
import mq from '@ckfw/utils/mq.js'
import { resolvePublishError } from '../utils/publishError.js'
import { MsgTopics } from '../events/msgkeys.js'

const PROJECT_TOPICS = {
  add: MsgTopics.guiProjectAdd,
  remove: MsgTopics.guiProjectRemove,
  list: MsgTopics.guiProjectList,
}

function guiProject(action, body = {}) {
  const topic = PROJECT_TOPICS[action]
  if (!topic) throw new Error('gui.project.' + action + ': unknown action')
  return mq.emit(topic, body, { timeout: 30000 }).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(topic + ': backend unreachable')
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = resolvePublishError(backend) || (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) throw new Error(emsg || topic + ' failed')
    return p
  })
}

export function addProject(workDir, dataDir = '') {
  return guiProject('add', { work_dir: workDir, data_dir: dataDir })
}

export function removeProject(workDir) {
  return guiProject('remove', { work_dir: workDir })
}

export async function listProjects() {
  const r = await guiProject('list', {})
  return Array.isArray(r.projects) ? r.projects : []
}