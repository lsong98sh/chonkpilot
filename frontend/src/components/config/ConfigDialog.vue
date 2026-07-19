<template>
  <el-dialog v-model="visible" title="设置" width="680px" append-to-body destroy-on-close draggable class="settings-dialog">
    <div class="dialog-body-scroll">
    <el-tabs v-model="tab">
      <el-tab-pane label="LLM" name="llm">
        <div class="toolbar-actions">
          <el-button size="small" type="primary" @click="addLLM()">
            <el-icon><Plus /></el-icon> 添加 LLM
          </el-button>
        </div>
        <el-table :data="form.llms" size="small" style="width: 100%" empty-text="未配置 LLM">
          <el-table-column label="#" type="index" width="40" />
          <el-table-column label="名称" prop="name" min-width="120" />
          <el-table-column label="Protocol" prop="protocol" width="100" />
          <el-table-column label="模型" prop="model" min-width="120" />
          <el-table-column label="默认" width="70" align="center">
            <template #default="{ $index }">
              <el-radio v-model="form.defaultLLM" :value="$index" size="small" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="120" align="center">
            <template #default="{ $index }">
              <el-button text size="small" @click="editLLM($index)">编辑</el-button>
              <el-button text size="small" type="danger" @click="deleteLLM($index)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
      <el-tab-pane label="环境" name="env">
        <el-form label-position="top" size="small">
          <el-form-item label="Chrome 路径">
            <div class="path-input-wrap">
              <el-input v-model="form.chromePath" placeholder="自动检测，可手动覆盖" />
              <el-button :icon="FolderOpened" @click="pickFile('chromePath')" />
            </div>
          </el-form-item>
          <el-form-item label="Java 路径（java.exe）">
            <div class="path-input-wrap">
              <el-input v-model="form.javaPath" placeholder="自动检测，可手动覆盖" />
              <el-button :icon="FolderOpened" @click="pickFile('javaPath')" />
            </div>
          </el-form-item>
          <el-form-item label="Python 路径（python.exe）">
            <div class="path-input-wrap">
              <el-input v-model="form.pythonPath" placeholder="自动检测，可手动覆盖" />
              <el-button :icon="FolderOpened" @click="pickFile('pythonPath')" />
            </div>
          </el-form-item>
          <el-form-item label="Node.js 路径（node.exe）">
            <div class="path-input-wrap">
              <el-input v-model="form.nodePath" placeholder="自动检测，可手动覆盖" />
              <el-button :icon="FolderOpened" @click="pickFile('nodePath')" />
            </div>
          </el-form-item>
          <p class="chrome-tip">不设置 Chrome 路径无法使用 Web 自动化工具（web_start / web_click / web_screenshot 等不可用）</p>
        </el-form>
      </el-tab-pane>
      <el-tab-pane label="MCP" name="mcp">
        <div class="toolbar-actions">
          <el-button size="small" type="primary" @click="addMCP">
            <el-icon><Plus /></el-icon> 添加 MCP Server
          </el-button>
        </div>
        <el-table :data="form.mcpServers" size="small" style="width: 100%" empty-text="未配置 MCP Server">
          <el-table-column label="#" type="index" width="40" />
          <el-table-column label="名称" prop="name" min-width="120" />
          <el-table-column label="URL" prop="url" min-width="200" show-overflow-tooltip />
          <el-table-column label="描述" prop="description" min-width="140" show-overflow-tooltip />
          <el-table-column label="启用" width="60" align="center">
            <template #default="{ $index }">
              <el-switch v-model="form.mcpServers[$index].enabled" size="small" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="120" align="center">
            <template #default="{ $index }">
              <el-button text size="small" @click="editMCP($index)">编辑</el-button>
              <el-button text size="small" type="danger" @click="form.mcpServers.splice($index, 1)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
      <!-- 代码索引 tab -->
      <el-tab-pane label="代码索引" name="codeIndex">
        <el-form label-position="top" size="small">
          <el-form-item label="代码索引温度">
            <el-input-number v-model="form.codeIndexTemperature" :min="0" :max="1" :step="0.05" controls-position="right" :style="{ width: '180px' }" />
          </el-form-item>
        </el-form>
      </el-tab-pane>
      <el-tab-pane label="常规" name="general">
            <el-form label-position="left" label-width="150px" size="small">
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="日志级别">
                    <el-select v-model="form.logLevel">
                      <el-option label="Debug" value="debug" />
                      <el-option label="Info" value="info" />
                      <el-option label="Warn" value="warn" />
                      <el-option label="Error" value="error" />
                    </el-select>
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="主题">
                    <el-select v-model="form.theme">
                      <el-option label="浅色" value="light" />
                      <el-option label="暗色" value="dark" />
                      <el-option label="高对比度" value="high-contrast" />
                    </el-select>
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="LLM 重试次数">
                    <el-input-number v-model="form.retryCount" :min="0" :max="10" controls-position="right" />
                    <span class="form-hint">0 = 不重试</span>
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="重试间隔(秒)">
                    <el-input-number v-model="form.retryDelay" :min="1" :max="120" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="最大工具迭代">
                    <el-input-number v-model="form.maxToolIterations" :min="0" :max="2000" :step="50" controls-position="right" />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="响应超时(秒)">
                    <el-input-number v-model="form.responseTimeout" :min="0" :max="600" :step="30" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="流空闲超时(秒)">
                    <el-input-number v-model="form.streamTimeout" :min="0" :max="600" :step="30" controls-position="right" />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="工具嵌套深度">
                    <el-input-number v-model="form.toolMaxDepth" :min="1" :max="20" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="任务轮询间隔(ms)">
                    <el-input-number v-model="form.taskPollIntervalMs" :min="50" :max="2000" :step="50" controls-position="right" />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="搜索结果上限">
                    <el-input-number v-model="form.searchMaxResults" :min="10" :max="5000" :step="10" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="HTTP 获取(KB)">
                    <el-input-number v-model="form.fetchMaxBodySizeKB" :min="1" :max="10000" :step="10" controls-position="right" />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="浏览器窗口宽度">
                    <el-input-number v-model="form.browserWindowWidth" :min="800" :max="3840" :step="100" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="浏览器窗口高度">
                    <el-input-number v-model="form.browserWindowHeight" :min="600" :max="2160" :step="100" controls-position="right" />
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="控制台日志上限">
                    <el-input-number v-model="form.browserLogCap" :min="100" :max="5000" :step="100" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="TLS 握手超时(秒)">
                    <el-input-number v-model="form.llmTLSHandshakeTimeout" :min="5" :max="120" controls-position="right" />
                  </el-form-item>
                </el-col>
              </el-row>
            </el-form>
          </el-tab-pane>
        </el-tabs>
      </div>

      <!-- Edit LLM Dialog (shared for global + project) -->
    <el-dialog v-model="editLLMDialog.visible" title="编辑 LLM Provider" width="520px" append-to-body>
      <el-form label-position="top" size="small">
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="名称">
              <el-input v-model="editLLMDialog.data.name" placeholder="my-openai" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="Protocol">
              <el-select v-model="editLLMDialog.data.protocol">
                <el-option label="OpenAI" value="openai" />
                <el-option label="Claude" value="claude" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
        <el-form-item label="API Key">
          <el-input v-model="editLLMDialog.data.apiKey" type="password" show-password />
        </el-form-item>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="模型">
              <el-input v-model="editLLMDialog.data.model" placeholder="gpt-4o" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="Base URL">
              <el-input v-model="editLLMDialog.data.baseUrl" placeholder="https://api.openai.com/v1" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="Temperature">
              <el-slider v-model="editLLMDialog.data.temperature" :min="0" :max="2" :step="0.1" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="最大输出 Token">
              <el-input-number v-model="editLLMDialog.data.maxTokens" :min="256" :max="65536" :step="256" />
            </el-form-item>
          </el-col>
        </el-row>
        <el-row :gutter="12">
          <el-col :span="12">
            <el-form-item label="思考模式">
              <el-switch v-model="editLLMDialog.data.thinking" />
            </el-form-item>
          </el-col>
          <el-col :span="12">
            <el-form-item label="推理强度">
              <el-select v-model="editLLMDialog.data.reasoningEffort" clearable placeholder="auto" :disabled="!editLLMDialog.data.thinking">
                <el-option label="High" value="high" />
                <el-option label="Max" value="max" />
              </el-select>
            </el-form-item>
          </el-col>
        </el-row>
      </el-form>
      <template #footer>
        <el-button @click="editLLMDialog.visible = false">取消</el-button>
        <el-button type="primary" @click="confirmEditLLM">保存</el-button>
      </template>
    </el-dialog>

    <!-- Edit MCP Dialog -->
    <el-dialog v-model="editMCPDialog.visible" title="编辑 MCP Server" width="520px" append-to-body draggable>
      <el-form label-position="top" size="small">
        <el-form-item label="名称">
          <el-input v-model="editMCPDialog.data.name" placeholder="my-database-mcp" />
        </el-form-item>
        <el-form-item label="Server URL">
          <el-input v-model="editMCPDialog.data.url" placeholder="http://localhost:8081/mcp" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="editMCPDialog.data.description" type="textarea" :rows="2" placeholder="可选描述" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="editMCPDialog.data.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editMCPDialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="savingMcp" @click="confirmEditMCP">保存</el-button>
      </template>
    </el-dialog>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" @click="save">保存</el-button>
    </template>
  </el-dialog>
