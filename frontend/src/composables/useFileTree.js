import { ref } from 'vue'
import bridge from '../utils/bridge'

/**
 * useFileTree — 文件变更事件通道
 *
 * 后端 FileWatcher 每 60ms 合并推送一次 file:dir-contents 事件。
 * 本模块直接透传，不做额外防抖，前端直接根据推送修改 treeData。
 *
 * 单例模式：模块级变量跨组件共享。
 */

let subscribed = false
let callback = null

const loading = ref(false)

export function useFileTree() {
  if (!subscribed) {
    subscribed = true
    bridge.on('file:dir-contents', (data) => {
      if (data?.dir && callback) {
        callback([{ dir: data.dir, children: data.children || [] }])
      }
    })
  }

  function onFileChanged(cb) {
    callback = cb
  }

  function teardown() {
    callback = null
    subscribed = false
  }

  return { loading, onFileChanged, teardown }
}
