<template>
  <div class="cmd-editor">
    <div class="ce-row">
      <label>命令类型</label>
      <select :value="model?.type ?? ''" @change="onType">
        <option value="">未配置（保存时会被拦截）</option>
        <option v-for="t in COMMAND_TYPES" :key="t.value" :value="t.value">{{ t.label }}</option>
      </select>
    </div>

    <!-- 打开 view 类命令（对话框 / 自由 tabs 区） -->
    <template v-if="viewCmd">
      <div class="ce-row">
        <label>view</label>
        <select :value="viewCmd.viewKey" @change="onView">
          <optgroup label="单例 view / 容器">
            <option
              v-for="v in singletonViews"
              :key="v.key"
              :value="v.key"
              :disabled="v.disabled && v.key !== viewCmd.viewKey"
            >
              {{ v.icon }} {{ v.label }}{{ v.disabled && v.key !== viewCmd.viewKey ? v.takenLabel : '' }}
            </option>
          </optgroup>
          <optgroup label="多实例 view（需选择配套 key）">
            <option v-for="v in multiViews" :key="v.key" :value="v.key">{{ v.icon }} {{ v.label }}</option>
          </optgroup>
        </select>
      </div>

      <div v-if="def?.mode === 'multi'" class="ce-row">
        <label>配套 key</label>
        <select :value="viewCmd.instanceKey ?? ''" @change="onKey">
          <option value="" disabled>必须选择一个 key…</option>
          <option v-for="k in keyList" :key="k.key" :value="k.key">
            {{ k.label }}（{{ k.key }} = {{ k.value }}）
          </option>
        </select>
      </div>

      <div v-if="viewCmd.type === 'open-dialog'" class="ce-row">
        <label>对话框标题</label>
        <input type="text" :value="viewCmd.title ?? ''" placeholder="留空则使用 view 名称" @input="onTitle" />
      </div>

      <div v-if="viewCmd.type === 'open-tabs'" class="ce-row">
        <label>目标自由 tab 容器</label>
        <select :value="viewCmd.targetLeafId ?? ''" @change="onTarget">
          <option value="">自动（当前激活的自由 tab 容器 → 第一个）</option>
          <option v-for="l in freeTabLeaves" :key="l.id" :value="l.id">
            {{ l.title || '自由 tab 容器' }}
          </option>
        </select>
      </div>
    </template>

    <!-- 打开抽屉 -->
    <template v-else-if="model?.type === 'open-drawer'">
      <div class="ce-row">
        <label>抽屉</label>
        <select :value="model.drawerId ?? ''" @change="onDrawer">
          <option value="" disabled>必须选择一个抽屉…</option>
          <option v-for="d in drawers" :key="d.id" :value="d.id">
            {{ d.label }}（{{ positionLabel(d.position) }}）
          </option>
        </select>
      </div>
      <div class="ce-row">
        <label>覆盖内容 view</label>
        <select :value="model.viewKey ?? ''" @change="onDrawerView">
          <option value="">不覆盖（打开抽屉默认内容）</option>
          <optgroup label="单例 view">
            <option v-for="v in singletonViews" :key="v.key" :value="v.key" :disabled="v.disabled">
              {{ v.icon }} {{ v.label }}{{ v.disabled ? v.takenLabel : '' }}
            </option>
          </optgroup>
          <optgroup label="多实例 view">
            <option v-for="v in multiViews" :key="v.key" :value="v.key">{{ v.icon }} {{ v.label }}</option>
          </optgroup>
        </select>
      </div>
      <div v-if="drawerViewDef?.mode === 'multi'" class="ce-row">
        <label>配套 key</label>
        <select :value="model.instanceKey ?? ''" @change="onDrawerKey">
          <option value="" disabled>必须选择一个 key…</option>
          <option v-for="k in keyList" :key="k.key" :value="k.key">
            {{ k.label }}（{{ k.key }} = {{ k.value }}）
          </option>
        </select>
      </div>
    </template>

    <!-- 开 / 关抽屉：只选抽屉，同一个按钮切换 -->
    <template v-else-if="model?.type === 'toggle-drawer'">
      <div class="ce-row">
        <label>抽屉</label>
        <select :value="model.drawerId ?? ''" @change="onDrawer">
          <option value="" disabled>必须选择一个抽屉…</option>
          <option v-for="d in drawers" :key="d.id" :value="d.id">
            {{ d.label }}（{{ positionLabel(d.position) }}）
          </option>
        </select>
      </div>
      <p class="ce-tip">已打开则关闭，未打开则打开。</p>
    </template>

    <!-- 关闭当前标签页 -->
    <template v-else-if="model?.type === 'close-tabs-active'">
      <div class="ce-row">
        <label>目标自由 tab 容器</label>
        <select :value="model.targetLeafId ?? ''" @change="onTarget">
          <option value="">自动（当前激活的自由 tab 容器 → 第一个）</option>
          <option v-for="l in freeTabLeaves" :key="l.id" :value="l.id">
            {{ l.title || '自由 tab 容器' }}
          </option>
        </select>
      </div>
    </template>

    <!-- 发布 MQ 动作（UILAYOUT-1 裁决 1）：只允许引用既有主题 + 载荷 -->
    <template v-else-if="model?.type === 'publish-mq'">
      <div class="ce-row">
        <label>MQ 主题</label>
        <input
          type="text"
          :value="model.topic ?? ''"
          placeholder="既有主题，如 preview-tab-open"
          @input="onTopic"
        />
      </div>
      <div class="ce-row">
        <label>载荷（JSON）</label>
        <input
          type="text"
          :value="payloadDraft"
          placeholder='{ "kind": "settings-llm" }'
          @input="onPayload"
        />
      </div>
      <p class="ce-tip">只允许引用**既有**主题（不得新增 / 修改主题或 payload 形状）。</p>
      <p v-if="!model.topic" class="ce-tip warn">未填写主题，保存时会被拦截。</p>
      <p v-if="payloadInvalid" class="ce-tip warn">载荷不是合法 JSON（当前按空对象发送，保存前请修正）。</p>
    </template>

    <p v-if="!model" class="ce-tip warn">该条目还没有命令，必须配置后才能保存。</p>
    <p v-else class="ce-tip">命令摘要：{{ describeCommand(model) }}</p>
    <p v-if="def?.mode === 'multi' && !viewCmd?.instanceKey" class="ce-tip warn">
      {{ def.label }} 是多实例 view，必须选择配套 key。
    </p>
    <p v-if="drawerViewDef?.mode === 'multi' && !model?.instanceKey" class="ce-tip warn">
      {{ drawerViewDef.label }} 是多实例 view，必须选择配套 key。
    </p>
    <p v-if="model?.type === 'open-tabs' && !freeTabLeaves.length" class="ce-tip warn">
      布局里还没有「自由 tab 容器」区域，请先在「布局编辑」中把某个区域设为该类型。
    </p>
    <p v-if="model?.type === 'open-drawer' && !drawers.length" class="ce-tip warn">
      还没有配置抽屉，请先在「抽屉」页签中添加。
    </p>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { allViews, getView } from '../../config/views'
