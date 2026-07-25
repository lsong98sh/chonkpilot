<template>
  <div />
</template>

<script setup>
import { ref, h, defineComponent } from 'vue'
import { message, confirm } from '../ui'
import { Input, Textarea, Button, Table } from '../ui'
import Icon from '../icon/Icon.vue'
import { dialog } from '../dialog'

const emit = defineEmits(['changed'])

function open() {
  const scenarios = ref([])
  const editing = ref(false)
  const isNew = ref(false)
  const form = ref({ name: '', description: '', systemPrompt: '' })

  async function loadList() {
    try {
      const res = await window.go.main.App.GetScenarioList()
      scenarios.value = res.scenarios || []
    } catch (e) {
      scenarios.value = []
    }
  }

  function startAdd() {
    isNew.value = true
    form.value = { name: '', description: '', systemPrompt: '' }
    editing.value = true
  }

  function startEdit(row) {
    isNew.value = false
    form.value = { ...row }
    editing.value = true
  }

  function cancelEdit() {
    editing.value = false
  }

  async function confirmEdit() {
    try {
      const res = await window.go.main.App.SaveScenario({ scenario: form.value })
      if (res?.scenario) {
        message.success(isNew.value ? '场景已创建' : '场景已更新')
      }
      editing.value = false
      await loadList()
      emit('changed')
    } catch (e) {
      message.error('保存失败: ' + (e.message || e))
    }
  }

  async function handleDelete(row) {
    try {
      await confirm(`确定删除场景「${row.name}」？`, '确认删除')
      await window.go.main.App.DeleteScenario({ id: row.id })
      message.success('场景已删除')
      await loadList()
      emit('changed')
    } catch (e) {
      if (e !== 'cancel') message.error('删除失败: ' + (e.message || e))
    }
  }

  loadList()

  const columns = [
    { label: '名称', prop: 'name', minWidth: 160 },
    { label: '说明', prop: 'description', minWidth: 200 },
    { label: '操作', type: 'action', width: 160, align: 'center' },
  ]

  const handle = dialog.show(defineComponent({
    setup() {
      return () => h('div', { class: 'dialog-content' }, [
        !editing.value
          ? [
              h('div', { class: 'toolbar-actions' }, [
                h(Button, { size: 'small', type: 'primary', onClick: startAdd }, () => [
                  h(Icon, { name: 'plus', size: 14 }),
                  ' 添加场景',
                ]),
              ]),
              h(Table, {
                columns,
                data: scenarios.value,
                emptyText: '暂无场景',
              }, {
                action: ({ row }) => [
                  h(Button, { text: true, size: 'small', onClick: () => startEdit(row) }, () => '编辑'),
                  h(Button, { text: true, size: 'small', type: 'danger', onClick: () => handleDelete(row) }, () => '删除'),
                ]
              }),
            ]
          : [
              h('div', { class: 'edit-header' }, [
                h(Button, { size: 'small', onClick: cancelEdit }, () => '返回列表'),
                h('span', { class: 'edit-title' }, isNew.value ? '添加场景' : '编辑场景'),
                h(Button, { size: 'small', type: 'primary', onClick: confirmEdit }, () => '保存'),
              ]),
              h('form', { class: 'b-form edit-form' }, () => [
                h('div', { class: 'b-row' }, () => [
                  h('div', { class: 'b-col b-col--6' }, () => h('div', { class: 'b-form-item' }, [
                    h('label', { class: 'b-form-label' }, '名称'),
                    h(Input, {
                      modelValue: form.value.name,
                      'onUpdate:modelValue': (v) => { form.value.name = v },
                      placeholder: '场景名称',
                    }),
                  ])),
                  h('div', { class: 'b-col b-col--18' }, () => h('div', { class: 'b-form-item' }, [
                    h('label', { class: 'b-form-label' }, '说明'),
                    h(Input, {
                      modelValue: form.value.description,
                      'onUpdate:modelValue': (v) => { form.value.description = v },
                      placeholder: '场景说明',
                    }),
                  ])),
                ]),
                h('div', { class: 'b-form-item prompt-form-item' }, [
                  h('label', { class: 'b-form-label' }, '系统提示词'),
                  h(Textarea, {
                    modelValue: form.value.systemPrompt,
                    'onUpdate:modelValue': (v) => { form.value.systemPrompt = v },
                    rows: 20,
                    placeholder: '输入场景的系统提示词，选择此场景后会自动覆盖默认系统提示词',
                  }),
                ]),
              ]),
            ],
      ])
    }
  }), {
    title: '场景管理',
    width: '90vw',
    bodyClass: 'scenario-dialog-body',
    closable: true,
    onAction: (action) => {
      if (action === 'close' || action === 'cancel') handle.close()
    }
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
.dialog-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  min-height: 0;
}
.toolbar-actions {
  margin-bottom: 12px;
  flex-shrink: 0;
}
.scenario-table {
  flex: 1;
  overflow-y: auto;
}
.edit-header {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.edit-title {
  flex: 1;
  font-weight: 700;
  font-size: 14px;
}
.edit-form {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.prompt-form-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  margin-bottom: 0;
}
.prompt-editor {
  font-family: var(--font-mono, 'Cascadia Code', 'JetBrains Mono', monospace) !important;
  flex: 1;
  min-height: 0;
}
</style>
