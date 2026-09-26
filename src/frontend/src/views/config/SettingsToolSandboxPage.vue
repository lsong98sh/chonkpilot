<!--
  工具沙箱配置页（usr 级；设置菜单 → preview tab kind = settings-tool-sandbox）

  需求（用户口径，[42 §2 (104)/(109)]）：新增一个页签，列出**扫描到的 runtime 工具**
  （chonkpilot-mcp-tools 的 core / desktop / browser 执行器工具，由 tools-list 的
  `_meta.category` 判定），逐个选择**是否隔离**；开关写 usr 自由键 `tool_sandbox`
  （未设置 = 不隔离，默认兼容）。

  读写面（**零新增 MQ 主题**）与三态语义见 composables/useToolSandbox.js。
  展示风格与「工具异步配置」页一致：按 MCP（`_meta.server`）分组。
-->
<template>
  <div class="settings-page">
    <div class="page-body">
      <div class="tool-toolbar">
        <span class="hint">{{ $t('config.toolSandbox.pageHint') }}</span>
        <Button size="small" :loading="loading" @click="reload">
          <Icon name="refresh" :size="13" /> {{ $t('config.page.redetect') }}
        </Button>
      </div>

      <!-- 两页状态互见：server 级（mcps[].sandbox，仅 stdio）已开启数 + 一键跳转 -->
      <div class="cross-hint">
        <span>{{ $t('config.toolSandbox.serverSummary', { n: serverSandboxOn }) }}</span>
        <Button text size="small" @click="gotoMcp">{{ $t('config.toolSandbox.gotoMcp') }}</Button>
      </div>

      <!-- ② 显著内联预警（非 toast）：已开启沙箱但信任目录为空 → agentbox 空允许集 = 全拒，
           本页已开启的工具其**所有文件操作都会被拒绝**；按钮直达「可读/可写目录」配置。
           目录配置后（data-prj-security-refresh）警告即时消失（见 onMounted 订阅）。 -->
      <div v-if="trustWarning" class="trust-warn" role="alert">
        <span class="trust-warn-text">{{ $t('config.toolSandbox.trustDirWarning') }}</span>
        <Button size="small" type="primary" @click="gotoTrustDirs">{{ $t('config.toolSandbox.gotoTrustDirs') }}</Button>
      </div>

      <div v-if="!groups.length" class="tool-empty">{{ $t('config.toolSandbox.empty') }}</div>

      <div v-for="g in groups" :key="g.key" class="tool-group">
        <div class="group-title">{{ g.key }}<span class="group-count">（{{ g.tools.length }}）</span></div>

        <div v-for="row in g.tools" :key="row.name" class="tool-item" :data-tool="row.name">
          <div class="tool-row">
            <div class="tool-name" :title="row.name">
              <span class="mono">{{ stripToolPrefix(row.name, row.server) }}</span>
              <span class="tool-desc">{{ row.description }}</span>
              <!-- ② 对应行的内联警告：仅在该行已开启且信任目录为空时显示 -->
              <span v-if="trustWarning && row.on" class="tool-trust-warn">{{ $t('config.toolSandbox.trustDirWarningRow') }}</span>
            </div>

            <span class="tool-cat" :title="$t('config.toolSandbox.category') + ': ' + row.category">{{ row.category }}</span>

            <div class="tool-switch" :title="row.thirdParty === true ? $t('config.toolSandbox.thirdPartyNotApplicable') : $t('config.toolSandbox.switchHint')">
              <Switch
                :model-value="row.on"
                :disabled="row.thirdParty === true"
                :aria-label="$t('config.toolSandbox.switchHint') + ': ' + row.name"
                @update:model-value="(v) => onToggle(row, v)"
              />
            </div>

            <span class="tool-state">{{ stateText(row) }}</span>
            <!-- I-109 边界：第三方（spawned/proxied）工具在第三方进程内执行 → agentbox 不可注入 -->
            <span
              v-if="row.thirdParty === true"
              class="tool-na"
              :title="$t('config.toolSandbox.thirdPartyNotApplicable')"
            >{{ $t('config.toolSandbox.naBadge') }}</span>

            <div class="tool-actions">
              <Button text type="danger" :disabled="!row.userSet" @click="onRestore(row)">
                {{ $t('config.toolSandbox.restoreDefault') }}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Button, Switch, message } from '../../components/ui'
