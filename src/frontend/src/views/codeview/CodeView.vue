<template>
  <div ref="rootRef" class="code-view" :class="{ empty: !tabs.length }">
    <template v-if="tabs.length">
      <div class="tab-panels">
        <div
          v-for="tab in tabs"
          :key="tab.key"
          v-show="tab.key === activeKey"
          class="tab-panel"
          :class="{ 'no-pad': tab.kind === 'file' && noPadTypes.includes(tab.renderType) }"
        >
          <!-- ── 功能页 tab（settings/scenario）── -->
          <component
            v-if="tab.kind !== 'file'"
            :is="specialComponent(tab.kind)"
            class="special-tab"
            v-bind="specialProps(tab)"
          />
          <!-- ── 文件 tab ── -->
          <template v-else>
            <div class="code-header">
              <Select
                v-if="!filetreeVisible"
                v-model="treeSelectValue"
                class="tree-selector"
                :placeholder="$t('common.select_file')"
                filterable
                :options="fileOptions"
                @update:model-value="onTreeSelect"
              />
              <span class="file-path" :title="tab.path">{{ tab.path }}</span>
              <span v-if="tab.deleted" class="deleted-badge">{{ $t('common.deleted_file') }}</span>
              <span class="file-type-tag">{{ tab.renderType }}</span>
              <!-- 截断提示：>512KB 文本经 filesys.content 截断 → 与 HexView「仅显示前 N 字节」同口径（E-35） -->
              <span v-if="showTruncated(tab)" class="truncated-badge">{{ $t('common.content_truncated') }}</span>
              <span v-if="tab.renderType === 'markdown' || tab.renderType === 'html'" class="source-toggle">
                <Button text :type="tab.showSource ? 'primary' : ''" v-mq:[EventNames.codeShowSource].click>{{ $t('common.code') }}</Button>
                <Button text :type="!tab.showSource ? 'primary' : ''" v-mq:[EventNames.codeShowPreview].click>{{ $t('common.preview') }}</Button>
              </span>
            </div>
            <div class="code-body">
              <div v-if="tab.loading" class="loading-state">
                <Icon name="loading" :size="24" class="is-loading" />
                <span>{{ $t('common.loading') }}</span>
              </div>
              <FileViewer
                v-else-if="tab.renderType === 'code' || tab.renderType === 'docx' || tab.renderType === 'xlsx' || tab.renderType === 'pptx'"
                :url="tab.rawUrl"
                :name="tab.name"
                :options="fileViewerOptions(tab)"
                class="file-preview-container"
                :class="{ 'code-preview-pad': tab.renderType === 'code' }"
              />
              <MarkdownRender
                v-else-if="tab.renderType === 'markdown' && !tab.showSource"
                :content="tab.content"
                class="markdown-preview"
              />
              <pre v-else-if="tab.renderType === 'markdown' && tab.showSource" class="source-code"><code>{{ tab.content }}</code></pre>
              <!-- PDF 预览 sandbox 豁免（用户 2026-10-08）：Chromium 原生 PDF 查看器与 sandbox 属性不兼容（加 sandbox 即空白）；PDFium 内嵌 JS 默认禁用，无可利用攻击链。 -->
              <iframe
                v-else-if="tab.renderType === 'pdf'"
                :src="tab.rawUrl"
                class="pdf-preview"
                frameborder="0"
              />
              <iframe
                v-else-if="tab.renderType === 'html' && !tab.showSource"
                :src="tab.rawUrl"
                class="html-preview"
                frameborder="0"
                sandbox="allow-same-origin"
              />
              <pre v-else-if="tab.renderType === 'html' && tab.showSource" class="source-code"><code>{{ tab.content }}</code></pre>
              <div
                v-else-if="tab.renderType === 'image'"
                :ref="(el) => setImgContainer(tab.key, el)"
                class="image-preview-area"
                @wheel.prevent="onImgWheel(tab, $event)"
                @mousedown="onImgMouseDown(tab, $event)"
              >
                <img
                  :src="tab.rawUrl"
                  class="preview-image"
                  :style="imgStyle(tab)"
                  alt="preview"
                  @load="onImgLoad(tab, $event)"
                  draggable="false"
                />
                <span class="image-zoom-label">{{ Math.round(tab.imageZoom * 100) }}%</span>
              </div>
              <audio
                v-else-if="tab.renderType === 'audio'"
                :src="tab.rawUrl"
                controls
                class="media-preview"
              />
              <video
                v-else-if="tab.renderType === 'video'"
                :src="tab.rawUrl"
                controls
                class="media-preview"
              />
              <HexView
                v-else-if="tab.renderType === 'hex'"
                :key="tab.key"
                :path="tab.path"
                class="file-preview-container"
              />
              <PrimitivePanel
                v-else-if="tab.renderType === 'primitive'"
                :key="tab.key"
                :path="tab.path"
                class="primitive-panel"
              />
              <div v-else-if="tab.renderType === 'unsupported'" class="unsupported-state">
                <Icon name="warning-filled" :size="48" color="var(--text-muted)" />
                <p>{{ $t('common.preview_not_available') }}</p>
              </div>
              <pre v-else-if="tab.renderType === 'text'" class="source-code"><code>{{ tab.content }}</code></pre>
            </div>
          </template>
        </div>
      </div>
      <!-- 底部页签栏（公共 TabBar：溢出折叠/右键关闭/双击固定）
           右键菜单四项统一走 MQ（preview-tab-close-*，下方 onMounted 订阅）；此处只接页签自身交互事件 -->
      <TabBar
        v-model="activeKey"
        :tabs="tabItems"
        position="bottom"
        :ctx-topics="previewTabCtxTopics"
        @close="closeTab"
        @pin="pinTabByKey"
      />
    </template>
    <div v-else class="empty-state">
      <Icon name="document" :size="48" color="var(--text-muted)" />
      <p>{{ $t('common.select_file_to_preview') }}</p>
      <p class="hint">{{ $t('common.choose_from_sidebar') }}</p>
    </div>

    <!-- 选中文本浮动操作条：预览区选中文字 → 添加到主 chat 输入框 -->
    <Teleport to="body">
      <div
        v-if="selBar.visible"
        ref="selBarEl"
        class="preview-selection-bar"
        :style="{ left: selBar.x + 'px', top: selBar.y + 'px' }"
        v-mq:[EventNames.previewSelectionToChat].click
      >
        <Icon name="chat-dot-round" :size="12" />
        <span>{{ $t('chat.add_to_chat') }}</span>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import Icon from '../../components/icon/Icon.vue'
