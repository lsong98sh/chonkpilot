// 记忆类别（记忆库）共享状态与交互（A4 / P1 迁移）：
// 状态栏「记忆总 token 数」入口（主入口）与上下文管理页（只读总量 + 行内编辑）共用同一份
// 「读配置开关 → 取类别清单 → 汇总 token → 打开分类列表 → 打开内容编辑弹框」逻辑，
// 避免两处各写一份（数据源 = 既有 data-memory-list / data-memory-read / data-memory-save）。
import { ref, computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import { message } from '../components/ui'
import { dialog } from '../components/dialog'
import TextEditDialog from '../components/common/TextEditDialog.vue'
import MemoryCategoryListDialog from '../views/settings/MemoryCategoryListDialog.vue'
import dataClient, { dataRequest } from '../utils/dataClient'
import { loadFailedText } from '../utils/settingsFeedback'

export function useMemoryCategories() {
  const { t } = useI18n()
  // 记忆库总开关（memory.enabled；由调用方在读到 prj 配置后 setEnabled）
  const enabled = ref(false)
  // 类别清单（data-memory-list 形态：{ category, level, tokens }）
  const list = ref([])
  // 记忆总 token 数（各启用类别 tokens 之和）
  const total = computed(() => list.value.reduce((sum, c) => sum + (Number(c.tokens) || 0), 0))

  function setEnabled(v) {
    enabled.value = v === true
  }

  // load 取类别清单：仅记忆库启用时发 data-memory-list（关闭态后端不落盘预置文件、页面也不渲染
  // 类别区）；关闭 → 清空清单。失败 → 抛错，由调用方决定可见提示。
  async function load() {
    if (!enabled.value) {
      list.value = []
      return
    }
    const res = await dataClient.list('memory')
    list.value = Array.isArray(res.list) ? res.list : []
  }

  // ── 内容编辑弹框（读内容 → 弹框内编辑/优化 → 保存即关 + 刷新清单）──
  // 当前打开的内容编辑弹框（删类别 / 清空内容时按类别同步关闭，避免陈旧草稿回写）。
  // 弹框已由 X 关闭时 handle.close() 为幂等空操作（DialogManager.close 找不到 id 直接返回）。
  let openEditorRef = { handle: null, category: '' }

  function showContentEditor(category, content) {
    const handle = dialog.show(h(TextEditDialog, {
      content,
      placeholder: t('projectConfig.memory_content_placeholder'),
      // 弹框自带「优化」（A1）：复用既有提示词优化链路（gui.prompt-optimise）
      optimize: {
        title: t('projectConfig.memory_optimize_title', { name: category }),
        useCase: t('projectConfig.memory_optimize_use_case'),
      },
      onSave: async (text) => {
        await dataClient.save('memory', { category, content: text })
        message.success(t('projectConfig.saved'))
        handle.close()
        await load() // 刷新预估 token 数（含总量）
      },
      // 弹框「取消」：TextEditDialog emit('cancel') → 关闭弹框（不落库）
      onCancel: () => handle.close(),
    }), {
      title: category,
      width: 760,
      height: 560,
      bodyClass: 'text-edit-dialog-body',
      closable: true,
    })
    openEditorRef = { handle, category }
  }

  // openContentEditor：先读内容（读失败 → 可见提示且不打开）→ 弹框内编辑 → 保存成功后自动关闭。
  async function openContentEditor(c) {
    if (!c || !c.category) return
    let content = ''
    try {
      const res = await dataRequest('memory', 'read', { data: { category: c.category } })
      const d = res && res.data ? res.data : {}
      content = d.content || ''
    } catch (e) {
      message.error(loadFailedText(t, t('projectConfig.memory_section'), e))
      return
    }
    showContentEditor(c.category, content)
  }

  // closeEditorFor：类别被删除/清空时关闭其内容编辑弹框（类别已不存在 → 避免回写陈旧草稿）。
  function closeEditorFor(category) {
    if (openEditorRef.category !== category) return
    openEditorRef.handle?.close()
    openEditorRef = { handle: null, category: '' }
  }

  // openCategoryList：点击「记忆总 token 数」→ 记忆分类列表 → 选中 → 内容编辑弹框。
  function openCategoryList() {
    const handle = dialog.show(h(MemoryCategoryListDialog, {
      categories: list.value,
      total: total.value,
      onPick: (c) => {
        handle.close()
        openContentEditor(c)
      },
    }), {
      title: t('projectConfig.memory_categories_title'),
      width: 420,
      height: 460,
      closable: true,
    })
  }

  return { enabled, list, total, setEnabled, load, openContentEditor, openCategoryList, closeEditorFor, showContentEditor }
}
