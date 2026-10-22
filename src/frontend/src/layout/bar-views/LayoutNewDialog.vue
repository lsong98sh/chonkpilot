<template>
  <div class="new-layout">
    <label class="field">
      <span class="label">名称</span>
      <input
        v-model="name"
        class="input"
        maxlength="24"
        placeholder="新布局"
        @keydown.enter.prevent="confirm"
      />
    </label>
    <p v-if="error" class="error">{{ error }}</p>

    <label class="field">
      <span class="label">复制自</span>
      <select v-model="from" class="input">
        <option value="blank">空白布局</option>
        <option v-for="p in store.list" :key="p.id" :value="p.id">{{ p.name }}</option>
      </select>
    </label>

    <p class="tip">创建后进入编辑器；保存只写入档案，不会切换当前布局。</p>

    <div class="actions">
      <button class="btn" @click="cancel">取消</button>
      <button class="btn primary" @click="confirm">创建</button>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useProfileStore } from '../profiles.js'
import { nextDefaultName, validateName } from '../profiles-core.js'
import { EventNames } from '../../layout-free/events/event-names.js'
import mq from '../../utils/mq'

const props = defineProps({
  /** 关闭对话框（由调用方注入） */
  onDone: { type: Function, default: null },
})

const store = useProfileStore()
const name = ref(nextDefaultName(store.list))
const from = ref('blank')
const error = ref('')

const canSubmit = computed(() => name.value.trim().length > 0)

function confirm() {
  const err = validateName(store.list, name.value)
  if (err) {
    error.value = err
    return
  }
  mq.emit(EventNames.layoutProfileCreate, { name: name.value.trim(), from: from.value })
  props.onDone?.()
}

function cancel() {
  props.onDone?.()
}
</script>

<style scoped>
.new-layout {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px 16px;
  font-size: 12px;
  color: var(--lf-text, #222);
}
.field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}
.label {
  color: var(--lf-text-weak, #666);
  font-size: 11px;
}
.input {
  height: 28px;
  padding: 0 8px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 5px;
  background: var(--lf-surface, #fff);
  color: inherit;
  font-size: 12px;
  outline: none;
}
.input:focus {
  border-color: var(--lf-accent, #3b82f6);
}
.error {
  margin: -6px 0 0;
  color: #d93025;
  font-size: 11px;
}
.tip {
  margin: 0;
  color: var(--lf-text-weak, #666);
  font-size: 11px;
  line-height: 1.5;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 2px;
}
.btn {
  height: 28px;
  padding: 0 14px;
  border: 1px solid var(--lf-border, #d8dee9);
  border-radius: 5px;
  background: transparent;
  color: inherit;
  font-size: 12px;
  cursor: pointer;
}
.btn.primary {
  border-color: var(--lf-accent, #3b82f6);
  background: var(--lf-accent, #3b82f6);
  color: #fff;
}
</style>
