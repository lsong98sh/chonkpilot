<template>
  <div ref="root" class="lp">
    <button class="lp-btn" type="button" title="布局" @mousedown.stop @click="toggle">
      <LfIcon name="grid" :size="13" />
      <span class="lp-name">{{ activeName }}</span>
      <LfIcon name="arrow-down" :size="10" />
    </button>

    <div v-if="open" class="lp-pop" @mousedown.stop>
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

    <div v-if="renaming" class="lp-rename" @mousedown.stop>
      <input
        v-model="renameText"
        class="lp-input"
        maxlength="24"
        @keydown.enter.prevent="commitRename"
        @keydown.esc="cancelRename"
      />
      <button type="button" class="lp-ok" @click="commitRename">确定</button>
    </div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useProfileStore } from '../profiles.js'
import { EventNames } from '../../layout-free/events/event-names.js'
import mq from '../../utils/mq'

const store = useProfileStore()
const root = ref(null)
const open = ref(false)
const renaming = ref(false)
const renameText = ref('')
const confirmDelete = ref(false)

const activeName = computed(() => store.active?.name || '布局')

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
  if (id !== store.activeId) mq.emit(EventNames.layoutProfileSwitch, { id })
}

function newLayout() {
  close()
  mq.emit(EventNames.layoutProfileNew, {})
}

function editCurrent() {
  close()
  mq.emit(EventNames.layoutEdit, {})
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
  mq.emit(EventNames.layoutProfileRename, { id: store.activeId, name })
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
  mq.emit(EventNames.layoutProfileDelete, { id: store.activeId })
  close()
}

function restore() {
  close()
  mq.emit(EventNames.layoutProfileRestore, { id: store.activeId })
}

function onDocDown(e) {
  if (!root.value) return
  if (!root.value.contains(e.target)) close()
}

onMounted(() => document.addEventListener('mousedown', onDocDown, true))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocDown, true))
</script>

<style scoped>
.lp {
  position: relative;
  display: flex;
  align-items: center;
  height: 100%;
}
.lp-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  height: 22px;
  padding: 0 7px;
  border: 1px solid transparent;
  border-radius: 5px;
  background: transparent;
  color: var(--lf-text, #222);
  font-size: 11px;
  cursor: pointer;
  max-width: 180px;
}
.lp-btn:hover {
  background: var(--lf-hover, rgba(0, 0, 0, 0.06));
}
.lp-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.lp-pop {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  min-width: 188px;
  padding: 4px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 7px;
  background: var(--lf-surface, #fff);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
  z-index: 40;
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
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  display: flex;
  gap: 4px;
  padding: 6px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 7px;
  background: var(--lf-surface, #fff);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
  z-index: 40;
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
</style>
