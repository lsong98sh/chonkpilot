<template>
  <div class="b-popover" ref="popoverRef">
    <div class="b-popover__reference" ref="referenceRef" @click="toggle">
      <slot name="reference" />
    </div>
    <Teleport to="body">
      <Transition name="b-fade">
        <div
          v-if="visible"
          ref="popperRef"
          class="b-popover__popper"
          :class="`b-popover--${placement}`"
          :style="popperStyle"
          @click="closeOnContentClick && hide()"
        >
          <div class="b-popover__arrow" />
          <div class="b-popover__content">
            <slot />
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'

const props = defineProps({
  placement: { type: String, default: 'bottom-end' },
  width: { type: Number, default: 160 },
  closeOnContentClick: { type: Boolean, default: true },
})

const visible = ref(false)
const popoverRef = ref(null)
const referenceRef = ref(null)
const popperRef = ref(null)
const popperPos = ref({ top: 0, left: 0 })

function updatePosition() {
  if (!visible.value || !referenceRef.value) return
  nextTick(() => {
    if (!popperRef.value) return
    const refRect = referenceRef.value.getBoundingClientRect()
    const popperW = popperRef.value.offsetWidth || props.width
    const popperH = popperRef.value.offsetHeight || 0
    const gap = 6
    let top = 0, left = 0

    switch (props.placement) {
      case 'bottom-end':
        top = refRect.bottom + gap
        left = refRect.right - popperW
        break
      case 'bottom':
        top = refRect.bottom + gap
        left = refRect.left + (refRect.width - popperW) / 2
        break
      case 'bottom-start':
        top = refRect.bottom + gap
        left = refRect.left
        break
      case 'top-end':
        top = refRect.top - popperH - gap
        left = refRect.right - popperW
        break
      case 'top':
        top = refRect.top - popperH - gap
        left = refRect.left + (refRect.width - popperW) / 2
        break
      case 'top-start':
        top = refRect.top - popperH - gap
        left = refRect.left
        break
    }

    // Clamp to viewport
    top = Math.max(4, Math.min(top, window.innerHeight - popperH - 4))
    left = Math.max(4, Math.min(left, window.innerWidth - popperW - 4))

    popperPos.value = { top, left }
  })
}

const popperStyle = computed(() => ({
  position: 'fixed',
  top: popperPos.value.top + 'px',
  left: popperPos.value.left + 'px',
  width: props.width + 'px',
}))

function toggle() {
  visible.value = !visible.value
  if (visible.value) {
    nextTick(updatePosition)
  }
}

function hide() {
  visible.value = false
}

defineExpose({ hide, toggle })

function handleClickOutside(e) {
  if (popoverRef.value && !popoverRef.value.contains(e.target) &&
      popperRef.value && !popperRef.value.contains(e.target)) {
    visible.value = false
  }
}

function handleScroll() {
  if (visible.value) updatePosition()
}

function handleResize() {
  if (visible.value) updatePosition()
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside, true)
  document.addEventListener('scroll', handleScroll, true)
  window.addEventListener('resize', handleResize)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside, true)
  document.removeEventListener('scroll', handleScroll, true)
  window.removeEventListener('resize', handleResize)
})
</script>

<style scoped>
.b-popover {
  position: relative;
  display: inline-flex;
}

.b-popover__reference {
  cursor: pointer;
}

.b-popover__popper {
  z-index: 2000;
  background: var(--bg-secondary, #fff);
  border: 1px solid var(--border, #dee2e6);
  border-radius: var(--border-radius);
  box-shadow: 0 6px 16px rgba(0, 0, 0, 0.1);
  padding: 8px 0;
  font-size: var(--font-size-sm);
  color: var(--text-primary, #212529);
  box-sizing: border-box;
}

.b-popover__arrow {
  position: absolute;
  width: 0;
  height: 0;
}

.b-popover--bottom-end .b-popover__arrow,
.b-popover--bottom .b-popover__arrow,
.b-popover--bottom-start .b-popover__arrow {
  top: -6px;
  right: 12px;
  border-left: 6px solid transparent;
  border-right: 6px solid transparent;
  border-bottom: 6px solid var(--border, #dee2e6);
}

.b-popover--bottom-end .b-popover__arrow::after,
.b-popover--bottom .b-popover__arrow::after,
.b-popover--bottom-start .b-popover__arrow::after {
  content: '';
  position: absolute;
  top: 2px;
  left: -5px;
  border-left: 5px solid transparent;
  border-right: 5px solid transparent;
  border-bottom: 5px solid var(--bg-secondary, #fff);
}

.b-popover--top .b-popover__arrow,
.b-popover--top-end .b-popover__arrow,
.b-popover--top-start .b-popover__arrow {
  bottom: -6px;
  right: 12px;
  border-left: 6px solid transparent;
  border-right: 6px solid transparent;
  border-top: 6px solid var(--border, #dee2e6);
}

.b-popover--top .b-popover__arrow::after,
.b-popover--top-end .b-popover__arrow::after,
.b-popover--top-start .b-popover__arrow::after {
  content: '';
  position: absolute;
  bottom: 2px;
  left: -5px;
  border-left: 5px solid transparent;
  border-right: 5px solid transparent;
  border-top: 5px solid var(--bg-secondary, #fff);
}

.b-popover--bottom .b-popover__arrow {
  right: auto;
  left: 50%;
  transform: translateX(-50%);
}

.b-popover--top .b-popover__arrow {
  right: auto;
  left: 50%;
  transform: translateX(-50%);
}

.b-popover--top-start .b-popover__arrow,
.b-popover--bottom-start .b-popover__arrow {
  right: auto;
  left: 12px;
}

.b-popover__content {
  padding: 0 12px;
}

/* Transition */
.b-fade-enter-active,
.b-fade-leave-active {
  transition: opacity 0.15s, transform 0.15s;
}
.b-fade-enter-from,
.b-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>