import { Button, Tag, Select } from '../../components/ui'
import { FileViewer } from '@file-viewer/vue3'
import '@file-viewer/vue3/dist/file-viewer3.css'
import litePreset from '@file-viewer/preset-lite'
import officePreset from '@file-viewer/preset-office'
import MarkdownRender from '@ashlesss/markstream-vue'
import '@ashlesss/markstream-vue/index.css'
import HexView from './HexView.vue'
import { readFile, getFileTree, getFileTreeChildren, getFileUrl, loadInitData, saveOpenedFiles, setWorkDir } from '../../api/file'
import TabBar from '../../components/tabs/TabBar.vue'
import PrimitivePanel from '../knowledge/PrimitivePanel.vue'
import { isPrimitiveFile } from '../../utils/primitive'
import mq from '../../utils/mq'
import { EventNames } from '../../events/event-names'
import { GuiInitDataKeys } from '../../events/msgkeys'

const baseViewerOptions = { preset: [litePreset, officePreset], theme: 'light' }
// 代码预览：隐藏全局工具栏（缩放/搜索/下载/打印/导出/主题）与 renderer 内部
// 元信息栏（"EXT N lines"），直接显示代码
const codeViewerOptions = {
  preset: [litePreset, officePreset],
  theme: 'light',
  text: { toolbar: false },
  toolbar: { zoom: false, search: false, download: false, print: false, exportHtml: false, theme: false },
}
function fileViewerOptions(tab) {
  return tab.renderType === 'code' ? codeViewerOptions : baseViewerOptions
}

const props = defineProps({
  filetreeVisible: { type: Boolean, default: true },
})

// ── 多 tab 数据模型 ───────────────────────────────────
// file tab:  { key, kind:'file', path, name, fullTitle, renderType, content, loading, showSource, deleted, rawUrl, imageZoom, naturalSize }
// 功能 tab:  { key, kind:'settings'|'scenario', name, fullTitle }
const tabs = ref([])
const activeKey = ref(null)
let keySeq = 0
function nextKey() { return 'tab-' + (++keySeq) }
function basename(path) {
  const parts = String(path).replace(/\\/g, '/').split('/')
  return parts[parts.length - 1] || path
}
function findTab(pred) { return tabs.value.find(pred) }

const activeTab = computed(() => tabs.value.find(t => t.key === activeKey.value) || null)

// ── 公共 TabBar 适配（底部页签栏逻辑抽出为 components/tabs/TabBar.vue）──
const tabItems = computed(() => tabs.value.map(tab => ({
  key: tab.key,
  name: tab.name,
  title: tab.fullTitle || tab.name,
  temporary: !!tab.temporary,
})))
function tabByKey(key) { return tabs.value.find(x => x.key === key) || null }
function pinTabByKey(key) { const tab = tabByKey(key); if (tab) pinTab(tab) }
function closeTabsRightTo(key) {
  const idx = tabs.value.findIndex(x => x.key === key)
  if (idx >= 0) closeTabs(tabs.value.slice(idx + 1).map(x => x.key))
}
function closeTabsOthersTo(key) {
  closeTabs(tabs.value.filter(x => x.key !== key).map(x => x.key))
}
// 预览页签栏右键菜单的 MQ 事件（event-names.js「预览 tab 栏右键菜单」家族）：
// **唯一路径** = TabBar 菜单项 v-mq（经 ctx-topics 注入）→ 本组件订阅（下方 onMounted）；
// 批量关闭动作不再有 emit 直连副本。
const previewTabCtxTopics = {
  closeThis: EventNames.previewTabCloseThis,
  closeRight: EventNames.previewTabCloseRight,
  closeOthers: EventNames.previewTabCloseOthers,
  closeAll: EventNames.previewTabCloseAll,
}
// 右键菜单 MQ 载荷 { key }（右键所在页签）；缺省回落当前激活页签
function ctxMenuKey(e) { return (e && e.key) || activeKey.value }

