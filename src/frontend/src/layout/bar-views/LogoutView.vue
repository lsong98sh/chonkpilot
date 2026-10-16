<!--
  栏视图条目 `logout`（UILAYOUT-1 裁决 1/2 / §7 P4①）：登出入口。
  **条件显示**由宿主视图自持（desktop 形态 requireAuth=false → 不渲染）。
  为什么不是配置按钮 + publish-mq：登出是**跨模块写操作**（signOut + 复位实例认领 + 复位跨 view 状态），
  无法用单条既有主题表达 → 走宿主视图（与 `chrome-warn` 同口径）。
-->
<template>
  <button v-if="needsAuth" class="auth-btn" :title="$t('toolbar.sign_out')" @click="logout">
    <Icon name="user" :size="15" />
  </button>
</template>

<script setup>
import { Icon } from '../../components/icon'
import { requireAuth } from '../../utils/instanceState'
import { useAuth } from '../../composables/useAuth'
import { useInstanceClaim } from '../../composables/useInstanceClaim'
import { useSelectionStore } from '../../stores/selection'

const needsAuth = requireAuth()
const { signOut } = useAuth()
const { resetClaim } = useInstanceClaim()

async function logout() {
  await signOut()
  resetClaim()
  // 身份边界：清空跨 view 共享状态（UILAYOUT-1 §3.7 P2③）
  useSelectionStore().reset()
}
</script>

<style scoped>
.auth-btn {
  background: transparent;
  border: 1px solid transparent;
  height: 28px;
  padding: 0 6px;
  display: inline-flex;
  align-items: center;
  cursor: pointer;
  -webkit-app-region: no-drag;
}
.auth-btn:hover {
  background: var(--bg-hover);
}
</style>
