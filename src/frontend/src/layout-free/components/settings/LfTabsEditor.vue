<template>
  <div class="tabs-editor">
    <div class="te-head">
      <b>{{ title }}</b>
      <span class="count">共 {{ model.length }} 个</span>
      <span class="grow" />
      <button class="lf-btn" :disabled="!hostNodeId" v-mq:[EventNames.containerClick].click="add">＋ 添加</button>
    </div>

    <div v-if="!model.length" class="te-empty">{{ emptyText }}</div>

    <div v-for="(tab, i) in model" :key="i" class="te-row">
      <span class="idx">{{ i + 1 }}</span>
      <select :value="tab.viewKey" @change="onView(i, $event)">
        <option
          v-for="v in optionsFor(i)"
          :key="v.key"
          :value="v.key"
          :disabled="v.disabled && v.key !== tab.viewKey"
        >
          {{ v.label }}{{ v.disabled && v.key !== tab.viewKey ? v.takenLabel : '' }}
        </option>
      </select>
      <select
        v-if="isMulti(tab.viewKey)"
        :value="tab.instanceKey ?? ''"
        title="多实例 view 需要配套 key"
        @change="onKey(i, $event)"
      >
        <option value="">不使用 key</option>
        <option v-for="k in keyList" :key="k.key" :value="k.key">
          {{ k.label }}（{{ k.key }} = {{ k.value }}）
        </option>
      </select>
      <span v-else class="single">单例</span>
      <input type="text" :value="tab.label ?? ''" placeholder="标签文字（可选）" @input="onLabel(i, $event)" />
      <button class="mini" title="上移" :disabled="i === 0" v-mq:[EventNames.containerClick].click="() => move(i, -1)">↑</button>
      <button
        class="mini"
        title="下移"
        :disabled="i === model.length - 1"
        v-mq:[EventNames.containerClick].click="() => move(i, 1)"
      >
        ↓
      </button>
      <button class="mini danger" title="删除" v-mq:[EventNames.containerClick].click="() => remove(i)">✕</button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { getView } from '../../config/views'
import { useKeysStore } from '../../stores/keys'
import { useConfigStore } from '../../stores/config'
import { EventNames } from '../../events/event-names'
import { getMq } from '../../deps.js'

/**
 * 标签页编辑器（区域 与 抽屉 共用）。
 *
 * ⚠️ 交互是「父级持有数组」模式：本组件算出**改完之后的完整数组**，
 * 先发一条标签页主题（带 owner 信息，供外部观察 / 统一分发），再 `@change` 交给父级落库。
 * owner = `{ type: 'area' | 'drawer', id }`，与 mqCommands 里的宿主分发一致。
 */

const props = defineProps({
  /** 标签页配置数组 [{ viewKey, instanceKey, label }] */
  model: { type: Array, required: true },
  /** 宿主 view key（排除自身，避免嵌套） */
  hostKey: { type: String, default: '' },
  /** 该配置所属节点 id（区域场景） */
  hostNodeId: { type: String, default: '' },
  /** 宿主标识：{ type: 'area' | 'drawer', id } —— 标签页主题按它分发 */
  owner: { type: Object, default: null },
  /** 避免循环嵌套；抽屉 / 自由 tabs 内容允许任意 view */
  allowedAny: { type: Boolean, default: false },
  /**
   * 占用登记：slotKey 前缀 + 位置。
   * 形如 { prefix: 'area:leaf_x', label: '区域「文件」' } → 第 i 个标签页的 slot = `${prefix}#${i}`
   */
  slot: { type: Object, default: null },
  title: { type: String, default: '标签页配置' },
  emptyText: { type: String, default: '尚未配置内容。' },
})

const emit = defineEmits(['change'])

const keys = useKeysStore()
const config = useConfigStore()

const keyList = computed(() => keys.list)

/** 单例占用：每个标签页自己的 slot（自己不算占用） */
function slotKeyOf(index) {
  if (!props.slot?.prefix) return ''
  return `${props.slot.prefix}#${index}`
}

function optionsFor(index) {
  const exclude = props.allowedAny ? [] : [props.hostKey]
  return config.viewOptions(slotKeyOf(index), exclude)
}

function isMulti(viewKey) {
  return getView(viewKey)?.mode === 'multi'
}