// ── 功能页 tab（懒加载组件 + i18n 标题）──
const specialComponents = {
  // 设置页（工具栏下拉菜单 → preview 开页；12-数据层）
  'settings-llm': defineAsyncComponent(() => import('../config/SettingsLLMPage.vue')),
  'settings-mcp': defineAsyncComponent(() => import('../config/SettingsMCPPage.vue')),
  'settings-tool-async': defineAsyncComponent(() => import('../config/SettingsToolAsyncPage.vue')),
  'settings-tool-sandbox': defineAsyncComponent(() => import('../config/SettingsToolSandboxPage.vue')),
  'settings-paths': defineAsyncComponent(() => import('../config/SettingsPathsPage.vue')),
  'settings-params': defineAsyncComponent(() => import('../config/SettingsParamsPage.vue')),
  'settings-project': defineAsyncComponent(() => import('../preview/ProjectConfig.vue')),
  scenario: defineAsyncComponent(() => import('../scenario/ScenarioDialogContent.vue')),
  // 「用户偏好」只读预览（左侧「项目记忆」点击 user 级条目 → 预览区，与项目级一致，不再弹窗）
  'memory-user-pref': defineAsyncComponent(() => import('../memory/UserPrefView.vue')),
}
function specialComponent(kind) { return specialComponents[kind] || null }

// 功能页组件附加 props：项目配置页支持定位到具体页签（innerTab + innerTabNonce）；
// 其它功能页不注入额外属性（避免无谓的透传属性落到根节点）。
function specialProps(tab) {
  if (tab.kind === 'settings-project') {
    return { innerTab: tab.innerTab || '', innerTabNonce: tab.innerTabNonce || 0 }
  }
  return {}
}

// 功能 tab 标题（i18n）
function specialTitle(kind) {
  switch (kind) {
    case 'settings-llm': return t('config.page.llm')
    case 'settings-mcp': return t('config.page.mcp')
    case 'settings-tool-async': return t('config.page.toolAsync')
    case 'settings-tool-sandbox': return t('config.page.toolSandbox')
    case 'settings-paths': return t('config.page.paths')
    case 'settings-params': return t('config.page.params')
    case 'settings-project': return t('config.page.project')
    case 'scenario': return t('scenario.title')
    case 'memory-user-pref': return t('projectConfig.memory_user_pref')
    default: return kind
  }
}

function openSpecialTab(kind, title, innerTab) {
  let tab = findTab(t => t.kind === kind)
  if (tab) {
    activeKey.value = tab.key
    // 已开页：可选 innerTab（如项目配置页签）→ 更新请求（nonce 递增以触发页内切换）
    if (innerTab) {
      tab.innerTab = innerTab
      tab.innerTabNonce = (tab.innerTabNonce || 0) + 1
    }
    return
  }
  tab = reactive({
    key: nextKey(),
    kind,
    name: title || specialTitle(kind),
    fullTitle: title || specialTitle(kind),
  })
  if (innerTab) {
    tab.innerTab = innerTab
    tab.innerTabNonce = 1
  }
  tabs.value.push(tab)
  activeKey.value = tab.key
}

// ── 文件树下拉（filetreeVisible=false 时的文件选择器）──
const flatFiles = ref([])
const treeSelectValue = ref('')
const fileOptions = computed(() => flatFiles.value.map(f => ({ label: f.name, value: f.path })))

// 文件选择器（filetreeVisible=false 时的文件下拉）：filesys.list{work_dir,path} 只返回
// 浅层子项，这里递归展开全部子目录，供下拉选择任意文件。
async function flattenTreeAsync(nodes, list = []) {
  for (const node of nodes || []) {
    if (!node.is_dir) {
      list.push({ path: node.path, name: node.name })
      continue
    }
    try {
      const res = await getFileTreeChildren(node.path)
      await flattenTreeAsync(res.children || [], list)
    } catch (e) {
      // 子目录读取失败：跳过该目录（不阻塞整个文件列表）
    }
  }
  return list
}

async function loadFileTree() {
  try {
    const res = await getFileTree()
    flatFiles.value = []
    await flattenTreeAsync(res.tree?.children || [], flatFiles.value)
  } catch (e) {
    console.error('Failed to load file tree:', e)
  }
}

function onTreeSelect(path) {
  if (path) mq.emit(EventNames.fileOpen, { path })
}

// ── renderType 判定 ───────────────────────────────────
const codeExtensions = ['js', 'ts', 'py', 'go', 'java', 'css', 'json', 'xml', 'yaml', 'yml', 'sh', 'bat', 'cmd', 'ps1', 'psm1', 'vbs', 'sql', 'rs', 'vue', 'cpp', 'c', 'h', 'hpp', 'swift', 'kt', 'rb', 'ruby', 'php', 'pl', 'r', 'm', 'properties', 'log']
const noPadTypes = ['code', 'pdf', 'docx', 'xlsx', 'pptx', 'image', 'audio', 'video', 'markdown', 'html', 'primitive']
const hexExtensions = ['exe', 'dll', 'so', 'bin', 'obj', 'lib', 'dylib', 'class', 'pyc', 'o', 'a', 'out', 'wasm', 'dat']
const unsupportedExtensions = ['zip', '7z', 'rar', 'tar', 'gz']

function getBinaryType(ext) {
  const docx = ['docx', 'doc']
  const xlsx = ['xlsx', 'xls']
  const pptx = ['pptx', 'ppt']
  const images = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico', 'bmp']
  const audio = ['mp3', 'wav', 'wma', 'ogg']
  const video = ['mp4', 'webm', 'mkv', 'avi', 'mov']
  if (docx.includes(ext)) return 'docx'
  if (xlsx.includes(ext)) return 'xlsx'
  if (pptx.includes(ext)) return 'pptx'
  if (images.includes(ext)) return 'image'
  if (audio.includes(ext)) return 'audio'
  if (video.includes(ext)) return 'video'
  return ''
}

function getExtension(path) { return path?.split('.').pop()?.toLowerCase() || '' }

