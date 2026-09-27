<!--
  工具沙箱配置页（usr 级；设置菜单 → preview tab kind = settings-tool-sandbox）

  需求（用户口径，[42 §2 (104)/(109)]，2026-09-26 改为 **executor 级**）：沙箱不是工具级，
  而是 **self 的执行器级** —— 三个 executor（core / desktop / browser，= 契约
  `_meta.category`）各一个开关，管制该执行器下**全部工具**；开关写 usr 自由键 `tool_sandbox`
  （`{"core":bool,"desktop":bool,"browser":bool}`，未设置 = 不隔离，默认兼容）。

  仅 **self 的 executor 工具**（本仓 spawn 子进程执行）可隔离；第三方 stdio&spawn 的 MCP 的沙箱
  在 MCP 对话框「运行信息」页签（usr `mcps[].sandbox`）设置，http/sse（及非 spawn 的 stdio）无法施加沙箱。

  手动保存：拨动 Switch / 「恢复未设置」只改本地待保存态（显示「未保存」），点【保存】才落库；
  **无改动时保存按钮禁用**。

  读写面（**零新增 MQ 主题**）与三态语义见 composables/useToolSandbox.js。
-->
<template>
  <div class="settings-page">
    <div class="page-body">
      <div class="tool-toolbar">
        <span class="hint">{{ $t('config.toolSandbox.pageHint') }}</span>
        <span v-if="dirty" class="unsaved-mark" data-sandbox-unsaved>{{ $t('config.feedback.unsaved') }}</span>
        <Button size="small" :loading="loading" @click="reload">
          <Icon name="refresh" :size="13" /> {{ $t('config.page.redetect') }}
        </Button>
        <Button
          type="primary"
          size="small"
          data-sandbox-save
          :disabled="!dirty"
          :loading="saving"
          @click="onSave"
        >{{ $t('common.save') }}</Button>
      </div>

      <!-- 两页状态互见：server 级（mcps[].sandbox，仅 stdio）已开启数 + 一键跳转 -->
      <div class="cross-hint">
        <span>{{ $t('config.toolSandbox.serverSummary', { n: serverSandboxOn }) }}</span>
        <Button text size="small" @click="gotoMcp">{{ $t('config.toolSandbox.gotoMcp') }}</Button>
      </div>

      <!-- 边界说明：沙箱只对 self 的 executor 工具生效；第三方 MCP 沙箱在 MCP 对话框设置，
           http/sse（及非 spawn 的 stdio）无法施加沙箱。 -->
      <div class="exec-note">
        <span class="exec-note-text">{{ $t('config.toolSandbox.thirdPartyHint') }}</span>
        <Button text size="small" @click="gotoMcp">{{ $t('config.toolSandbox.gotoMcp') }}</Button>
      </div>

      <!-- ② 显著内联预警（非 toast）：已开启沙箱但信任目录为空 → agentbox 空允许集 = 全拒，
           已开启 executor 的**所有文件操作都会被拒绝**；按钮直达「可读/可写目录」配置。
           目录配置后（data-prj-security-refresh）警告即时消失（见 onMounted 订阅）。 -->
      <div v-if="trustWarning" class="trust-warn" role="alert">
        <span class="trust-warn-text">{{ $t('config.toolSandbox.trustDirWarning') }}</span>
        <Button size="small" type="primary" @click="gotoTrustDirs">{{ $t('config.toolSandbox.gotoTrustDirs') }}</Button>
      </div>

      <div v-if="!totalTools" class="tool-empty">{{ $t('config.toolSandbox.empty') }}</div>

      <div class="exec-list">
        <div v-for="row in rows" :key="row.category" class="exec-row" :data-exec="row.category">
          <div class="exec-row-head">
            <span class="exec-name">{{ execLabel(row.category) }}</span>
            <span class="exec-cat mono">（{{ row.category }}）</span>

            <div class="exec-switch" :title="$t('config.toolSandbox.switchHint')">
              <Switch
                :model-value="row.on"
                :aria-label="$t('config.toolSandbox.switchHint') + ': ' + row.category"
                @update:model-value="(v) => onToggle(row, v)"
              />
            </div>

            <span class="exec-state">{{ stateText(row) }}</span>
            <!-- ② 对应行的内联警告：仅在该行已开启且信任目录为空时显示 -->
            <span v-if="trustWarning && row.on" class="exec-trust-warn">{{ $t('config.toolSandbox.trustDirWarningRow') }}</span>

            <div class="exec-actions">
              <Button text type="danger" :disabled="!row.userSet" @click="onRestore(row)">
                {{ $t('config.toolSandbox.restoreDefault') }}
              </Button>
            </div>
          </div>

          <!-- 只读：该 executor 支持的工具（数据源 = tools-list 的 _meta.category；
               展示剥前缀，:title = 完整暴露名） -->
          <div class="exec-tools">
            <span v-if="!row.tools.length" class="exec-none">{{ $t('config.toolSandbox.noTools') }}</span>
            <template v-else>
              <span class="exec-tools-label">{{ $t('config.toolSandbox.supportedTools') }}:</span>
              <span
                v-for="t in row.tools"
                :key="t.name"
                class="exec-tool mono"
                :title="t.name"
                :data-tool="t.name"
              >{{ stripToolPrefix(t.name, t.server) }}</span>
            </template>
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
const { loading, saving, rows, totalTools, dirty, serverSandboxOn, trustWarning, loadTrustDirs, reload, setSandbox, restore, save } = useToolSandbox()

