import mq, { currentInstanceId } from '../utils/mq'
import { createInitDataPrefetch } from '../utils/initDataPrefetch'
import { FieldKeys, GuiUiSaveKeys, FilesysListKeys, FilesysContentKeys } from '../events/msgkeys.js'

// 文件域直连 filesys（20-gui / 61-消息一览 §2）：
//   - 目录子项：filesys.list{work_dir, path} → HTTP result {path, is_dir, children}
//   - 文本内容：filesys.content{work_dir, path} → result {path, kind, content, truncated}
//     （二进制/大文件预览走 /show 字节流，不传内容）
//   - 写操作：filesys.create / mkdir / remove / rename / copy / duplicate → result {ok, path}
//   - 声明：filesys.watch / unwatch（展开/折叠，单向无返回）
//   - 变更广播：filesys.changed（children=目录批次 / 单文件 operation），前端 mq.on 订阅
// 返回结构保持旧契约（{content}/{tree}/{children}），组件消费字段不变；错误 err.code 保留（exists 等）。

// 前端当前工作目录（项目根）：由 FileTree / CodeView 在 LoadInitData 后写入，
// filesys 请求自动填充 work_dir（越界校验与 watch 范围均依赖它）。
let _workDir = ''
export function setWorkDir(dir) {
  _workDir = dir || ''
}

// 历史用途：请求 req_id 关联（reply 事件）；publish promise 化后不再需要，保留兼容（展开/折叠载荷仍携带）。
let _seq = 0
export function newFileReqId() {
  return 'freq-' + Date.now().toString(36) + '-' + (++_seq).toString(36) + '-' + Math.random().toString(36).slice(2, 8)
}

/**
 * 发送 filesys.<action> 并 await 收集结果（publish + promise；61-消息一览 §0.1/§2）。
 * @param {string} action - list / content / create / mkdir / remove / rename / copy / watch / unwatch
 * @param {object} body - 业务载荷（path/dir/name/content/new_name/dest_dir…）
 * @param {{timeout?: number}} [opts]
 * @returns {Promise<object>} 结果载荷（children/content/ok/path…）；错误 err.code 保留（exists 等）
 */
function fileRequest(action, body = {}, opts = {}) {
  const timeout = opts.timeout || 30000
  const topic = 'filesys.' + action
  return mq.emit(topic, { [FieldKeys.work_dir]: _workDir, ...body }, { timeout }).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(topic + ': backend unreachable')
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && (p.message || p.error)) || ''
    if (!backend.ok || emsg || p.code) {
      const e = new Error(emsg || (p && p.message) || (topic + ' failed'))
      if (p && p.code) e.code = p.code
      throw e
    }
    return p
  })
}

/**
 * 目录子项快照（filesys.list）。path 为空时取工作目录根。
 * 返回 {tree: {path, is_dir, children}}（对齐旧 GetFileTree 契约）。
 */
export function getFileTree(path) {
  return fileRequest('list', { path: path || _workDir || '' })
    .then((p) => ({
      tree: { path: p[FilesysListKeys.path] || '', is_dir: true, children: p[FilesysListKeys.children] || [] },
    }))
}

/**
 * Lazy-load children of a directory (depth=1)。
 * 返回 {children}（对齐旧 GetFileTreeChildren 契约）。
 */
export function getFileTreeChildren(dir) {
  return fileRequest('list', { path: dir })
    .then((p) => ({ children: p[FilesysListKeys.children] || [] }))
}

/**
 * 文本内容（filesys.content；二进制/大文件走 /show 预览，不读内容）。
 * 返回 {content, truncated}（对齐 61 §2.1 契约：result = {path, kind, content, truncated}）。
 */
export function readFile(path) {
  return fileRequest('content', { path })
    .then((p) => ({
      content: p[FilesysContentKeys.content] || '',
      truncated: !!p[FilesysContentKeys.truncated],
    }))
}

/**
 * 获取文件的 URL（通过 AssetServer Handler 的 /show/ 端点）
 * 用于二进制预览：图片、PDF、Office 文档等
 * 返回形如 /show/D:/path/to/file?instance_id=<id> 的 URL（同源，无需 CORS）
 * instance_id 为业务惯例必带字段（61-消息一览 §0）；桥侧 /show/ 只按路径取文件，忽略该 query。
 */
export function getFileUrl(path) {
  if (!path) return ''
  const url = '/show/' + path.replace(/\\/g, '/')
  const id = currentInstanceId()
  return id ? url + '?instance_id=' + encodeURIComponent(id) : url
}

