<!--
  代码索引详情页（STB-1，preview 功能页 `status-index`）。
  数据源 = prj 配置键（`plugins.codebase.codegraph.enabled` / `codegraph.status` /
  `plugins.codebase.vfts.enabled` / `vfts.status`；刷新 = 既有 `data-prj-config-refresh`）。
  字段口径 = 引擎 Status + 插件回写 phase/message。
  AST：引擎状态**未单独暴露 AST 阶段**（phase 仅 configure|index）→ 并入本页「索引阶段」说明，不编造。
-->
<template>
  <TaskDetailLayout
    detail="index"
    title-key="statusBar.detail.indexTitle"
    icon="collection"
    :tone="headerTone"
    :state-text="headerState"
    :settings-tab="'codegraph'"
    :error="headerError"
  >
    <section class="td-block" data-engine-detail="codegraph">
      <div class="td-block-head">
        <h4 class="td-h4">{{ $t('statusBar.detail.codegraph') }}</h4>
        <span class="td-badge" :class="'is-' + cg.tone">{{ cg.stateText }}</span>
        <span v-if="phaseText" class="td-phase">{{ phaseText }}</span>
        <span class="td-spacer"></span>
        <Button
          size="small"
          data-action="go-settings-codegraph"
          v-mq:[EventNames.projectConfigOpen].click="{ tab: 'codegraph' }"
        >{{ $t('statusBar.detail.goSettings') }}</Button>
      </div>
      <dl class="td-kv">
        <dt>{{ $t('statusBar.detail.enabled') }}</dt>
        <dd>{{ cg.enabled ? $t('statusBar.detail.yes') : $t('statusBar.detail.no') }}</dd>
        <dt>{{ $t('statusBar.detail.progress') }}</dt>
        <dd>
          <template v-if="cg.hasProgress">{{ cg.done }}/{{ cg.total }}</template>
          <template v-else>—</template>
          <div v-if="cg.hasProgress" class="td-progress">
            <div class="td-progress-fill" :style="{ width: pct(cg) + '%' }"></div>
          </div>
        </dd>
        <dt>{{ $t('statusBar.detail.files') }}</dt>
        <dd>{{ cg.files }}</dd>
        <dt>{{ $t('statusBar.detail.symbols') }}</dt>
        <dd>{{ cg.symbols }}</dd>
        <dt>{{ $t('statusBar.detail.lastIndexed') }}</dt>
        <dd>{{ statusTime(cg.lastIndexedAt) || '—' }}</dd>
        <dt>{{ $t('statusBar.detail.loaded') }}</dt>
        <dd>{{ cg.loaded ? $t('statusBar.detail.yes') : $t('statusBar.detail.no') }}</dd>
        <dt v-if="cg.error">{{ $t('statusBar.detail.error') }}</dt>
        <dd v-if="cg.error" class="td-error-text">{{ cg.error }}</dd>
      </dl>
      <div class="td-note">{{ $t('statusBar.detail.astNote') }}</div>
    </section>

    <section class="td-block" data-engine-detail="vfts">
      <div class="td-block-head">
        <h4 class="td-h4">{{ $t('statusBar.detail.vfts') }}</h4>
        <span class="td-badge" :class="'is-' + vf.tone">{{ vf.stateText }}</span>
        <span class="td-spacer"></span>
        <Button
          size="small"
          data-action="go-settings-vfts"
          v-mq:[EventNames.projectConfigOpen].click="{ tab: 'vfts' }"
        >{{ $t('statusBar.detail.goSettings') }}</Button>
      </div>
      <dl class="td-kv">
        <dt>{{ $t('statusBar.detail.enabled') }}</dt>
        <dd>{{ vf.enabled ? $t('statusBar.detail.yes') : $t('statusBar.detail.no') }}</dd>
        <dt>{{ $t('statusBar.detail.progress') }}</dt>
        <dd>
          <template v-if="vf.hasProgress">{{ vf.done }}/{{ vf.total }}</template>
          <template v-else>—</template>
          <div v-if="vf.hasProgress" class="td-progress">
            <div class="td-progress-fill" :style="{ width: pct(vf) + '%' }"></div>
          </div>
        </dd>
        <dt>{{ $t('statusBar.detail.chunks') }}</dt>
        <dd>{{ vf.chunks }}</dd>
        <dt>{{ $t('statusBar.detail.lastIndexed') }}</dt>
        <dd>{{ statusTime(vf.lastIndexedAt) || '—' }}</dd>
        <dt v-if="vf.error">{{ $t('statusBar.detail.error') }}</dt>
        <dd v-if="vf.error" class="td-error-text">{{ vf.error }}</dd>
      </dl>
    </section>
  </TaskDetailLayout>
