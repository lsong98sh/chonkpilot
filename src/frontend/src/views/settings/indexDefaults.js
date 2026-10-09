/**
 * 设置页（CodegraphConfig / VftsConfig / FileTreeConfig）的**默认值**镜像。
 *
 * 来源 = 引擎/服务引用的共享实现（不落配置，配置为空时即用这些默认）：
 *   - 索引排除规则：`src/lib/ignore/ignore.go` builtinPatterns（内置强制，不可被 '!' 反选）
 *                   + defaultPatterns（默认可被 '!' 反选）
 *   - 扩展名：  codegraph = server/index.go:supportedExts（lang.go 各语言 ext 并集）
 *               vfts      = server/ext.go:defaultExts
 *   - 文件树隐藏目录：`src/lib/filesys/config.go` defaultHiddenDirs
 * 用途 = 设置页在项目配置为空时回填显示"有效默认"，与实际生效值保持一致（I-65 ⑫）。
 * 注：此处仅为展示回填；后端仍以「空配置 → 回落上述默认」为准。
 */

// DEFAULT_SKIP_DIRS 两个引擎一致的默认排除规则（gitignore 语法：尾 '/' = 仅目录）。
// 前 4 条为**内置强制**（不可被 '!' 反选），其余为默认排除（可用 '!' 反选）。
export const DEFAULT_SKIP_DIRS = [
  '.git/', '.svn/', '.hg/', '.chonkpilot/',
  'node_modules/', '__pycache__/', '.venv/', 'venv/', '.trae/',
  'dist/', 'build/', '.next/', '.nuxt/', 'out/', 'target/', 'vendor/',
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

// DEFAULT_HIDDEN_DIRS 文件树「不显示的目录」默认清单（目录名匹配，任意层级）。
// 与后端 `src/lib/filesys/config.go` defaultHiddenDirs 保持一致；这三项已被「点开头恒隐藏」
// 规则覆盖，故默认行为不变——本清单用于**可配置扩展**（追加 target / node_modules 等）。
export const DEFAULT_HIDDEN_DIRS = ['.git', '.svn', '.chonkpilot']