/** 宿主信息（标签页主题按它分发；抽屉场景 ownerId 也塞进 drawerId 兼容旧写法） */
function ownerPayload() {
  const type = props.owner?.type === 'drawer' ? 'drawer' : 'area'
  const id = props.owner?.id ?? props.hostNodeId
  return type === 'drawer'
    ? { ownerType: 'drawer', ownerId: id, drawerId: id }
    : { ownerType: 'area', ownerId: id, nodeId: id }
}

/**
 * 落库：先发布标签页主题（外部可观察，也满足「交互统一走 mq」的约定），
 * 再把完整数组交给父级 —— 父级持有数组并负责写进 config / 运行时。
 *
 * payload 里带上 `tabs`（完整结果）与 `index`：订阅方优先按 `tabs` 整体写入；
 * 只有 payload 没有 `tabs` 时（外部只发了个「加一个」）才用粒度动作。
 */
function commit(topic, next, index) {
  getMq().emit(topic, { ...ownerPayload(), tabs: next, ...(index === undefined ? {} : { index }) })
  emit('change', next)
}

function add() {
  const opts = optionsFor(props.model.length)
  const first = opts.find((v) => !v.disabled && v.mode === 'singleton') ?? opts.find((v) => !v.disabled)
  if (!first) return
  const next = [
    ...props.model,
    { viewKey: first.key, instanceKey: first.mode === 'multi' ? keyList.value[0]?.key ?? null : null, label: '' },
  ]
  commit(EventNames.tabsAdd, next, next.length - 1)
}

function onView(i, e) {
  const viewKey = e.target.value
  const def = getView(viewKey)
  const next = props.model.map((t, idx) =>
    idx === i ? { ...t, viewKey, instanceKey: def?.mode === 'multi' ? keyList.value[0]?.key ?? null : null } : t,
  )
  getMq().emit(EventNames.tabsUpdate, {
    ...ownerPayload(),
    index: i,
    patch: { viewKey, instanceKey: next[i].instanceKey },
  })
  emit('change', next)
}

function onKey(i, e) {
  const instanceKey = e.target.value || null
  getMq().emit(EventNames.tabsUpdate, { ...ownerPayload(), index: i, patch: { instanceKey } })
  emit('change', props.model.map((t, idx) => (idx === i ? { ...t, instanceKey } : t)))
}

function onLabel(i, e) {
  const label = e.target.value
  getMq().emit(EventNames.tabsUpdate, { ...ownerPayload(), index: i, patch: { label } })
  emit('change', props.model.map((t, idx) => (idx === i ? { ...t, label } : t)))
}

function move(i, delta) {
  const next = [...props.model]
  const target = i + delta
  if (target < 0 || target >= next.length) return
  const [item] = next.splice(i, 1)
  next.splice(target, 0, item)
  commit(EventNames.tabsMove, next, i)
}

function remove(i) {
  const next = [...props.model]
  next.splice(i, 1)
  commit(EventNames.tabsRemove, next, i)
}
</script>

<style scoped>
.tabs-editor {
  border: 1px solid var(--lf-border);
  border-radius: 8px;
  padding: 8px;
  background: #fbfcfe;
  margin-bottom: 10px;
}

.te-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}

.te-head .count {
  font-size: 11px;
  color: var(--lf-text-weak);
}

.te-head .grow {
  flex: 1;
}

.te-empty {
  font-size: 12px;
  color: #9a6a00;
  background: #fff8e6;
  border: 1px solid #f0dca8;
  border-radius: 6px;
  padding: 6px 8px;
}

.te-row {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 4px 0;
}

.te-row select,
.te-row input {
  height: 24px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  padding: 0 6px;
  background: #fff;
  min-width: 0;
}

.te-row select:nth-of-type(1) {
  flex: 1.5;
}

.te-row select:nth-of-type(2) {
  flex: 1.2;
}

.te-row input {
  flex: 1;
}

.idx {
  width: 16px;
  font-size: 11px;
  color: var(--lf-text-weak);
  text-align: right;
  flex: none;
}

.single {
  font-size: 11px;
  color: var(--lf-text-weak);
  border: 1px dashed var(--lf-border-strong);
  border-radius: 5px;
  padding: 3px 8px;
  flex: 0 0 auto;
}

.mini {
  width: 22px;
  height: 22px;
  border: 1px solid var(--lf-border-strong);
  border-radius: 5px;
  background: #fff;
  color: var(--lf-text-weak);
  flex: none;
  line-height: 1;
  cursor: pointer;
}

.mini:hover:not(:disabled) {
  border-color: var(--lf-accent);
  color: var(--lf-accent);
}

.mini:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.mini.danger:hover {
  border-color: #d33;
  color: #d33;
}
</style>
