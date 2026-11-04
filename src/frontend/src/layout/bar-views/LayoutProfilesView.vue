<template>
  <div ref="root" class="lp">
    <button ref="btnRef" class="lp-btn" type="button" :title="btnTitle" @mousedown.stop @click="toggle">
      <LfIcon name="grid" :size="15" />
    </button>

    <!-- 弹层 Teleport 到 body：栏内 view / zone 都是 overflow:hidden，绝对定位弹层会被裁掉 -->
    <Teleport to="body">
      <div v-if="open" ref="popRef" class="lp-pop" :style="popStyle" @mousedown.stop>
        <button
          v-for="p in store.sorted"
          :key="p.id"
          type="button"
          class="lp-item"
          :class="{ active: p.id === store.activeId }"
          @click="switchTo(p.id)"
        >
          <span class="lp-tick">{{ p.id === store.activeId ? '✓' : '' }}</span>
          <span class="lp-nm">{{ p.name }}</span>
        </button>

        <div class="lp-sep" />
        <button type="button" class="lp-item" :disabled="!store.canCreate" @click="newLayout">
          新建布局…
        </button>
        <button type="button" class="lp-item" @click="editCurrent">编辑当前布局</button>
        <button type="button" class="lp-item" :disabled="!store.active" @click="startRename">
          重命名当前布局…
        </button>
        <button
          type="button"
          class="lp-item danger"
          :disabled="!store.active"
          @click="removeCurrent"
        >
          {{ confirmDelete ? '再次点击确认删除' : '删除当前布局' }}
        </button>
        <button v-if="store.activeIsSeed" type="button" class="lp-item" @click="restore">
          恢复初始布局
        </button>
      </div>

      <!-- 打开布局编辑框 / 新建佈局前：重扫插件（PLUGIN-LOAD-1 D2/D5），期间禁止操作 -->
      <div v-if="refreshing" class="lp-mask" @mousedown.stop>
        <div class="lp-mask-card">
          <LfIcon name="loading" :size="14" class="lp-spin" />
          <span>正在刷新插件…</span>
        </div>
      </div>

      <div v-if="renaming" ref="popRef" class="lp-rename" :style="popStyle" @mousedown.stop>
        <input
          v-model="renameText"
          class="lp-input"
          maxlength="24"
          @keydown.enter.prevent="commitRename"
          @keydown.esc="cancelRename"
        />
        <button type="button" class="lp-ok" @click="commitRename">确定</button>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  useProfileStore,
  switchLayoutProfile,
  openNewLayoutDialog,
  editLayoutProfile,
  renameLayoutProfile,
  deleteLayoutProfile,
  restoreLayoutProfile,
} from '@ckfw/layout/profiles.js'
import { refreshPluginsHard } from '../../../packages/plugin-ui/src/index.js'
import { message } from '@ckfw/components/ui/index.js'

const store = useProfileStore()
const root = ref(null)
const btnRef = ref(null)
const popRef = ref(null)
const open = ref(false)
const renaming = ref(false)
const renameText = ref('')
const confirmDelete = ref(false)
/** 打开编辑框 / 新建对话框前的插件重扫（PLUGIN-LOAD-1 D2）；期间遮罩禁操作（D5）。 */
const refreshing = ref(false)

const activeName = computed(() => store.active?.name || '布局')
const btnTitle = computed(() => `布局：${activeName.value}`)

/** 弹层定位：贴着按钮右下（fixed，Teleport 到 body 后仍跟随窗口） */
const POP_MIN_WIDTH = 200
const popStyle = computed(() => {
  // 必须引用 open / renaming：getBoundingClientRect 不是响应式依赖，computed
  // 无响应式依赖时首次求值后永久缓存 → 窗口最大化 / resize 后复用旧坐标，
  // 弹层飞到按钮旧位置。引用开合状态保证每次打开都重新实测。
  if (!open.value && !renaming.value) return {}
  const el = btnRef.value
  if (!el) return {}
  const rect = el.getBoundingClientRect()
  const left = Math.max(8, Math.min(rect.right - POP_MIN_WIDTH, window.innerWidth - POP_MIN_WIDTH - 8))
  return {
    position: 'fixed',
    top: rect.bottom + 4 + 'px',
    left: left + 'px',
    minWidth: POP_MIN_WIDTH + 'px',
    zIndex: 3000,
  }
})

function toggle() {
  open.value = !open.value
  confirmDelete.value = false
}

function close() {
  open.value = false
  confirmDelete.value = false
}

function switchTo(id) {
  close()
  if (id !== store.activeId) switchLayoutProfile(id)
}

