/**
 * 「打开目录 / 最近项目」动作（宿主侧，UILAYOUT-1 §7 P4①）。
 *
 * 为什么抽出来：栏条目化后，【打开目录】是**配置按钮**（`publish-mq {topic:'workdir-open'}`），
 * 【最近项目】是**栏视图**（`RecentDirsView`）——两处都要「打开目录 + 就地刷新」这套逻辑，
 * 故收敛到本模块（原实现内联在 legacy `Toolbar.vue`，该文件已随 UILAYOUT-2-B 删除）。
 *
 * 行为：
 *   选目录（gui.select-dir）+ 同进程打开该项目的主窗口（gui.dir.open，已开则置前；含占用校验 I-74）；
 *   目标项目在**新窗口**上呈现，当前窗口保持不变 → 只需刷新「最近项目」清单。
 */
import { getRecentDirs, removeRecentDir, openDir } from '../api/config'
import { useDirPicker } from '../composables/useDirPicker'
import { message } from '../components/ui'
import { classifyError } from '../utils/errorMessage'
import { i18n } from '../plugins/i18n'

/** 目录选择控件标识（两段式 select-dir/selected-dir 的关联键，须唯一） */
const { pickDir } = useDirPicker('layout-workdir')

function t(key, params) {
  return i18n && i18n.global ? i18n.global.t(key, params || {}) : key
}

/** 打开指定目录并就地刷新（最近项目选中 / 选目录后共用）。 */
export async function openDirAndRefresh(path) {
  if (!path) return
  try {
    await openDir(path)
  } catch (e) {
    const cls = classifyError(e && e.message ? e.message : String(e))
    message.error(t(cls.key, cls.params || {}))
    return
  }
  await loadRecentDirs()
}

/** 【打开目录】按钮：选目录 → 打开并刷新。 */
export async function pickAndOpenDir() {
  let path = ''
  try {
    path = await pickDir()
  } catch (e) {
    console.error('[layout] open directory dialog failed:', e)
    return
  }
  await openDirAndRefresh(path)
}

/** 读最近项目清单（失败 → 空数组）。 */
export async function loadRecentDirs() {
  try {
    const res = await getRecentDirs()
    return (res && res.dirs) || []
  } catch (_) {
    return []
  }
}

/** 删除一条「最近项目」记录（仅删记录，不删目录 / 数据资产）。返回是否成功。 */
export async function removeRecent(dir) {
  try {
    await removeRecentDir(dir)
    return true
  } catch (e) {
    console.warn('[layout] remove recent dir failed:', e)
    return false
  }
}