</template>

<script setup>
import { ref, reactive, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { FolderOpened } from '@element-plus/icons-vue'
import { getUserConfig, saveUserConfig } from '../../api/config'


const visible = ref(false)
const tab = ref('llm')

const defaultLLM = () => ({
  name: '', protocol: 'openai', apiKey: '', model: 'deepseek-v4-flash',
  baseUrl: 'https://api.deepseek.com', temperature: 0.7, maxTokens: 4096,
  thinking: true, reasoningEffort: '',
})

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
  // 代码索引
  codeIndexTemperature: 0.1,
  // 工具执行
  toolMaxDepth: 5,
  taskPollIntervalMs: 200,
  // 搜索限制
  searchMaxResults: 200,
  fetchMaxBodySizeKB: 100,
  // 浏览器
  browserWindowWidth: 1280,
  browserWindowHeight: 800,
  browserLogCap: 500,
  // LLM 网络
  llmTLSHandshakeTimeout: 30,
})

// Edit LLM state (shared for global + project)
const editLLMDialog = reactive({
  visible: false,
  index: -1,
  data: defaultLLM(),
})

// Edit MCP state
const defaultMCP = () => ({ name: '', url: '', enabled: true, description: '' })
const editMCPDialog = reactive({
  visible: false,
  index: -1,
  data: defaultMCP(),
})
const savingMcp = ref(false)

