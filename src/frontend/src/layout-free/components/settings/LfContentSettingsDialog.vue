<template>
  <div v-if="edit.open" class="editor-mask" v-mq:[EventNames.contextMenuClose].mousedown>
    <div class="editor" v-mq:[EventNames.containerClick].mousedown.stop>
      <header class="ed-head">
        <span class="ico"><LfIcon name="settings" :size="15" /></span>
        <span class="ttl">{{ tr('editor.title') }}</span>
        <nav class="tabs">
          <button
            v-for="t in tabs"
            :key="t.key"
            class="tab"
            :class="{ active: edit.tab === t.key }"
            v-mq:[EventNames.editorTabSelect].click="{ tab: t.key }"
          >
            <LfIcon :name="t.icon" :size="14" />{{ t.label }}
          </button>
        </nav>
        <span class="grow" />
        <span v-if="config.dirty" class="dirty">● {{ tr('editor.dirty') }}</span>
        <button class="lf-btn" v-mq:[EventNames.editorRevert].click>
          <LfIcon name="refresh-cw" :size="13" /> {{ tr('editor.revert') }}
        </button>
        <button class="lf-btn" v-mq:[EventNames.editorReset].click>
          <LfIcon name="trash-2" :size="13" /> {{ tr('editor.reset') }}
        </button>
        <button class="lf-btn" v-mq:[EventNames.editorJsonToggle].click="syncJsonText">
          <LfIcon name="braces" :size="13" /> {{ tr('editor.json') }}
        </button>
        <button class="lf-btn primary" v-mq:[EventNames.editorSave].click>
          <LfIcon name="save" :size="13" /> {{ tr('editor.save') }}
        </button>
        <button class="lf-btn ghost close" :title="tr('editor.close')" v-mq:[EventNames.editorClose].click>
          <LfIcon name="x" :size="14" />
        </button>
      </header>

      <!-- 保存被拦下的原因：固定在头部下方（不随内容滚动），否则用户看不到
           「为什么点了保存却没关掉」，看起来就像按钮坏了。 -->
      <div v-if="config.messages.length" ref="msgRef" class="ed-msg" role="alert" tabindex="-1">
        <div class="msg-title">{{ tr('editor.blockedTitle', { count: config.messages.length }) }}</div>
        <ul>
          <li v-for="(m, i) in config.messages" :key="i">{{ m }}</li>
        </ul>
        <div class="msg-actions">
          <span class="msg-tip">{{ tr('editor.blockedTip') }}</span>
          <button class="lf-btn ghost" v-mq:[EventNames.containerClick].click="() => clearMessages()">{{ tr('editor.dismiss') }}</button>
        </div>
      </div>

      <div class="ed-body">
        <LfLayoutEditor v-if="edit.tab === 'layout'" />
        <LfBarEditor v-else-if="edit.tab === 'topToolbar' || edit.tab === 'toolbar'" bar-key="topToolbar" />
        <LfBarEditor v-else-if="edit.tab === 'statusbar'" bar-key="statusbar" />
        <LfToolbarEditor v-else-if="edit.tab === 'toolbars'" />
        <LfDrawerEditor v-else />
      </div>

      <div v-if="edit.showJson" class="json-drawer">
        <div class="json-head">
          <b>{{ tr('editor.jsonDraftTitle') }}</b>
          <span class="grow" />
          <button class="lf-btn" v-mq:[EventNames.editorImportJson].click="() => importText()">{{ tr('editor.jsonImport') }}</button>
          <button class="lf-btn ghost" v-mq:[EventNames.editorJsonToggle].click="syncJsonText">
            <LfIcon name="x" :size="13" />
          </button>
        </div>
        <div class="json-body">
          <textarea v-model="jsonText" spellcheck="false" />
        </div>
        <div v-if="importError" class="json-err">{{ importError }}</div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, ref } from 'vue'
import LfLayoutEditor from './LfLayoutEditor.vue'
import LfBarEditor from './LfBarEditor.vue'
import LfToolbarEditor from './LfToolbarEditor.vue'
import LfDrawerEditor from './LfDrawerEditor.vue'
import { getMq, tr } from '../../deps.js'
import { useEditStore } from '../../stores/edit'
import { useConfigStore } from '../../stores/config'
import { EventNames } from '../../events/event-names'

const edit = useEditStore()
const config = useConfigStore()

/** 页签标题走 i18n（语言切换即时跟随；`tabs` 为模块级常量 → 改 computed，§3.12） */
const tabs = computed(() => [
  { key: 'layout', label: tr('editor.tabLayout'), icon: 'layout-dashboard' },
  { key: 'topToolbar', label: tr('editor.tabTopToolbar'), icon: 'wrench' },
  { key: 'statusbar', label: tr('editor.tabStatusbar'), icon: 'activity' },
  { key: 'toolbars', label: tr('editor.tabToolbars'), icon: 'sliders-horizontal' },
  { key: 'drawer', label: tr('editor.tabDrawer'), icon: 'panel-right' },
])

