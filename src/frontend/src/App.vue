<template>
  <!-- 认证形态（desktop / browser）未认证 → 登录/注册视图（61 §4.6：`authed` 由服务端判定） -->
  <AuthView v-if="showAuth" :initial-step="showLogin ? 'creds' : 'dir'" @ready="onAuthReady" />
  <!-- 视图分派（24 §6.1 / U-2 已决）：**以 URL 为准** —— `?session-id=…#chat` → 纯对话窗口；
       其余 → 主窗口完整布局。宿主零视图判断（不注入 `__ck.view`）。 -->
  <ChatOnlyView v-else-if="ready && chatOnly" :session-id="route.sessionId" />
  <MainLayout v-else-if="ready" />
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
import AuthView from './views/auth/AuthView.vue'
import { useInstanceClaim, CLAIM_OK, CLAIM_UNANSWERED } from './composables/useInstanceClaim'
import { useAuth } from './composables/useAuth'
import { useUserConfigSync } from './composables/useUserConfigSync'
import { requireAuth } from './utils/instanceState.js'
import { parseViewRoute, VIEW_CHAT } from './utils/viewRoute.js'
import { prefetchInitData } from './api/file'

const { t } = useI18n()
const { state, claim } = useInstanceClaim()
const { signedIn } = useAuth()

// usr 配置（theme / locale）跨窗口**即时同步**（61 §3.1 · 24 §6.5 · WIN-021）：本窗口订阅
// data-user-config-changed 并各自应用 —— 主窗口（Toolbar）与纯对话窗口（无 Toolbar）都经此
// 入口，保证「每个窗口都收到并各自应用」。
useUserConfigSync()

// URL 分派（一次性解析：无前端路由，URL 不再变）。
const route = parseViewRoute(window.location.search, window.location.hash)
const chatOnly = route.view === VIEW_CHAT

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
  if (!ready.value || chatOnly) return
  if (dslStep.isDslStep) return // DSL 步骤只读窗口同口径：不读活动会话、不广播 session-changed
  try {
    const activeRes = await getActiveSessionID()
    const sessionId = activeRes?.session_id || null
    mq.emit(EventNames.sessionChanged, { session_id: sessionId })
  } catch (e) {
    console.warn('[App] init session-changed failed:', e)
    mq.emit(EventNames.sessionChanged, { session_id: null })
  }
}

// 登录 / 选目录 + 认领完成（AuthView 内部已 claim 成功）→ 等主页挂载（nextTick）→ 既有初始化。
async function onAuthReady() {
  await nextTick()
  init()
}

// desktop（免鉴权）：启动即认领（不带 user/work_dir，服务端用启动参数）；
// 认证形态：由 AuthView「登录 → 选目录」后认领（61 §4.6 流程），此处不预先发起。
if (!requireAuth()) {
  // 与认领**并行**预取 init-data：布局/UI 恢复（MainLayout.applyLayout）不再等主视图挂载
  // 才发起 → 首帧即按落盘布局渲染（消除默认布局闪现；见 api/file.js 预取说明）。
  // 对话窗口不预取：它不参与 layout/window 恢复（不写也不读几何，24 §3.3 C5）。
  if (!chatOnly) prefetchInitData()
  claim().then(async () => {
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
