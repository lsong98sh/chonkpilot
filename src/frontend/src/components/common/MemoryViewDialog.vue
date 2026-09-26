<template>
  <!-- 记忆条目只读查看弹框（「项目记忆」面板 · user 级「用户偏好」专用）：
       「用户偏好」落 `~/.chonkpilot/用户偏好.md`（工作目录之外）→ 走 file-open 会被 filesys
       越界校验拒绝，故经既有 `data-memory-read` 读全文后在此**只读**展示（无编辑/保存）。 -->
  <div class="mem-view-body">
    <pre class="mem-view-text">{{ content }}</pre>
  </div>
</template>

<script setup>
defineOptions({ name: 'MemoryViewDialog' })

defineProps({
  // 记忆条目全文（调用方经 data-memory-read 取得）
  content: { type: String, default: '' },
})
</script>

<style scoped>
.mem-view-body {
  padding: 4px 16px 12px;
}
.mem-view-text {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--font-mono, 'Consolas', 'Courier New', monospace);
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-primary);
}
</style>
