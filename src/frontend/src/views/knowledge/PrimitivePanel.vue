<template>
  <!-- 原语文件（*.type.md）编辑面板（P3-C2，2026-09-24）：
       布局 = 顶部标题 / 中间 Tabs（撑满）/ 底部 保存·恢复；
       页签 = meta · 描述 · 参数 · 正文；「参数」= JSON Schema 编辑器（共通控件 JsonSchemaEditor）。
       保存走 SavePrimitive（后端按 mcp 契约 [meta]/[description]/[parameters|arguments]/[content] 序列化）。 -->
  <div class="prim-panel">
    <!-- 顶部标题：类型标签 + 标题（可编辑）+ 路径 + 未保存标记 -->
    <div class="prim-head">
      <Tag v-if="token" size="small" :type="tagType">{{ typeLabel }}</Tag>
      <Input v-model="form.title" class="prim-title" :placeholder="t('knowledgeList.title_ph')" />
      <span class="prim-path" :title="path">{{ path }}</span>
      <span v-if="dirty" class="prim-dirty">{{ t('common.unsaved') }}</span>
    </div>

    <!-- 中间：Tabs（撑满） -->
    <Tabs v-model="curTab" :tabs="tabs" class="prim-tabs">
      <template #meta>
        <div class="prim-tab-body">
          <div class="pf-meta-grid">
            <div class="pf-meta-head"><span>key</span><span>value</span><span></span></div>
            <div v-for="(row, i) in form.metaRows" :key="i" class="pf-meta-row">
              <Input v-model="row.k" size="small" placeholder="key" />
              <Input v-model="row.v" size="small" placeholder="value" />
              <Button text size="small" @click="form.metaRows.splice(i, 1)">✕</Button>
            </div>
            <Button text size="small" class="pf-add" @click="form.metaRows.push({ k: '', v: '' })">
              <Icon name="plus" :size="12" /> {{ t('knowledgeList.add_row') }}
            </Button>
          </div>
        </div>
      </template>

      <template #description>
        <div class="prim-tab-body">
          <div class="pf-desc-head">
            <Button size="small" text class="pf-extract" @click="handleExtractDescription">
              {{ t('knowledgeList.extract_from_content') }}
            </Button>
          </div>
          <Textarea
            v-model="form.description"
            class="pf-area"
            :placeholder="t('knowledgeList.description_placeholder')"
          />
        </div>
      </template>

      <template #parameters>
        <div class="prim-tab-body">
          <div class="pf-hint">{{ paramsLabel }} · {{ t('knowledgeList.params_hint') }}</div>
          <JsonSchemaEditor
            v-if="loaded"
            :key="'jse-' + paramsVersion"
            v-model="form.parameters"
            class="pf-schema"
          />
        </div>
      </template>

      <template #content>
        <div class="prim-tab-body">
          <Textarea
            v-model="form.content"
            class="pf-area"
            :placeholder="t('knowledgeList.content_placeholder')"
          />
        </div>
      </template>
    </Tabs>

    <!-- 底部：保存 / 恢复（+ 恢复默认） -->
    <div class="prim-footer">
      <span v-if="saveError" class="pf-error">{{ saveError }}</span>
      <Button
        v-if="upperLevels.length > 0"
        size="small"
        :disabled="!upperSource"
        :title="upperSource ? t('common.restore_default_hint') : t('common.restore_default_none')"
        @click="handleRestoreDefault"
      >{{ t('common.restore_default') }}</Button>
      <Button size="small" :loading="saving" v-mq:[EventNames.primSave].click>{{ t('common.save') }}</Button>
      <Button size="small" v-mq:[EventNames.primRestore].click>{{ t('common.restore') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '../../components/icon/Icon.vue'
import { Input, Textarea, Button, Tag, message, confirm } from '../../components/ui'
import Tabs from '../../components/ui/Tabs.vue'
import JsonSchemaEditor from '../../components/editor/JsonSchemaEditor.vue'
import { readPrimitive, savePrimitive, getKnowledgeRoot } from '../../api/knowledge'
import { primitiveTokenOf, primitiveTagType } from '../../utils/primitive'
import { extractDescriptionFromContent } from '../../utils/descriptionExtract'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const props = defineProps({
  path: { type: String, required: true },
})

const { t } = useI18n()

const token = computed(() => primitiveTokenOf(props.path))
const typeLabel = computed(() => t('fileTree.type_' + token.value))
const tagType = computed(() => primitiveTagType(token.value))

const curTab = ref('meta')
const tabs = computed(() => [
  { name: 'meta', label: t('knowledgeList.tab_meta') },
  { name: 'description', label: t('knowledgeList.tab_description') },
  { name: 'parameters', label: t('knowledgeList.tab_parameters') },
  { name: 'content', label: t('knowledgeList.tab_content') },
])

const loaded = ref(false)
const saving = ref(false)
const saveError = ref('')
const pristine = ref(null) // 最近一次读取/保存后的 form JSON（dirty 基准 + 恢复目标）
const paramsSection = ref('')
// JSON Schema 编辑器重挂载标记：每次装载/恢复表单后自增（父侧 :key 变化 → 控件按新文本重建，
// 无需 watch props）
const paramsVersion = ref(0)

// 「恢复默认」：用户级/项目级原语可一键回填**上一级**（项目→用户→系统 app；用户→系统 app）
// 的同名原语。系统级（app）只读（无编辑入口）→ 不显示该按钮。回填仅改本表单（未保存草稿），
// 落库沿用既有【保存】语义（data-knowledge-save）。数据源复用 data-knowledge-read。
const levelRoots = ref({}) // { app, user, project } → 各级 capability 根（绝对路径）
const upperSource = ref(null) // { level, doc }：最近可回填的上一级原语（无则 null → 按钮禁用）

const form = reactive({ title: '', metaRows: [], description: '', parameters: '', content: '' })

const dirty = computed(() => {
  if (!pristine.value) return false
  return JSON.stringify({ ...buildFormForCompare() }) !== pristine.value
})
const paramsLabel = computed(() => {
  return paramsSection.value || (token.value === 'prompt' ? '[arguments]' : '[parameters]')
})

function normPath(p) {
  return String(p || '').replace(/\\/g, '/').replace(/\/+$/, '')
}

// 当前原语所属级别：按 capability 根前缀归属（具体级优先）。app 级 = 系统只读，不提供恢复默认。
const curLevel = computed(() => {
  const p = normPath(props.path)
  if (!p) return ''
  for (const lv of ['project', 'user', 'app']) {
    const r = normPath(levelRoots.value[lv])
    if (r && (p === r || p.startsWith(r + '/'))) return lv
  }
  return ''
})
// 上一级链（项目→用户→系统 app；用户→系统 app；系统级无）
const upperLevels = computed(() => {
  if (curLevel.value === 'project') return ['user', 'app']
  if (curLevel.value === 'user') return ['app']
  return []
})

function buildFormForCompare() {
  return {
    title: form.title,
    meta: form.metaRows.filter(r => r.k.trim()).reduce((m, r) => { m[r.k.trim()] = r.v; return m }, {}),
    description: form.description,
    parameters: form.parameters,
    content: form.content,
    params_section: paramsSection.value,
  }
}

function fillForm(doc) {
  form.title = doc.title || ''
  form.metaRows = Object.entries(doc.meta || {}).map(([k, v]) => ({ k, v: String(v) }))
  form.description = doc.description || ''
  form.parameters = doc.parameters || ''
  form.content = doc.content || ''
  paramsSection.value = doc.params_section || (token.value === 'prompt' ? '[arguments]' : '[parameters]')
  paramsVersion.value += 1 // 参数文本已换 → Schema 编辑器按新文本重建
}

function applyDoc(doc) {
  fillForm(doc)
  pristine.value = JSON.stringify(buildFormForCompare())
}

async function load() {
  try {
    const res = await readPrimitive(props.path)
    const doc = (res && res.doc) || {}
    applyDoc(doc)
    loaded.value = true
    saveError.value = ''
  } catch (e) {
    loaded.value = true
    saveError.value = (e && e.message) || t('knowledgeList.load_failed')
  }
}

// 取三级 capability 根（用于判定当前原语的级别，从而定出上一级）。
async function loadLevelRoots() {
  const out = {}
  for (const k of ['app', 'user', 'project']) {
    try {
      const r = await getKnowledgeRoot(k)
      out[k] = (r && r.root) || ''
    } catch (_) {
      out[k] = ''
    }
  }
  levelRoots.value = out
}

// 逐级降级探测上一级同名原语：上一级不存在则取 app；都不存在 → upperSource=null（按钮禁用）。
async function resolveUpperSource() {
  upperSource.value = null
  const levels = upperLevels.value
  const p = normPath(props.path)
  const root = normPath(levelRoots.value[curLevel.value])
  if (levels.length === 0 || !root || !p.startsWith(root + '/')) return
  const rel = p.slice(root.length + 1)
  for (const lv of levels) {
    const r = normPath(levelRoots.value[lv])
    if (!r) continue
    try {
      const res = await readPrimitive(`${r}/${rel}`)
      if (res && res.doc) {
        upperSource.value = { level: lv, doc: res.doc }
        return
      }
    } catch (_) { /* 该级无同名原语 → 继续下一级 */ }
  }
}

// 回填为未保存草稿（不刷新 pristine → dirty=true），由用户点【保存】落库。
function handleRestoreDefault() {
  const src = upperSource.value
  if (!src) {
    message.info(t('common.restore_default_none'))
    return
  }
  fillForm(src.doc || {})
  saveError.value = ''
  message.info(t('common.restore_default_done', { level: t('fileTree.kb_level_' + src.level) }))
}

// 从「正文」本地确定性提取描述（纯函数，不依赖 LLM）；描述非空时先确认再覆盖，否则直接写入。
async function handleExtractDescription() {
  if (!(form.content || '').trim()) {
    message.warning(t('knowledgeList.extract_no_content'))
    return
  }
  const text = extractDescriptionFromContent(form.content)
  if (!text) {
    message.warning(t('knowledgeList.extract_no_content'))
    return
  }
  if ((form.description || '').trim()) {
    try {
      await confirm(t('knowledgeList.extract_confirm'), t('knowledgeList.extract_from_content'))
    } catch (_) {
      return
    }
  }
  form.description = text
}

async function handleSave() {
  if (!props.path) return
  saving.value = true
  saveError.value = ''
  try {
    const meta = {}
    for (const r of form.metaRows) if (r.k.trim()) meta[r.k.trim()] = r.v
    await savePrimitive(props.path, {
      title: form.title,
      meta,
      description: form.description,
      parameters: form.parameters,
      content: form.content,
      params_section: paramsSection.value,
    })
    message.success(t('common.saved'))
    await load() // 同步 doc（后端规范化后），dirty 清除
  } catch (e) {
    saveError.value = (e && e.message) || t('knowledgeList.save_failed')
    message.error(t('knowledgeList.save_failed'))
  } finally {
    saving.value = false
  }
}

function handleRestore() {
  if (!pristine.value) return
  try {
    const snap = JSON.parse(pristine.value)
    form.title = snap.title || ''
    form.metaRows = Object.entries(snap.meta || {}).map(([k, v]) => ({ k, v: String(v) }))
    form.description = snap.description || ''
    form.parameters = snap.parameters || ''
    form.content = snap.content || ''
    paramsSection.value = snap.params_section || ''
    paramsVersion.value += 1 // 参数文本已回滚 → Schema 编辑器重建
    saveError.value = ''
    message.info(t('common.restored'))
  } catch (e) { /* noop */ }
}

function onFileChanged(data) {
  if (!data || Array.isArray(data.children)) return
  const p = data && (data.path || data.file)
  if (!p || String(p).replace(/\\/g, '/') !== props.path) return
  const op = data.operation || data.op
  if (op === 'remove' || op === 'deleted') return
  if (dirty.value) return // 编辑中有未保存改动：不覆盖
  load()
}

const _unsubs = []
onMounted(async () => {
  load()
  _unsubs.push(mq.on(EventNames.primSave, handleSave))
  _unsubs.push(mq.on(EventNames.primRestore, handleRestore))
  _unsubs.push(mq.on(EventNames.fileChanged, onFileChanged))
  // 「恢复默认」依赖三级根判定当前级别 → 解析后探测上一级同名原语
  await loadLevelRoots()
  await resolveUpperSource()
})
onUnmounted(() => {
  for (const fn of _unsubs) fn()
  _unsubs.length = 0
})
</script>

<style scoped>
.prim-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.prim-head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  flex-shrink: 0;
  border-bottom: 1px solid var(--border);
  background: var(--bg-secondary);
}
.prim-title {
  width: 240px;
  flex-shrink: 0;
}
.prim-path {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
  color: var(--text-muted);
  font-family: var(--font-mono, Consolas, monospace);
}
.prim-dirty {
  font-size: 11px;
  color: var(--warning, #e6a23c);
}
/* 中间 Tabs 撑满可用高度（每个页签内容区各自滚动） */
.prim-tabs {
  flex: 1;
  min-height: 0;
}
.prim-tab-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
/* 描述/正文 Textarea 纵向撑满页签：容器 flex 列 + textarea 自身 flex 撑高。
   `.pf-area` 落在 Textarea 根节点（即 textarea 本体），故直接命中；原实现把 `.pf-area` 与
   `:deep(textarea)` 组合使用 —— 指向不存在的嵌套 textarea、选择器恒不命中（既有 bug）。
   提高选择器特异性以稳定覆盖 Textarea 组件自身的 `resize: vertical` 等规则。 */
.prim-tab-body .pf-area {
  flex: 1;
  min-height: 0;
  resize: none;
  font-size: 13px;
  line-height: 1.5;
}
.pf-desc-head {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}
.pf-schema {
  min-height: 0;
}
.pf-meta-grid {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 6px 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  max-width: 860px;
}
.pf-meta-head, .pf-meta-row {
  display: flex;
  align-items: center;
  gap: 6px;
}
.pf-meta-head span { flex: 1; font-size: 12px; color: var(--text-muted); }
.pf-meta-head span:last-child { flex: 0 0 28px; }
.pf-meta-row > :first-child { flex: 1; }
.pf-meta-row > :nth-child(2) { flex: 1.4; }
.pf-meta-row > :last-child { flex: 0 0 28px; }
.pf-add {
  align-self: flex-start;
}
.pf-hint {
  font-size: 11px;
  color: var(--text-muted);
  opacity: 0.75;
}
.prim-footer {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-top: 1px solid var(--border);
  flex-shrink: 0;
}
.pf-error {
  flex: 1;
  font-size: 12px;
  color: var(--danger, #f56c6c);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