function isBackupFile(path) {
  if (!path) return false
  // 后缀/前缀比较统一小写（spec §5：所有扩展名比较均 .toLowerCase()；E-15）。
  const name = (path.split('\\').pop()?.split('/').pop() || '').toLowerCase()
  return name.endsWith('~') || name.startsWith('~$') || name.endsWith('.swp') || name.endsWith('.swo')
}

function computeRenderType(path) {
  if (path && path.startsWith('db://')) return 'none'
  if (isBackupFile(path)) return 'unsupported'
  if (isPrimitiveFile(path)) return 'primitive' // *.tool.md / *.skill.md / *.prompt.md / *.resource.md / *.agent.md（智能体编辑器）
  const ext = getExtension(path)
  if (ext === 'md') return 'markdown'
  if (ext === 'pdf') return 'pdf'
  if (codeExtensions.includes(ext)) return 'code'
  if (ext === 'html' || ext === 'htm') return 'html'
  const binType = getBinaryType(ext)
  if (binType) return binType
  if (hexExtensions.includes(ext)) return 'hex'
  if (unsupportedExtensions.includes(ext)) return 'unsupported'
  return 'text'
}

// ── 打开 / 加载 ───────────────────────────────────────
async function handleFileOpen(event) {
  const path = event?.path
  if (!path) return

  // db:// 虚拟路径（数据库查看器已移除）：无可渲染内容，忽略
  if (path.startsWith('db://')) return

  const temporary = event?.temporary === true // 单击=临时页签（FP）；双击/其他=固定

  const existing = findTab(tab => tab.kind === 'file' && tab.path === path)
  if (existing) {
    // 双击打开已存在的临时页签 → 转为固定
    if (!temporary && existing.temporary) existing.temporary = false
    activeKey.value = existing.key
    return
  }

  // 临时页签规则：每个 preview 同时最多一个临时页签（覆盖旧临时页签）
  if (temporary) {
    const tmp = tabs.value.find(t => t.kind === 'file' && t.temporary)
    if (tmp) closeTab(tmp.key)
  }

  const tab = reactive({
    key: nextKey(),
    kind: 'file',
    path,
    name: basename(path),
    fullTitle: path,
    temporary,
    renderType: computeRenderType(path),
    loading: false,
    content: '',
    truncated: false,
    showSource: false,
    deleted: false,
    rawUrl: getFileUrl(path),
    imageZoom: 1,
    naturalSize: { w: 0, h: 0 },
  })
  tabs.value.push(tab)
  activeKey.value = tab.key

  // 打开文件持久化：**立即做一次全量写**（I-81：原先"单元素覆盖写 + 300ms 去抖全量写"并发，
  // 若在该窗口内进程被杀，键会退化为只剩最后打开的一个文件 → 下次启动页签丢失）。
  // db:// 虚拟文件由 flushOpenedFiles 内部过滤。
  flushOpenedFiles()
  await loadFileTab(tab)
}

// 双击临时页签 tab → 转为固定页签（FP：双击临时页签的 tab 也可转固定）
function pinTab(tab) {
  if (tab && tab.kind === 'file' && tab.temporary) {
    tab.temporary = false
  }
}

// showTruncated 是否展示截断提示：仅对**由 tab.content 渲染**的类型（markdown/html/text）；
// code 预览走 FileViewer 经 /show 取全量字节流，不受 filesys.content 截断影响（E-35）。
const CONTENT_RENDER_TYPES = ['markdown', 'html', 'text']
function showTruncated(tab) {
  return tab.truncated && CONTENT_RENDER_TYPES.includes(tab.renderType)
}

async function loadFileTab(tab) {
  const rt = tab.renderType
  if (rt === 'none' || rt === 'primitive') return
  const ext = getExtension(tab.path)
  const binType = getBinaryType(ext)
  const isPdf = ext === 'pdf'
  if (binType || isPdf || hexExtensions.includes(ext) || unsupportedExtensions.includes(ext)) return
  tab.loading = true
  try {
    const result = await readFile(tab.path)
    if (!tabs.value.includes(tab)) return // tab 已关闭，丢弃
    tab.content = result.content || ''
    // >512KB 文本由 filesys.content 截断（result.truncated=true）→ 透出提示，避免误以为看到完整文件（E-35）
    tab.truncated = !!result.truncated
  } catch (err) {
    console.error('Failed to read file:', err)
    tab.content = ''
    tab.truncated = false
  } finally {
    if (tabs.value.includes(tab)) tab.loading = false
  }
}

function handleTabOpen(event) {
  const kind = event?.kind
  if (!kind) return
  openSpecialTab(kind, event.title, event.tab)
}

function closeTab(key) {
  const idx = tabs.value.findIndex(t => t.key === key)
  if (idx < 0) return
  tabs.value.splice(idx, 1)
  if (activeKey.value === key) {
    const next = tabs.value[Math.min(idx, tabs.value.length - 1)]
    activeKey.value = next ? next.key : null
  }
  persistOpenedFiles()
}

// 批量关闭（右键菜单）：保持最后一个 tab 激活
function closeTabs(keys) {
  const set = new Set(keys)
  if (!set.size) return
  const removingActive = set.has(activeKey.value)
  tabs.value = tabs.value.filter(t => !set.has(t.key))
  if (removingActive) {
    const next = tabs.value[tabs.value.length - 1]
    activeKey.value = next ? next.key : null
  }
  persistOpenedFiles()
}

function closeAllTabs() {
  tabs.value.splice(0, tabs.value.length)
  activeKey.value = null
  persistOpenedFiles()
}

