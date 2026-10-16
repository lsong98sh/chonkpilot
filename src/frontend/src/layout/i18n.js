/**
 * i18n 适配（UILAYOUT-1 §3.12 / §7 P3④）。
 *
 * 机制层（`layout-free/`）**不认识宿主的 vue-i18n 实例**：它只按 `deps.i18n` 的
 * `t(key, params)` 取值（缺省恒等，standalone 期组件内硬编码）。本模块负责：
 *   1. 把宿主 i18n 包成容器可用的 `t`（自动加 `layoutFree.` 命名空间）；
 *   2. 提供宿主侧（适配层）取值入口 `lfT` —— 命令摘要 / 拒绝原因等文案也走同一份 json。
 *
 * 本模块是**叶子模块**（零 import、零副作用）：不得 import `plugins/i18n`（那里 import 了
 * `*.json`，Node ESM 直跑需要 import attributes）→ 宿主 i18n 由装配层注入
 * （`layout/index.js` → `setLayoutTranslator(createLayoutTranslator(i18n))`）。
 */

/** i18n 命名空间（`locales/{zh-CN,en-US}/layoutFree.json`） */
export const LAYOUT_I18N_NS = 'layoutFree'

let translator = (key) => key

/** 注入容器/适配层文案取值函数（幂等；非法入参忽略）。 */
export function setLayoutTranslator(fn) {
  if (typeof fn === 'function') translator = fn
}

/** 取值：`t('view.chat')` → `layoutFree.view.chat`。 */
export function lfT(key, params) {
  return translator(key, params)
}

/**
 * 用宿主 vue-i18n 实例构造 `t`。
 * @param {{global?: {t?: Function}}} i18n 宿主 i18n（`plugins/i18n.js` 导出的实例）
 */
export function createLayoutTranslator(i18n) {
  const g = i18n && i18n.global
  if (!g || typeof g.t !== 'function') return (key) => key
  return (key, params) => (key ? g.t(`${LAYOUT_I18N_NS}.${key}`, params || {}) : '')
}