function addLLM() {
  editLLMDialog.index = -1
  editLLMDialog.data = defaultLLM()
  editLLMDialog.visible = true
}

function editLLM(index) {
  editLLMDialog.index = index
  editLLMDialog.data = { ...form.llms[index] }
  editLLMDialog.visible = true
}

function confirmEditLLM() {
  const target = form.llms
  if (editLLMDialog.index === -1) {
    target.push({ ...editLLMDialog.data })
  } else {
    target[editLLMDialog.index] = { ...editLLMDialog.data }
  }
  editLLMDialog.visible = false
}

function deleteLLM(index) {
  form.llms.splice(index, 1)
}

function addMCP() {
  editMCPDialog.index = -1
  editMCPDialog.data = defaultMCP()
  editMCPDialog.visible = true
}

function editMCP(index) {
  editMCPDialog.index = index
  editMCPDialog.data = { ...form.mcpServers[index] }
  editMCPDialog.visible = true
}

async function confirmEditMCP() {
  // Validate server name: must start with letter, only letters/digits/underscore
  const name = editMCPDialog.data.name.trim()
  if (!name) {
    ElMessage.warning('MCP Server 名称不能为空')
    return
  }
  if (!/^[a-zA-Z][a-zA-Z0-9_]*$/.test(name)) {
    ElMessage.warning('MCP Server 名称必须以英文字母开头，只能包含字母、数字和下划线')
    return
  }
  const url = editMCPDialog.data.url.trim()
  if (!url) {
    ElMessage.warning('Server URL 不能为空')
    return
  }
  editMCPDialog.data.name = name
  editMCPDialog.data.url = url

  // Always discover tools (both new and edit)
  savingMcp.value = true
  try {
    const result = await window.go.main.App.DiscoverMCPServerTools(name, url)
    const tools = result.tools || []
    const transport = result.transport || 'direct'

    // Build tool list for preview dialog
    const esc = s => String(s || '').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;')
    const toolItems = tools.map((t, i) =>
      `<div style="padding:12px;${i < tools.length - 1 ? 'border-bottom:1px solid #e8e8e8;' : ''}">
         <div style="font-weight:500;font-size:13px;line-height:1.4;">${esc(t.name)}</div>
         <div style="font-size:11px;color:#999;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;line-height:1.4;margin-top:2px;">${esc(t.description || '')}</div>
       </div>`
    ).join('')

    await ElMessageBox.confirm(
      `<div style="width:394px;">
         <div style="margin-bottom:8px;font-size:13px;color:#666;">
           共 <strong>${tools.length}</strong> 个工具，传输方式：<strong>${transport}</strong>
         </div>
         <div style="max-height:235px;overflow-y:auto;border:1px solid #e0e0e0;border-radius:4px;">
           ${toolItems || '<div style="padding:12px;color:#999;">未发现工具</div>'}
         </div>
       </div>`,
      'MCP 工具发现结果',
      {
        dangerouslyUseHTMLString: true,
        width: '420px',
        confirmButtonText: '确认保存',
        cancelButtonText: '取消',
      }
    )

    // User confirmed — save server config with discovered tools and transport
    editMCPDialog.data.discoveredTools = tools
    editMCPDialog.data.transport = transport
    if (editMCPDialog.index === -1) {
      form.mcpServers.push({ ...editMCPDialog.data })
    } else {
      form.mcpServers[editMCPDialog.index] = { ...editMCPDialog.data }
    }
    editMCPDialog.visible = false
  } catch (err) {
    // User clicked cancel/close on messagebox is not an error
    if (err === 'cancel' || err === 'close') return
    const msg = typeof err === 'string' ? err : (err?.message || '验证 MCP Server 失败')
    ElMessage.error(msg)
  } finally {
    savingMcp.value = false
  }
}

