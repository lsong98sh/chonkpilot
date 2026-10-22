<template>
  <!-- 日志窗口（PRODSIM-1）：`#log` → 只读日志尾部，不参与认证 / 认领 / 项目视图 -->
  <LogView v-if="logOnly" />
  <!-- 认证形态（desktop / browser）未认证 → 登录/注册视图（61 §4.6：`authed` 由服务端判定） -->
  <AuthView v-else-if="showAuth" :initial-step="showLogin ? 'creds' : 'dir'" @ready="onAuthReady" />
  <!-- 视图分派（24 §6.1 / U-2 已决）：**以 URL 为准** —— `?session-id=…#chat` → 纯对话窗口；
       其余 → 主窗口完整布局。宿主零视图判断（不注入 `__ck.view`）。 -->
  <ChatOnlyView v-else-if="ready && chatOnly" :session-id="route.sessionId" />
  <!-- 项目就地切换（browser「打开目录」）：`:key="projectEpoch"` 自增 → 主布局整树重挂载，
       等价于整页重载后的组件重建（不整页重载）。见 utils/projectReinit.js。 -->
  <MainLayout v-else-if="ready" :key="projectEpoch" />
  <div v-else class="ck-startup">
    <div class="ck-startup__box">{{ t(textKey) }}</div>
  </div>
</template>