let persistTimer = null
// flushOpenedFiles 立即把当前页签列表整体落库（取消待执行去抖，保证"最后一次写"是完整列表）。
function flushOpenedFiles() {
  clearTimeout(persistTimer)
  persistTimer = null
  const paths = tabs.value
    .filter(t => t.kind === 'file' && !t.path.startsWith('db://'))
    .map(t => t.path)
  saveOpenedFiles(paths).catch(() => {})
}
// persistOpenedFiles 高频变更（关/切页签）走 300ms 去抖，最终仍是全量写。
function persistOpenedFiles() {
  clearTimeout(persistTimer)
  persistTimer = setTimeout(flushOpenedFiles, 300)
}

// ── 图片缩放 / 平移（per-tab 状态）──
const imgContainers = new Map()
function setImgContainer(key, el) {
  if (el) imgContainers.set(key, el)
  else imgContainers.delete(key)
}

function imgStyle(tab) {
  const z = tab.imageZoom
  const { w, h } = tab.naturalSize
  if (w && h) {
    return { width: `${w * z}px`, height: `${h * z}px`, maxWidth: 'none', maxHeight: 'none' }
  }
  return { maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }
}

function onImgLoad(tab, e) {
  tab.naturalSize = { w: e.target.naturalWidth, h: e.target.naturalHeight }
}

function onImgWheel(tab, e) {
  const delta = e.deltaY > 0 ? -0.05 : 0.05
  tab.imageZoom = Math.max(0.1, Math.min(5, Math.round((tab.imageZoom + delta) * 100) / 100))
}

const imgDrag = ref(null)
function onImgMouseDown(tab, e) {
  const container = imgContainers.get(tab.key)
  if (!container) return
  if (e.button !== 0) return
  imgDrag.value = {
    key: tab.key,
    startX: e.clientX,
    startY: e.clientY,
    scrollLeft: container.scrollLeft,
    scrollTop: container.scrollTop,
  }
  container.style.cursor = 'grabbing'
  window.addEventListener('mousemove', onImgWindowMouseMove)
  window.addEventListener('mouseup', onImgMouseUp)
}

function onImgWindowMouseMove(e) {
  if (!imgDrag.value) return
  const container = imgContainers.get(imgDrag.value.key)
  if (!container) return
  const dx = e.clientX - imgDrag.value.startX
  const dy = e.clientY - imgDrag.value.startY
  container.scrollLeft = imgDrag.value.scrollLeft - dx
  container.scrollTop = imgDrag.value.scrollTop - dy
}

function onImgMouseUp() {
  if (imgDrag.value) {
    const container = imgContainers.get(imgDrag.value.key)
    if (container) container.style.cursor = ''
  }
  imgDrag.value = null
  window.removeEventListener('mousemove', onImgWindowMouseMove)
  window.removeEventListener('mouseup', onImgMouseUp)
}

// ── 文件变更刷新（多 tab：匹配 path 的所有 file tab）──
// filesys.changed 载荷：{work_dir, path, operation, children?}（61-消息一览 §2.4）
// children = 目录批次，非本视图关注（单文件才处理）。
// I-48：tab.path 来自 file-open（前端拼接，盘符可能大写），事件 path 来自
// watcher（filepath.ToSlash，盘符大小写随 work_dir 声明方）→ 直接 === 可能不命中。
// Windows 路径大小写不敏感、分隔符混用，匹配前统一正斜杠 + 小写比较（归一化 key）。
function normPathKey(p) {
  return String(p || '').replace(/\\/g, '/').toLowerCase()
}
let changedSeq = 0
async function onFileChanged(data) {
  if (!data || Array.isArray(data.children)) return
  const changedPath = data?.path
  if (!changedPath) return
  const ext = getExtension(changedPath)
  if (getBinaryType(ext) || ext === 'pdf' || hexExtensions.includes(ext) || unsupportedExtensions.includes(ext)) return
  const changedKey = normPathKey(changedPath)
  const target = tabs.value.find(tab => tab.kind === 'file' && normPathKey(tab.path) === changedKey && tab.renderType !== 'primitive')
  if (!target) return

  // op 映射：operation 'remove' ↔ 旧 'deleted'
  const op = data.operation || data.op
  if (op === 'remove' || op === 'deleted') {
    target.deleted = true
    return
  }

  const seq = ++changedSeq
  try {
    const result = await readFile(target.path)
    if (seq !== changedSeq) return
    const newContent = result.content || ''
    target.truncated = !!result.truncated
    if (newContent !== target.content) {
      target.content = newContent
    }
  } catch (err) {
    console.error('[CodeView] failed to reload changed file:', err)
  }
}

// ── 预览区选中文本 → 主 chat（浮动操作条 + 拖拽，复用 chat-insert-text 通道）──
const rootRef = ref(null)
const selBarEl = ref(null)
const selBar = reactive({ visible: false, x: 0, y: 0, text: '' })

const DROP_SELECTOR = '[data-drop-target="chat-input"]'
let selTimer = null
let dragCandidate = null // 拖拽候选（mousedown 落在选区上）
let dragText = ''
let dragGhostEl = null // 跟随鼠标的幽灵浮层
let dragTargetEl = null // 当前高亮的 drop 目标

// 选区是否完全位于预览区根节点内
function isSelectionInside(container) {
  const sel = window.getSelection()
  if (!sel || sel.isCollapsed || !sel.rangeCount) return false
  let anchor = sel.anchorNode
  let focus = sel.focusNode
  if (!anchor || !focus) return false
  if (anchor.nodeType === Node.TEXT_NODE) anchor = anchor.parentNode
  if (focus.nodeType === Node.TEXT_NODE) focus = focus.parentNode
  return container.contains(anchor) && container.contains(focus)
}

