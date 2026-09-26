/**
 * configIO — 配置 导入 / 导出 / 恢复出厂 纯逻辑（用户视角缺陷批 3 · ⑯，2026-09-20）。
 *
 * 背景：设置面原先只有**逐项 Reset**（`data-user-config-delete` 带单键），
 * 没有导出/导入，也没有「恢复出厂」入口；后端其实早已具备「清空整份 usr 配置」的能力
 * （`chonkpilot-data/persist/persist_userconfig.go` handleUserConfig delete 无 key 分支），
 * 只是未在 UI 暴露。
 *
 * 分工（本模块只做纯函数，不碰 UI / 不碰 mq）：
 *   - 快照外形（导出文件与自动备份文件**同一格式**）：{app, schema, scope, exportedAt,
 *     secretsExcluded, data:{…usr 主库视图…}}；
 *   - 键白名单过滤（未知键忽略并计数，避免脏数据入库）；
 *   - 密钥剔除（导出可选「排除密钥」）；
 *   - 文件名（含时间戳）；需重启键判定（与 SettingsPathsPage 的 usr 路径键口径一致）。
 *
 * **键白名单唯一权威 = `chonkpilot-data/persist/persist_userconfig.go`**
 * （userConfigKeyKinds ∪ userConfigFreeKeys ∪ collectionKeys）；单测逐键与该 Go 源码核对，
 * 防两边漂移。
 */

/** 快照外形标识（导入侧用它识别「本应用导出文件」并可展示导出时间） */
export const EXPORT_APP = 'chonkpilot'
export const EXPORT_SCHEMA = 'chonkpilot-config/1'

/** 导出范围：仅 usr 全局配置（不含项目层 prj/prjusr —— 读侧口径见下方 SCOPE 说明） */
export const EXPORT_SCOPE = 'usr'

/**
 * 标量键（persist `userConfigKeyKinds`）：按类型读写，读侧缺失补系统默认。
 */
export const SNAPSHOT_SCALAR_KEYS = [
  'theme', 'locale',
  'chromePath', 'javaPath', 'pythonPath', 'nodePath', 'goPath', 'rustPath', 'cCompilerPath',
  'responseTimeout', 'streamTimeout', 'retryCount', 'retryDelay',
  'defaultLLM', 'defaultScenario',
]

/** 自由键（persist `userConfigFreeKeys`）：无类型无默认，值以字符串形态存取 */
export const SNAPSHOT_FREE_KEYS = ['recent_dirs', 'tool_async', 'tool_sandbox']

/** 集合键（persist `collectionKeys` → usr 专用表 llms / mcps） */
export const SNAPSHOT_COLLECTION_KEYS = ['llms', 'mcpServers']

/** 导入键白名单（= 上三者并集；白名单外一律忽略并计数） */
export const SNAPSHOT_KEYS = [
  ...SNAPSHOT_SCALAR_KEYS,
  ...SNAPSHOT_FREE_KEYS,
  ...SNAPSHOT_COLLECTION_KEYS,
]

/**
 * 「需重启才生效」的键（usr 工具链路径）：与批 2 口径一致
 * （`views/config/SettingsPathsPage.vue` → `settingsFeedback.APPLY_RESTART`
 * 「用户级路径保存后需重启应用才生效」；prj 路径才热生效）。
 */
export const RESTART_KEYS = [
  'chromePath', 'javaPath', 'pythonPath', 'nodePath', 'goPath', 'rustPath', 'cCompilerPath',
]

/** 密钥名判定：llms[].apiKey / mcps[].env 行名 / mcps[].headers 键名 */
export const SECRET_NAME_RE = /(api[-_]?key|token|secret|password|passwd|authorization|credential)/i

/** 单个名字是否为密钥名 */
export function isSecretName(name) {
  return SECRET_NAME_RE.test(String(name == null ? '' : name))
}

const pad2 = (n) => String(n).padStart(2, '0')

/** 时间戳 `YYYYMMDD-HHmmss`（本地时区；文件发现场时间） */
export function timestamp(d = new Date()) {
  const t = d instanceof Date ? d : new Date(d)
  return String(t.getFullYear()) + pad2(t.getMonth() + 1) + pad2(t.getDate()) +
    '-' + pad2(t.getHours()) + pad2(t.getMinutes()) + pad2(t.getSeconds())
}

/** 导出文件名：`chonkpilot-config-YYYYMMDD-HHmmss.json` */
export function exportFileName(d = new Date()) {
  return `chonkpilot-config-${timestamp(d)}.json`
}

/** 备份文件名（导入前 / 恢复出厂前自动备份；与导出同格式，便于用户回退） */
export function backupFileName(d = new Date()) {
  return `chonkpilot-config-backup-${timestamp(d)}.json`
}

/**
 * 剔除密钥（**返回新对象，不改入参**）：
 *   - llms[*]：删掉密钥名字段（apiKey 等）；
 *   - mcpServers[*].env：删掉 `K=…` 中 K 为密钥名的行；
 *   - mcpServers[*].headers：删掉密钥名的键。
 * @returns {{data: object, removed: number}} removed = 被剔除的字段/行数（UI 据此提示）
 */
