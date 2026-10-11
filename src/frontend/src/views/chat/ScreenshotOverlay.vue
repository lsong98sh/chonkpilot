<template>
  <div
    class="screenshot-overlay"
    @mousedown="onMouseDown"
    @mousemove="onMouseMove"
    @mouseup="onMouseUp"
    @dblclick="emit('cancel')"
  >
    <!-- 全屏截图预览（隐藏窗口后截取）：用户拖拽选区 → canvas 裁剪。
         img 与选框同处一个相对定位容器：选框以容器（= img 尺寸）为参照绝对定位，
         与 localPoint 的「相对 img」坐标一致；否则 overlay 居中偏移会使选框整体错位。 -->
    <div class="screenshot-stage">
      <img ref="imgRef" :src="image" class="screenshot-img" draggable="false" alt="" />
      <div v-if="rect" class="screenshot-select" :style="rectStyle"></div>
    </div>
    <div class="screenshot-tip">{{ $t('chat.screenshot_tip') }}</div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  // 截图 dataURL（CaptureScreen 返回 b64 → data:image/png;base64,）
  image: { type: String, required: true },
})
const emit = defineEmits(['done', 'cancel'])

const imgRef = ref(null)
const start = ref(null) // {x, y}（相对 img 左上）
const cur = ref(null) // {x, y}
const rect = computed(() => {
  if (!start.value || !cur.value) return null
  const x = Math.min(start.value.x, cur.value.x)
  const y = Math.min(start.value.y, cur.value.y)
  return {
    x, y,
    w: Math.abs(cur.value.x - start.value.x),
    h: Math.abs(cur.value.y - start.value.y),
  }
})
const rectStyle = computed(() => {
  const r = rect.value
  if (!r) return {}
  return { left: r.x + 'px', top: r.y + 'px', width: r.w + 'px', height: r.h + 'px' }
})

function localPoint(e) {
  const img = imgRef.value
  if (!img) return { x: 0, y: 0 }
  const r = img.getBoundingClientRect()
  return { x: e.clientX - r.left, y: e.clientY - r.top }
}

function onMouseDown(e) {
  if (e.button !== 0) return
  e.preventDefault()
  start.value = localPoint(e)
  cur.value = { ...start.value }
}

function onMouseMove(e) {
  if (!start.value) return
  cur.value = localPoint(e)
}

function onMouseUp() {
  const r = rect.value
  if (!r || r.w < 8 || r.h < 8) {
    start.value = null
    cur.value = null
    return
  }
  const img = imgRef.value
  if (!img) return
  // 物理像素换算：CSS 显示尺寸 → 原图自然尺寸
  const sx = Math.round(r.x * (img.naturalWidth / img.clientWidth))
  const sy = Math.round(r.y * (img.naturalHeight / img.clientHeight))
  const sw = Math.round(r.w * (img.naturalWidth / img.clientWidth))
  const sh = Math.round(r.h * (img.naturalHeight / img.clientHeight))
  const canvas = document.createElement('canvas')
  canvas.width = sw
  canvas.height = sh
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  try {
    ctx.drawImage(img, sx, sy, sw, sh, 0, 0, sw, sh)
  } catch (err) {
    console.warn('[ScreenshotOverlay] crop failed:', err)
    return
  }
  emit('done', canvas.toDataURL('image/png'))
}
</script>

<style scoped>
.screenshot-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  background: rgba(0, 0, 0, 0.75);
  cursor: crosshair;
  overflow: hidden;
}
/* 相对定位舞台：包裹 img 与选框，选框绝对定位以此为参照（= img 尺寸），
   与 localPoint 的相对 img 坐标一致；水平居中由舞台 margin:auto 承担。 */
.screenshot-stage {
  position: relative;
  width: fit-content;
  margin: auto;
}
.screenshot-img {
  display: block;
  max-width: 100vw;
  max-height: 100vh;
  user-select: none;
  -webkit-user-drag: none;
}
.screenshot-select {
  position: absolute;
  border: 1.5px dashed var(--accent, #409eff);
  background: rgba(64, 158, 255, 0.12);
  pointer-events: none;
  z-index: 2;
}
.screenshot-tip {
  position: absolute;
  top: 8px;
  left: 50%;
  transform: translateX(-50%);
  padding: 4px 12px;
  border-radius: 4px;
  background: rgba(0, 0, 0, 0.6);
  color: #fff;
  font-size: 12px;
  z-index: 3;
  pointer-events: none;
}
</style>