// 鼠标坐标是否落在选区矩形内（按住选中文本才能拖拽）
function isPointInSelection(x, y) {
  const sel = window.getSelection()
  if (!sel || sel.isCollapsed || !sel.rangeCount) return false
  for (let i = 0; i < sel.rangeCount; i++) {
    const rects = sel.getRangeAt(i).getClientRects()
    for (const r of rects) {
      if (x >= r.left && x <= r.right && y >= r.top && y <= r.bottom) return true
    }
  }
  return false
}

function hideSelBar() {
  selBar.visible = false
  selBar.text = ''
}

function updateSelBar() {
  if (dragGhostEl) return
  const root = rootRef.value
  const sel = window.getSelection()
  if (!root || root.classList.contains('empty') || !sel || sel.isCollapsed || !sel.rangeCount) {
    hideSelBar()
    return
  }
  if (!isSelectionInside(root)) {
    hideSelBar()
    return
  }
  const text = sel.toString()
  if (!text.trim()) {
    hideSelBar()
    return
  }
  const rect = sel.getRangeAt(0).getBoundingClientRect()
  if (!rect || (!rect.width && !rect.height)) {
    hideSelBar()
    return
  }
  selBar.text = text
  const barW = 116
  selBar.x = Math.max(4, Math.min(rect.left + rect.width / 2 - barW / 2, window.innerWidth - barW - 8))
  selBar.y = Math.max(4, rect.top - 34)
  selBar.visible = true
}

function onSelectionChange() {
  clearTimeout(selTimer)
  selTimer = setTimeout(updateSelBar, 120)
}

function clearPreviewSelection() {
  const sel = window.getSelection()
  if (sel) sel.removeAllRanges()
}

// 浮动操作条「添加到对话」→ 插入主 chat 输入框
function onSelectionToChat() {
  if (selBar.text) {
    mq.emit(EventNames.chatInsertText, { text: selBar.text })
  }
  clearPreviewSelection()
  hideSelBar()
}

// ── 拖拽：按住选中文本拖动到 chat 输入框 ──
function onWinMouseDown(e) {
  // 点击浮动操作条自身：不隐藏、不拦截（让 click 正常触发）
  if (selBarEl.value && selBarEl.value.contains(e.target)) return
  hideSelBar()
  if (e.button !== 0) return
  const root = rootRef.value
  if (!root || root.classList.contains('empty') || !root.contains(e.target)) return
  const sel = window.getSelection()
  if (!sel || sel.isCollapsed || !sel.rangeCount) return
  if (!isSelectionInside(root)) return
  if (!isPointInSelection(e.clientX, e.clientY)) return
  // 按住选中文本：阻止浏览器取消选择，进入拖拽候选
  dragCandidate = { x: e.clientX, y: e.clientY }
  dragText = sel.toString()
  e.preventDefault()
}

function onWinMouseMove(e) {
  if (!dragCandidate) return
  const dx = e.clientX - dragCandidate.x
  const dy = e.clientY - dragCandidate.y
  if (!dragGhostEl && Math.hypot(dx, dy) >= 5) {
    startDragGhost(e)
  }
  if (dragGhostEl) {
    dragGhostEl.style.left = e.clientX + 'px'
    dragGhostEl.style.top = e.clientY + 'px'
    updateDropTarget(e)
  }
}

function startDragGhost(e) {
  const ghost = document.createElement('div')
  ghost.className = 'preview-drag-ghost'
  const preview = dragText.length > 60 ? dragText.slice(0, 60) + '…' : dragText
  ghost.textContent = '『' + preview + '』'
  ghost.style.left = e.clientX + 'px'
  ghost.style.top = e.clientY + 'px'
  document.body.appendChild(ghost)
  dragGhostEl = ghost
  hideSelBar()
  clearPreviewSelection()
  document.body.style.userSelect = 'none'
}

function updateDropTarget(e) {
  const el = document.elementFromPoint(e.clientX, e.clientY)
  const target = el ? el.closest(DROP_SELECTOR) : null
  if (target !== dragTargetEl) {
    if (dragTargetEl) dragTargetEl.classList.remove('drag-over')
    dragTargetEl = target
    if (dragTargetEl) dragTargetEl.classList.add('drag-over')
  }
}

function onWinMouseUp(e) {
  if (dragGhostEl) {
    const el = document.elementFromPoint(e.clientX, e.clientY)
    const target = el ? el.closest(DROP_SELECTOR) : null
    if (target && dragText) {
      mq.emit(EventNames.chatInsertText, { text: dragText })
    }
    cleanupDrag()
  }
  dragCandidate = null
}

function onWinScroll() {
  if (!dragGhostEl) hideSelBar()
}

function cleanupDrag() {
  if (dragGhostEl) {
    dragGhostEl.remove()
    dragGhostEl = null
  }
  if (dragTargetEl) {
    dragTargetEl.classList.remove('drag-over')
    dragTargetEl = null
  }
  document.body.style.userSelect = ''
  dragText = ''
}

// ── 事件订阅 ──────────────────────────────────────────
const _mqUnsubs = []

