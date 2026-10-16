<!--
  群聊配置（项目级；项目配置页 → tab kind = settings-project 的「群聊」页签）

  覆盖（SUBCHAT-3 阶段 E；**只做最小配置项**，群聊窗口 UI 属阶段 G）：
    ① prj `group_chat.max_rounds` 前端入口 —— 读/写既有 prj config 表（getAllConfig/setConfig/
       deleteConfig，零新增 MQ 主题）；后端读点 = `src/lib/llm/server/groupchat.go`
       （群聊调度器启动/续跑前现读 → 保存即生效）。
    ② 取值语义（群聊方案 §7 ③ / D49）：默认 20、可选区间 20–40；区间外/非法 → 后端回落默认 20
       （故本页只给区间内选项，用户无法配出非法值）。

  位置说明：本页只写配置，不承载群聊窗口 / 头像状态 / 结论流（阶段 G）。
-->
<template>
  <div class="gc-root">
    <form class="form-layout">
      <div class="form-item form-item-full">
        <label class="form-label">{{ $t('projectConfig.groupchat_max_rounds_label') }}</label>
        <div class="gc-select">
          <Select
            :model-value="rounds"
            :options="roundOptions"
            aria-label="group_chat.max_rounds"
            @update:model-value="onRoundsChange"
          />
        </div>
        <div class="gc-hint">{{ $t('projectConfig.groupchat_max_rounds_hint') }}</div>
      </div>
    </form>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { Select, message } from '../../components/ui'
import { getAllConfig, setConfig, deleteConfig } from '../../api/config'
import { saveFailedText, loadFailedText } from '../../utils/settingsFeedback'

const { t } = useI18n()

// 配置键与取值（与后端 `src/lib/llm/server/groupchat.go` 同源：默认 20 / 区间 20–40）。
const MAX_ROUNDS_KEY = 'group_chat.max_rounds'
const DEFAULT_ROUNDS = 20
const ROUND_CHOICES = [20, 25, 30, 35, 40]

const rounds = ref(DEFAULT_ROUNDS)
const roundOptions = computed(() => ROUND_CHOICES.map(v => ({
  // 20 标为默认值：选中 20 = 与缺省同义（保存时按缺省处理，不写库）。
  label: v === DEFAULT_ROUNDS
    ? t('projectConfig.groupchat_max_rounds_default', { n: v })
    : String(v),
  value: v,
})))

// 解析后端值：区间内 → 原样；缺失/非法/区间外 → 默认 20（与后端回落口径一致）。
function parseRounds(raw) {
  const n = Number.parseInt(String(raw ?? '').trim(), 10)
  if (!Number.isFinite(n) || n < 20 || n > 40) return DEFAULT_ROUNDS
  return n
}

async function loadConfig() {
  try {
    const res = await getAllConfig()
    const c = res.config || res
    rounds.value = parseRounds(c[MAX_ROUNDS_KEY])
  } catch (e) {
    message.error(loadFailedText(t, t('projectConfig.groupchat_max_rounds_label'), e))
  }
}

// 保存即生效（后端每次现读该键）：选默认值 = 删项目级键（回落缺省），否则写显式值。
async function onRoundsChange(v) {
  if (!v) return
  const prev = rounds.value
  rounds.value = v
  try {
    if (v === DEFAULT_ROUNDS) await deleteConfig(MAX_ROUNDS_KEY)
    else await setConfig(MAX_ROUNDS_KEY, String(v))
    message.success(t('projectConfig.groupchat_saved'))
  } catch (e) {
    rounds.value = prev
    message.error(saveFailedText(t, e))
  }
}

onMounted(loadConfig)
</script>

<style scoped>
.gc-root {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
}
.form-layout {
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
}
.form-item {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  width: 100%;
}
.form-item-full {
  width: 100%;
}
.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
}
.gc-select {
  width: 220px;
}
.gc-hint {
  font-size: 12px;
  color: var(--text-muted);
  line-height: 1.6;
}
</style>
