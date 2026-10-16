<!--
  栏视图条目 `search`（UILAYOUT-1 §3.4 / §7 P4①）：从 `views/toolbar/Toolbar.vue` 中段搜索箱
  **抽出**成独立 view，注册为 `host:'bar'` 的栏内嵌条目。行为与抽出前一致：
    `gui.search {query}` → 结果下拉 → 选中 = `file-open {temporary:true}` + `file-search {path}`；
    250ms 防抖 + 序号作废在途请求。
  保留 `.search-input-wrap` / `.search-result-item` 等既有类名（L4 选择器口径）。
-->
<template>
  <div class="search-wrapper">
    <div class="search-input-wrap">
      <Icon name="search" class="search-prefix" />
      <Input
        v-model="searchQuery"
        :placeholder="$t('toolbar.search_placeholder')"
        @keyup.enter="handleSearchEnter"
        @input="onSearchInput"
        @focus="showSearchResults = true"
        @blur="hideSearchResults"
      />
      <Teleport to="body">
        <div v-if="showSearchResults && searchResults.length > 0" class="search-popper" :style="searchPopperStyle">
          <div
            v-for="item in searchResults"
            :key="item.path"
            class="search-result-item"
            @mousedown.prevent="handleSearchSelect(item)"
          >
            <Tag :type="matchTagType(item.matchType)" size="mini">
              {{ matchTagLabel(item.matchType) }}
            </Tag>
            <span class="search-filename">{{ basename(item.path) }}</span>
            <span class="search-path">{{ item.path }}</span>
            <span v-if="item.snippet" class="search-snippet">{{ snippetPreview(item.snippet) }}</span>
          </div>
        </div>
      </Teleport>
    </div>
  </div>
</template>

<script setup>
import { computed, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Icon } from '../../components/icon'
import { Input, Tag } from '../../components/ui'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { MsgTopics, FieldKeys, GuiSearchKeys } from '../../events/msgkeys'

const { t } = useI18n()

const searchQuery = ref('')
const searchResults = ref([])
const showSearchResults = ref(false)

let searchDebounceTimer = null
let hideSearchTimer = null
let searchSeq = 0

const searchPopperStyle = computed(() => {
  const el = document.querySelector('.search-input-wrap')
  if (!el) return {}
  const rect = el.getBoundingClientRect()
  return {
    position: 'fixed',
    top: rect.bottom + 2 + 'px',
    left: rect.left + 'px',
    width: rect.width + 'px',
    zIndex: 9999,
  }
})

function basename(path) {
  const parts = String(path || '').replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || path
}

function matchTagType(matchType) {
  switch (matchType) {
    case 'filename': return 'info'
    case 'symbol': return 'warning'
    case 'content': return 'danger'
    default: return 'success'
  }
}

function matchTagLabel(matchType) {
  switch (matchType) {
    case 'filename': return t('toolbar.match_filename')
    case 'symbol': return t('toolbar.match_symbol')
    case 'content': return t('toolbar.match_content')
    default: return t('toolbar.match_path')
  }
}

function snippetPreview(snippet) {
  const s = String(snippet || '').trim()
  if (!s) return ''
  return s.length > 60 ? s.slice(0, 60) + '…' : s
}

/** 项目内检索（gui.search；索引未接入 → 占位空），payload 与既有面一致 */
async function searchFiles(q) {
  try {
    const env = await mq.emit(MsgTopics.guiSearch, { [FieldKeys.query]: q })
    const r = env && env.backend && env.backend.result
    return (r && Array.isArray(r[GuiSearchKeys.results])) ? r[GuiSearchKeys.results] : []
  } catch (_) {
    return []
  }
}

function onSearchInput() {
  const q = searchQuery.value.trim()
  if (!q) {
    searchSeq++
    if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
    searchResults.value = []
    return
  }
  if (searchDebounceTimer) clearTimeout(searchDebounceTimer)
  const seq = ++searchSeq
  searchDebounceTimer = setTimeout(async () => {
    try {
      const results = await searchFiles(q)
      if (seq !== searchSeq) return
      searchResults.value = results.slice(0, 15)
      showSearchResults.value = true
    } catch (_) {
      if (seq === searchSeq) searchResults.value = []
    }
  }, 250)
}

function handleSearchSelect(item) {
  if (item && item.path) {
    mq.emit(EventNames.fileOpen, { path: item.path, temporary: true })
    mq.emit(EventNames.fileSearch, item.path)
  }
  showSearchResults.value = false
}

function handleSearchEnter() {
  if (!searchQuery.value.trim()) return
  searchFiles(searchQuery.value.trim()).then((results) => {
    if (results && results.length > 0 && results[0].path) {
      const p = results[0].path
      mq.emit(EventNames.fileOpen, { path: p, temporary: true })
      mq.emit(EventNames.fileSearch, p)
    }
  }).catch(() => {})
  showSearchResults.value = false
}

function hideSearchResults() {
  if (hideSearchTimer) clearTimeout(hideSearchTimer)
  hideSearchTimer = setTimeout(() => {
    showSearchResults.value = false
  }, 200)
}

onUnmounted(() => {
  if (searchDebounceTimer) { clearTimeout(searchDebounceTimer); searchDebounceTimer = null }
  if (hideSearchTimer) { clearTimeout(hideSearchTimer); hideSearchTimer = null }
})
</script>

<style scoped>
.search-wrapper {
  width: 100%;
  min-width: 160px;
  max-width: 380px;
  -webkit-app-region: no-drag;
}
.search-input-wrap {
  position: relative;
  display: flex;
  align-items: center;
  background: var(--input-bg);
  border-radius: 6px;
  border: 1px solid var(--border);
  padding: 0 8px;
  gap: 4px;
}
.search-input-wrap :deep(.b-input) {
  flex: 1;
  border: none;
  background: transparent;
}
.search-input-wrap :deep(.b-input) input {
  font-size: 13px;
  color: var(--text-primary);
  outline: none;
  font-family: inherit;
  line-height: 1.5;
}
.search-input-wrap :deep(.b-input) input::placeholder {
  color: var(--text-muted);
}
.search-prefix {
  flex-shrink: 0;
  color: var(--text-muted);
}
.search-result-item {
  display: flex;
  flex-direction: column;
  padding: 6px 8px;
  gap: 2px;
  cursor: pointer;
}
.search-result-item:hover {
  background: var(--bg-hover);
}
.search-filename {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}
.search-path {
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.search-snippet {
  font-size: 11px;
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>

<style>
/* 搜索结果弹层（Teleport 到 body，需非 scoped） */
.search-popper {
  background: var(--bg-primary, #fff);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.12);
  padding: 4px 0;
  max-height: 360px;
  overflow-y: auto;
}
</style>
