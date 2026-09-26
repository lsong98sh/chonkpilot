<template>
  <div />
</template>

<script setup>
import { h, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
import { dialog } from '../../components/dialog'

// 懒加载：首次打开 Scenario 时才加载内容组件
const ScenarioDialogContent = defineAsyncComponent(() => import('./ScenarioDialogContent.vue'))

const { t } = useI18n()
const emit = defineEmits(['changed'])

function open() {
  const handle = dialog.show(h(ScenarioDialogContent, {
    onChanged: () => {
      emit('changed')
    },
  }), {
    title: t('scenario.title'),
    width: 620,
    bodyClass: 'scenario-dialog-body',
    closable: true,
  })
}

defineExpose({ open })
</script>

<style>
.scenario-dialog-body {
  padding: 16px;
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
