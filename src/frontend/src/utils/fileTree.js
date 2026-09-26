import { ref } from 'vue'
import mq from './mq'
import { EventNames } from '../events/event-names'

/**
 * File change event channel — module-level singleton.
 * 文件域直连 filesys（61-消息一览 §2.4）：订阅统一变更广播 filesys.changed，
 * 按载荷分流：
 *   - 有 children = 目录批次（免二次 filesys.list）→ callback([{dir, children}])，FileTree 整批刷树
 *   - 无 children = 单文件变更（operation: create|write|remove|rename）→ eventCallback（VCS 刷新等）
 */

let callback = null
let eventCallback = null
const loading = ref(false)

mq.on(EventNames.fileDirContents, (data) => {
  if (!data || !data.path) return
  if (Array.isArray(data.children)) {
    // 目录批次：{path(dir), children} → [{dir, children}]
    if (callback) callback([{ dir: data.path, children: data.children }])
  } else if (eventCallback) {
    // 单文件：{path, operation}
    eventCallback(data)
  }
})

function onFileChanged(cb) {
  callback = cb
}

function onFileChangedEvent(cb) {
  eventCallback = cb
}

export {
  loading,
  onFileChanged,
  onFileChangedEvent,
}
