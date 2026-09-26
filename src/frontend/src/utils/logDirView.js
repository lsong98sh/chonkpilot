/**
 * 日志目录入口视图模型（数据源 = `gui.init-data` 的只读字段 `logDir`）。
 *
 * `logDir` = `<dataDir>/logs`（chonkpilot-gui/logfile.go），**仅挂上文件 sink 时下发**
 * （bridge/local.go：未挂 sink → 字段缺省）。故：
 *   - 缺 `logDir` → **不得显示空路径**：给明确提示（未启用文件日志）。
 *   - 有 `logDir` + GUI 形态 → 用既有 native `reveal` 打开目录。
 *   - 有 `logDir` + browser 形态（native 不可用）→ 显示路径 + 复制按钮 + 明确「不可直接打开」。
 *
 * 纯函数：返回 { show, path, canOpen, canCopy, hintKey }。
 * hintKey 为 i18n key（projectConfig.*）；空串 = 无需提示。
 */
export function logDirView({ logDir, isBrowser } = {}) {
  const path = String(logDir || '').trim()
  if (!path) {
    return {
      show: false,
      path: '',
      canOpen: false,
      canCopy: false,
      hintKey: 'projectConfig.log_dir_unavailable',
    }
  }
  if (isBrowser) {
    return {
      show: true,
      path,
      canOpen: false,
      canCopy: true,
      hintKey: 'projectConfig.log_dir_browser_hint',
    }
  }
  return { show: true, path, canOpen: true, canCopy: false, hintKey: '' }
}
