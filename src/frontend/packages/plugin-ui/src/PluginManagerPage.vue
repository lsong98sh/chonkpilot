<template>
  <div class="plugin-manager">
    <div class="pm-head">
      <div class="pm-title">
        <Icon name="tool" :size="18" />
        <span>{{ $t('layoutFree.plugin.manager') }}</span>
        <span class="pm-count">{{ state.plugins.length }}</span>
      </div>
      <Button text @click="refresh">{{ $t('layoutFree.plugin.refresh') }}</Button>
    </div>

    <div v-if="!state.ready" class="pm-empty">{{ $t('common.loading') }}</div>
    <div v-else-if="!state.plugins.length" class="pm-empty">{{ $t('layoutFree.plugin.empty') }}</div>

    <!-- 插件维护卡片：名称 / 启动命令 / 说明 / 启用开关 / 配置 + 插件注册的动作按钮 -->
    <div v-else class="pm-grid">
      <div v-for="p in state.plugins" :key="p.id" class="pm-card" :class="{ 'is-off': p.enabled === false }">
        <div class="pm-card-head">
          <Icon :name="p.icon || 'tool'" :size="18" class="pm-card-icon" />
          <div class="pm-card-title">
            <div class="pm-name" :title="p.id">{{ p.name || p.id }}</div>
            <div class="pm-meta">{{ p.id }} · v{{ p.version }}</div>
          </div>
          <span class="pm-dot" :class="'is-' + p.status" :title="statusText(p)" />
        </div>

        <div class="pm-desc" :title="p.description">{{ p.description || '—' }}</div>

        <div class="pm-cmd">
          <span class="pm-cmd-label">{{ $t('layoutFree.plugin.command') }}</span>
          <code class="pm-cmd-value" :title="p.command">{{ p.command || '—' }}</code>
        </div>

        <div class="pm-status">
          <span class="pm-status-text">{{ statusText(p) }}</span>
          <span v-if="p.detail" class="pm-status-detail" :title="p.detail">{{ p.detail }}</span>
        </div>

        <div class="pm-actions">
          <label class="pm-switch">
            <Switch
              :model-value="p.enabled !== false"
              :disabled="!!pending[p.id]"
              @update:model-value="(v) => toggle(p, v)"
            />
            <span>{{ p.enabled !== false ? $t('layoutFree.plugin.enable') : $t('layoutFree.plugin.disable') }}</span>
          </label>

          <div class="pm-btns">
            <!-- 配置按钮：仅当插件注册了配置视图（ui.views 中 id=config） -->
            <button
              v-if="p.config_view"
              class="pm-btn"
              :style="buttonStyle('icon-text')"
              :title="$t('layoutFree.plugin.config')"
              @click="openView(p, p.config_view, 'preview')"
            >
              <Icon name="setting" :size="14" />
              <span>{{ $t('layoutFree.plugin.config') }}</span>
            </button>

            <!-- 插件注册的动作按钮（名称 / 图标 / 命令 / 打开容器均由 manifest 决定） -->
            <button
              v-for="a in p.actions || []"
              :key="a.id"
              class="pm-btn"
              :style="buttonStyle(a.style)"
              :title="a.tooltip || a.label"
              @click="runAction(p, a)"
            >
              <span v-if="isSvg(a.icon)" class="pm-btn-svg" v-html="a.icon" />
              <Icon v-else-if="a.icon" :name="a.icon" :size="14" />
              <span v-if="a.style !== 'icon'">{{ a.label }}</span>
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { reactive } from 'vue'
import { pluginHost } from './host.js'
import { pluginUIComponents } from './components.js'
import { usePlugins } from './usePlugins.js'

// 宿主端口适配：mq 由依赖注入，设计系统组件由宿主注册。
const mq = {
  on: (...args) => pluginHost().mq.on(...args),
  emit: (...args) => pluginHost().mq.emit(...args),
}
const { Button, Switch, Icon } = pluginUIComponents()

const { state, refresh } = usePlugins()
const pending = reactive({})

const STATUS_TEXT = {
  running: '运行中',
  starting: '启动中',
  registering: '注册中',
  degraded: '降级',
  error: '失败',
  stopped: '已停止',
  disabled: '已禁用',
}

function statusText(p) {
  return STATUS_TEXT[p.status] || p.status || '—'
}

function isSvg(icon) {
  return typeof icon === 'string' && icon.trim().startsWith('<svg')
}

/** 按钮外观：style = icon | icon-text | text（与布局栏按钮同口径） */
function buttonStyle(style) {
  return style === 'icon' ? 'padding:4px 6px' : ''
}

async function toggle(p, enabled) {
  pending[p.id] = true
  try {
    await mq.emit('plugin-set-enabled', { id: p.id, enabled })
    await refresh()
  } finally {
    pending[p.id] = false
  }
}