// executor 显示名（新增 i18n；回退到类别名本身）
function execLabel(category) {
  const key = 'config.toolSandbox.exec' + category.charAt(0).toUpperCase() + category.slice(1)
  return t(key)
}

// 「两页状态互见」跳转 / 第三方沙箱入口：既有 preview tab 通道（kind=settings-mcp），零新增消息面。
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

// 拨动 Switch = 只改本地待保存态（不落库、不弹提示；点【保存】才写）
function onToggle(row, v) {
  setSandbox(row.category, v)
}

// 「恢复未设置」= 从待保存态移除该类别（不落库；点【保存】才写）
function onRestore(row) {
  restore(row.category)
}

// 手动保存：无改动按钮已禁用（此处兜底）；成功给一次明确反馈。
async function onSave() {
  if (!dirty.value) return
  try {
    const wrote = await save()
    if (wrote) message.success(t('config.toolSandbox.saved'))
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
  /* 两轴滚动都由页面正文承担（2026-09-27 用户口径，与工具异步页同范式）：
     横向滚动条贴 preview 区底部，而不是滚到内容底边才见到。 */
  overflow: auto;
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
  color: var(--fg-secondary);
  flex: 1;
}
.unsaved-mark {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--warning, #e6a23c);
}
.cross-hint {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--fg-secondary);
}
.exec-note {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--fg-secondary);
}
.exec-note-text {
  flex: 1;
  line-height: 1.6;
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
.tool-empty {
  font-size: 13px;
  color: var(--text-muted);
  padding: 12px 4px;
}
.exec-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.exec-row {
  border: 1px solid var(--border);
  border-radius: var(--border-radius);
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.exec-row-head {
  display: flex;
  align-items: center;
  gap: 10px;
}
.exec-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text-primary);
}
.exec-cat {
  font-size: 11px;
  color: var(--text-muted);
}
.exec-switch {
  flex-shrink: 0;
}
.exec-state {
  width: 56px;
  flex-shrink: 0;
  font-size: 11px;
  color: var(--text-muted);
}
.exec-trust-warn {
  flex: 1;
  font-size: 11px;
  line-height: 1.5;
  color: var(--danger, #dc3545);
}
.exec-actions {
  width: 110px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
}
.exec-tools {
  display: flex;
  align-items: baseline;
  flex-wrap: wrap;
  gap: 4px 8px;
  padding: 4px 0 2px;
  border-top: 1px dashed var(--border);
}
.exec-tools-label {
  font-size: 11px;
  color: var(--text-muted);
}
.exec-tool {
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--bg-subtle, rgba(127, 127, 127, 0.08));
  border-radius: 3px;
  padding: 1px 5px;
}
.exec-none {
  font-size: 11px;
  color: var(--text-muted);
}
.mono {
  font-family: var(--font-mono, Consolas, monospace);
}
</style>
