import { createI18n } from 'vue-i18n'
import { saveUIState } from '../api/file'

// Import all locale modules statically
import zhCommon from '../locales/zh-CN/common.json'
import zhToolbar from '../locales/zh-CN/toolbar.json'
import zhScenario from '../locales/zh-CN/scenario.json'
import zhConfig from '../locales/zh-CN/config.json'
import zhStatusBar from '../locales/zh-CN/statusBar.json'
import zhDialog from '../locales/zh-CN/dialog.json'
import zhChat from '../locales/zh-CN/chat.json'
import zhFileTree from '../locales/zh-CN/fileTree.json'
import zhProjectConfig from '../locales/zh-CN/projectConfig.json'
import zhCodeIndex from '../locales/zh-CN/codeIndex.json'
import zhSecurity from '../locales/zh-CN/security.json'
import zhKnowledgeList from '../locales/zh-CN/knowledgeList.json'
import zhTaskView from '../locales/zh-CN/taskView.json'
import zhHistoryConfig from '../locales/zh-CN/historyConfig.json'
import zhPluginFailure from '../locales/zh-CN/pluginFailure.json'
import zhConfigIO from '../locales/zh-CN/configIO.json'
import zhMemoryIO from '../locales/zh-CN/memoryIO.json'
import zhJsonSchema from '../locales/zh-CN/jsonSchema.json'
import zhWizard from '../locales/zh-CN/wizard.json'
import zhPromptVars from '../locales/zh-CN/promptVars.json'

import enCommon from '../locales/en-US/common.json'
import enToolbar from '../locales/en-US/toolbar.json'
import enScenario from '../locales/en-US/scenario.json'
import enConfig from '../locales/en-US/config.json'
import enStatusBar from '../locales/en-US/statusBar.json'
import enDialog from '../locales/en-US/dialog.json'
import enChat from '../locales/en-US/chat.json'
import enFileTree from '../locales/en-US/fileTree.json'
import enProjectConfig from '../locales/en-US/projectConfig.json'
import enCodeIndex from '../locales/en-US/codeIndex.json'
import enSecurity from '../locales/en-US/security.json'
import enKnowledgeList from '../locales/en-US/knowledgeList.json'
import enTaskView from '../locales/en-US/taskView.json'
import enHistoryConfig from '../locales/en-US/historyConfig.json'
import enPluginFailure from '../locales/en-US/pluginFailure.json'
import enConfigIO from '../locales/en-US/configIO.json'
import enMemoryIO from '../locales/en-US/memoryIO.json'
import enJsonSchema from '../locales/en-US/jsonSchema.json'
import enWizard from '../locales/en-US/wizard.json'
import enPromptVars from '../locales/en-US/promptVars.json'

// Priority: localStorage > default
function getInitialLocale() {
  try {
    const saved = localStorage.getItem('chonkpilot-locale')
    if (saved) return saved
  } catch (_) {}
  return 'zh-CN'
}

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: getInitialLocale(),
  fallbackLocale: 'zh-CN',
  messages: {
    'zh-CN': {
      common: zhCommon,
      toolbar: zhToolbar,
      scenario: zhScenario,
      config: zhConfig,
      dialog: zhDialog,
      chat: zhChat,
      statusBar: zhStatusBar,
      fileTree: zhFileTree,
      projectConfig: zhProjectConfig,
      codeIndex: zhCodeIndex,
      security: zhSecurity,
      knowledgeList: zhKnowledgeList,
      taskView: zhTaskView,
      historyConfig: zhHistoryConfig,
      pluginFailure: zhPluginFailure,
      configIO: zhConfigIO,
      memoryIO: zhMemoryIO,
      jsonSchema: zhJsonSchema,
      wizard: zhWizard,
      promptVars: zhPromptVars,
    },
    'en-US': {
      common: enCommon,
      toolbar: enToolbar,
      scenario: enScenario,
      config: enConfig,
      dialog: enDialog,
      chat: enChat,
      statusBar: enStatusBar,
      fileTree: enFileTree,
      projectConfig: enProjectConfig,
      codeIndex: enCodeIndex,
      security: enSecurity,
      knowledgeList: enKnowledgeList,
      taskView: enTaskView,
      historyConfig: enHistoryConfig,
      pluginFailure: enPluginFailure,
      configIO: enConfigIO,
      memoryIO: enMemoryIO,
      jsonSchema: enJsonSchema,
      wizard: enWizard,
      promptVars: enPromptVars,
    },
  },
})

/**
 * 应用语言到**当前窗口**（仅本地：i18n + localStorage，**不写 DB**）。
 * 供跨窗口即时同步（`useUserConfigSync`）复用 —— 事件驱动的应用**不得回写** DB，
 * 否则形成 save → data-user-config-changed → save 自激（61 §3.1）。
 */
export function applyLocale(locale) {
  i18n.global.locale.value = locale
  try { localStorage.setItem('chonkpilot-locale', locale) } catch (_) {}
}

/** Set locale, save to localStorage + DB（ui config，与窗口/布局统一持久化） */
export function setLocale(locale) {
  applyLocale(locale)
  saveUIState({ locale }).catch(() => {})
}

/** 支持的语言列表（Toolbar / LangSwitcher 共用） */
export const SUPPORTED_LANGUAGES = [
  { code: 'zh-CN', label: '中文', flag: '🇨🇳' },
  { code: 'en-US', label: 'English', flag: '🇺🇸' },
]
