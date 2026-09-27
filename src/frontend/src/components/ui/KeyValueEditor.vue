<!--
  KeyValueEditor —— KV / 行 编辑器（2026-09-27 用户口径）。

  用途：把「每行 K=V」（kv）或「每行一条」（list）的文本改为可增删行的行编辑控件；
  **序列化回同一文本格式**，故调用方（EditMCPDialog）保存链路（textToHeaders /
  textToList / textToArgs）一行都不用改。

  props.modelValue 为**既有文本格式**（String）：kv = `K=V` 逐行；list = 每行一条。
  变更后 emit('update:modelValue', text)：
    - kv  ：`K=V` 逐行（键为空的行**不序列化**，与 textToHeaders 的 `indexOf('=') > 0` 一致）
    - list：每行一条（空行不序列化，与 textToArgs 的 filter(Boolean) 一致）
  解析/序列化均保留行内内容（除空行外不做裁剪），键值仅按**第一个 `=`** 切分。

  变更用受控回调（不用 watch，遵循项目规范「禁止 watch/watchEffect 监听 props」）。
-->
<template>
  <div class="kv-editor">
    <div v-for="(row, i) in rows" :key="i" class="kv-row">
      <Input
        v-if="mode === 'kv'"
        class="kv-key"
        :model-value="row.k"
        :placeholder="keyPlaceholder"
        @update:model-value="(v) => setField(i, 'k', v)"
      />
      <Input
        class="kv-value"
        :model-value="row.v"
        :placeholder="valuePlaceholder"
        @update:model-value="(v) => setField(i, 'v', v)"
      />
      <Button class="kv-del" text size="mini" :title="deleteLabel" @click="removeRow(i)">
        <Icon name="delete" :size="13" />
      </Button>
    </div>
    <Button class="kv-add" size="small" @click="addRow">
      <Icon name="plus" :size="13" /> {{ addLabel }}
    </Button>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import Input from './Input.vue'
import Button from './Button.vue'
import Icon from '../icon/Icon.vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  mode: { type: String, default: 'kv' }, // 'kv' | 'list'
  keyPlaceholder: { type: String, default: '' },
  valuePlaceholder: { type: String, default: '' },
  addLabel: { type: String, default: '' },
  deleteLabel: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

// 行数组：kv 模式 = {k, v}；list 模式 = {v}（k 空）。
function parse(text, mode) {
  const lines = String(text || '').split('\n').filter((l) => l.trim() !== '')
  if (mode === 'kv') {
    return lines.map((line) => {
      const i = line.indexOf('=')
      return i >= 0 ? { k: line.slice(0, i), v: line.slice(i + 1) } : { k: line, v: '' }
    })
  }
  return lines.map((line) => ({ k: '', v: line }))
}

function serialize(rows, mode) {
  if (mode === 'kv') {
    return rows
      .filter((r) => String(r.k).trim() !== '')
      .map((r) => `${r.k}=${r.v}`)
      .join('\n')
  }
  return rows.filter((r) => String(r.v).trim() !== '').map((r) => r.v).join('\n')
}

const rows = ref(parse(props.modelValue, props.mode))

function sync() {
  emit('update:modelValue', serialize(rows.value, props.mode))
}
function setField(i, field, value) {
  rows.value[i][field] = value
  sync()
}
function addRow() {
  rows.value.push({ k: '', v: '' })
}
function removeRow(i) {
  rows.value.splice(i, 1)
  sync()
}
</script>

<style scoped>
.kv-editor {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}
.kv-row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.kv-key {
  flex: 0 0 34%;
  min-width: 0;
}
.kv-value {
  flex: 1;
  min-width: 0;
}
.kv-del {
  flex-shrink: 0;
  color: var(--fg-secondary);
}
.kv-add {
  align-self: flex-start;
}
</style>
