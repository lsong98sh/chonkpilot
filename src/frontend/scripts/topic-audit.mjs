/**
 * mq topic 审计（纯函数，仓库级）。
 *
 * 校验 `src/frontend/src/layout-free/events/event-names.js` 里的每个 topic 是否
 * **既有发布方也有订阅方**：
 *   - 只订阅不发布 → 订阅是死代码（组件绕开 mq 直接调了 store）；
 *   - 只发布不订阅 → 事件发出去没人处理。
 *
 * 来源：UILAYOUT-1 §7 P1④ / §3.13 —— 子工程 `scripts/topic-audit.mjs` 的断言
 * **迁入仓库级**：由 `scripts/audit-topics.mjs`（CLI，`npm run audit:topics`）与
 * `test/layoutFreeTopicAudit.test.js`（随 `npm test` 跑）共用。
 */
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

/** 只用来「阻止冒泡」的主题，本来就不需要订阅方 */
const NO_SUB_NEEDED = new Set(['containerClick'])

/** 历史别名主题：由 mqCommands 内部转发，字符串搜索看不到直接发布方 */
const ALIAS_PUBLISHED = new Set(['drawerTabAdd', 'drawerTabRemove', 'drawerTabMove', 'drawerTabUpdate'])

/**
 * 发布方**由宿主承担**的主题（**合法豁免**，UILAYOUT-1 裁决 4（2026-10-16））。
 *
 * 裁决 4 把这一类从「待补（hostPending）」正式改为**合法豁免并登记**：不再是缺陷，
 * 而是「该主题的发布方天然在宿主层、机制层只负责订阅」的既定分工。当前仅剩：
 *   - `dialogCloseTop`（`lf-layout-close-top-dialog`）：语义 = 「关闭最上层对话框」，
 *     一期由宿主全局对话框栈承担（机制层的 `dialogClose` 已改由 `DialogManager.onClose`
 *     透出后于 `mqCommands` 内发布，见下 `MQ_EMIT_PUBLISHED`）。
 */
const HOST_EXEMPT = new Set(['dialogCloseTop'])

/**
 * **无生产者**的主题（登记为无源；UILAYOUT-1 裁决 4）。
 *
 * `keysSetValue`（`lf-keys-set-value`）= 编辑器「变量写值」通道。一期布局**全 singleton**、
 * 无多实例变量生产者（参考工程里由多实例预置视图产生）→ 机制层只有订阅、没有发布方；
 * 登记为「无源」合法状态，**不算孤儿**（不阻塞审计）。
 *
 * `areaToggleVisible`（`lf-area-toggle-visible`）= 运行态区域显隐的**用户可配置命令**入口
 * （UILAYOUT-2 D2）：默认布局不预置按钮，发布方由用户配置的 `publish-mq` 条目承担 →
 * 机制层只有订阅、没有源码内发布方，登记为「无源」合法状态。
 */
const NO_SOURCE = new Set(['keysSetValue', 'areaToggleVisible'])

/**
 * 发布方**在 `mqCommands.js` 内**（审计默认排除该文件以避免把「订阅点」误判为发布方）——
 * 裁决 4 的「调审计口径」：允许把 mqCommands 里的**直接 emit 调用**（`emit(EventNames.<key>`）
 * 计为发布方。当前覆盖 `dialogClose`（`DialogManager.onClose` 透出后广播）。
 */
function mqCommandEmitKeys(mqCmdText) {
  const keys = new Set()
  for (const m of mqCmdText.matchAll(/emit\(\s*EventNames\.(\w+)/g)) keys.add(m[1])
  return keys
}

function sourceFiles(dir, out = []) {
  for (const f of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, f.name)
    if (f.isDirectory()) sourceFiles(p, out)
    else if (/\.(vue|js)$/.test(f.name)) out.push(p)
  }
  return out
}

/**
 * @param {string} root layout-free 子系统根目录（绝对路径或相对 cwd）
 */
export function findOrphanTopics(root) {
  const en = readFileSync(join(root, 'events', 'event-names.js'), 'utf8')
  const defined = [...en.matchAll(/^\s{2}(\w+):\s*'([^']+)'/gm)].map((m) => ({ key: m[1], topic: m[2] }))

  const mqCmdPath = join(root, 'stores', 'mqCommands.js')
  const mqCmd = readFileSync(mqCmdPath, 'utf8')
  /** mqCommands 内的直接 emit（裁决 4 口径） */
  const mqCmdEmitKeys = mqCommandEmitKeys(mqCmd)
  const otherText = sourceFiles(root)
    .filter((f) => f !== mqCmdPath && !f.endsWith('event-names.js'))
    .map((f) => readFileSync(f, 'utf8'))
    .join('\n')

  const hasPub = (d) =>
    otherText.includes(`EventNames.${d.key}`) ||
    otherText.includes(`'${d.topic}'`) ||
    otherText.includes(`"${d.topic}"`) ||
    mqCmdEmitKeys.has(d.key) ||
    new RegExp(`emit\\(\\s*['"\`]${d.topic}['"\`]`).test(otherText)

  const hasSub = (d) => mqCmd.includes(`EventNames.${d.key}`) || NO_SUB_NEEDED.has(d.key)

  return {
    total: defined.length,
    topics: defined.map((d) => d.topic),
    keys: defined.map((d) => d.key),
    noPub: defined
      .filter((d) => hasSub(d) && !hasPub(d) && !ALIAS_PUBLISHED.has(d.key) && !HOST_EXEMPT.has(d.key) && !NO_SOURCE.has(d.key))
      .map((d) => d.topic),
    /** 发布方由宿主承担（**合法豁免**，登记见裁决 4 / 设计稿 §4.1） */
    hostExempt: defined
      .filter((d) => hasSub(d) && !hasPub(d) && HOST_EXEMPT.has(d.key))
      .map((d) => d.topic),
    /** **无生产者**（登记为无源，裁决 4） */
    noSource: defined
      .filter((d) => hasSub(d) && !hasPub(d) && NO_SOURCE.has(d.key))
      .map((d) => d.topic),
    noSub: defined.filter((d) => hasPub(d) && !hasSub(d)).map((d) => d.topic),
  }
}
