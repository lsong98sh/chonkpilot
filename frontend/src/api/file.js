import { GetHttpPort, GetWorkDir, GetFileTree, GetFileTreeChildren, ReadFileContent, CreateFileInDir, CreateDirInDir, RenameFile, DeleteFilePath, DuplicateFile, RevealInExplorer, OpenWithDefault, OpenWithDialog } from '../../wailsjs/go/main/App'

let _httpPort = null
let _workDir = null

async function ensureHttpConfig() {
  if (_httpPort === null) {
    _httpPort = await GetHttpPort()
  }
  if (_workDir === null) {
    _workDir = await GetWorkDir()
    // Normalize: E:\GoDev\chonkpilot → E:/GoDev/chonkpilot
    _workDir = _workDir.replace(/\\/g, '/')
  }
}

export function getFileTree(path) {
  return GetFileTree(path || '')
}

/**
 * Lazy-load children of a directory (depth=1).
 * Used by el-tree lazy loading.
 */
export function getFileTreeChildren(dir) {
  return GetFileTreeChildren(dir)
}

export function readFile(path) {
  return ReadFileContent(path, false)
}

/**
 * 获取文件的 HTTP URL（通过本地 HTTP 服务的 /raw/ 端点）
 * 用于二进制预览：图片、PDF、Office 文档等
 * 返回形如 http://127.0.0.1:PORT/raw/path/to/file 的 URL
 */
export async function getFileUrl(path) {
  if (!path) return ''
  await ensureHttpConfig()
  // Convert absolute path to relative to workDir
  const normalized = path.replace(/\\/g, '/')
  if (normalized.startsWith(_workDir)) {
    const relative = normalized.slice(_workDir.length)
    return `http://127.0.0.1:${_httpPort}/raw${relative}`
  }
  // Fallback to file:/// for paths outside workDir
  return 'file:///' + normalized
}

/**
 * Pre-warm HTTP config cache. Call early to avoid async delay.
 */
export async function warmUpHttpConfig() {
  await ensureHttpConfig()
}

/**
 * Synchronous version for computed properties that can't be async.
 * Returns a placeholder and the caller should refresh.
 */
export function getFileUrlSync(path) {
  if (!path) return ''
  if (_httpPort && _workDir) {
    const normalized = path.replace(/\\/g, '/')
    if (normalized.startsWith(_workDir)) {
      const relative = normalized.slice(_workDir.length)
      return `http://127.0.0.1:${_httpPort}/raw${relative}`
    }
  }
  return 'file:///' + path.replace(/\\/g, '/')
}

// ─── File tree context menu operations ─────────────

export function createFileInDir(dirPath, fileName) {
  return CreateFileInDir(dirPath, fileName)
}

export function createDirInDir(dirPath, dirName) {
  return CreateDirInDir(dirPath, dirName)
}

export function renameFile(oldPath, newName) {
  return RenameFile(oldPath, newName)
}

export function deleteFilePath(path) {
  return DeleteFilePath(path)
}

export function duplicateFile(path) {
  return DuplicateFile(path)
}

export function revealInExplorer(path) {
  return RevealInExplorer(path)
}

export function openWithDefault(path) {
  return OpenWithDefault(path)
}

export function openWithDialog(path) {
  return OpenWithDialog(path)
}