const jsonText = ref(config.savedJson)
const importError = ref('')
const msgRef = ref(null)

/**
 * 保存被拦下时把原因条「顶到眼前」：滚动可见 + 聚焦。
 * 原为 `watch(config.messages.length)` —— 改为订阅 `lf-editor-save`：
 * 装配端（`stores/mqCommands`）在保存失败时写入 `messages`，本订阅者按注册序在其后执行
 * → 消息驱动、不侦听 state（§4.2 禁 watch）。
 */
const offSave = getMq().on(EventNames.editorSave, () => {
  if (!config.messages.length) return
  nextTick(() => {
    const el = msgRef.value
    if (!el) return
    el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    el.focus({ preventScroll: true })
  })
})
onBeforeUnmount(() => offSave())

/**
 * 打开 JSON 抽屉前把草稿文本同步为最新（替代原 `watch(edit.showJson)`）：
 * 由按钮的 mq 载荷回调完成，仍是「交互 → 发主题」一条链。
 * 原 `watch(config.savedJson)` 同步已冗余（每次打开都会重新取 `exportJson()`），一并移除。
 */
function syncJsonText() {
  jsonText.value = config.exportJson()
  importError.value = ''
}

function importText() {
  const res = config.importJson(jsonText.value)
  importError.value = res.ok ? '' : res.error ?? '导入失败'
}

function clearMessages() {
  config.messages = []
}
</script>

<style scoped>
.editor-mask {
  position: fixed;
  inset: 0;
  background: rgba(18, 26, 40, 0.42);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1500;
  backdrop-filter: blur(1.5px);
}

.editor {
  width: min(1500px, 95vw);
  height: min(920px, 92vh);
  background: var(--lf-panel);
  border-radius: 12px;
  box-shadow: 0 28px 70px rgba(10, 18, 32, 0.4);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  position: relative;
}

.ed-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  height: 46px;
  padding: 0 10px;
  border-bottom: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.ed-head .ico {
  display: inline-flex;
  color: var(--lf-accent);
}

.ed-head .ttl {
  font-weight: 700;
  font-size: 14px;
}

.ed-head .grow {
  flex: 1;
}

.tabs {
  display: flex;
  gap: 2px;
  margin-left: 10px;
  background: #eef1f6;
  padding: 3px;
  border-radius: 8px;
}

.tab {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 26px;
  padding: 0 12px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: var(--lf-text-weak);
  cursor: pointer;
}

.tab.active {
  background: #fff;
  color: var(--lf-accent);
  font-weight: 600;
  box-shadow: 0 1px 3px rgba(20, 30, 50, 0.12);
}

.ed-head .lf-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.dirty {
  color: #d98200;
  font-size: 12px;
}

.close {
  font-size: 15px;
}

.ed-msg {
  flex: none;
  background: #fff8e6;
  border-bottom: 1px solid #f0dca8;
  padding: 8px 12px;
  max-height: 160px;
  overflow: auto;
  position: relative;
  outline: none;
}

.msg-title {
  font-weight: 600;
  color: #9a6a00;
  margin-bottom: 4px;
}

.ed-msg ul {
  margin: 0;
  padding-left: 18px;
  color: #7a5800;
}

/* 说明 + 关闭按钮排一行（按钮不再绝对定位，避免和小字重叠） */
.msg-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 6px;
}

.msg-tip {
  flex: 1;
  font-size: 11px;
  color: #9a6a00;
}

.ed-body {
  flex: 1;
  min-height: 0;
  display: flex;
}

.ed-body > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.json-drawer {
  position: absolute;
  right: 0;
  bottom: 0;
  width: min(680px, 60%);
  height: 62%;
  background: #fff;
  border-left: 1px solid var(--lf-border-strong);
  border-top: 1px solid var(--lf-border-strong);
  border-radius: 12px 0 0 0;
  box-shadow: -12px -12px 40px rgba(16, 26, 44, 0.2);
  display: flex;
  flex-direction: column;
  z-index: 5;
}

.json-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px;
  border-bottom: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.json-head .grow {
  flex: 1;
}

.json-body {
  flex: 1;
  min-height: 0;
  padding: 8px;
}

.json-body textarea {
  width: 100%;
  height: 100%;
  resize: none;
  border: 1px solid var(--lf-border-strong);
  border-radius: 8px;
  padding: 8px;
  font-family: Consolas, 'Cascadia Mono', monospace;
  font-size: 12px;
  line-height: 1.5;
  background: #fcfdff;
}

.json-err {
  flex: none;
  color: #c62828;
  padding: 0 10px 8px;
  font-size: 12px;
}
</style>
