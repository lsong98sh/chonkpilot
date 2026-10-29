/**
 * 测试支撑 · 统一读取「前端源码」（TOPO-1 §2.22.2 B 档后）。
 *
 * 前端源码分两处：
 *   - 应用侧 `src/frontend/src`（业务视图 / api / 业务 utils / 视图登记表）；
 *   - 平台基座 `chonkpilot-framework/host/web/src`（窗口 chrome / 布局引擎 / 通用控件 / mq）。
 *
 * 源码守卫类用例按**相对路径**（如 `components/ui/Input.vue`、`layout/persist.js`）读文件：
 * 先查应用侧，未命中回落基座 —— 与源码里 `@ckfw/...` 引用的实际落点一致。
 */
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))

/** 应用侧源码根（`src/frontend/src`）。 */
export const APP_SRC = join(HERE, '..', '..', 'src')

/** 平台基座源码根（`chonkpilot-framework/host/web/src`）。 */
export const FW_SRC = join(
  HERE, '..', '..', '..', '..', '..', 'chonkpilot-framework', 'host', 'web', 'src',
)

/** 相对路径 → 绝对路径（应用侧优先，未命中回落基座）。 */
export function resolveSrc(rel) {
  const app = join(APP_SRC, rel)
  if (existsSync(app)) return app
  const fw = join(FW_SRC, rel)
  if (existsSync(fw)) return fw
  return app
}

/** 读取源码文本（编码 utf8）。 */
export function readSrc(rel) {
  return readFileSync(resolveSrc(rel), 'utf8')
}

/** 主仓根（`chonkpilot/`：Go 源码 / docs / initdata / public 的唯一源）。 */
export const REPO_ROOT = join(HERE, '..', '..', '..', '..')

/** 读主仓内文件（Go / docs / 契约 / 静态资源；与前端源码无关）。 */
export function readRepo(rel) {
  return readFileSync(join(REPO_ROOT, rel), 'utf8')
}