import Icon from '../../components/icon/Icon.vue'
import { useToolSandbox } from '../../composables/useToolSandbox'
import { stripToolPrefix } from '../../utils/toolSource'
import { saveFailedText } from '../../utils/settingsFeedback'
import { onDataRefresh } from '../../utils/dataClient'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'

const { t } = useI18n()
const { loading, groups, serverSandboxOn, trustWarning, loadTrustDirs, reload, setSandbox, restore } = useToolSandbox()

// 「两页状态互见」跳转：既有 preview tab 通道（kind=settings-mcp），零新增消息面。
function gotoMcp() {
  mq.emit(EventNames.previewTabOpen, { kind: 'settings-mcp' })
}

// ② 直达「可读/可写目录」配置：既有 preview tab 通道（kind=settings-project 默认落在「安全」页签）
function gotoTrustDirs() {
  mq.emit(EventNames.previewTabOpen, { kind: 'settings-project' })
}

// 初始加载 + 订阅既有 prj-security 变更广播：配置信任目录后警告即时消失（无 watch）
const unsubs = []
onMounted(() => {
  reload()
  unsubs.push(onDataRefresh('prj-security', loadTrustDirs))
})
onUnmounted(() => unsubs.forEach(fn => fn()))

// 三态展示：开 / 关（显式 false）/ 未设置（缺键）
function stateText(row) {
  if (!row.userSet) return t('config.toolSandbox.stateUnset')
  return row.on ? t('config.toolSandbox.stateOn') : t('config.toolSandbox.stateOff')
}

async function onToggle(row, v) {
  // I-109：第三方工具不可施加（开关已禁用，此处兜底防误写）
  if (row.thirdParty === true) return
  try {
    await setSandbox(row, v)
    message.success(t('config.toolSandbox.saved'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}

async function onRestore(row) {
  try {
    await restore(row)
    message.success(t('config.toolSandbox.restored'))
  } catch (e) {
    message.error(saveFailedText(t, e))
  }
}
</script>

<style scoped>
.settings-page {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.page-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 8px 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.tool-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
}
.hint {
  font-size: 12px;
  color: var(--text-muted);
  flex: 1;
}
.cross-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--text-muted);
}
/* ② 空信任目录显著内联预警（常驻，非一闪而过的 toast） */
.trust-warn {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: var(--border-radius);
  background: var(--warning-bg, rgba(230, 162, 60, 0.12));
  border: 1px solid var(--warning-border, var(--warning, #e6a23c));
  flex-shrink: 0;
}
.trust-warn-text {
  flex: 1;
  font-size: 12px;
  line-height: 1.6;
  color: var(--text-primary);
}
.tool-trust-warn {
  font-size: 11px;
  line-height: 1.5;
  color: var(--danger, #dc3545);
}
.tool-empty {
  font-size: 13px;
  color: var(--text-muted);
  padding: 12px 4px;
}
.tool-group {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 6px 10px 10px;
}
.group-title {
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
  padding: 4px 0 8px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 8px;
}
.group-count {
  font-weight: 400;
  color: var(--text-muted);
}
.tool-item + .tool-item {
  border-top: 1px dashed var(--border);
}
.tool-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
}
.tool-name {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.mono {
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 12px;
  color: var(--text-primary);
  word-break: break-all;
}
.tool-desc {
  font-size: 11px;
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tool-cat {
  width: 70px;
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.tool-switch {
  flex-shrink: 0;
}
.tool-state {
  width: 56px;
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.tool-na {
  flex-shrink: 0;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 3px;
  background: var(--warning, #e6a23c);
  color: #fff;
  cursor: help;
}
.tool-actions {
  width: 110px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
}
</style>
