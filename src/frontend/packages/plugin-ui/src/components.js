/**
 * 设计系统组件端口：插件 UI 包复用宿主组件（Icon / Button / Switch），
 * 但不把宿主设计系统打进包里 —— 由宿主注册。
 */
let components = {}

export function setPluginUIComponents(map) {
  if (!map) return
  components = { ...components, ...map }
}

export function pluginUIComponents() {
  return components
}
