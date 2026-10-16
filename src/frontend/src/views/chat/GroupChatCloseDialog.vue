<template>
  <!-- G4 · 关窗二选一（群聊方案 §15.4 D37 / N22）：
       群聊是「自动长跑」（依次认领 → 结论 → 下一轮，无人也在转）→ **不能默认悄悄继续跑**，
       关窗必须让用户明确选「暂停」还是「终止」。
       ① 暂停 = **不发 `subchat-finish`**：窗口关闭后由既有 `instance-exit` → 后端**调度即停**，
          `lifecycle` 保持原值；重开窗口点「继续」即可在原线程续跑（D45 断点补跑）。
       ② 终止 = 发 `subchat-finish` → `lifecycle=finished`（并停掉在跑调度器）。
       **普通聊天窗口 / 单聊窗口的关窗行为不受本对话框影响**（本组件只挂群聊窗口）。 -->
  <div class="group-close">
    <div class="group-close-hint">{{ $t('chat.group_chat_close_hint') }}</div>
    <div class="group-close-actions">
      <button type="button" class="gc-btn gc-pause" @click="choose('pause')">
        {{ $t('chat.group_chat_pause') }}
      </button>
      <button type="button" class="gc-btn gc-terminate" @click="choose('terminate')">
        {{ $t('chat.group_chat_terminate') }}
      </button>
      <button type="button" class="gc-btn gc-cancel" @click="choose('')">
        {{ $t('common.cancel') }}
      </button>
    </div>
  </div>
</template>

<script setup>
/** 关窗选择对话框内容：`onChoose('pause'|'terminate'|'')` 由调用方收口（含关闭本对话框）。 */
const props = defineProps({
  /** 选择回调（'pause' = 仅暂停关窗；'terminate' = 终止后关窗；'' = 取消）。 */
  onChoose: { type: Function, default: null },
})

function choose(kind) {
  if (props.onChoose) props.onChoose(kind)
}
</script>

<style scoped>
.group-close {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.group-close-hint {
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-secondary);
}
.group-close-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.gc-btn {
  padding: 4px 12px;
  font-size: 12px;
  border-radius: var(--border-radius);
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-primary);
  cursor: pointer;
}
.gc-btn:hover {
  border-color: var(--accent);
  color: var(--accent);
}
.gc-terminate:hover {
  border-color: var(--danger);
  color: var(--danger);
}
.gc-btn:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
