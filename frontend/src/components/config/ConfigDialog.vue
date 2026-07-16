<template>
  <el-dialog v-model="visible" title="设置" width="680px" append-to-body destroy-on-close draggable>
    <el-tabs v-model="tab">
      <el-tab-pane label="LLM" name="llm">
        <div class="toolbar-actions">
          <el-button size="small" type="primary" @click="addLLM()">
            <el-icon><Plus /></el-icon> 添加 LLM
          </el-button>
        </div>
        <el-table :data="form.llms" size="small" style="width: 100%" empty-text="未配置 LLM Provider">
          <el-table-column label="#" type="index" width="40" />
          <el-table-column label="名称" prop="name" min-width="120" />
          <el-table-column label="Provider" prop="provider" width="100" />
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
      <el-tab-pane label="常规" name="general">
            <el-form label-position="top" size="small">
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
              <el-divider />
              <el-row :gutter="12">
                <el-col :span="12">
                  <el-form-item label="LLM 重试次数">
                    <el-input-number v-model="form.retryCount" :min="0" :max="10" />
                    <span class="form-hint">0 = LLM 出错不重试</span>
                  </el-form-item>
                </el-col>
                <el-col :span="12">
                  <el-form-item label="重试间隔（秒）">
                    <el-input-number v-model="form.retryDelay" :min="1" :max="120" />
                    <span class="form-hint">重试间等待时间</span>
                  </el-form-item>
                </el-col>
              </el-row>
              <el-divider />
              <el-row :gutter="12">
                <el-col :span="8">
                  <el-form-item label="最大工具迭代次数">
                    <el-input-number v-model="form.maxToolIterations" :min="0" :max="2000" :step="50" />
                    <span class="form-hint">0 = 不限 | 默认 800</span>
                  </el-form-item>
                </el-col>
                <el-col :span="8">
                  <el-form-item label="响应超时（秒）">
                    <el-input-number v-model="form.responseTimeout" :min="0" :max="600" :step="30" />
                    <span class="form-hint">0 = 不超时 | 默认 180</span>
                  </el-form-item>
                </el-col>
                <el-col :span="8">
                  <el-form-item label="流空闲超时（秒）">
                    <el-input-number v-model="form.streamTimeout" :min="0" :max="600" :step="30" />
                    <span class="form-hint">0 = 不超时 | 默认 180</span>
                  </el-form-item>
                </el-col>
              </el-row>
            </el-form>
          </el-tab-pane>
        </el-tabs>

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
            <el-form-item label="Provider">
              <el-select v-model="editLLMDialog.data.provider">
                <el-option label="DeepSeek" value="deepseek" />
                <el-option label="GLM" value="glm" />
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

  // For new MCP servers, validate and discover tools via backend
  if (editMCPDialog.index === -1) {
    savingMcp.value = true
    try {
      const result = await window.go.main.App.DiscoverMCPServerTools(name, url)
      // Attach discovered tools to the server config
      editMCPDialog.data.discoveredTools = result.tools || []
      form.mcpServers.push({ ...editMCPDialog.data })
      editMCPDialog.visible = false
    } catch (err) {
      const msg = typeof err === 'string' ? err : (err?.message || '验证 MCP Server 失败')
      ElMessage.error(msg)
      return
    } finally {
      savingMcp.value = false
    }
  } else {
    // Editing existing server — keep existing discoveredTools
    const existing = form.mcpServers[editMCPDialog.index]
    editMCPDialog.data.discoveredTools = existing?.discoveredTools || []
    form.mcpServers[editMCPDialog.index] = { ...editMCPDialog.data }
    editMCPDialog.visible = false
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
  } catch (e) { console.warn('[ConfigDialog] Failed to load user config:', e) }
}

async function save() {
  try {
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
:deep(.el-dialog__body) {
  padding-top: 8px;
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
</style>
