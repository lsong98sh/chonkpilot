<template>
  <LfTabbedContainer
    :tabs="tabModels"
    :active-key="activeKey"
    :active-index="activeIndex"
    :position="tabPosition"
    :empty-text="tr('container.fixedTabsEmpty')"
    ctx-scope="fixed"
    :ctx-owner-id="nodeId"
  >
    <!-- 保活（§3.11）：**全部标签页都渲染**，仅用 v-show 控制可见 —— 切标签不卸载，
         区块身份（会话 / 流式 / 页签集合）不因切换而丢失。
         key 用 `viewKey|instanceKey` 稳定标识（不含下标）：增删标签只影响被删的那个。 -->
    <div v-for="(t, i) in tabs" :key="tabKey(t)" v-show="i === activeIndex" class="pane">
      <LfViewHost :view-ref="viewRefOf(t)" :design="design" />
    </div>
    <div v-if="!tabs.length" class="pane pane-empty">{{ tr('container.fixedTabsEmptyHint') }}</div>
  </LfTabbedContainer>
</template>

<script setup>
import { computed } from 'vue'
import LfTabbedContainer from '../components/tabs/LfTabbedContainer.vue'
import LfViewHost from './LfViewHost.vue'
import { viewIcon, viewLabel } from '../config/views'
import { EventNames } from '../events/event-names'
import { useRuntimeStore } from '../stores/runtime'
import { tr } from '../deps.js'

const props = defineProps({
  /** 区域节点 id */
  nodeId: { type: String, default: '' },
  /** 该区域的标签页配置 */
  tabs: { type: Array, default: () => [] },
  /** 标签栏位置 top | bottom | left | right */
  tabPosition: { type: String, default: 'top' },
  /** 设计态（只读渲染内部 view） */
  design: { type: Boolean, default: false },
})

const runtime = useRuntimeStore()

/**
 * 标签页的**稳定标识**：`viewKey|instanceKey`，不含数组下标。
 *
 * ⚠️ 刻意不用下标：以前 key 是 `${viewKey}-${instanceKey}-${index}`，
 * 一旦在中间插入 / 删除标签页，后面所有标签的 key 全部平移 ——
 * 而运行时激活项与右键菜单 payload 都按 key 传递，
 * 结果就是「在 A 前面插入新标签 → 当前激活的内容莫名换成新标签」。
 * 用稳定 key 后，增删只影响「被删掉的那个」，其余标签身份不变。
 */
function tabKey(t) {
  return `${t.viewKey}|${t.instanceKey ?? ''}`
}

/** 视图引用（含 `params`：区块需要的参数经它传给适配壳，§3.4） */
function viewRefOf(t) {
  return { viewKey: t.viewKey, instanceKey: t.instanceKey ?? null, label: t.label, params: t.params }
}

/** 运行时记录的激活 key（老配置里可能是数字下标 → 兜底解析成稳定 key） */
const activeKeyStored = computed(() => {
  const raw = runtime.activeFixedTab[props.nodeId]
  if (typeof raw === 'string' && raw.includes('|')) return raw
  const i = Number(raw)
  if (Number.isInteger(i) && i >= 0) return props.tabs[i] ? tabKey(props.tabs[i]) : ''
  return ''
})

/** 激活项下标：按稳定 key 反查，找不到就退回第一个 */
const activeIndex = computed(() => {
  const i = props.tabs.findIndex((t) => tabKey(t) === activeKeyStored.value)
  return i >= 0 ? i : 0
})

/**
 * 被激活的标签**被删掉**时的回落：原为 `watch(tabs)` 回写 `runtime.setFixedTab`，
 * 现改为**纯派生**（`activeIndex` 找不到稳定 key 时退回第 0 项）——
 * 可见行为一致（展示第一个标签为激活），但不再产生「派生里写 state」的副作用
 * （§4.2 禁 watch；§3.7 组件内 computed 仅用于纯展示派生）。
 */

const tabModels = computed(() =>
  props.tabs.map((t, i) => ({
    key: tabKey(t),
    label: t.label || viewLabel(t.viewKey),
    icon: viewIcon(t.viewKey),
    instanceKey: t.instanceKey,
    pinned: t.pinned === true,
    title: `${t.label || viewLabel(t.viewKey)}${t.instanceKey ? ` · ${t.instanceKey}` : ''}`,
    closable: t.pinned !== true,
    topic: EventNames.fixedTabsActivate,
    payload: { nodeId: props.nodeId, key: tabKey(t), index: i },
  })),
)

const activeKey = computed(() => {
  const t = props.tabs[activeIndex.value]
  return t ? tabKey(t) : ''
})
</script>

<style scoped>
/* 每个标签页一个 pane：全部挂载，仅激活项可见（v-show）→ 切换不卸载（§3.11） */
.pane {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
}

.pane > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}

.pane-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  padding: 20px;
  text-align: center;
  color: var(--lf-text-weak);
  font-size: 12px;
}
</style>