</template>

<script setup>
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import TaskDetailLayout from './TaskDetailLayout.vue'
import { Button } from '../../components/ui'
import { EventNames } from '../../events/event-names'
import { usePrjConfigSnapshot } from '../../composables/usePrjConfigSnapshot'
import { indexDetail, statusTime } from '../../utils/indexStatus'

const { t } = useI18n()
const { prjCfg } = usePrjConfigSnapshot()

const cg = computed(() => indexDetail({
  enabledRaw: prjCfg.value['plugins.codebase.codegraph.enabled'],
  statusRaw: prjCfg.value['codegraph.status'],
  t,
}))
const vf = computed(() => indexDetail({
  enabledRaw: prjCfg.value['plugins.codebase.vfts.enabled'],
  statusRaw: prjCfg.value['vfts.status'],
  t,
}))

// 头部取「codegraph 优先，未启用则 vfts」的状态（详情页内两引擎各自完整展示）
const primary = computed(() => (cg.value.enabled ? cg.value : vf.value))
const headerTone = computed(() => primary.value.tone)
const headerState = computed(() => t('statusBar.detail.engineSummary', {
  cg: cg.value.stateText,
  vf: vf.value.stateText,
}))
const headerError = computed(() => cg.value.error || vf.value.error || '')

const phaseText = computed(() => {
  const p = cg.value.phase
  if (!p) return ''
  const key = p === 'configure' ? 'statusBar.detail.phaseConfigure' : 'statusBar.detail.phaseIndex'
  return t(key)
})

function pct(m) {
  if (!m || !m.hasProgress) return 0
  return Math.max(0, Math.min(100, Math.round((m.done / m.total) * 100)))
}
</script>

<style scoped>
.td-block {
  margin-bottom: 16px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 4px;
}
.td-block-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
}
.td-h4 {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-secondary);
}
.td-phase {
  color: var(--text-muted);
}
.td-spacer {
  flex: 1;
}
.td-kv {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 4px 12px;
  margin: 0;
}
.td-kv dt {
  color: var(--text-muted);
}
.td-kv dd {
  margin: 0;
  word-break: break-word;
}
.td-progress {
  margin-top: 4px;
  height: 4px;
  width: 180px;
  background: var(--bg-secondary);
  border-radius: 2px;
  overflow: hidden;
}
.td-progress-fill {
  height: 100%;
  background: var(--accent, #409eff);
  transition: width 0.2s;
}
.td-error-text {
  color: var(--fg-important);
  white-space: pre-wrap;
  word-break: break-word;
}
.td-note {
  margin-top: 6px;
  color: var(--text-muted);
}
.td-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 3px;
  font-size: 11px;
  white-space: nowrap;
}
.td-badge.is-disabled { color: var(--fg-disabled); background: var(--bg-secondary); }
.td-badge.is-progress { color: var(--fg-secondary); background: var(--bg-secondary); }
.td-badge.is-ready { color: var(--text-primary); background: var(--bg-secondary); }
.td-badge.is-error { color: var(--fg-important); background: var(--bg-secondary); }
</style>