/** 便捷式动作：按注册的 container 打开已注册视图（preview / tab / drawer / dialog）。 */
function openView(p, viewId, container) {
  const view = (p.views || []).find((v) => v.id === viewId)
  const viewKey = `plugin:${p.id}:${viewId}`
  const title = view?.title || p.name || p.id
  const where = container || (Array.isArray(view?.containers) ? view.containers[0] : '') || 'preview'
  switch (where) {
    case 'tab':
      mq.emit('lf-layout-open-tabs', { targetLeafId: null, viewKey, instanceKey: null })
      return
    case 'drawer':
      mq.emit('lf-layout-open-drawer', {
        drawerId: `plugin_${p.id}_${viewId}`,
        viewKey,
        instanceKey: null,
        label: title,
        icon: view?.icon || p.icon || '',
      })
      return
    case 'dialog':
      mq.emit('lf-layout-open-dialog', { viewKey, instanceKey: null, title })
      return
    default:
      mq.emit('preview-tab-open', { kind: 'plugin-page', plugin: p.id, page: viewId, title })
  }
}

/** 动作执行：命令式（manifest.command）优先，否则按 view + container 打开。 */
function runAction(p, a) {
  if (a?.command && a.command.type) {
    const cmd = JSON.parse(JSON.stringify(a.command))
    // 约定：plugin-command 的载荷缺 id 时补当前插件 id（插件无需重复写自身 id）。
    if (cmd.type === 'publish-mq' && cmd.topic === 'plugin-command') {
      cmd.payload = cmd.payload || {}
      if (!cmd.payload.id) cmd.payload.id = p.id
    }
    dispatchCommand(cmd)
    return
  }
  if (a?.view) openView(p, a.view, a.container)
}

/**
 * 命令 → MQ（与布局栏按钮同一套命令对象语义，见 layout-free/utils/commands.js `commandToMq`）：
 * open-dialog / open-tabs / open-drawer / toggle-drawer / close-tabs-active / publish-mq。
 * 这里就地转换（不依赖布局 store），插件维护页在任意容器里都能执行动作。
 */
function dispatchCommand(cmd) {
  switch (cmd.type) {
    case 'open-dialog':
      mq.emit('lf-layout-open-dialog', {
        viewKey: cmd.viewKey,
        instanceKey: cmd.instanceKey ?? null,
        title: cmd.title || '',
      })
      return
    case 'open-tabs':
      mq.emit('lf-layout-open-tabs', {
        targetLeafId: cmd.targetLeafId ?? null,
        viewKey: cmd.viewKey,
        instanceKey: cmd.instanceKey ?? null,
      })
      return
    case 'open-drawer':
      mq.emit('lf-layout-open-drawer', {
        drawerId: cmd.drawerId,
        viewKey: cmd.viewKey ?? null,
        instanceKey: cmd.instanceKey ?? null,
      })
      return
    case 'toggle-drawer':
      mq.emit('lf-layout-toggle-drawer', { drawerId: cmd.drawerId })
      return
    case 'close-tabs-active':
      mq.emit('lf-layout-close-tabs-active', { targetLeafId: cmd.targetLeafId ?? null })
      return
    case 'publish-mq':
      if (cmd.topic) mq.emit(cmd.topic, cmd.payload ?? {})
  }
}
</script>

<style scoped>
.plugin-manager {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  padding: 12px 16px;
  gap: 10px;
  overflow: hidden;
}
.pm-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: none;
}
.pm-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 14px;
  font-weight: 600;
}
.pm-count {
  font-size: 11px;
  font-weight: 400;
  color: var(--text-muted);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 0 6px;
}
.pm-empty {
  padding: 24px;
  color: var(--text-muted);
  font-size: 13px;
}

/* ── 卡片网格 ── */
.pm-grid {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));
  gap: 12px;
  align-content: start;
  padding-bottom: 8px;
}
.pm-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: var(--bg-panel, var(--bg));
  min-width: 0;
}
.pm-card.is-off {
  opacity: 0.72;
}
.pm-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.pm-card-icon {
  flex: none;
  color: var(--accent);
}
.pm-card-title {
  flex: 1;
  min-width: 0;
}
.pm-name {
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pm-meta {
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.pm-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--text-muted);
  flex: none;
}
.pm-dot.is-running { background: #22c55e; }
.pm-dot.is-starting, .pm-dot.is-registering, .pm-dot.is-degraded { background: #f59e0b; }
.pm-dot.is-error { background: #ef4444; }
.pm-dot.is-disabled, .pm-dot.is-stopped { background: #9ca3af; }

.pm-desc {
  font-size: 12px;
  line-height: 1.5;
  color: var(--text-secondary, var(--text-muted));
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.pm-cmd {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.pm-cmd-label {
  flex: none;
  font-size: 11px;
  color: var(--text-muted);
}
.pm-cmd-value {
  flex: 1;
  min-width: 0;
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 4px;
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pm-status {
  display: flex;
  align-items: baseline;
  gap: 6px;
  min-width: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.pm-status-detail {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pm-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-top: 2px;
  padding-top: 8px;
  border-top: 1px solid var(--border);
}
.pm-switch {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--text-muted);
  cursor: pointer;
  white-space: nowrap;
}
.pm-btns {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  justify-content: flex-end;
}
.pm-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 26px;
  padding: 0 8px;
  font-size: 12px;
  color: var(--text);
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 6px;
  cursor: pointer;
}
.pm-btn:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.05));
  border-color: var(--accent);
}
.pm-btn-svg {
  display: inline-flex;
  width: 14px;
  height: 14px;
}
.pm-btn-svg :deep(svg) {
  width: 14px;
  height: 14px;
}
</style>