<script setup>
import { computed, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import mq from './utils/mq'
import { EventNames } from './events/event-names'
import { getActiveSessionID } from './api/session'
import MainLayout from './views/layout/MainLayout.vue'
import ChatOnlyView from './views/chat/ChatOnlyView.vue'
import LogView from './views/log/LogView.vue'
import AuthView from './views/auth/AuthView.vue'
import { useInstanceClaim, CLAIM_OK, CLAIM_UNANSWERED } from './composables/useInstanceClaim'
import { useAuth } from './composables/useAuth'
import { useUserConfigSync } from './composables/useUserConfigSync'
import { useGuiShowDialog } from './composables/useGuiShowDialog'
import { maybeShowUnfinishedSubChats } from './composables/useSubChats'
import { requireAuth } from './utils/instanceState.js'
import { parseViewRoute, VIEW_CHAT, VIEW_LOG } from './utils/viewRoute.js'
import { prefetchInitData, invalidateInitDataCache } from './api/file'
import { projectEpoch, setProjectReinitHandler } from './utils/projectReinit.js'
import { useSelectionStore } from './stores/selection'

const { t } = useI18n()
const { state, claim } = useInstanceClaim()
const { signedIn } = useAuth()

// usr 配置（theme / locale）跨窗口**即时同步**（61 §3.1 · 24 §6.5 · WIN-021）：本窗口订阅
// data-user-config-changed 并各自应用 —— 主窗口（Toolbar）与纯对话窗口（无 Toolbar）都经此
// 入口，保证「每个窗口都收到并各自应用」。
useUserConfigSync()

// gui.show-dialog 全局消费（browser 形态「选择目录」：服务端无原生框 → 请 UI 弹选择器，
// 用户选完由 UI 自发 gui.selected-dir；模块级单例，只订阅一次）。
useGuiShowDialog()

// URL 分派（一次性解析：无前端路由，URL 不再变）。
const route = parseViewRoute(window.location.search, window.location.hash)
const chatOnly = route.view === VIEW_CHAT
const logOnly = route.view === VIEW_LOG
// PRODSIM-1（多项目主窗口）：主窗口 URL 携带本项目的 work_dir（`?work-dir=`），
// claim 时作为提议上送；缺失（对话窗口 / 旧链接）→ 服务端回落实例登记的 work_dir。
const workDirParam = new URLSearchParams(window.location.search).get('work-dir') || ''

// 认证形态（gui / browser：`__ck.requireAuth`）：需登录 / 选目录后才进主页。
// 首屏 `authed`（服务端读凭证判定）→ 已认证者直接进「选目录」步（**不再要求凭证**）；
// desktop（requireAuth=false）→ 无登录视图，启动即认领（见下方）。
const needCreds = computed(() => requireAuth() && !signedIn.value)
const needDir = computed(() => requireAuth() && signedIn.value && !ready.value)
const showAuth = computed(() => needCreds.value || needDir.value)
const showLogin = computed(() => !signedIn.value)

// **认领成功**（或「无应答」降级态）才进主页 —— 未认证 / 无权访问一律停在前置视图
// （**不静默失败**，也不进半可用主页）。
const ready = computed(() => state.value === CLAIM_OK || state.value === CLAIM_UNANSWERED)

// 前置视图文案（desktop 认领失败 / 认领中）。
const TEXT_KEYS = {
  claiming: 'common.instance_claiming',
  unauthorized: 'common.instance_unauthorized',
  forbidden: 'common.instance_forbidden',
  failed: 'common.instance_failed',
}
const textKey = computed(() => TEXT_KEYS[state.value] || 'common.instance_claiming')

// ─── 应用初始化：认领就绪（主页已挂载）后统一触发 ───
// 对话窗口**跳过**：它绑定 URL 的 `session-id` → 不读活动会话（`session-active-get`）、
// 不广播 `session-changed`（否则会覆盖该窗口绑定的会话，24 §3.3 C5 / §6.2）。
async function init() {
  if (!ready.value || chatOnly || logOnly) return
  try {
    const activeRes = await getActiveSessionID()
    const sessionId = activeRes?.session_id || null
    mq.emit(EventNames.sessionChanged, { session_id: sessionId })
  } catch (e) {
    console.warn('[App] init session-changed failed:', e)
    mq.emit(EventNames.sessionChanged, { session_id: null })
  }
  // 单聊 F10（§3.4.7）：主窗口就绪后判断一次「有未结束子聊天 → 启动提醒对话框」。
  // **仅主窗口**（对话窗口跳过）；`subchat-list{}`（全局，不带 parent_id）；每次启动判断一次
  // （页会话内只判一次，见 useSubChats 的幂等守卫）。
  maybeShowUnfinishedSubChats()
}

// 登录 / 选目录 + 认领完成（AuthView 内部已 claim 成功）→ 等主页挂载（nextTick）→ 既有初始化。
async function onAuthReady() {
  await nextTick()
  init()
}

// 项目**就地切换**（browser「打开目录」：`gui.dir.open` 服务端切本实例 work_dir）→
// Toolbar 调 `reinitProjectState()` 触发本序列，**不整页重载**（见 utils/projectReinit.js）：
//   ① 清 init-data 单飞缓存 → 重挂后的组件重新拉取新项目快照（workDir/布局/已打开文件）；
//   ② 等主布局重挂完成（子组件 onMounted 完毕）；
//   ③ 重跑启动序列 `init()`（读新项目活动会话 → 广播 session-changed，驱动会话树/会话面板）。
setProjectReinitHandler(async () => {
  // ⓪ 跨 view 共享状态复位（UILAYOUT-1 §3.7 P2③）：状态已外置到 store，组件重挂不再自带"归零"
  //    → 必须**先**清（同步，早于下方 nextTick 触发的整树重挂），否则新项目首帧会闪现旧项目的
  //    当前会话 / 任务 / 已打开文件（本序列的既有语义即"当前会话与任务树均被重置"）。
  useSelectionStore().reset()
  invalidateInitDataCache()
  await nextTick()
  await init()
})

// desktop（免鉴权）：启动即认领（不带 user/work_dir，服务端用启动参数）；
// 认证形态：由 AuthView「登录 → 选目录」后认领（61 §4.6 流程），此处不预先发起。
if (!requireAuth() && !logOnly) {
  // 与认领**并行**预取 init-data：布局/UI 恢复（MainLayout.applyLayout）不再等主视图挂载
  // 才发起 → 首帧即按落盘布局渲染（消除默认布局闪现；见 api/file.js 预取说明）。
  // 对话窗口不预取：它不参与 layout/window 恢复（不写也不读几何，24 §3.3 C5）。
  if (!chatOnly) prefetchInitData()
  claim(workDirParam ? { work_dir: workDirParam } : {}).then(async () => {
    await nextTick()
    init()
  })
}
</script>

<style scoped>
.ck-startup {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100vh;
  background: var(--bg-primary, #1e1e1e);
}
.ck-startup__box {
  max-width: 480px;
  padding: 16px 20px;
  border-radius: 6px;
  background: var(--bg-secondary, #252526);
  color: var(--text-primary, #ddd);
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
}
</style>
