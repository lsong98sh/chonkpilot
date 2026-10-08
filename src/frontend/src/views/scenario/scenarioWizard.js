// 场景向导「打开」助手（唯一入口）：
//   - MainLayout 订阅 `agent-wizard` 事件后调用；
//   - ScenarioEditDialog 底部【向导】按钮复用同一入口。
// 模块级 flag 防重复打开（同窗口内同一时刻仅一个向导对话框）；对话框卸载时（任意关闭路径，
// 含右上角 ✕）经 onClosed 复位，保证下次仍可打开。
import { h, defineAsyncComponent } from 'vue'
import { dialog } from '../../components/dialog'

// 懒加载：仅在打开向导时加载（与 MainLayout 懒加载 ScenarioDialogContent 同口径）
const ScenarioWizardDialog = defineAsyncComponent(() => import('./ScenarioWizardDialog.vue'))

let _wizardOpen = false
// 自动弹出标记（本会话仅一次）：`wizard_required` 兜底字段与 `agent-wizard` 事件可能同源双触发 → 去重。
let _autoOpened = false

/**
 * 打开场景向导对话框。
 * @param {{reason?:string, work_dir?:string, workDir?:string, spec_path?:string, specPath?:string}} [payload]
 *        来自 `agent-wizard` 事件（reason/work_dir/spec_path）或手动入口（可空）。
 * @returns {object|null} DialogManager handle；已在打开中时返回 null。
 */
export function openScenarioWizard(payload = {}) {
  if (_wizardOpen) return null
  _wizardOpen = true
  let handle = null
  try {
    handle = dialog.show(
      h(ScenarioWizardDialog, {
        reason: payload.reason || '',
        workDir: payload.work_dir || payload.workDir || '',
        specPath: payload.spec_path || payload.specPath || '',
        // 关闭请求（底部【关闭】按钮）→ 关闭对话框；不写任何文件。
        requestClose: () => { if (handle) handle.close() },
        // 已关闭（onUnmounted）→ 复位防重入 flag。
        notifyClosed: () => { _wizardOpen = false },
      }),
      {
        title: '场景向导',
        width: 1120,
        height: 720,
        closable: true,
        bodyClass: 'wizard-dialog-body',
      },
    )
  } catch (e) {
    _wizardOpen = false
    throw e
  }
  return handle
}

/**
 * 启动检测**自动弹出**（本会话仅一次；手动入口不受此限，仍调 `openScenarioWizard`）。
 *
 * 为何需要二者：
 *  - `init-data` 在 App 引导期**预取**（早于 MainLayout 订阅）→ 桥在应答时下发的
 *    `agent-wizard` 事件**可能无订阅者**（漏收）；
 *  - 桥同时把判据放进应答字段 `wizard_required` → 由 MainLayout 消费 init-data 时**兜底**打开；
 *  - 两条路径都走本函数 → 去重，避免弹两次。
 * @param {{reason?:string, work_dir?:string, workDir?:string, spec_path?:string}} [payload]
 */
export function maybeAutoOpenWizard(payload = {}) {
  if (_autoOpened) return null
  _autoOpened = true
  return openScenarioWizard(payload)
}
