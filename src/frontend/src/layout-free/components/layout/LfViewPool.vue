<template>
  <!-- 视图池（WIN-027）：每个运行时单例 view 只在这里挂载一次；
       槽位存在 → Teleport 把 DOM 搬进槽位；无槽位 → 落「停车场」隐藏保活。 -->
  <div class="lf-view-pool">
    <div v-for="key in viewPool.keys" :key="key" class="lf-pool-slot">
      <Teleport :to="targetOf(key)" :disabled="!targetOf(key)">
        <div :ref="(el) => setAnimEl(key, el)" class="lf-view-anim">
          <component
            :is="compOf(key)"
            v-if="compOf(key)"
            :instance-key="refOf(key).instanceKey"
            :params="refOf(key).params"
            :label="refOf(key).label"
            :design="false"
          />
        </div>
      </Teleport>
    </div>
  </div>
</template>

<script setup>
import { resolveViewComponent } from '../../deps.js'
import { viewPool, targetOf, viewRefOf, splitKey, setAnimEl } from '../../stores/viewPool.js'

function compOf(key) {
  return resolveViewComponent(splitKey(key).viewKey)
}

function refOf(key) {
  return viewRefOf(key) || {}
}
</script>

<style scoped>
/* 停车场：不占布局、不可交互；内容被 Teleport 搬走后此处为空 */
.lf-view-pool {
  position: absolute;
  width: 0;
  height: 0;
  overflow: hidden;
  pointer-events: none;
}

.lf-pool-slot {
  width: 0;
  height: 0;
  overflow: hidden;
}

/* 动画包裹层：填充槽位（Teleport 后它就是槽位的唯一子节点） */
.lf-view-anim {
  width: 100%;
  height: 100%;
  display: flex;
  min-width: 0;
  min-height: 0;
}

.lf-view-anim > * {
  flex: 1;
  min-width: 0;
  min-height: 0;
}
</style>
