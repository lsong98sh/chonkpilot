<!--
  栏视图条目 `recent-dirs`（UILAYOUT-1 裁决 1 / §7 P4①）：【最近项目】下拉。
  为什么是**视图**而不是配置菜单：条目清单是**运行时动态数据**（`gui.recent.list`），
  配置里的静态菜单树装不下（裁决 2 同口径：实时/动态 → 栏视图条目）。
  【打开目录】本身 = 配置按钮（`publish-mq {topic:'workdir-open'}`），由 `barActions` 订阅处理。
  保留 `.dropdown-arrow` / `.b-dropdown-popper` / `.b-dropdown-item` 等既有类名。
-->
<template>
  <div class="recent-dirs" ref="triggerRef">
    <button class="rd-arrow" :title="$t('toolbar.recent_dirs')" @click="toggle">
      <Icon name="arrow-down" :size="13" />
    </button>
    <Teleport to="body">
      <div v-if="show" class="b-dropdown-popper" :style="popperStyle" @click.stop>
        <div
          v-for="dir in recentDirs"
          :key="dir"
          class="b-dropdown-item recent-row"
          @click="select(dir)"
        >
          <Icon name="folder" :size="13" />
          <span class="recent-item" :title="dir">{{ dir }}</span>
          <button
            type="button"
            class="recent-remove"
            :title="$t('toolbar.remove_recent_dir')"
            @click.stop="remove(dir)"
          >
            <Icon name="close" :size="12" />
          </button>
        </div>
        <div v-if="recentDirs.length === 0" class="b-dropdown-item is-disabled">
          {{ $t('toolbar.no_recent_dirs') }}
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { Icon } from '../../components/icon'
import { loadRecentDirs, removeRecent, openDirAndRefresh } from '../workdirActions'

const triggerRef = ref(null)
const recentDirs = ref([])
const show = ref(false)

const popperStyle = computed(() => {
  const el = triggerRef.value
  if (!el) return {}
  const rect = el.getBoundingClientRect()
  return {
    position: 'fixed',
    top: rect.bottom + 4 + 'px',
    left: rect.left + 'px',
    minWidth: rect.width + 'px',
    zIndex: 9999,
  }
})

async function refresh() {
  recentDirs.value = await loadRecentDirs()
}

function toggle() {
  show.value = !show.value
  if (show.value) refresh()
}

async function select(dir) {
  show.value = false
  await openDirAndRefresh(dir)
}

async function remove(dir) {
  await removeRecent(dir)
  await refresh()
}

function onClickOutside(e) {
  if (!show.value) return
  if (triggerRef.value && triggerRef.value.contains(e.target)) return
  const popper = document.querySelector('.b-dropdown-popper')
  if (popper && popper.contains(e.target)) return
  show.value = false
}

onMounted(() => {
  refresh()
  document.addEventListener('click', onClickOutside, true)
})
onUnmounted(() => document.removeEventListener('click', onClickOutside, true))
</script>

<style scoped>
.recent-dirs {
  display: inline-flex;
  align-items: center;
  -webkit-app-region: no-drag;
}
.rd-arrow {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 28px;
  padding: 0 5px;
  border: 1px solid transparent;
  border-radius: 4px;
  background: transparent;
  color: var(--text-secondary);
}
.rd-arrow:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
.recent-item {
  flex: 1;
  min-width: 0;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
}
.recent-remove {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  padding: 0;
  margin-left: 8px;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  visibility: hidden;
}
.b-dropdown-item:hover .recent-remove,
.recent-remove:focus-visible {
  visibility: visible;
}
.recent-remove:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}
</style>

<style>
/* 下拉弹层（Teleport 到 body，需非 scoped；与 legacy Toolbar 同一样式口径） */
.b-dropdown-popper {
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  padding: 4px 0;
  max-height: 320px;
  overflow-y: auto;
}
.b-dropdown-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  white-space: nowrap;
}
.b-dropdown-item:hover {
  background: var(--bg-hover);
}
.b-dropdown-item.is-disabled {
  color: var(--text-muted);
  cursor: default;
  opacity: 0.6;
}
.b-dropdown-item.is-disabled:hover {
  background: transparent;
}
</style>
