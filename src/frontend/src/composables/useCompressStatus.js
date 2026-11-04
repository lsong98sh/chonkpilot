// 上下文压缩进行中状态（OP-03，2026-10-06）：
// 内建压缩（`src/lib/llm/server/compress.go`）在每会话 worker 处理前后经**既有通知面**
// tool-notify 投递 notice=compress-start / compress-done（61-消息一览 §4.3），前端据此显示
// **会话级**「正在压缩上下文」指示（completion 后消失）。
//
// 与压缩记录刷新区分开：压缩本身是**内部方法面**（2026-10-31 起 `session-compress` 已退出
// 消息面），本状态只由 compress-start/done 两个取值驱动。
//
// 状态 = 模块级「进行中会话集合」：多会话并发压缩时各自独立；消费方按当前会话查询。
import { ref } from 'vue'

const compressingSessions = ref([])

export function useCompressStatus() {
  // handleNotice 处理一条 tool-notify：compress-start 加入 / compress-done 移除某会话。
  function handleNotice(data) {
    if (!data || !data.notice) return
    const sid = data.session_id || ''
    if (!sid) return
    if (data.notice === 'compress-start') {
      if (!compressingSessions.value.includes(sid)) {
        compressingSessions.value = [...compressingSessions.value, sid]
      }
    } else if (data.notice === 'compress-done') {
      if (compressingSessions.value.includes(sid)) {
        compressingSessions.value = compressingSessions.value.filter((s) => s !== sid)
      }
    }
  }

  // isCompressing 指定会话是否正在压缩。
  function isCompressing(sessionId) {
    return !!sessionId && compressingSessions.value.includes(sessionId)
  }

  return { compressingSessions, handleNotice, isCompressing }
}
