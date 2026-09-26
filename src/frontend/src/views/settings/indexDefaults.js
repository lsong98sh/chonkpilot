/**
 * 索引设置页（CodegraphConfig / VftsConfig）的**引擎默认值**镜像。
 *
 * 来源 = 引擎代码内的 fallback 常量（不落配置，配置文件为空时引擎即用这些默认）：
 *   - 排除目录：chonkpilot-codegraph-mcp-server/server/index.go:defaultSkipDirs
 *               chonkpilot-vfts-mcp-server/server/index.go:defaultSkipDirs（与 codegraph 一致）
 *   - 扩展名：  codegraph = server/index.go:supportedExts（lang.go 各语言 ext 并集）
 *               vfts      = server/ext.go:defaultExts
 * 用途 = 设置页在项目配置为空时回填显示"有效默认"，与实际生效值保持一致（I-65 ⑫）。
 * 注：此处仅为展示回填；引擎仍以「空配置 → 回落上述默认」为准。
 */

// DEFAULT_SKIP_DIRS 两个引擎一致的默认跳过目录名（引擎侧为用户配置 ∪ 本默认）。
export const DEFAULT_SKIP_DIRS = [
  '.git', '.svn', '.hg', 'node_modules', '__pycache__',
  '.venv', 'venv', '.trae', '.chonkpilot', 'dist', 'build',
  '.next', '.nuxt', 'out', 'target', 'vendor',
]

// CODEGRAPH_DEFAULT_EXTS codegraph 默认扩展名（全部受支持语言，已排序）。
export const CODEGRAPH_DEFAULT_EXTS = [
  '.cjs', '.go', '.java', '.js', '.jsx', '.mjs', '.py', '.pyw', '.rs', '.ts', '.tsx',
]

// VFTS_DEFAULT_EXTS vfts 默认扩展名（源码 + 纯文本，已排序）。
export const VFTS_DEFAULT_EXTS = [
  '.bat', '.c', '.cc', '.cjs', '.cpp', '.cs', '.css', '.go', '.h', '.hpp', '.html',
  '.java', '.js', '.json', '.jsx', '.kt', '.md', '.mjs', '.php', '.ps1', '.py', '.pyw',
  '.rb', '.rs', '.scss', '.sh', '.sql', '.toml', '.ts', '.tsx', '.txt', '.vue', '.xml',
  '.yaml', '.yml',
]
