import { ref } from 'vue'
import bridge from './bridge'

/**
 * File change event channel — module-level singleton.
 * Backend FileWatcher pushes file:dir-contents events every 60ms.
 */

let callback = null
const loading = ref(false)

bridge.on('file:dir-contents', (data) => {
  if (data?.dir && callback) {
    callback([{ dir: data.dir, children: data.children || [] }])
  }
})

function onFileChanged(cb) {
  callback = cb
}

export {
  loading,
  onFileChanged,
}