/**
 * 打开布局编辑框 / 新建布局对话框前先重扫插件（PLUGIN-LOAD-1 D2）：
 * 新放进去的插件可被加载，删掉的插件被注销；刷新期间遮罩禁操作（D5）。
 * 刷新失败 → 提示并**取消打开**（D4：不带着未收敛的清单继续编辑）。
 * @returns {Promise<boolean>} 是否可继续打开
 */
async function preparePlugins() {
  if (refreshing.value) return true
  refreshing.value = true
  try {
    await refreshPluginsHard()
    return true
  } catch (e) {
    message.error(`插件刷新失败，已取消：${(e && e.message) || e}`)
    return false
  } finally {
    refreshing.value = false
  }
}

async function newLayout() {
  close()
  if (!(await preparePlugins())) return
  openNewLayoutDialog()
}

async function editCurrent() {
  close()
  if (!(await preparePlugins())) return
  editLayoutProfile()
}

function startRename() {
  const p = store.active
  if (!p) return
  renaming.value = true
  renameText.value = p.name
  open.value = false
}

function commitRename() {
  const name = renameText.value.trim()
  if (!name) return
  renameLayoutProfile(store.activeId, name)
  renaming.value = false
}

function cancelRename() {
  renaming.value = false
}

function removeCurrent() {
  if (!store.active) return
  if (!confirmDelete.value) {
    confirmDelete.value = true
    return
  }
  deleteLayoutProfile(store.activeId)
  close()
}

function restore() {
  close()
  restoreLayoutProfile(store.activeId)
}

function onDocDown(e) {
  if (root.value && root.value.contains(e.target)) return
  // 弹层已 Teleport 到 body，不在 root 内 → 单独判定
  if (popRef.value && popRef.value.contains(e.target)) return
  close()
  if (renaming.value) cancelRename()
}

onMounted(() => document.addEventListener('mousedown', onDocDown, true))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocDown, true))
</script>

<style scoped>
.lp {
  position: relative;
  display: flex;
  align-items: center;
  /* 历史文档可能持久化过较宽的 view 盒（width=120），按钮贴右，
     避免与右侧窗口控件之间留死空隙 */
  justify-content: flex-end;
  height: 100%;
}
.lp-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 8px;
  border: 1px solid transparent;
  border-radius: 6px;
  background: transparent;
  color: var(--lf-text, #222);
  font-size: 11px;
  cursor: pointer;
}
.lp-btn:hover {
  background: var(--lf-hover, rgba(0, 0, 0, 0.06));
}
.lp-pop {
  min-width: 188px;
  max-height: calc(100vh - 80px);
  overflow: auto;
  padding: 4px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 7px;
  background: var(--lf-surface, #fff);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
}
.lp-item {
  display: flex;
  align-items: center;
  gap: 4px;
  width: 100%;
  height: 26px;
  padding: 0 8px;
  border: none;
  border-radius: 5px;
  background: transparent;
  color: inherit;
  font-size: 11px;
  text-align: left;
  cursor: pointer;
}
.lp-item:hover:not(:disabled) {
  background: var(--lf-hover, rgba(0, 0, 0, 0.06));
}
.lp-item:disabled {
  opacity: 0.45;
  cursor: default;
}
.lp-item.active {
  color: var(--lf-accent, #3b82f6);
  font-weight: 600;
}
.lp-item.danger {
  color: #d93025;
}
.lp-tick {
  width: 12px;
  flex: none;
  text-align: center;
}
.lp-nm {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.lp-sep {
  height: 1px;
  margin: 4px 2px;
  background: var(--lf-border, #d8dee9);
}
.lp-rename {
  display: flex;
  gap: 4px;
  padding: 6px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 7px;
  background: var(--lf-surface, #fff);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
}
.lp-input {
  width: 130px;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 4px;
  font-size: 11px;
  outline: none;
}
.lp-ok {
  height: 24px;
  padding: 0 8px;
  border: 1px solid var(--lf-accent, #3b82f6);
  border-radius: 4px;
  background: var(--lf-accent, #3b82f6);
  color: #fff;
  font-size: 11px;
  cursor: pointer;
}
/* 插件重扫遮罩（PLUGIN-LOAD-1 D5）：不可关闭、盖住整窗，只提示一行 */
.lp-mask {
  position: fixed;
  inset: 0;
  z-index: 3200;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(18, 26, 40, 0.28);
}
.lp-mask-card {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 16px;
  border-radius: 8px;
  background: var(--lf-surface, #fff);
  box-shadow: 0 10px 28px rgba(10, 18, 32, 0.24);
  color: var(--lf-text, #222);
  font-size: 12px;
}
.lp-spin {
  animation: lp-spin 0.9s linear infinite;
}
@keyframes lp-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
