<template>
  <div />
</template>

<script setup>
import { ref, reactive, h, defineComponent } from 'vue'
import { Button, Tabs, Table, Input, Textarea, Select, Switch, message, confirm } from '../ui'
import Icon from '../icon/Icon.vue'
import { dialog } from '../dialog'
import { getUserConfig, saveUserConfig } from '../../api/config'

let settingsHandle = null

const defaultLLM = () => ({
  name: '', protocol: 'openai', apiKey: '', model: 'deepseek-v4-flash',
  baseUrl: 'https://api.deepseek.com', temperature: 0.7, maxTokens: 4096,
  thinking: true, reasoningEffort: '',
})

const defaultMCP = () => ({ name: '', url: '', enabled: true, description: '' })

function open() {
  const tab = ref('llm')
  const form = reactive({
    llms: [],
    defaultLLM: 0,
    mcpServers: [],
    chromePath: '',
    javaPath: '',
    pythonPath: '',
    nodePath: '',
    maxToolIterations: 800,
    responseTimeout: 180,
    streamTimeout: 180,
    logLevel: 'info',
    theme: 'light',
    retryCount: 0,
    retryDelay: 5,
    codeIndexTemperature: 0.1,
    toolMaxDepth: 5,
    taskPollIntervalMs: 200,
    searchMaxResults: 200,
    fetchMaxBodySizeKB: 100,
    browserWindowWidth: 1280,
    browserWindowHeight: 800,
    browserLogCap: 500,
    llmTLSHandshakeTimeout: 30,
  })

  async function loadConfig() {
    try {
      const ures = await getUserConfig()
      const uc = ures.config || ures
      if (uc.llms && uc.llms.length > 0) form.llms = uc.llms
      if (uc.defaultLLM !== undefined) form.defaultLLM = uc.defaultLLM
      if (uc.mcpServers && uc.mcpServers.length > 0) form.mcpServers = uc.mcpServers
      if (uc.chromePath !== undefined) form.chromePath = uc.chromePath
      if (uc.javaPath !== undefined) form.javaPath = uc.javaPath
      if (uc.pythonPath !== undefined) form.pythonPath = uc.pythonPath
      if (uc.nodePath !== undefined) form.nodePath = uc.nodePath
      if (uc.maxToolIterations !== undefined) form.maxToolIterations = uc.maxToolIterations
      if (uc.responseTimeout !== undefined) form.responseTimeout = uc.responseTimeout
      if (uc.streamTimeout !== undefined) form.streamTimeout = uc.streamTimeout
      if (uc.theme !== undefined) form.theme = uc.theme
      if (uc.logLevel !== undefined) form.logLevel = uc.logLevel
      if (uc.retryCount !== undefined) form.retryCount = uc.retryCount
      if (uc.retryDelay !== undefined) form.retryDelay = uc.retryDelay
      if (uc.codeIndexTemperature !== undefined) form.codeIndexTemperature = uc.codeIndexTemperature
      if (uc.toolMaxDepth !== undefined) form.toolMaxDepth = uc.toolMaxDepth
      if (uc.taskPollIntervalMs !== undefined) form.taskPollIntervalMs = uc.taskPollIntervalMs
      if (uc.searchMaxResults !== undefined) form.searchMaxResults = uc.searchMaxResults
      if (uc.fetchMaxBodySizeKB !== undefined) form.fetchMaxBodySizeKB = uc.fetchMaxBodySizeKB
      if (uc.browserWindowWidth !== undefined) form.browserWindowWidth = uc.browserWindowWidth
      if (uc.browserWindowHeight !== undefined) form.browserWindowHeight = uc.browserWindowHeight
      if (uc.browserLogCap !== undefined) form.browserLogCap = uc.browserLogCap
      if (uc.llmTLSHandshakeTimeout !== undefined) form.llmTLSHandshakeTimeout = uc.llmTLSHandshakeTimeout
    } catch (e) { console.warn('[ConfigDialog] Failed to load user config:', e) }
  }

  // Edit LLM state
  const editLLMState = reactive({ visible: false, index: -1, data: defaultLLM() })
  // Edit MCP state
  const editMCPState = reactive({ visible: false, index: -1, data: defaultMCP() })
  const savingMcp = ref(false)

  function addLLM() {
    editLLMState.index = -1
    editLLMState.data = defaultLLM()
    openEditLLMDialog()
  }

  function editLLM(index) {
    editLLMState.index = index
    editLLMState.data = { ...form.llms[index] }
    openEditLLMDialog()
  }

  function openEditLLMDialog() {
    const localData = reactive({ ...editLLMState.data })
    const llmHandle = dialog.show(defineComponent({
      setup() {
        return () => h('div', { class: 'config-form' }, [
          h('div', { class: 'form-row' }, [
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, '名称'),
              h(Input, { modelValue: localData.name, 'onUpdate:modelValue': (v) => { localData.name = v }, placeholder: 'my-openai' }),
            ]),
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, 'Protocol'),
              h(Select, {
                modelValue: localData.protocol,
                'onUpdate:modelValue': (v) => { localData.protocol = v },
                options: [{ label: 'OpenAI', value: 'openai' }, { label: 'Claude', value: 'claude' }]
              }),
            ]),
          ]),
          h('div', { class: 'form-item' }, [
            h('label', { class: 'form-label' }, 'API Key'),
            h(Input, { modelValue: localData.apiKey, 'onUpdate:modelValue': (v) => { localData.apiKey = v }, type: 'password' }),
          ]),
          h('div', { class: 'form-row' }, [
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, '模型'),
              h(Input, { modelValue: localData.model, 'onUpdate:modelValue': (v) => { localData.model = v }, placeholder: 'gpt-4o' }),
            ]),
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, 'Base URL'),
              h(Input, { modelValue: localData.baseUrl, 'onUpdate:modelValue': (v) => { localData.baseUrl = v }, placeholder: 'https://api.openai.com/v1' }),
            ]),
          ]),
          h('div', { class: 'form-row' }, [
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, 'Temperature'),
              h('input', {
                type: 'range',
                value: localData.temperature,
                onInput: (e) => { localData.temperature = Number(e.target.value) },
                min: 0, max: 2, step: 0.1,
                style: { width: '100%' }
              }),
              h('span', { style: { fontSize: '12px', color: 'var(--text-muted)', marginLeft: '4px' } }, String(localData.temperature)),
            ]),
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, '最大输出 Token'),
              h('input', {
                type: 'number',
                value: localData.maxTokens,
                onInput: (e) => { localData.maxTokens = Number(e.target.value) },
                min: 256, max: 65536, step: 256,
                class: 'b-input',
                style: { width: '100%' }
              }),
            ]),
          ]),
          h('div', { class: 'form-row' }, [
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, '思考模式'),
              h(Switch, { modelValue: localData.thinking, 'onUpdate:modelValue': (v) => { localData.thinking = v } }),
            ]),
            h('div', { class: 'form-col form-col-12' }, [
              h('label', { class: 'form-label' }, '推理强度'),
              h(Select, {
                modelValue: localData.reasoningEffort,
                'onUpdate:modelValue': (v) => { localData.reasoningEffort = v },
                options: [{ label: 'High', value: 'high' }, { label: 'Max', value: 'max' }],
                placeholder: 'auto',
                disabled: !localData.thinking
              }),
            ]),
          ]),
          h('div', { style: { display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' } }, [
            h(Button, { onClick: () => llmHandle.close() }, () => '取消'),
            h(Button, { type: 'primary', onClick: () => {
              if (editLLMState.index === -1) {
                form.llms.push({ ...localData })
              } else {
                form.llms[editLLMState.index] = { ...localData }
              }
              llmHandle.close()
            }}, () => '保存'),
          ]),
        ])
      }
    }), {
      title: '编辑 LLM Provider',
      width: 520,
      closable: true,
      onAction: (action) => {
        if (action === 'close' || action === 'cancel') llmHandle.close()
      }
    })
  }

  function addMCP() {
    editMCPState.index = -1
    editMCPState.data = defaultMCP()
    openEditMCPDialog()
  }

  function editMCP(index) {
    editMCPState.index = index
    editMCPState.data = { ...form.mcpServers[index] }
    openEditMCPDialog()
  }

  function openEditMCPDialog() {
    const localData = reactive({ ...editMCPState.data })
    const mcpHandle = dialog.show(defineComponent({
      setup() {
        const localSaving = ref(false)

        async function confirmEditMCP() {
          const name = localData.name.trim()
          if (!name) {
            message.warning('MCP Server 名称不能为空')
            return
          }
          if (!/^[a-zA-Z][a-zA-Z0-9_]*$/.test(name)) {
            message.warning('MCP Server 名称必须以英文字母开头，只能包含字母、数字和下划线')
            return
          }
          const url = localData.url.trim()
          if (!url) {
            message.warning('Server URL 不能为空')
            return
          }
          localData.name = name
          localData.url = url

          localSaving.value = true
          try {
            const result = await window.go.main.App.DiscoverMCPServerTools(name, url)
            const tools = result.tools || []
            const transport = result.transport || 'direct'

            const esc = s => String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')
            const toolItems = tools.map((t, i) =>
              `<div style="padding:12px;${i < tools.length - 1 ? 'border-bottom:1px solid #e8e8e8;' : ''}">
                 <div style="font-weight:500;font-size:13px;line-height:1.4;">${esc(t.name)}</div>
                 <div style="font-size:11px;color:#999;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;line-height:1.4;margin-top:2px;">${esc(t.description || '')}</div>
               </div>`
            ).join('')

            // Show custom confirm dialog with HTML content via a Promise
            const confirmed = await new Promise((resolve) => {
              const confirmContent = h('div', { style: { width: '394px' } }, [
                h('div', { style: { marginBottom: '8px', fontSize: '13px', color: '#666' } }, [
                  '共 ',
                  h('strong', {}, String(tools.length)),
                  ' 个工具，传输方式：',
                  h('strong', {}, transport),
                ]),
                h('div', { style: { maxHeight: '235px', overflowY: 'auto', border: '1px solid #e0e0e0', borderRadius: '4px' } }, [
                  toolItems
                    ? h('div', { innerHTML: toolItems })
                    : h('div', { style: { padding: '12px', color: '#999' } }, '未发现工具'),
                ]),
                h('div', { style: { display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' } }, [
                  h(Button, { onClick: () => { resolve(false); confirmDlg.close() } }, () => '取消'),
                  h(Button, { type: 'primary', onClick: () => { resolve(true); confirmDlg.close() } }, () => '确认保存'),
                ]),
              ])
              const confirmDlg = dialog.show(confirmContent, {
                title: 'MCP 工具发现结果',
                width: 460,
                closable: true,
                minimizable: false,
                collapsible: false,
                resizable: false,
                onAction: (action) => {
                  if (action === 'close' || action === 'closed') {
                    resolve(false)
                  }
                },
              })
            })

            if (!confirmed) return

            localData.discoveredTools = tools
            localData.transport = transport
            if (editMCPState.index === -1) {
              form.mcpServers.push({ ...localData })
            } else {
              form.mcpServers[editMCPState.index] = { ...localData }
            }
            mcpHandle.close()
          } catch (err) {
            if (err === 'cancel' || err === 'close') return
            const msg = typeof err === 'string' ? err : (err?.message || '验证 MCP Server 失败')
            message.error(msg)
          } finally {
            localSaving.value = false
          }
        }

        return () => [
          h('div', { class: 'config-form' }, [
            h('div', { class: 'form-item' }, [
              h('label', { class: 'form-label' }, '名称'),
              h(Input, { modelValue: localData.name, 'onUpdate:modelValue': (v) => { localData.name = v }, placeholder: 'my-database-mcp' }),
            ]),
            h('div', { class: 'form-item' }, [
              h('label', { class: 'form-label' }, 'Server URL'),
              h(Input, { modelValue: localData.url, 'onUpdate:modelValue': (v) => { localData.url = v }, placeholder: 'http://localhost:8081/mcp' }),
            ]),
            h('div', { class: 'form-item' }, [
              h('label', { class: 'form-label' }, '描述'),
              h(Textarea, { modelValue: localData.description, 'onUpdate:modelValue': (v) => { localData.description = v }, rows: 2, placeholder: '可选描述' }),
            ]),
            h('div', { class: 'form-item' }, [
              h('label', { class: 'form-label' }, '启用'),
              h(Switch, { modelValue: localData.enabled, 'onUpdate:modelValue': (v) => { localData.enabled = v } }),
            ]),
          ]),
          h('div', { style: { display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' } }, [
            h(Button, { onClick: () => mcpHandle.close() }, () => '取消'),
            h(Button, { type: 'primary', loading: localSaving.value, onClick: confirmEditMCP }, () => '保存'),
          ]),
        ]
      }
    }), {
      title: '编辑 MCP Server',
      width: 520,
      closable: true,
      onAction: (action) => {
        if (action === 'close' || action === 'cancel') mcpHandle.close()
      }
    })
  }

  function deleteLLM(index) {
    form.llms.splice(index, 1)
  }

  async function save() {
    try {
      if (!form.llms || form.llms.length === 0) {
        message.warning('至少配置一个 LLM')
        return
      }
      for (let i = 0; i < form.llms.length; i++) {
        const llm = form.llms[i]
        if (!llm.name?.trim()) {
          message.warning(`第 ${i + 1} 个 LLM 缺少名称`)
          return
        }
        if (!llm.model?.trim()) {
          message.warning(`LLM "${llm.name}" 缺少模型名`)
          return
        }
      }
      await saveUserConfig({
        llms: form.llms,
        defaultLLM: form.defaultLLM,
        mcpServers: form.mcpServers,
        chromePath: form.chromePath || '',
        javaPath: form.javaPath || '',
        pythonPath: form.pythonPath || '',
        nodePath: form.nodePath || '',
        maxToolIterations: form.maxToolIterations,
        responseTimeout: form.responseTimeout,
        streamTimeout: form.streamTimeout,
        theme: form.theme,
        logLevel: form.logLevel,
        retryCount: form.retryCount,
        retryDelay: form.retryDelay,
        codeIndexTemperature: form.codeIndexTemperature,
        toolMaxDepth: form.toolMaxDepth,
        taskPollIntervalMs: form.taskPollIntervalMs,
        searchMaxResults: form.searchMaxResults,
        fetchMaxBodySizeKB: form.fetchMaxBodySizeKB,
        browserWindowWidth: form.browserWindowWidth,
        browserWindowHeight: form.browserWindowHeight,
        browserLogCap: form.browserLogCap,
        llmTLSHandshakeTimeout: form.llmTLSHandshakeTimeout,
      })
      settingsHandle?.close()
    } catch (e) {
      console.error(e)
    }
  }

  async function pickFile(field) {
    try {
      const result = await window.go.main.App.PickExecutableFile()
      if (result && result.path) {
        form[field] = result.path
      }
    } catch (e) {
      console.warn('[ConfigDialog] PickExecutableFile failed:', e)
    }
  }

  loadConfig()

  settingsHandle = dialog.show(defineComponent({
    setup() {
      return () => [
        h('div', { class: 'config-dialog-body-scroll' }, [
          h(Tabs, {
            tabs: [
              { label: 'LLM', name: 'llm' },
              { label: '环境', name: 'env' },
              { label: 'MCP', name: 'mcp' },
              { label: '代码索引', name: 'codeIndex' },
              { label: '常规', name: 'general' },
            ],
            modelValue: tab.value,
            'onUpdate:modelValue': (v) => { tab.value = v }
          }, {
            llm: () => [
              h('div', { class: 'config-toolbar-actions' }, [
                h(Button, { type: 'primary', onClick: addLLM }, () => [
                  h(Icon, { name: 'plus', size: 14 }),
                  ' 添加 LLM',
                ]),
              ]),
              h(Table, {
                columns: [
                  { label: '#', type: 'index', width: 40 },
                  { label: '名称', prop: 'name', minWidth: 120 },
                  { label: 'Protocol', prop: 'protocol', width: 100 },
                  { label: '模型', prop: 'model', minWidth: 120 },
                  { label: '操作', type: 'action', width: 200, align: 'center' },
                ],
                data: form.llms,
                emptyText: '未配置 LLM',
                size: 'small'
              }, {
                action: ({ row, index }) => [
                  form.defaultLLM === index
                    ? h('span', { style: { color: 'var(--success, #67c23a)', fontSize: '12px', marginRight: '6px' } }, '★默认')
                    : h(Button, { text: true, onClick: () => { form.defaultLLM = index } }, () => '设默认'),
                  h(Button, { text: true, onClick: () => editLLM(index) }, () => '编辑'),
                  h(Button, { text: true, type: 'danger', onClick: () => deleteLLM(index) }, () => '删除'),
                ]
              }),
            ],
            env: () =>
              h('div', { class: 'config-form' }, [
                [['chromePath', 'Chrome 路径'], ['javaPath', 'Java 路径（java.exe）'], ['pythonPath', 'Python 路径（python.exe）'], ['nodePath', 'Node.js 路径（node.exe）']]
                  .map(([field, label]) =>
                    h('div', { class: 'form-item' }, [
                      h('label', { class: 'form-label' }, label),
                      h('div', { class: 'config-path-input-wrap' }, [
                        h(Input, { modelValue: form[field], 'onUpdate:modelValue': (v) => { form[field] = v }, placeholder: '自动检测，可手动覆盖' }),
                        h(Button, { onClick: () => pickFile(field) }, () => '...'),
                      ]),
                    ])
                  ),
                h('p', { class: 'config-chrome-tip' }, '不设置 Chrome 路径无法使用 Web 自动化工具（web_start / web_click / web_screenshot 等不可用）'),
              ]),
            mcp: () => [
              h('div', { class: 'config-toolbar-actions' }, [
                h(Button, { type: 'primary', onClick: addMCP }, () => [
                  h(Icon, { name: 'plus', size: 14 }),
                  ' 添加 MCP Server',
                ]),
              ]),
              h(Table, {
                columns: [
                  { label: '#', type: 'index', width: 40 },
                  { label: '名称', prop: 'name', minWidth: 120 },
                  { label: 'URL', prop: 'url', minWidth: 200 },
                  { label: '描述', prop: 'description', minWidth: 140 },
                  { label: '启用', prop: 'enabled', width: 60, align: 'center' },
                  { label: '操作', type: 'action', width: 140, align: 'center' },
                ],
                data: form.mcpServers,
                emptyText: '未配置 MCP Server',
                size: 'small'
              }, {
                action: ({ row, index }) => [
                  h(Button, { text: true, onClick: () => editMCP(index) }, () => '编辑'),
                  h(Button, { text: true, type: 'danger', onClick: () => form.mcpServers.splice(index, 1) }, () => '删除'),
                ]
              }),
            ],
            codeIndex: () =>
              h('div', { class: 'config-form' }, [
                h('div', { class: 'form-item' }, [
                  h('label', { class: 'form-label' }, '代码索引温度'),
                  h('input', {
                    type: 'number',
                    value: form.codeIndexTemperature,
                    onInput: (e) => { form.codeIndexTemperature = Number(e.target.value) },
                    min: 0, max: 1, step: 0.05,
                    class: 'b-input',
                    style: { width: '180px' }
                  }),
                ]),
              ]),
            general: () =>
              h('div', { class: 'config-form config-form-left' }, [
                h('div', { class: 'form-row' }, [
                  h('div', { class: 'form-col form-col-12' }, [
                    h('label', { class: 'form-label' }, '日志级别'),
                    h(Select, {
                      modelValue: form.logLevel,
                      'onUpdate:modelValue': (v) => { form.logLevel = v },
                      options: ['debug','info','warn','error'].map(l => ({ label: l.charAt(0).toUpperCase() + l.slice(1), value: l }))
                    }),
                  ]),
                  h('div', { class: 'form-col form-col-12' }, [
                    h('label', { class: 'form-label' }, '主题'),
                    h(Select, {
                      modelValue: form.theme,
                      'onUpdate:modelValue': (v) => { form.theme = v },
                      options: [
                        { label: '浅色', value: 'light' },
                        { label: '暗色', value: 'dark' },
                        { label: '高对比度', value: 'high-contrast' },
                      ]
                    }),
                  ]),
                ]),
                // Numeric fields
                ...[['retryCount', 'LLM 重试次数', 0, 10], ['retryDelay', '重试间隔(秒)', 1, 120], ['maxToolIterations', '最大工具迭代', 0, 2000],
                  ['responseTimeout', '响应超时(秒)', 0, 600], ['streamTimeout', '流空闲超时(秒)', 0, 600], ['toolMaxDepth', '工具嵌套深度', 1, 20],
                  ['taskPollIntervalMs', '任务轮询间隔(ms)', 50, 2000], ['searchMaxResults', '搜索结果上限', 10, 5000],
                  ['fetchMaxBodySizeKB', 'HTTP 获取(KB)', 1, 10000], ['browserWindowWidth', '浏览器窗口宽度', 800, 3840],
                  ['browserWindowHeight', '浏览器窗口高度', 600, 2160], ['browserLogCap', '控制台日志上限', 100, 5000],
                  ['llmTLSHandshakeTimeout', 'TLS 握手超时(秒)', 5, 120],
                ].map(([field, label, min, max]) => {
                  const step = field === 'maxToolIterations' ? 50 :
                               field === 'responseTimeout' || field === 'streamTimeout' || field === 'llmTLSHandshakeTimeout' ? 30 :
                               field === 'browserWindowWidth' || field === 'browserWindowHeight' ? 100 :
                               field === 'browserLogCap' ? 100 :
                               field === 'taskPollIntervalMs' ? 50 :
                               field === 'searchMaxResults' || field === 'fetchMaxBodySizeKB' ? 10 : 1
                  return h('div', { class: 'form-row' }, [
                    h('div', { class: 'form-col form-col-12' }, [
                      h('div', { class: 'form-item' }, [
                        h('label', { class: 'form-label' }, label),
                        h('input', {
                          type: 'number',
                          value: form[field],
                          onInput: (e) => { form[field] = Number(e.target.value) },
                          min, max, step,
                          class: 'b-input',
                          style: { width: '100%' }
                        }),
                        field === 'retryCount' ? h('span', { class: 'config-form-hint' }, '0 = 不重试') : null,
                      ]),
                    ]),
                  ])
                }),
              ]),
          }),
        ]),
        // Footer buttons
        h('div', { style: { display: 'flex', justifyContent: 'flex-end', gap: '8px', padding: '12px 0 0', borderTop: '1px solid var(--border)', marginTop: '12px' } }, [
          h(Button, { onClick: () => settingsHandle?.close() }, () => '取消'),
          h(Button, { type: 'primary', onClick: save }, () => '保存'),
        ]),
      ]
    }
  }), {
    title: '设置',
    width: 680,
    closable: true,
    bodyClass: 'config-dialog-body',
    onAction: (action) => {
      if (action === 'close' || action === 'cancel') settingsHandle?.close()
    }
  })
}

defineExpose({ open })
</script>

<style>
.config-dialog-body {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  padding-top: 8px;
}

.config-dialog-body-scroll {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.config-dialog-body-scroll .b-tabs {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.config-dialog-body-scroll .b-tabs .b-tabs-body {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.config-toolbar-actions {
  margin-bottom: 12px;
}

.config-form-hint {
  font-size: 11px;
  color: var(--text-muted);
  flex: 0 0 100%;
  margin-top: 2px;
}

.config-chrome-tip {
  font-size: 12px;
  color: var(--text-muted, #909399);
  margin: 0;
  padding: 0 4px;
}

.config-path-input-wrap {
  display: flex;
  gap: 4px;
  width: 100%;
}

.config-path-input-wrap .b-input {
  flex: 1;
}

.config-dialog-body .config-form .form-item {
  margin-bottom: 6px;
}

.config-dialog-body input[type="number"].b-input {
  width: 100%;
}

.config-dialog-body .b-select {
  width: 100%;
}

/* Form layout */
.config-form {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.config-form-left {
  max-width: 100%;
}

.form-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin-bottom: 12px;
}

.form-label {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-primary);
  line-height: 1.4;
}

.form-row {
  display: flex;
  gap: 12px;
  width: 100%;
}

.form-col {
  flex: 1;
  min-width: 0;
}

.form-col-12 {
  flex: 0 0 calc(50% - 6px);
}
</style>