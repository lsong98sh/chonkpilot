/**
 * `GET /dirs` 客户端（browser 形态）：native 目录选择器（`gui.dir.open-dialog`）的
 * **服务端等价面**（非 MQ HTTP 路由，见 19 §8.9）。
 *
 * 响应：`{instance_id, work_dir, dirs:[{path,name,depth}]}` —— 本 instance 的 work_dir 及其
 * 子目录（服务端已限作用域：只读、上限 200 项 / 深度 3、跳过隐藏与 skipDirs）。
 * `work_dir` 由服务端绑定，客户端**不可改**、也不得据此拼接任意绝对路径。
 */
import { normPath } from '../utils/dirPicker.js'

/**
 * 拉取本 instance 允许的目录清单。
 * @returns {Promise<{workDir: string, dirs: Array<{path: string, name: string, depth: number}>}>}
 * @throws {Error} 网络/HTTP/解析失败（调用方须给出明确提示，不得静默）
 */
export async function fetchDirs() {
  // cache: 'no-store' —— 目录清单是实时只读面，避免浏览器缓存旧列表。
  const res = await fetch('/dirs', { cache: 'no-store' })
  if (!res || !res.ok) {
    throw new Error('GET /dirs: HTTP ' + (res ? res.status : 'network error'))
  }
  let data
  try {
    data = await res.json()
  } catch (e) {
    throw new Error('GET /dirs: bad json (' + (e && e.message ? e.message : e) + ')')
  }
  const dirs = data && Array.isArray(data.dirs) ? data.dirs : []
  return {
    workDir: normPath(data && data.work_dir),
    dirs: dirs
      .filter((d) => d && typeof d.path === 'string' && d.path !== '')
      .map((d) => ({ path: normPath(d.path), name: d.name || '', depth: d.depth || 0 })),
  }
}
