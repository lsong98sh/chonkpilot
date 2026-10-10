/**
 * 项目（实例绑定）切换的**就地重初始化**（2026-10-10，去整页重载）。
 *
 * 背景：browser 形态「打开目录」= `gui.dir.open` —— 服务端把**本实例** work_dir 切到目标目录
 * （内部复用 `instance-claim` 重绑定：实例 id 不变，仅 work_dir / 数据面切换，见 19 §8.9 §C /
 * 42 §2.23）。过去前端选后 `window.location.reload()` 整页重载来刷新数据面 —— 浏览器端实测
 * 会**卡死交互**（server 侧无异常），故改为**就地刷新**（不重载页面）。
 *
 * 做法（最小且正确）：
 *   - 自增 `projectEpoch` → `MainLayout` 以新 key **整树重挂载**：文件树 / 文件预览页签 /
 *     会话导航 / 会话面板 / 任务区等全部随 `onMounted` 重新拉取各自数据（等价于整页重载后
 *     的重建，但不丢 JS 上下文）；
 *   - 触发 App 注册的**重初始化序列**（清 init-data 单飞缓存 → 重读新项目活动会话 →
 *     广播 `session-changed`），与启动就绪（`App.init`）**复用同一段代码**。
 *
 * 覆盖范围：**所有随实例绑定变化的数据面**（init-data 含 workDir/布局/已打开文件 · 文件树 ·
 * 会话列表 · 当前会话与任务树 · 预览页签）均被重置；实例 id 与鉴权（cookie）不变 → 无需重新登录。
 * 复用既有主题与前端内部事件，**零新增/修改消息面**（61 为唯一准则）。
 *
 * 本模块为**叶子**（仅依赖 vue 的 ref）→ 可被 App / Toolbar 安全引用，无循环依赖。
 */
import { ref } from 'vue'

/** 项目纪元：切换项目 → 自增 → 主布局整树重挂载（`:key`）。 */
export const projectEpoch = ref(0)

let reinitHandler = null

/** 注册「就地重初始化」序列（App 引导期注册一次；重复调用覆盖）。 */
export function setProjectReinitHandler(fn) {
  reinitHandler = fn
}

/**
 * 就地切换到新项目：自增项目纪元（触发主布局重挂载）+ 执行已注册的重初始化序列。
 * 由「打开目录」入口在 `gui.dir.open` 成功后调用（browser 形态）。
 */
export async function reinitProjectState() {
  projectEpoch.value += 1
  if (reinitHandler) await reinitHandler()
}
