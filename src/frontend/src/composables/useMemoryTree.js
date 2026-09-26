// 左侧资源面板「项目记忆」模式（第 4 模式，2026-09-26）状态与交互。
//
// 数据来源**全部复用既有消息面**（零新增主题）：
//   - 类别清单 = `data-memory-list`（经 `useMemoryCategories`，沿用同门控：记忆库关闭不发 list）；
//   - 类别启用 = prj 键 `memory.category.<类别名>`（缺省启用，与上下文管理页 `categoryEnabled` 同口径）；
//   - 项目级条目点击 → 既有 `file-open`（`{path, temporary:true}`，走预览区 `.md` markdown 渲染）；
//   - 「用户偏好」（唯一 user 级，落 `~/.chonkpilot/用户偏好.md`，在工作目录之外，走 `file-open`
//     会被 filesys 越界校验拒绝）→ 改用既有 `data-memory-read` + 只读弹框；
//   - 记忆写回广播 `data-memory-refresh` → 调用方订阅后自动刷新。
import { computed, h, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { message } from '../components/ui'
import { dialog } from '../components/dialog'
import MemoryViewDialog from '../components/common/MemoryViewDialog.vue'
import { getAllConfig } from '../api/config'
import { dataRequest } from '../utils/dataClient'
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'
import { useMemoryCategories } from './useMemoryCategories'
import { loadFailedText } from '../utils/settingsFeedback'

// 类别启用键前缀（prj 键 `memory.category.<类别名>`；与 ContextConfig / Go 侧同字面量）。
const MEMORY_CATEGORY_PREFIX = 'memory.category.'

export function useMemoryTree() {
  const { t } = useI18n()
  // 复用记忆类别共享状态（同一门控：enabled=false 时 load() 不发 data-memory-list）。
  const { enabled, list, setEnabled, load } = useMemoryCategories()
  // prj 配置平铺 map（读 `memory.enabled` 与逐类 `memory.category.<类别名>`）
  const cfg = ref({})
  const loading = ref(false)

  // 类别启用判定：缺 key / 空值 → 启用（与 ContextConfig.categoryEnabled 同口径）。
  function categoryEnabled(category) {
    const v = cfg.value[MEMORY_CATEGORY_PREFIX + category]
    return v === undefined || v === '' ? true : v === 'true'
  }

  // items：仅**已启用**类别（唯一 user 级「用户偏好」同键 `memory.category.用户偏好` 门控）；
  // 记忆库关闭 → 空（配合面板空态提示）。
  const items = computed(() => (
    enabled.value ? list.value.filter(c => categoryEnabled(c.category)) : []
  ))

  // refresh：读配置（总开关 + 逐类开关）→ 关闭 → 清空且**不请求** data-memory-list；
  // 开启 → 取清单。读配置失败 → 按关闭处理（不谎报已启用）。
  async function refresh() {
    loading.value = true
    try {
      const res = await getAllConfig()
      cfg.value = (res && (res.config || res)) || {}
      setEnabled(cfg.value['memory.enabled'] === 'true')
    } catch (_) {
      cfg.value = {}
      setEnabled(false)
    }
    try {
      await load()
    } catch (e) {
      message.error(loadFailedText(t, t('fileTree.mode_memory'), e))
    } finally {
      loading.value = false
    }
  }

  // openItem：点击查看 —— 项目级 → 预览区打开（`file-open`，`.md` 走 markdown 渲染）；
  // user 级「用户偏好」（工作目录之外）→ 只读弹框（不绕过 filesys 越界校验）。
  async function openItem(item) {
    if (!item || !item.category) return
    if (item.level === 'user') {
      await openUserPref(item)
      return
    }
    mq.emit(EventNames.fileOpen, { path: item.path, temporary: true })
  }

  // openUserPref：经既有 `data-memory-read` 读全文 → 只读弹框；读失败 → 可见提示且不开弹框。
  async function openUserPref(item) {
    let content = ''
    try {
      const res = await dataRequest('memory', 'read', { data: { category: item.category } })
      const d = res && res.data ? res.data : {}
      content = d.content || ''
    } catch (e) {
      message.error(loadFailedText(t, t('fileTree.mode_memory'), e))
      return
    }
    dialog.show(h(MemoryViewDialog, { content }), {
      title: item.category,
      width: 760,
      height: 560,
      closable: true,
    })
  }

  return { enabled, items, loading, refresh, openItem }
}
