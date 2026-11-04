<!--
  栏视图条目 `ck-store`（PLUGIN-LOAD-1 D17）：状态栏右侧内置图标 → 抽屉查看 / 刷新本窗口
  已注册的 CK Store 条目（key + 当前值）。

  口径（设计稿 §3.5）：打开时求值一次；顶部「刷新」按钮手动重算；不做搜索 / 附加列 / 展开；
  getter 抛错的行显示错误文本，不影响其它行。
-->
<template>
  <button class="cs-btn" type="button" title="CK Store（本窗口注册的状态 getter）" @mousedown.stop @click="toggle">
    <Icon name="collection" :size="14" />
  </button>

  <Teleport to="body">
    <div v-if="open" class="cs-overlay" @mousedown.stop @click="close" />
    <aside v-if="open" class="cs-drawer" @mousedown.stop>
      <header class="cs-head">
        <span class="cs-title">CK Store</span>
        <span class="cs-count">{{ rows.length }}</span>
        <span class="cs-grow" />
        <button class="cs-act" type="button" :disabled="loading" @click="evaluate">
          {{ loading ? '刷新中…' : '刷新' }}
        </button>
        <button class="cs-act" type="button" title="关闭" @click="close">✕</button>
      </header>
      <div class="cs-body">
        <div v-if="!rows.length" class="cs-empty">当前窗口没有注册项</div>
        <div v-for="r in rows" :key="r.key" class="cs-row">
          <div class="cs-key" :title="r.key">{{ r.key }}</div>
          <div class="cs-val" :class="{ err: !!r.error }" :title="r.error || r.text">
            {{ r.error || r.text }}
          </div>
        </div>
      </div>
    </aside>
  </Teleport>
</template>

<script setup>
import { ref } from 'vue'
import { Icon } from '@ckfw/components/icon/index.js'
import { ckStore } from '../../utils/ckStore'

const open = ref(false)
const loading = ref(false)
const rows = ref([])

/** 值的展示文本（对象 / 数组走 JSON；空值显示「空」）。 */
function format(v) {
  if (v === null || v === undefined) return '空'
  if (typeof v === 'string') return v || '空'
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  try {
    return JSON.stringify(v)
  } catch (_) {
    return String(v)
  }
}

/** 求值一次：列表 = 当前窗口已注册 key + 当前值（逐行独立，单行失败不影响其它行）。 */
async function evaluate() {
  loading.value = true
  const keys = ckStore.listKeys()
  rows.value = keys.map((key) => ({ key, text: '', error: '' }))
  await Promise.all(
    keys.map(async (key) => {
      let text = ''
      let error = ''
      try {
        text = format(await ckStore.get(key))
      } catch (e) {
        error = (e && e.message) || String(e)
      }
      const row = rows.value.find((r) => r.key === key)
      if (!row) return
      row.text = text
      row.error = error
    }),
  )
  loading.value = false
}

function toggle() {
  open.value = !open.value
  if (open.value) evaluate()
}

function close() {
  open.value = false
}
</script>

<style scoped>
.cs-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 20px;
  padding: 0;
  border: none;
  border-radius: 3px;
  background: transparent;
  color: var(--text-secondary, #666);
  cursor: pointer;
  -webkit-app-region: no-drag;
}
.cs-btn:hover {
  background: var(--bg-hover, #e8e8e8);
  color: var(--text-primary, #222);
}
.cs-overlay {
  position: fixed;
  inset: 0;
  z-index: 2100;
  background: rgba(18, 26, 40, 0.18);
}
.cs-drawer {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  z-index: 2101;
  width: 360px;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--border, #d8dee9);
  background: var(--bg-primary, #fff);
  color: var(--text-primary, #222);
  box-shadow: -8px 0 28px rgba(16, 26, 44, 0.16);
  font-size: 11px;
}
.cs-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 8px 0 12px;
  border-bottom: 1px solid var(--border, #d8dee9);
  background: var(--bg-secondary, #f7f9fc);
}
.cs-title {
  font-weight: 600;
  font-size: 12px;
}
.cs-count {
  color: var(--text-muted, #888);
}
.cs-grow {
  flex: 1;
}
.cs-act {
  height: 22px;
  padding: 0 8px;
  border: 1px solid var(--border, #d8dee9);
  border-radius: 4px;
  background: transparent;
  color: inherit;
  font-size: 11px;
  cursor: pointer;
}
.cs-act:hover:not(:disabled) {
  background: var(--bg-hover, #e8e8e8);
}
.cs-act:disabled {
  opacity: 0.5;
  cursor: default;
}
.cs-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 4px 0;
}
.cs-empty {
  padding: 12px;
  color: var(--text-muted, #888);
}
.cs-row {
  display: flex;
  gap: 8px;
  padding: 5px 12px;
  border-bottom: 1px solid var(--border-weak, rgba(0, 0, 0, 0.06));
}
.cs-key {
  flex: none;
  width: 132px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: Consolas, 'Courier New', monospace;
  color: var(--text-secondary, #555);
}
.cs-val {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: Consolas, 'Courier New', monospace;
  color: var(--text-primary, #222);
}
.cs-val.err {
  color: #d93025;
}
</style>
