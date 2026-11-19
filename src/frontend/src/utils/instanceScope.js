/**
 * 前端「事件按 instance 过滤」（2026-09-19 实例隔离第二批，缺口 8）。
 *
 * 桥侧已按 instance 过滤（`bridge.forwardEvent` → `acceptEventInstance`）；本模块是前端侧
 * 的**二次防线**：split / browser 下同进程多 instance 时，A 的 `tasks.*` / `mcp-*` 事件
 * 不应被 B 的前端消费（多 instance 同进程不串）。
 *
 * 判据与桥一致（单 instance 恒真）：
 *   - 事件载荷带**非空** `instance_id` 且 ≠ 本实例 → 不属本实例；
 *   - 载荷无 `instance_id`（data-\* 与 filesys.\* 等按 workdir 管理的事件、server 级广播）→ 放行；
 *   - 本实例未标识（无注入）→ 放行（引入过滤前的旧行为）。
 */
import { currentInstanceId } from '@ckfw/utils/mq.js'

/** 事件是否属本实例（缺口 8）。instanceId 缺省 = 当前注入的实例 id。 */
export function eventBelongsToInstance(payload, instanceId = currentInstanceId()) {
  // 带项目作用域的事件：后端已按 sink 的 scope 订阅投递（允许跨 instance），前端不再二次按实例丢。
  if (payload && typeof payload === 'object' && payload.work_dir) return true
  const ev = payload && typeof payload === 'object' && payload.instance_id != null
    ? String(payload.instance_id)
    : ''
  if (!instanceId || !ev) return true
  return ev === instanceId
}