onMounted(() => {
  _mqUnsubs.push(mq.on(EventNames.fileOpen, handleFileOpen))
  _mqUnsubs.push(mq.on(EventNames.fileChanged, onFileChanged))
  _mqUnsubs.push(mq.on(EventNames.previewTabOpen, handleTabOpen))
  // 预览页签栏右键菜单（唯一路径：菜单 v-mq → 此处订阅，四项齐备；同时供测试/命令驱动）
  _mqUnsubs.push(mq.on(EventNames.previewTabCloseAll, closeAllTabs))
  _mqUnsubs.push(mq.on(EventNames.previewTabCloseThis, e => closeTab(ctxMenuKey(e))))
  _mqUnsubs.push(mq.on(EventNames.previewTabCloseRight, e => closeTabsRightTo(ctxMenuKey(e))))
  _mqUnsubs.push(mq.on(EventNames.previewTabCloseOthers, e => closeTabsOthersTo(ctxMenuKey(e))))
  // 头部按钮事件化：作用于当前激活 tab
  _mqUnsubs.push(mq.on(EventNames.codeShowSource, () => { if (activeTab.value) activeTab.value.showSource = true }))
  _mqUnsubs.push(mq.on(EventNames.codeShowPreview, () => { if (activeTab.value) activeTab.value.showSource = false }))
  // 预览区选中文本 → 主 chat
  _mqUnsubs.push(mq.on(EventNames.previewSelectionToChat, onSelectionToChat))
  window.addEventListener('mousedown', onWinMouseDown, true)
  window.addEventListener('mousemove', onWinMouseMove, true)
  window.addEventListener('mouseup', onWinMouseUp, true)
  document.addEventListener('selectionchange', onSelectionChange)
  window.addEventListener('scroll', onWinScroll, true)
  window.addEventListener('blur', cleanupDrag)
  loadFileTree()
  // 启动恢复上次打开的文件（多 tab 列表；兼容旧版单个 openedFile）
  loadInitData().then(r => {
    const workDir = r && r[GuiInitDataKeys.workDir]
    if (workDir) setWorkDir(String(workDir).replace(/\\/g, '/'))
    const opened = r && r[GuiInitDataKeys.openedFiles]
    const single = r && r[GuiInitDataKeys.openedFile]
    const files = (opened && opened.length) ? opened : (single ? [single] : [])
    files.forEach(p => mq.emit(EventNames.fileOpen, { path: p }))
  }).catch(() => {})
})

onUnmounted(() => {
  for (const fn of _mqUnsubs) fn()
  _mqUnsubs.length = 0
  onImgMouseUp()
  window.removeEventListener('mousedown', onWinMouseDown, true)
  window.removeEventListener('mousemove', onWinMouseMove, true)
  window.removeEventListener('mouseup', onWinMouseUp, true)
  document.removeEventListener('selectionchange', onSelectionChange)
  window.removeEventListener('scroll', onWinScroll, true)
  window.removeEventListener('blur', cleanupDrag)
  clearTimeout(selTimer)
  cleanupDrag()
})
</script>

<style scoped>
.code-view {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--bg-primary);
}

.code-view.empty {
  align-items: center;
  justify-content: center;
}

.tab-panels {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.tab-panel {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.tab-panel.no-pad .code-body {
  padding: 0;
}

.code-header {
  display: flex;
  align-items: center;
  padding: 6px 16px;
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
  font-size: 13px;
  color: var(--text-secondary);
  flex-shrink: 0;
  gap: 8px;
}

.file-path {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tree-selector {
  width: 180px;
  margin-right: 12px;
  flex-shrink: 0;
}

.file-type-tag {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--bg-surface);
  color: var(--text-muted);
  text-transform: uppercase;
  flex-shrink: 0;
}

.deleted-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--danger);
  color: var(--bg-primary);
  flex-shrink: 0;
}

