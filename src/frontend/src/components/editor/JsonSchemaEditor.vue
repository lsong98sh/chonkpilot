<template>
  <!-- JSON Schema 编辑器（共通控件，P3-C2）：树形层级 + 校验 + 关键字/类型补全。
       供知识库「参数」页签（[parameters]/[arguments] = JSON Schema 文本）与后续其它配置复用；
       全部逻辑走 utils/jsonSchema.js（纯函数）。仅在用户操作时 emit（不因初始化/重解析回写）。 -->
  <div class="jse">
    <div class="jse-status">
      <span v-if="parseError" class="jse-bad">{{ parseError }}</span>
      <span v-else-if="errors.length > 0" class="jse-bad">{{ $t('jsonSchema.invalid') }}（{{ errors.length }}）</span>
      <span v-else class="jse-ok">{{ $t('jsonSchema.valid') }}</span>
      <span class="jse-hint">{{ $t('jsonSchema.hint') }}</span>
      <Button v-if="parseError" size="small" text class="jse-rewrite" @click="rewriteEmpty">
        {{ $t('jsonSchema.rewrite') }}
      </Button>
    </div>

    <ul v-if="!parseError && errors.length > 0" class="jse-errors">
      <li v-for="(e, i) in errors" :key="i">{{ e }}</li>
    </ul>

    <div v-if="!parseError" class="jse-tree">
      <div
        v-for="row in rows"
        :key="row.path"
        class="jse-row"
        :style="{ paddingLeft: (row.depth * 14 + 4) + 'px' }"
      >
        <span v-if="row.depth === 0" class="jse-name jse-root">$</span>
        <Input
          v-else-if="row.name !== 'items'"
          class="jse-name-input"
          size="small"
          :model-value="row.name"
          @change="onRename(row, $event.target.value)"
        />
        <span v-else class="jse-name">{{ row.name }}</span>

        <select class="jse-type" :value="row.type" @change="onType(row, $event.target.value)">
          <option value="">—</option>
          <option v-for="t in SCHEMA_TYPES" :key="t" :value="t">{{ t }}</option>
        </select>

        <label v-if="row.depth > 0 && row.name !== 'items'" class="jse-req">
          <input type="checkbox" :checked="row.required" @change="onRequired(row, $event.target.checked)" />
          {{ $t('jsonSchema.required') }}
        </label>

        <span v-if="row.type === 'object'" class="jse-actions">
          <Button size="small" text class="jse-act" @click="onAddProperty(row)">
            {{ $t('jsonSchema.add_property') }}
          </Button>
        </span>
        <span v-if="isEnumType(row.type)" class="jse-actions">
          <Button size="small" text class="jse-act" @click="onAddEnum(row)">
            {{ $t('jsonSchema.add_enum') }}
          </Button>
        </span>

        <select
          v-if="remainingKeywords(row).length > 0"
          class="jse-kw"
          :value="''"
          @change="onAddKeyword(row, $event.target.value, $event)"
        >
          <option value="">{{ $t('jsonSchema.complete_keyword') }}</option>
          <option v-for="k in remainingKeywords(row)" :key="k" :value="k">{{ k }}</option>
        </select>

        <Button v-if="row.depth > 0" size="small" text type="danger" class="jse-act" @click="onRemove(row)">
          ✕
        </Button>

        <span v-if="presentKeywords(row).length > 0" class="jse-enums">
          <span v-for="k in presentKeywords(row)" :key="k" class="jse-enum">
            {{ k }}
            <button type="button" class="jse-enum-x" :title="$t('jsonSchema.remove_keyword')" @click="onRemoveKeyword(row, k)">✕</button>
          </span>
        </span>
        <span v-if="row.node.enum && row.node.enum.length" class="jse-enums">
          <span v-for="(v, i) in row.node.enum" :key="i" class="jse-enum">
            {{ v }}
            <button type="button" class="jse-enum-x" @click="onRemoveEnum(row, i)">✕</button>
          </span>
        </span>
      </div>
    </div>

    <Textarea class="jse-preview" :model-value="serialized" :rows="8" readonly />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Input, Textarea, Button, promptInput } from '../../components/ui'
import {
  SCHEMA_TYPES, keywordSuggestions, inferType, parseSchemaText, serializeSchema, validateSchema,
  removeNode, addProperty, renameProperty, setNodeType, toggleRequired,
  addEnumValue, removeEnumValue, addKeyword, removeKeyword, schemaRows,
} from '../../utils/jsonSchema'

const props = defineProps({
  // 参数文本（JSON 或 mcp 契约模板形态的 YAML 子集）；用户操作时以规范 JSON 回写。
  modelValue: { type: String, default: '' },
})
const emit = defineEmits(['update:modelValue'])

const { t } = useI18n()

const schema = ref({})
const parseError = ref('')