import { useConfigStore } from '../../stores/config'
import { useKeysStore } from '../../stores/keys'
import { describeCommand, COMMAND_TYPES } from '../../utils/commands'

const props = defineProps({
  /** 命令对象，可为 null（未配置） */
  model: { type: Object, default: null },
  /** 占用登记 slotKey（该命令所在条目），用于单例过滤 */
  slotKey: { type: String, default: '' },
})

const emit = defineEmits(['change'])

const config = useConfigStore()
const keys = useKeysStore()

/** 单例选项：已被别处占用则禁用（本条目自己不算占用） */
const singletonViews = computed(() => config.viewOptions(props.slotKey))
// 区块定义表由**宿主注入**（`deps.views.definitions`，UILAYOUT-1 §3.4）→ 经 `allViews()` 取值
// （每次调用按当前语言解析 label，语言切换即时跟随；不再是子系统内的静态常量）
const multiViews = computed(() => allViews().filter((v) => v.mode === 'multi'))
const keyList = computed(() => keys.list)
const freeTabLeaves = computed(() => config.freeTabsLeaves)
const drawers = computed(() => config.drawers)

/** 打开 view 类命令（含 viewKey / instanceKey） */
const viewCmd = computed(() => {
  const m = props.model
  if (!m) return null
  if (['close-tabs-active', 'open-drawer', 'toggle-drawer', 'publish-mq'].includes(m.type)) return null
  return m
})

/** 「发布 MQ 动作」载荷的 JSON 文本（本地草稿；合法才 emit，非法保持文本不动） */
const payloadDraft = ref('')
const payloadInvalid = ref(false)
function initPayloadDraft() {
  const p = props.model && props.model.type === 'publish-mq' ? props.model.payload : null
  if (p === undefined || p === null) return ''
  try {
    const s = JSON.stringify(p)
    return s === '{}' ? '' : s
  } catch (_) {
    return ''
  }
}
payloadDraft.value = initPayloadDraft()

