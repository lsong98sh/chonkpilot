/**
 * `node --import` 引导：注册 `@ckfw` 解析钩子（见 alias-hook.mjs）。
 */
import { register } from 'node:module'

register('./alias-hook.mjs', import.meta.url)
