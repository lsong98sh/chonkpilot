<template>
  <div
    class="b-tooltip"
    :style="{ anchorName: anchorId }"
    @mouseenter="onMouseEnter"
    @mouseleave="onMouseLeave"
  >
    <slot />
    <Transition name="b-tooltip--fade">
      <div
        v-if="visible"
        class="b-tooltip__popper"
        :class="[`b-tooltip--${placement}`]"
        :style="{ positionAnchor: anchorId }"
      >
        {{ content }}<slot name="content" />
      </div>
    </Transition>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  content: { type: String, default: '' },
  placement: { type: String, default: 'top' },
  showAfter: { type: Number, default: 0 } // ms delay before showing
})

const visible = ref(false)
let timer = null

let uid
if (typeof window !== 'undefined') {
  if (window.__bTooltipUid === undefined) window.__bTooltipUid = 1
  uid = window.__bTooltipUid++
} else {
  uid = 0
}
const anchorId = `--b-tooltip-${uid}`

function onMouseEnter() {
  if (props.showAfter > 0) {
    timer = setTimeout(() => {
      visible.value = true
    }, props.showAfter)
  } else {
    visible.value = true
  }
}

function onMouseLeave() {
  if (timer) {
    clearTimeout(timer)
    timer = null
  }
  visible.value = false
}
</script>

<style scoped>
.b-tooltip {
  display: inline-flex;
}

.b-tooltip__popper {
  position: fixed;
  z-index: 9999;
  padding: 5px 9px;
  font-size: var(--font-size-sm);
  line-height: 1.4;
  color: var(--tooltip-color, #fff);
  background: var(--tooltip-bg, rgba(0, 0, 0, 0.8));
  border-radius: 4px;
  white-space: nowrap;
  pointer-events: none;
  margin: 0;
  max-width: 320px;
  word-break: break-all;
}

/* top */
.b-tooltip--top {
  bottom: anchor(top);
  left: anchor(center);
  transform: translateX(-50%);
  margin-bottom: 8px;
}
.b-tooltip--top::after {
  content: '';
  position: absolute;
  top: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 5px solid transparent;
  border-top-color: var(--tooltip-bg, rgba(0, 0, 0, 0.8));
}

/* bottom */
.b-tooltip--bottom {
  top: anchor(bottom);
  left: anchor(center);
  transform: translateX(-50%);
  margin-top: 8px;
}
.b-tooltip--bottom::after {
  content: '';
  position: absolute;
  bottom: 100%;
  left: 50%;
  transform: translateX(-50%);
  border: 5px solid transparent;
  border-bottom-color: var(--tooltip-bg, rgba(0, 0, 0, 0.8));
}

/* left */
.b-tooltip--left {
  right: anchor(left);
  top: anchor(center);
  transform: translateY(-50%);
  margin-right: 8px;
}
.b-tooltip--left::after {
  content: '';
  position: absolute;
  left: 100%;
  top: 50%;
  transform: translateY(-50%);
  border: 5px solid transparent;
  border-left-color: var(--tooltip-bg, rgba(0, 0, 0, 0.8));
}

/* right */
.b-tooltip--right {
  left: anchor(right);
  top: anchor(center);
  transform: translateY(-50%);
  margin-left: 8px;
}
.b-tooltip--right::after {
  content: '';
  position: absolute;
  right: 100%;
  top: 50%;
  transform: translateY(-50%);
  border: 5px solid transparent;
  border-right-color: var(--tooltip-bg, rgba(0, 0, 0, 0.8));
}

/* transition */
.b-tooltip--fade-enter-active,
.b-tooltip--fade-leave-active {
  transition: opacity 0.2s;
}
.b-tooltip--fade-enter-from,
.b-tooltip--fade-leave-to {
  opacity: 0;
}
</style>