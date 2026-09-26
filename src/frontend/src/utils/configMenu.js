// 全局配置菜单项（工具栏「设置」下拉 与 状态栏配置入口 共用同一清单，避免重复维护）。
// kind = preview 页签 kind（消费方 = CodeView.specialComponents / specialTitle）；
// icon = components/icon/icons.js 的图标键；labelKey = i18n 键（config.page.*）。
export const CONFIG_MENU_ITEMS = [
  { kind: 'settings-llm', icon: 'magic-stick', labelKey: 'config.page.llm' },
  { kind: 'settings-mcp', icon: 'link', labelKey: 'config.page.mcp' },
  { kind: 'settings-tool-async', icon: 'lightning', labelKey: 'config.page.toolAsync' },
  { kind: 'settings-tool-sandbox', icon: 'shield', labelKey: 'config.page.toolSandbox' },
  { kind: 'settings-paths', icon: 'tool', labelKey: 'config.page.paths' },
  { kind: 'settings-params', icon: 'clock', labelKey: 'config.page.params' },
  { kind: 'settings-project', icon: 'setting', labelKey: 'config.page.project' },
]

// configMenuItems 按当前语言生成菜单项（t = useI18n 的 t）。
export function configMenuItems(t) {
  return CONFIG_MENU_ITEMS.map(it => ({ kind: it.kind, icon: it.icon, label: t(it.labelKey) }))
}