// ─── File tree context menu operations ─────────────

// 文件写操作（61-消息一览 §2）：删除/重命名/复制直连 filesys（remove/rename/copy），
// 磁盘变化由 watcher 自然广播 filesys.changed；本端成功已由本地 UI 即时更新。
export function renameFile(oldPath, newName, overwrite = false) {
  return fileRequest('rename', { path: oldPath, new_name: newName, overwrite: overwrite ? 1 : 0 })
}

// 移动文件/目录到新路径（newName 含路径分隔符 → 按移动处理）；同名冲突 err.code='exists'
export function moveFile(path, newPath, overwrite = false) {
  return fileRequest('rename', { path, new_name: newPath, overwrite: overwrite ? 1 : 0 })
}

export function deleteFilePath(path) {
  return fileRequest('remove', { path })
}

// 复制文件/目录（filesys.copy，61-消息一览 §2.2）：同名冲突 err.code='exists'
// dest_dir 缺省 = 源文件所在目录；new_name 提供 = 改名复制（同目录「副本」名由前端算好传入）。
export function copyFileTo(srcPath, { dest_dir, new_name, overwrite } = {}) {
  const body = { path: srcPath, overwrite: overwrite ? 1 : 0 }
  if (dest_dir) body.dest_dir = dest_dir
  if (new_name) body.new_name = new_name
  return fileRequest('copy', body)
}

// 建文件/目录 → filesys.create / mkdir
export function createFileInDir(dirPath, fileName) {
  return fileRequest('create', { dir: dirPath, name: fileName })
}

export function createDirInDir(dirPath, dirName) {
  return fileRequest('mkdir', { dir: dirPath, name: dirName })
}

// ─── GUI 本地面辅助（gui.* 消息，61-消息一览 §1）：publish + await 收集回复，
// 与 data-session-* / filesys.* 同模式（请求结果取 backend.result，校验 ok/errors）。
function guiReq(action, body = {}) {
  const topic = 'gui.' + action
  return mq.emit(topic, body).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(topic + ': backend unreachable')
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || (topic + ' failed'))
      e.action = action
      throw e
    }
    return p
  })
}

export function revealInExplorer(path) {
  return guiReq('reveal', { path })
}

export function openWithDefault(path) {
  return guiReq('open-with', { path })
}

export function openWithDialog(path) {
  return guiReq('open-with', { path })
}

// ─── 新增 API 绑定（GUI 本地面；61-消息一览 §1）─────────────────

export function loadInitData() {
  return guiReq('init-data', {})
}

// ── 启动期预取（2026-09-22）──────────────────────────────────────────────
// 背景：主视图（MainLayout）挂载受**实例认领**（instance-claim）门控，而布局/UI 恢复用的
// init-data 过去在 MainLayout.onMounted 才发起 → 「认领往返 + init-data 往返」串行，
// 主视图先按默认布局渲染、随后才跳到落盘值（默认布局闪现；宿主 ready 早于恢复落点）。
// 预取 = 引导期（App setup）即发起一次、与认领**并行**，主视图挂载时已就绪 → 恢复随首帧生效。
// 状态机（单飞 / 一次性消费 / 失败回退）抽在 `utils/initDataPrefetch.js`（纯模块，可直测）。
const _initData = createInitDataPrefetch(() => guiReq('init-data', {}))

/** 启动期预取 init-data（App 引导调用；失败清空以便后续重取）。 */
export function prefetchInitData() {
  return _initData.prefetch()
}

/**
 * 取启动期预取结果并**消费**（一次性）：仅服务主视图首帧的布局/UI 恢复；
 * 无预取 / 已被消费 / 预取失败 → 回落实时读取（行为与既有 `loadInitData` 一致）。
 */
export function loadInitDataPrefetched() {
  return _initData.consume()
}

export function saveFileTreeState(state) {
  return guiReq('ui.save', { [GuiUiSaveKeys.filetree]: state })
}

export function saveWindowState(state) {
  return guiReq('ui.save', { [GuiUiSaveKeys.window]: state })
}

export function saveLayoutState(state) {
  return guiReq('ui.save', { [GuiUiSaveKeys.layout]: state })
}

export function saveUIState(state) {
  return guiReq('ui.save', { [GuiUiSaveKeys.ui]: state })
}

export function saveOpenedFiles(paths) {
  return guiReq('ui.save', { [GuiUiSaveKeys.opened_files]: paths || [] })
}