export function redactSecrets(config) {
  const src = config && typeof config === 'object' ? config : {}
  let removed = 0
  const out = { ...src }

  const llms = Array.isArray(out.llms) ? out.llms : []
  out.llms = llms.map((it) => {
    if (!it || typeof it !== 'object') return it
    const rec = { ...it }
    for (const k of Object.keys(rec)) {
      if (isSecretName(k)) {
        delete rec[k]
        removed += 1
      }
    }
    return rec
  })

  const mcps = Array.isArray(out.mcpServers) ? out.mcpServers : []
  out.mcpServers = mcps.map((it) => {
    if (!it || typeof it !== 'object') return it
    const rec = { ...it }
    if (Array.isArray(rec.env)) {
      rec.env = rec.env.filter((line) => {
        const hit = isSecretName(String(line == null ? '' : line).split('=')[0])
        if (hit) removed += 1
        return !hit
      })
    }
    if (rec.headers && typeof rec.headers === 'object' && !Array.isArray(rec.headers)) {
      const kept = {}
      for (const [k, v] of Object.entries(rec.headers)) {
        if (isSecretName(k)) {
          removed += 1
          continue
        }
        kept[k] = v
      }
      rec.headers = kept
    }
    return rec
  })

  return { data: out, removed }
}

/**
 * 组装快照信封（导出文件与自动备份共用）。
 * @param {object} config - usr 主库视图（`data-user-config-list` 的 list[0]）
 * @param {{excludeSecrets?: boolean, scope?: string, now?: Date}} [opts]
 * @returns {{envelope: object, keyCount: number, removedSecrets: number}}
 */
export function buildSnapshot(config, opts = {}) {
  const { excludeSecrets = false, scope = EXPORT_SCOPE } = opts
  const now = opts.now instanceof Date ? opts.now : new Date()
  const src = config && typeof config === 'object' ? { ...config } : {}
  delete src.id // list 视图带 id 字段（域标识），非配置本体

  let data = src
  let removedSecrets = 0
  if (excludeSecrets) {
    const r = redactSecrets(src)
    data = r.data
    removedSecrets = r.removed
  }

  return {
    envelope: {
      app: EXPORT_APP,
      schema: EXPORT_SCHEMA,
      scope,
      exportedAt: now.toISOString(),
      secretsExcluded: !!excludeSecrets,
      data,
    },
    keyCount: Object.keys(data).length,
    removedSecrets,
  }
}

/**
 * 序列化快照 → 待写盘字符串 + 建议文件名。
 * @returns {{json: string, fileName: string, keyCount: number, removedSecrets: number}}
 */
export function serializeSnapshot(config, opts = {}) {
  const now = opts.now instanceof Date ? opts.now : new Date()
  const { envelope, keyCount, removedSecrets } = buildSnapshot(config, { ...opts, now })
  return { json: JSON.stringify(envelope, null, 2), fileName: exportFileName(now), keyCount, removedSecrets }
}

/**
 * 解析导入文本（结构校验第一层：JSON 可解析 + 外形可识别）。
 * 接受两种外形：① 本应用导出的信封（含 `data` 对象，取 `data`）；
 * ② 裸配置对象（历史手写文件 / 只含键值）。
 * @returns {{ok: true, format: 'envelope'|'plain', data: object, meta: object}}
 *        | {{ok: false, reason: 'empty'|'json'|'shape', detail?: string}}
 */
export function parseImportText(text) {
  const raw = String(text == null ? '' : text).trim()
  if (!raw) return { ok: false, reason: 'empty' }
  let obj
  try {
    obj = JSON.parse(raw)
  } catch (e) {
    return { ok: false, reason: 'json', detail: (e && e.message) || String(e) }
  }
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return { ok: false, reason: 'shape' }

  if (obj.data && typeof obj.data === 'object' && !Array.isArray(obj.data)) {
    return {
      ok: true,
      format: 'envelope',
      data: obj.data,
      meta: {
        app: obj.app || '',
        schema: obj.schema || '',
        scope: obj.scope || '',
        exportedAt: obj.exportedAt || '',
        secretsExcluded: obj.secretsExcluded === true,
      },
    }
  }
  return { ok: true, format: 'plain', data: obj, meta: {} }
}

/**
 * 键白名单过滤（结构校验第二层）：白名单内留下（applied），白名单外忽略（ignored）。
 * 集合键强制数组（非数组 → 空数组，等价「清空该集合」——与 persist writeCollection 口径一致）。
 * @returns {{data: object, applied: string[], ignored: string[]}}
 */
export function filterImport(data) {
  const src = data && typeof data === 'object' && !Array.isArray(data) ? data : {}
  const picked = {}
  const applied = []
  const ignored = []
  for (const [k, v] of Object.entries(src)) {
    if (k === 'id') continue
    if (!SNAPSHOT_KEYS.includes(k)) {
      ignored.push(k)
      continue
    }
    if (SNAPSHOT_COLLECTION_KEYS.includes(k)) {
      picked[k] = Array.isArray(v) ? v : []
    } else {
      picked[k] = v
    }
    applied.push(k)
  }
  return { data: picked, applied, ignored }
}

/** 本次导入中「需重启才生效」的键（子集；UI 据此标注） */
export function restartKeysOf(appliedKeys) {
  return (Array.isArray(appliedKeys) ? appliedKeys : []).filter((k) => RESTART_KEYS.includes(k))
}