async function loadConfig() {
  try {
    // Load user-level config (LLM + global tools + general settings)
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

async function save() {
  try {
    // Basic validation: ensure at least one LLM with required fields
    if (!form.llms || form.llms.length === 0) {
      ElMessage.warning('至少配置一个 LLM')
      return
    }
    for (let i = 0; i < form.llms.length; i++) {
      const llm = form.llms[i]
      if (!llm.name?.trim()) {
        ElMessage.warning(`第 ${i + 1} 个 LLM 缺少名称`)
        return
      }
      if (!llm.model?.trim()) {
        ElMessage.warning(`LLM "${llm.name}" 缺少模型名`)
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
    visible.value = false
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

function open() {
  visible.value = true
  loadConfig()
}

defineExpose({ open })
</script>

<style scoped>
.settings-dialog :deep(.el-dialog) {
  max-height: 680px;
  display: flex;
  flex-direction: column;
}

.settings-dialog :deep(.el-dialog__body) {
  flex: 1;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  padding-top: 8px;
}

.dialog-body-scroll {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.dialog-body-scroll :deep(.el-tabs) {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.dialog-body-scroll :deep(.el-tabs__content) {
  flex: 1;
  overflow-y: auto;
  min-height: 0;
}

.toolbar-actions {
  margin-bottom: 12px;
}

.form-hint {
  font-size: 11px;
  color: var(--text-muted);
  flex: 0 0 100%;
  margin-top: 2px;
}

.chrome-tip {
  font-size: 12px;
  color: var(--el-text-color-secondary, #909399);
  margin: 0;
  padding: 0 4px;
}

.path-input-wrap {
  display: flex;
  gap: 4px;
  width: 100%;
}
.path-input-wrap .el-input {
  flex: 1;
}

.form-description {
  font-size: 12px;
  color: var(--text-muted);
  margin-top: 12px;
  padding: 0 4px;
}

.settings-dialog :deep(.el-form-item) {
  margin-bottom: 6px;
}

.settings-dialog :deep(.el-input-number) {
  width: 100%;
}

.settings-dialog :deep(.el-select) {
  width: 100%;
}
</style>
