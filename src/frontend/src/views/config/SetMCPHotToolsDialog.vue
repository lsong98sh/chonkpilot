<!--
  「设置高频工具」弹窗（2026-09-26 用户口径）：入口 = MCP 编辑对话框「运行信息」页签
  「高频工具」行的【设置】按钮（原逗号分隔文本框已摘除）。

  数据源 = 既有 `tools-list`（**零新增消息面**）：每项 `_meta.server` = { alias, node, category,
  description[, url] }，用它把工具归属到当前 MCP server（标识 = 该记录 `name`，匹配
  `_meta.server.alias === name` 或 `_meta.server.node === name`）。

  **写库值 = 工具原名（契约名）**：gateway `registry.isHot` 以「下游原名」比对 `HotTools`
  （`"*"` = 该 server 全部 hot），而 `tools-list` 的 `name` 是**暴露名**（默认
  `<节点 ID>_<原名>`，见 gateway `registry.applyPrefix`）→ 勾选保存时必须用
  `stripToolPrefix(暴露名, _meta.server)` 反解回原名（本文件 `loadTools` 内完成）。

  交互：本弹窗只是**草稿**——「确定」把结果回填主对话框（`confirm` 事件），由主对话框
  「保存」时才随 usr `mcps` 落库（保持既有「保存才落库」的时机不变）。
-->
<template>
  <div class="hot-tools-body" :data-loading="loading ? '1' : '0'">
    <div class="hot-tools-hint">{{ $t('config.mcp.hotToolsHint') }}</div>

    <label class="hot-all">
      <input
        type="checkbox"
        class="hot-all-cb"
        data-hot-all
        :checked="allHot"
        @change="onToggleAll"
      />
      <span>{{ $t('config.mcp.hotToolsAllLabel') }}</span>
    </label>

    <div v-if="loading" class="hot-tools-empty">{{ $t('config.mcp.hotToolsLoading') }}</div>
    <div v-else-if="!tools.length" class="hot-tools-empty">{{ $t('config.mcp.hotToolsEmpty') }}</div>
    <div v-else class="hot-tools-list">
      <label
        v-for="row in tools"
        :key="row.name"
        class="hot-tool-item"
        :data-tool="row.name"
      >
        <input
          type="checkbox"
          class="hot-tool-cb"
          :checked="allHot || selected.has(row.orig)"
          :disabled="allHot"
          @change="onToggleOne(row)"
        />
        <span class="hot-tool-name" :title="row.name">{{ row.orig }}</span>
        <span v-if="row.description" class="hot-tool-desc" :title="row.description">{{ row.description }}</span>
      </label>
    </div>

    <div class="hot-tools-footer">
      <Button size="small" data-hot-cancel @click="$emit('cancel')">{{ $t('common.cancel') }}</Button>
      <Button size="small" type="primary" data-hot-confirm @click="handleConfirm">{{ $t('common.ok') }}</Button>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { Button } from '../../components/ui'
import mq from '../../utils/mq'
import { stripToolPrefix } from '../../utils/toolSource'

const props = defineProps({
  // 当前 MCP server 标识（usr `mcps` 记录 `name`）；空 = 尚未命名 → 无工具可列
  serverName: { type: String, default: '' },
  // 当前 hot_tools（含 "*" = 全部 hot）
  hotTools: { type: Array, default: () => [] },
})

const emit = defineEmits(['confirm', 'cancel'])

const loading = ref(false)
// [{ name: 暴露名（data-tool/title）, orig: 原名（展示 + 写库值）, description }]
const tools = ref([])
const allHot = ref((props.hotTools || []).includes('*'))
// 逐项选择集 = 原名集合（"*" 由 allHot 单独表达）
const selected = ref(new Set((props.hotTools || []).filter((n) => n && n !== '*')))

async function loadTools() {
  loading.value = true
  try {
    const env = await mq.emit('tools-list', {})
    const res = env && env.backend && env.backend.result
    const list = res && Array.isArray(res.tools) ? res.tools : []
    const name = (props.serverName || '').trim()
    const rows = []
    if (name) {
      for (const tl of list) {
        const meta = (tl && tl._meta) || {}
        const srv = meta.server || {}
        if (srv.alias !== name && srv.node !== name) continue
        // 暴露名 → 原名（写库值）：默认前缀 <节点 ID>_ 可直接剥离；自定义 Namespace 时
        // 前缀不匹配 → stripToolPrefix 原样返回（此时网关 isHot 亦无法命中，宁缺勿错）。
        rows.push({
          name: tl.name,
          orig: stripToolPrefix(tl.name, srv),
          description: tl.description || '',
        })
      }
    }
    tools.value = rows
  } catch (e) {
    console.warn('[SetMCPHotToolsDialog] load tools failed:', e)
    tools.value = []
  } finally {
    loading.value = false
  }
}

function onToggleAll() {
  allHot.value = !allHot.value
}

function onToggleOne(row) {
  if (allHot.value) return
  const next = new Set(selected.value)
  if (next.has(row.orig)) next.delete(row.orig)
  else next.add(row.orig)
  selected.value = next
}

function handleConfirm() {
  const list = allHot.value
    ? ['*']
    : tools.value.filter((r) => selected.value.has(r.orig)).map((r) => r.orig)
  emit('confirm', list)
}

onMounted(loadTools)
</script>

<style scoped>
.hot-tools-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: 12px 16px 0;
}
.hot-tools-hint {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-muted);
  margin-bottom: 10px;
}
.hot-all {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  padding: 6px 0;
  cursor: pointer;
  border-bottom: 1px solid var(--border);
}
.hot-tools-list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 4px 0;
}
.hot-tool-item {
  display: flex;
  align-items: baseline;
  gap: 8px;
  padding: 5px 0;
  font-size: 13px;
  cursor: pointer;
}
.hot-tool-item:hover {
  background: var(--bg-hover);
}
.hot-tool-name {
  color: var(--text-primary);
  flex-shrink: 0;
}
.hot-tool-desc {
  color: var(--text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.hot-tools-empty {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px 0;
  font-size: 13px;
  color: var(--text-muted);
}
.hot-tools-footer {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 0;
}
</style>
