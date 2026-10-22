<template>
  <!-- 设计态（编辑器「布局效果」预览）：**一律骨架**，不挂真实组件 ——
       单例 view 在运行期由视图池各挂载一次，预览再挂一份就是「单例双开」（WIN-027）。 -->
  <div v-if="design" class="v-root v-design">
    <div class="v-head">
      <LfIcon class="v-icon" :name="iconName" :size="13" />
      <span class="v-title">{{ title }}</span>
    </div>
    <div class="v-body lf-empty">
      <LfIcon name="square-dashed" :size="22" />
      <span>{{ tr('container.designSkeleton') }}</span>
    </div>
  </div>

  <!-- 运行态 · 非池化（弹窗等）：直接挂载（不参与布局切换保活） -->
  <component
    :is="comp"
    v-else-if="comp && !pooled"
    :instance-key="viewRef.instanceKey"
    :params="viewRef.params"
    :label="viewRef.label"
    :design="design"
  />

  <!-- 运行态 · 池化：只渲染槽位，真实组件由 LfViewPool 经 Teleport 搬进来 -->
  <div v-else-if="comp" ref="slotRef" class="lf-view-slot" :data-view-key="viewRef.viewKey" />

  <!-- 未知 view：明确占位（不静默空白） -->
  <div v-else class="v-root view-missing">
    <div class="v-head">
      <LfIcon class="v-icon" name="circle-alert" :size="13" />
      <span class="v-title">{{ viewRef.label || viewRef.viewKey }}</span>
    </div>
    <div class="v-body lf-empty">{{ tr('container.viewMissing', { key: viewRef.viewKey }) }}</div>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, onUpdated, ref } from 'vue'
import { resolveViewComponent, tr } from '../deps.js'
import { getView, viewIcon } from '../config/views'
import { registerViewTarget, unregisterViewTarget, viewPoolKey } from '../stores/viewPool.js'

const props = defineProps({
  /** 视图引用 { viewKey, instanceKey, label?, tabs?, params? } */
  viewRef: { type: Object, required: true },
  /** 设计态：只渲染骨架（不挂真实组件） */
  design: { type: Boolean, default: false },
  /** 是否参与视图池（布局 / 栏 / 抽屉 = true；弹窗 = false） */
  pooled: { type: Boolean, default: true },
})

const comp = computed(() => resolveViewComponent(props.viewRef.viewKey))
const def = computed(() => getView(props.viewRef.viewKey))
const title = computed(() => def.value?.label || props.viewRef.label || props.viewRef.viewKey)
const iconName = computed(() => viewIcon(props.viewRef.viewKey))

/* ── 视图池：注册 / 迁移 / 注销槽位 ───────────────────── */
const slotRef = ref(null)
const poolKey = computed(() => viewPoolKey(props.viewRef.viewKey, props.viewRef.instanceKey))
let mountedKey = ''
let mountedEl = null

function sync() {
  if (!props.pooled) return
  // 槽位消失（view 被移除 / 切到设计态）→ 注销，池内实例落停车场保活
  if (!comp.value || !slotRef.value) {
    if (mountedEl) {
      unregisterViewTarget(mountedKey, mountedEl)
      mountedEl = null
    }
    return
  }
  const key = poolKey.value
  if (mountedEl && mountedKey !== key) {
    unregisterViewTarget(mountedKey, mountedEl)
  }
  mountedKey = key
  mountedEl = slotRef.value
  registerViewTarget(key, mountedEl, props.viewRef)
}

onMounted(sync)
// viewRef（instanceKey / params / label）可能原地更新 → 每次 patch 后同步注册信息
onUpdated(sync)
onBeforeUnmount(() => {
  if (mountedEl) unregisterViewTarget(mountedKey, mountedEl)
  mountedEl = null
})
</script>

<style scoped>
.v-root {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.v-head {
  flex: none;
  display: flex;
  align-items: center;
  gap: 5px;
  height: 22px;
  padding: 0 8px;
  font-size: 11px;
  color: var(--lf-text-weak);
  border-bottom: 1px solid var(--lf-border);
  background: #f7f9fc;
}

.v-title {
  font-weight: 600;
  color: var(--lf-text);
}

.v-body {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  font-size: 11px;
  color: var(--lf-text-weak);
}

.view-missing {
  border: 1px dashed var(--lf-border-strong);
}

/* 池化槽位：撑满区域（真实组件由 Teleport 搬进来） */
.lf-view-slot {
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  display: flex;
}

.lf-view-slot > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}
</style>