// 解析（挂载时；父组件经 :key 变更在重载/恢复后重建本控件 → 不需要 watch props）
function reparse() {
  const res = parseSchemaText(props.modelValue)
  parseError.value = res.error
  schema.value = res.schema === null || res.schema === undefined ? {} : res.schema
}
onMounted(reparse)

const rows = computed(() => schemaRows(schema.value))
const errors = computed(() => (parseError.value ? [] : validateSchema(schema.value)))
const serialized = computed(() => serializeSchema(schema.value))

function apply(next) {
  schema.value = next
  emit('update:modelValue', serializeSchema(next))
}

function isEnumType(type) {
  return type === 'string' || type === 'number' || type === 'integer'
}

// 关键字补全候选 = 该类型建议关键字中尚未使用的（按类型 + 通用）
function remainingKeywords(row) {
  const node = row.node || {}
  const type = row.type || inferType(node)
  return keywordSuggestions(type).filter(k => !(k in node))
}

// 已补全的可选关键字（结构关键字由类型下拉 / 必填勾选 / 子行表达 → 不重复列出）
const STRUCTURAL_KEYWORDS = ['type', 'properties', 'required', 'items', 'enum']
function presentKeywords(row) {
  const node = row.node || {}
  const type = row.type || inferType(node)
  return keywordSuggestions(type).filter(k => (k in node) && STRUCTURAL_KEYWORDS.indexOf(k) < 0)
}

function onRemoveKeyword(row, keyword) {
  apply(removeKeyword(schema.value, row.path, keyword))
}

function onRename(row, value) {
  const parentPath = row.path.slice(0, row.path.lastIndexOf('/properties/'))
  apply(renameProperty(schema.value, parentPath || '$', row.name, value))
}

function onType(row, type) {
  apply(setNodeType(schema.value, row.path, type))
}

function onRequired(row, checked) {
  const parentPath = row.path.slice(0, row.path.lastIndexOf('/properties/'))
  apply(toggleRequired(schema.value, parentPath || '$', row.name, checked))
}

async function onAddProperty(row) {
  let name
  try {
    name = await promptInput(t('jsonSchema.new_property_name'), '')
  } catch (_) { return }
  if (name === null || name === undefined) return
  const key = String(name).trim()
  if (!key) return
  apply(addProperty(schema.value, row.path, key, 'string'))
}

async function onAddEnum(row) {
  let value
  try {
    value = await promptInput(t('jsonSchema.enum_value'), '')
  } catch (_) { return }
  if (value === null || value === undefined) return
  apply(addEnumValue(schema.value, row.path, String(value)))
}

function onRemoveEnum(row, index) {
  apply(removeEnumValue(schema.value, row.path, index))
}

function onAddKeyword(row, keyword, event) {
  if (!keyword) return
  apply(addKeyword(schema.value, row.path, keyword, row.type))
  if (event && event.target) event.target.value = '' // 复位下拉（同一关键字可重复补全其它节点）
}

function onRemove(row) {
  apply(removeNode(schema.value, row.path))
}

// 解析失败时允许以空 Schema 重写（丢掉无法解析的旧文本；其余内容视图不动）
function rewriteEmpty() {
  parseError.value = ''
  apply({ type: 'object', properties: {} })
}
</script>

<style scoped>
.jse {
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-height: 0;
}
.jse-status {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
.jse-ok {
  color: var(--success, #67c23a);
}
.jse-bad {
  color: var(--danger, #f56c6c);
}
.jse-hint {
  flex: 1;
  min-width: 0;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.jse-errors {
  margin: 0;
  padding-left: 18px;
  font-size: 12px;
  color: var(--danger, #f56c6c);
  max-height: 120px;
  overflow: auto;
}
.jse-tree {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px 4px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-height: 46vh;
  overflow: auto;
}
.jse-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.jse-name {
  font-size: 12px;
  font-family: var(--font-mono, Consolas, monospace);
  color: var(--text-primary);
  flex-shrink: 0;
}
.jse-root {
  font-weight: 700;
}
.jse-name-input {
  width: 160px;
  flex-shrink: 0;
}
.jse-type,
.jse-kw {
  font-size: 12px;
  padding: 2px 4px;
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  background: var(--bg-primary);
  color: var(--text-primary);
  flex-shrink: 0;
}
.jse-req {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 11px;
  color: var(--text-muted);
  flex-shrink: 0;
}
.jse-actions {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}
.jse-act {
  font-size: 12px;
}
.jse-enums {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  flex-wrap: wrap;
  min-width: 0;
}
.jse-enum {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0 4px;
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 11px;
  color: var(--text-secondary);
}
.jse-enum-x {
  border: none;
  background: none;
  padding: 0;
  cursor: pointer;
  color: var(--text-muted);
  font-size: 10px;
}
.jse-preview :deep(textarea) {
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 12px;
  resize: vertical;
}
</style>
