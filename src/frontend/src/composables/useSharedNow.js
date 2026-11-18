/**
 * useSharedNow — 全局共享 1s 时钟（模块级单例）
 *
 * 长会话性能（评审第 11 条）：任务树里每个 running 的 LLM 节点此前各自挂一个
 * `setInterval` 刷新运行时长 → 多个并行子会话时 N 个 timer。现改为**全应用唯一**时钟：
 * 运行节点只 retain/release 引用计数，计数从 0 → 1 时启动唯一定时器，归零时清除。
 *
 * 约束：
 *   - `retain` / `release` 幂等且防负数（release 在 0 时为 no-op）；
 *   - 调用方须自行记住「我是否持有」（同一节点重复 release 不得误停别人的时钟）；
 *   - 首个 retain 时先同步一次 now，避免长时间无运行节点后 elapsed 基于陈旧时间戳。
 */
import { ref } from 'vue'

const now = ref(Date.now())

let timer = null
let refs = 0

function start() {
  if (timer != null) return
  now.value = Date.now() // 起点对齐：避免使用上一次停机时的陈旧 now
  timer = setInterval(() => {
    now.value = Date.now()
  }, 1000)
}

function retain() {
  refs += 1
  if (refs === 1) start()
}

function release() {
  if (refs <= 0) return // 防负数 / 重复 release
  refs -= 1
  if (refs === 0 && timer != null) {
    clearInterval(timer)
    timer = null
  }
}

// 返回同一份 API，调用方无需缓存函数引用。
const api = { now, retain, release }

export function useSharedNow() {
  return api
}