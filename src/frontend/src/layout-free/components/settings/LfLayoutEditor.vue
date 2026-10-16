<template>
  <div class="layout-editor">
    <!-- 左：布局区的树 -->
    <aside class="le-left">
      <div class="pane-head">
        <span class="ico"><LfIcon name="list-tree" :size="13" /></span>
        <b>布局区</b>
        <span class="grow" />
        <button class="lf-btn ghost tiny" title="全部展开" v-mq:[EventNames.containerClick].click="() => edit.expandAll()">
          ⤢
        </button>
      </div>
      <div class="pane-body" v-mq:[EventNames.layoutSelectNode].click="{ nodeId: '' }" @contextmenu="onRootContext">
        <LfLayoutTree :node="config.draft.layout" :depth="0" />
      </div>
      <div class="pane-foot">
        <button
          class="lf-btn"
          v-mq:[EventNames.layoutSplit].click="{ nodeId: targetNodeId, direction: 'row' }"
        >
          ▥ 左右分割
        </button>
        <button
          class="lf-btn"
          v-mq:[EventNames.layoutSplit].click="{ nodeId: targetNodeId, direction: 'column' }"
        >
          ▤ 上下分割
        </button>
      </div>
    </aside>

    <div class="le-right">
      <!-- 右上：布局效果（设计态：只读 view + 显示区域标题 + 可点选） -->
      <section class="le-preview">
        <div class="pane-head">
          <span class="ico"><LfIcon name="eye" :size="13" /></span>
          <b>布局效果</b>
          <span class="hint">点击选中区域 · 右键分割 · view 为只读设计态</span>
          <span class="grow" />
          <span class="lf-tag">选中：{{ edit.selectedNodeId || '无' }}</span>
          <span class="lf-tag">{{ leafCount }} 个区域</span>
        </div>
        <div class="preview-body" @contextmenu="onRootContext">
          <LfFreeLayoutContainer
            :layout="config.draft.layout"
            preview
            :selected-id="edit.selectedNodeId"
          />
        </div>
      </section>

      <!-- 右下：区域设置区 -->
      <section class="le-settings">
        <LfAreaSettings />
      </section>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import LfLayoutTree from './LfLayoutTree.vue'
import LfAreaSettings from './LfAreaSettings.vue'
import LfFreeLayoutContainer from '../../components/layout/LfFreeLayoutContainer.vue'
import { useConfigStore } from '../../stores/config'
import { useEditStore } from '../../stores/edit'
import { collectLeaves } from '../../utils/layout'
import { EventNames } from '../../events/event-names'

const config = useConfigStore()
const edit = useEditStore()

const leafCount = computed(() => collectLeaves(config.draft.layout).length)
const targetNodeId = computed(() => edit.selectedNodeId || config.draft.layout.id)

function onRootContext(event) {
  // 落在区域/容器上的右键由子组件处理并 stopPropagation；这里只处理空白处 → 针对根节点
  event.preventDefault()
  edit.openContextMenu(config.draft.layout.id, event.clientX, event.clientY)
}
</script>

<style scoped>
.layout-editor {
  display: flex;
  height: 100%;
  min-height: 0;
  background: var(--lf-bg);
}

.le-left {
  width: 280px;
  flex: none;
  display: flex;
  flex-direction: column;
  min-height: 0;
  background: #fff;
  border-right: 1px solid var(--lf-border);
}

.le-right {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
  gap: 6px;
  padding: 6px;
}

.le-preview {
  flex: 1.15;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: #fff;
  border: 1px solid var(--lf-border);
  border-radius: 8px;
  overflow: hidden;
}

.le-settings {
  flex: 1;
  min-height: 0;
  background: #fff;
  border: 1px solid var(--lf-border);
  border-radius: 8px;
  overflow: hidden;
}

.pane-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 30px;
  padding: 0 8px;
  border-bottom: 1px solid var(--lf-border);
  background: #fbfcfe;
  font-size: 12px;
}

.pane-head .grow {
  flex: 1;
}

.pane-head .hint {
  font-size: 11px;
  color: var(--lf-text-weak);
  margin-left: 6px;
}

.pane-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 4px;
}

.pane-foot {
  flex: none;
  display: flex;
  gap: 6px;
  padding: 6px;
  border-top: 1px solid var(--lf-border);
  background: #fbfcfe;
}

.preview-body {
  flex: 1;
  min-height: 0;
  padding: 8px;
  background: #eef1f6;
  display: flex;
}

.preview-body > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.tiny {
  height: 20px;
  padding: 0 6px;
}
</style>
