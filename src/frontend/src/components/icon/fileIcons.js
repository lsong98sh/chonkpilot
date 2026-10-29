// 文件类型图标解析（图标集 = file-icon-vectors "Vivid"，MIT，见 assets/file-icons/LICENSE）
// 只把实际用得到的类型拷进仓库（assets/file-icons/vivid/*.svg），不引全量包：
// 官方 CSS 逐个 url() 引用 300+ 独立 SVG，全量接入会把它们全部打进 chonkpilot.exe。
// 例外：vue / toml / kt / rs / docker / gradle 六枚 Vivid 没有，用同款「纸张 + 白色内容区」
// 模板套 Devicon（MIT，见 assets/file-icons/LICENSE-devicon）品牌 logo 合成，保证同一视觉语言。
const _mods = import.meta.glob('../../assets/file-icons/vivid/*.svg', {
  eager: true,
  query: '?url',
  import: 'default',
})

// 图标名 -> 打包后资源 URL
const URLS = {}
for (const [path, url] of Object.entries(_mods)) {
  const file = path.slice(path.lastIndexOf('/') + 1)
  URLS[file.slice(0, -4)] = url
}

// 未收录类型 → 语义就近的图标（vue/toml/kt/rs/docker/gradle 已自带图标，无需别名）
const EXT_ALIAS = {
  mjs: 'js', cjs: 'js',
  tsx: 'ts', mts: 'ts', cts: 'ts',
  hpp: 'h', hh: 'h', hxx: 'h', cc: 'cpp', cxx: 'cpp',
  kts: 'kt', scala: 'java', groovy: 'java',
  properties: 'conf', env: 'config',
  markdown: 'md', db3: 'sqlite', avif: 'png', jfif: 'jpg',
  mod: 'txt', sum: 'txt', out: 'txt', err: 'txt',
}

// 无后缀文件（Makefile / Dockerfile / LICENSE …）按文件名映射
const NAME_ICON = {
  makefile: 'config', dockerfile: 'docker', procfile: 'config', vagrantfile: 'config',
  license: 'txt', licence: 'txt', notice: 'txt', authors: 'txt', readme: 'md', changelog: 'md',
}

const FALLBACK = 'blank'

// 文件名 → 图标名（不含扩展名，等同于 assets/file-icons/vivid/<name>.svg）
export function fileIconName(label) {
  const name = String(label || '').split(/[\\/]/).pop().toLowerCase()
  if (!name) return FALLBACK
  if (NAME_ICON[name]) return NAME_ICON[name]
  const dot = name.lastIndexOf('.')
  if (dot < 0) return FALLBACK
  // 纯 dotfile（.gitignore 等）：点后即图标名，与 Vivid 命名一致
  const ext = dot === 0 ? name.slice(1) : name.slice(dot + 1)
  if (!ext) return FALLBACK
  const icon = EXT_ALIAS[ext] || ext
  return URLS[icon] ? icon : FALLBACK
}

// 文件名 → 图标资源 URL（未知类型回退通用文件图标）
export function fileIconUrl(label) {
  return URLS[fileIconName(label)] || URLS[FALLBACK] || ''
}