const def = computed(() => (viewCmd.value ? getView(viewCmd.value.viewKey) : undefined))
const drawerViewDef = computed(() => (props.model?.viewKey ? getView(props.model.viewKey) : undefined))

function defaultViewKey() {
  const first = singletonViews.value.find((v) => !v.disabled)
  return first?.key ?? allViews()[0]?.key ?? ''
}

function positionLabel(p) {
  return { left: '左侧', right: '右侧', top: '顶部', bottom: '底部' }[p] ?? p
}

function onType(e) {
  const type = e.target.value
  if (!type) {
    emit('change', null)
    return
  }
  const viewKey = viewCmd.value ? viewCmd.value.viewKey : defaultViewKey()
  const d = getView(viewKey)
  const instanceKey = d?.mode === 'multi' ? keyList.value[0]?.key ?? null : null
  if (type === 'open-dialog') emit('change', { type, viewKey, instanceKey, title: '' })
  else if (type === 'open-tabs') emit('change', { type, targetLeafId: null, viewKey, instanceKey })
  else if (type === 'open-drawer') {
    emit('change', { type, drawerId: drawers.value[0]?.id ?? '', viewKey: null, instanceKey: null })
  } else if (type === 'toggle-drawer') {
    emit('change', { type, drawerId: drawers.value[0]?.id ?? '' })
  } else if (type === 'publish-mq') {
    emit('change', { type, topic: '', payload: {} })
  } else emit('change', { type: 'close-tabs-active', targetLeafId: null })
}

function onView(e) {
  if (!viewCmd.value) return
  const viewKey = e.target.value
  const d = getView(viewKey)
  emit('change', {
    ...viewCmd.value,
    viewKey,
    instanceKey: d?.mode === 'multi' ? keyList.value[0]?.key ?? null : null,
  })
}

function onKey(e) {
  if (!viewCmd.value) return
  emit('change', { ...viewCmd.value, instanceKey: e.target.value || null })
}

function onTitle(e) {
  if (!viewCmd.value || viewCmd.value.type !== 'open-dialog') return
  emit('change', { ...viewCmd.value, title: e.target.value })
}

function onTarget(e) {
  if (!props.model) return
  emit('change', { ...props.model, targetLeafId: e.target.value || null })
}

/** 「发布 MQ 动作」：改主题 */
function onTopic(e) {
  if (!props.model || props.model.type !== 'publish-mq') return
  emit('change', { ...props.model, topic: e.target.value })
}

/** 「发布 MQ 动作」：改载荷（JSON 文本；非法则本地标红、不发新值） */
function onPayload(e) {
  if (!props.model || props.model.type !== 'publish-mq') return
  const raw = e.target.value
  payloadDraft.value = raw
  const text = String(raw || '').trim()
  if (text === '') {
    payloadInvalid.value = false
    emit('change', { ...props.model, payload: {} })
    return
  }
  try {
    const parsed = JSON.parse(text)
    payloadInvalid.value = false
    emit('change', { ...props.model, payload: parsed })
  } catch (_) {
    payloadInvalid.value = true
  }
}

function onDrawer(e) {
  if (!props.model) return
  emit('change', { ...props.model, drawerId: e.target.value })
}

function onDrawerView(e) {
  if (!props.model) return
  const viewKey = e.target.value
  const d = getView(viewKey)
  emit('change', {
    ...props.model,
    viewKey: viewKey || null,
    instanceKey: d?.mode === 'multi' ? keyList.value[0]?.key ?? null : null,
  })
}

function onDrawerKey(e) {
  if (!props.model) return
  emit('change', { ...props.model, instanceKey: e.target.value || null })
}
</script>

<style scoped>
.cmd-editor {
  border: 1px solid var(--lf-border);
  border-radius: 8px;
  padding: 8px;
  background: #fbfcfe;
}

.ce-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.ce-row > label {
  width: 120px;
  flex: none;
  font-size: 12px;
  color: var(--lf-text-weak);
}

.ce-row select,
.ce-row input {
  flex: 1;
  min-width: 0;
  height: 25px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  padding: 0 6px;
  background: #fff;
}

.ce-tip {
  font-size: 11px;
  color: var(--lf-text-weak);
  margin: 4px 0 0;
  line-height: 1.5;
}

.ce-tip.warn {
  color: #b23c00;
}
</style>
