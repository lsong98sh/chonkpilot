/**
 * 栏条目「发布 MQ 动作」的宿主订阅者（UILAYOUT-1 裁决 1，2026-10-16）。
 *
 * 机制层（`layout-free/`）只负责把条目配置里的 `{topic, payload}` 通过 mq 发出去
 * （`utils/commands.js#commandToMq` 的 `publish-mq` 分支）；**由谁处理这些既有主题**属宿主侧策略
 * —— 本模块就是这份策略的落点，且**只引用既有宿主主题，零新增**。
 *
 * legacy 分支（`Toolbar.vue`）已随 UILAYOUT-2-B 删除；本模块是唯一订阅点，不会双执行。
 *
 * 由 free 形态的顶栏外壳（`layout/ChonkTopBar.vue`）在挂载时安装、卸载时回收。
 */
import mq from '../utils/mq'
import { EventNames } from '../events/event-names'
import { setLocale } from '../plugins/i18n'
import { saveUIState } from '../api/file'
import { useUserConfigSync } from '../composables/useUserConfigSync'
import { pickAndOpenDir } from './workdirActions'

/** 主题选择（既有主题 `theme-select {themeId}`）：与 `Toolbar.setTheme` 同口径 */
function applyTheme(themeId) {
  if (!themeId) return
  const { theme } = useUserConfigSync()
  theme.value = themeId
  document.documentElement.setAttribute('data-theme', themeId)
  try { localStorage.setItem('chonkpilot-theme', themeId) } catch (_) { /* 忽略 */ }
  saveUIState({ theme: themeId }).catch(() => {})
}

/** 安装宿主侧栏动作订阅；返回取消函数。 */
export function installBarActions() {
  const offs = [
    mq.on(EventNames.workdirOpen, () => pickAndOpenDir()),
    mq.on(EventNames.themeSelect, (d) => applyTheme(d && d.themeId)),
    mq.on(EventNames.langSelect, (d) => {
      if (d && d.lang) setLocale(d.lang)
    }),
  ]
  return () => {
    for (const off of offs) {
      if (typeof off === 'function') off()
    }
  }
}
