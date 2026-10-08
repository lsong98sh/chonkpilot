import mq from '../utils/mq.js'
import {
  DataKnowledgeRootKeys, DataKnowledgeListKeys,
  DataKnowledgeReadKeys, DataKnowledgeCreateKeys,
} from '../events/msgkeys.js'

// data-knowledge-* 消息辅助：发送并等待回复（E-25：统一 30s 超时，对齐 fileRequest/guiReq 口径，
// 后端卡死时 fetch abort → resolve(null) → 按 backend unreachable 报错，不永久挂起）。
// 知识库维护（FP「preview 知识库维护」）：维护分类目录下的 *.原语.md 文件
// （tools / skills / prompts / resources 四类）。kind=app 通用 | project 项目。
function knowledgeReq(action, body = {}) {
  return mq.emit(`data-knowledge-${action}`, body, { timeout: 30000 }).then((env) => {
    const backend = env && env.backend
    if (!backend) throw new Error(`data-knowledge-${action}: backend unreachable`)
    const p = backend.result && typeof backend.result === 'object' ? backend.result : {}
    const emsg = (backend.errors && backend.errors[0]) || (p && p.error) || ''
    if (!backend.ok || emsg || p.ok === false) {
      const e = new Error(emsg || `data-knowledge-${action} failed`)
      e.action = action
      throw e
    }
    return p
  })
}

export function getKnowledgeRoot(kind = 'app') {
  return knowledgeReq('root', { kind }).then((r) => ({ root: r[DataKnowledgeRootKeys.root], kind: r[DataKnowledgeRootKeys.kind] }))
}

export function listPrimitives(dir) {
  return knowledgeReq('list', { dir }).then((r) => ({ dirs: r[DataKnowledgeListKeys.dirs], files: r[DataKnowledgeListKeys.files] }))
}

export function readPrimitive(path) {
  return knowledgeReq('read', { path }).then((r) => ({ source: r[DataKnowledgeReadKeys.source], doc: r[DataKnowledgeReadKeys.doc] }))
}

export function savePrimitive(path, doc) {
  return knowledgeReq('save', { path, doc }).then(() => {})
}

export function createPrimitive(dir, type, name) {
  return knowledgeReq('create', { dir, type, name }).then((r) => r[DataKnowledgeCreateKeys.path])
}

export function deletePrimitive(path) {
  return knowledgeReq('delete', { path }).then(() => {})
}

export function renamePrimitive(path, new_name) {
  return knowledgeReq('rename', { path, new_name }).then(() => {})
}

export function createPrimitiveDir(parent, name) {
  return knowledgeReq('mkdir', { parent, name }).then((r) => r[DataKnowledgeCreateKeys.path])
}

export function deletePrimitiveDir(path) {
  return knowledgeReq('rmdir', { path }).then(() => {})
}

export function renamePrimitiveDir(path, new_name) {
  return knowledgeReq('rename-dir', { path, new_name }).then(() => {})
}

// 拖拽移动知识库原语/目录（只改路径、不改内容）：**走知识库域** data-knowledge-rename / rename-dir
// （按「文件 / 目录」二选一，与 renamePrimitive / renamePrimitiveDir 分工一致；61-消息一览 §3.3）。
// G-26：不再复用 filesys.rename —— filesys 自 G-21 起只按 instance 登记表解析 work_dir、不采信载荷，
// 知识库 app/user 级 capability 路径在工作目录之外 → 自报 work_dir 会被拒；data-knowledge-* 由
// 服务端按 instance 解析 work_dir + kbRootOf 判定 capability 根，移动范围 = **同一知识库根内**。
// new_name = 目标相对知识库根的路径（含分隔符 = 跨目录移动；后端逐段校验，`..` / 越界 / 跨级一律拒绝）。
// 同名冲突：data-knowledge-* 无 overwrite 入参（61 冻结面，见 G-26 待确认项）→ 后端拒绝覆盖并报错
// （"target xxx exists"），由调用方按 e.message 提示（不静默覆盖）。
export function movePrimitive(path, new_path, root, isDir = false) {
  const rootSlash = String(root || '').replace(/\\/g, '/').replace(/\/+$/, '')
  const np = String(new_path || '').replace(/\\/g, '/')
  const new_name = rootSlash && np.startsWith(rootSlash + '/') ? np.slice(rootSlash.length + 1) : np
  return knowledgeReq(isDir ? 'rename-dir' : 'rename', { path, new_name }).then(() => {})
}