.truncated-badge {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 3px;
  background: var(--warning, #e6a23c);
  color: var(--bg-primary);
  flex-shrink: 0;
}

.db-config-tag {
  flex-shrink: 0;
}

.source-toggle {
  display: inline-flex;
  gap: 2px;
  margin-left: auto;
  flex-shrink: 0;
}

.code-body {
  flex: 1;
  min-height: 0;
  padding: 8px;
  overflow: auto;
  display: flex;
  flex-direction: column;
}

.special-tab {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 12px 16px;
  background: var(--bg-primary);
}

.loading-state {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 40px 0;
  color: var(--text-muted);
  font-size: 13px;
}

.markdown-preview {
  flex: 1;
  min-height: 0;
  overflow: auto;
  /* 预览内容左右留 0.5em，避免文字贴边 */
  padding: 0 0.5em;
}

.source-code {
  flex: 1;
  min-height: 0;
  margin: 0;
  padding: 12px;
  overflow: auto;
  font-family: var(--font-mono, Consolas, monospace);
  font-size: 13px;
  line-height: 1.6;
  color: var(--text-primary);
  background: var(--bg-primary);
  white-space: pre-wrap;
  word-break: break-all;
}

.pdf-preview,
.html-preview {
  flex: 1;
  min-height: 0;
  width: 100%;
  border: none;
  background: var(--bg-surface);
}

.image-preview-area {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  background: var(--bg-secondary);
  cursor: grab;
}

.image-preview-area:active {
  cursor: grabbing;
}

.preview-image {
  display: block;
  user-select: none;
  -webkit-user-drag: none;
}

.image-zoom-label {
  position: absolute;
  right: 10px;
  bottom: 10px;
  font-size: 12px;
  color: var(--text-muted);
  background: var(--bg-surface);
  padding: 2px 8px;
  border-radius: 3px;
  pointer-events: none;
}

.media-preview {
  max-width: 100%;
  margin: auto;
}

.file-preview-container {
  flex: 1;
  min-height: 0;
}

/* 代码预览左右半个字留白 */
.code-preview-pad {
  padding: 0 0.5em;
}

/* ── 底部 tab 栏 ── */
.preview-tabs {
  display: flex;
  align-items: center;
  gap: 0;
  padding: 2px 6px;
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
  flex-shrink: 0;
  height: 26px;
  position: relative;
}

/* 超出容器宽度的 tab：absolute 脱离布局（不占位不滚动），visibility 隐藏，
   保留布局框使 offsetWidth 可测（recompute 基于真实宽度收敛） */
.preview-tab.tabs-hidden {
  position: absolute;
  visibility: hidden;
  pointer-events: none;
  z-index: -1;
}

/* tab 过多时折叠到右侧 "..."（ResizeObserver 计算可见数，不滚动） */
.preview-tabs-inner {
  display: flex;
  align-items: center;
  gap: 2px;
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.preview-tabs-more {
  flex-shrink: 0;
  width: 30px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-secondary);
}
.preview-tabs-more:hover,
.preview-tabs-more.open {
  background: var(--bg-hover, rgba(0, 0, 0, 0.06));
  color: var(--text-primary);
}

.preview-tabs-more-pop {
  position: fixed;
  right: 12px;
  bottom: 34px;
  width: 260px;
  max-height: 320px;
  display: flex;
  flex-direction: column;
  background: var(--bg-surface, #fff);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.14);
  z-index: 3000;
  overflow: hidden;
}
.more-pop-title {
  flex-shrink: 0;
  padding: 7px 12px;
  font-size: 12px;
  color: var(--text-secondary);
  border-bottom: 1px solid var(--border);
}
/* 最多显示约 10 条，超出滚动 */
.more-pop-list {
  overflow-y: auto;
  max-height: 280px;
}
.more-tab-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  cursor: pointer;
  font-size: 13px;
  color: var(--text-secondary);
}
.more-tab-item:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
}
.more-tab-item.active {
  color: var(--accent);
}
.more-tab-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.more-tab-close {
  width: 14px;
  height: 14px;
  line-height: 12px;
  text-align: center;
  border-radius: 3px;
  color: var(--text-secondary);
  flex-shrink: 0;
}
.more-tab-close:hover {
  background: rgba(0, 0, 0, 0.08);
  color: var(--danger, #e53e3e);
}

.preview-tab {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 8px;
  height: 22px;
  max-width: 180px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 12px;
  color: var(--text-secondary);
  background: transparent;
  flex-shrink: 0;
  border: 1px solid transparent;
  white-space: nowrap;
}

/* 临时页签标题用斜体区分（FP：单击打开的页签为临时页签） */
.preview-tab.is-temporary .preview-tab-name {
  font-style: italic;
}

.preview-tab:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
}

.preview-tab.active {
  background: var(--bg-surface);
  color: var(--text-primary);
  border-color: var(--border);
}

.preview-tab-name {
  overflow: hidden;
  text-overflow: ellipsis;
}

.preview-tab-close {
  width: 14px;
  height: 14px;
  line-height: 12px;
  text-align: center;
  border-radius: 3px;
  font-size: 12px;
  color: var(--text-muted);
  flex-shrink: 0;
  /* 默认隐藏，仅在 hover 标签时显示，避免误点关闭 */
  visibility: hidden;
  opacity: 0;
  transition: opacity 0.12s;
}

.preview-tab:hover .preview-tab-close {
  visibility: visible;
  opacity: 1;
}

.preview-tab-close:hover {
  background: var(--danger);
  color: var(--bg-primary);
  visibility: visible;
  opacity: 1;
}

/* tab 右键菜单 */
.tab-context-menu {
  position: fixed;
  z-index: 1000;
  min-width: 140px;
  background: var(--bg-surface);
  border: 1px solid var(--border);
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.15);
  padding: 4px;
}

.tab-context-item {
  padding: 6px 12px;
  border-radius: 4px;
  font-size: 13px;
  color: var(--text-primary);
  cursor: pointer;
  white-space: nowrap;
}

.tab-context-item:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.06));
}

.tab-context-item.danger {
  color: var(--danger);
}

.tab-context-item.danger:hover {
  background: color-mix(in srgb, var(--danger) 10%, transparent);
}

.tab-context-sep {
  height: 1px;
  margin: 4px 8px;
  background: var(--border);
}

.unsupported-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
  font-size: 13px;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--text-muted);
  font-size: 13px;
}

.empty-state .hint {
  font-size: 12px;
  color: var(--text-muted);
  opacity: 0.7;
}

/* 浮动条：背景用主题强调色 token（原引用未定义 token `--accent-color` + 兜底 #409eff，
   恒落硬编码，白字仅 2.78:1；改 `--accent` + 主题最深文字色后三主题均 ≥4.5 */
.preview-selection-bar {
  position: fixed;
  z-index: 3000;
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border-radius: 6px;
  background: var(--accent);
  color: var(--bg-primary);
  font-size: 12px;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
  user-select: none;
  white-space: nowrap;
}

.preview-selection-bar:hover {
  filter: brightness(1.08);
}
</style>

<!-- 拖拽幽灵层为运行时动态创建（无 scoped 属性），需全局样式 -->
<style>
.preview-drag-ghost {
  position: fixed;
  z-index: 3001;
  max-width: 320px;
  padding: 5px 10px;
  border-radius: 6px;
  background: var(--accent);
  color: var(--bg-primary);
  font-size: 12px;
  line-height: 1.5;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.25);
  pointer-events: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  transform: translate(6px, -8px);
  opacity: 0.92;
  font-family: var(--font-mono, Consolas, monospace);
}
</style>
