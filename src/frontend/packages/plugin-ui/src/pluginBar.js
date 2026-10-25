/**
 * 插件状态栏条目（PLUGIN-1）：manifest `ui.statusbar[]` → 栏条目。
 *
 * 规则：不写入布局文档（插件禁用即消失）；`command.target=host` 打开插件视图，
 * `command.target=plugin` 转发 `plugin-command`。
 */
import { usePlugins } from './usePlugins.js'

function normalizeCommand(plugin, cmd) {
  if (!cmd) {
    return { type: 'publish-mq', topic: 'plugin-command', payload: { id: plugin.id, name: 'status' } }
  }
  if (cmd.target === 'plugin') {
    return {
      type: 'publish-mq',
      topic: 'plugin-command',
      payload: { id: plugin.id, name: cmd.name || cmd.action, params: cmd.params || {} },
    }
  }
  if (cmd.action === 'open-view' && cmd.view) {
    const view = (plugin.views || []).find((v) => v.id === cmd.view)
    const containers = Array.isArray(view?.containers) ? view.containers : []
    if (containers.includes('preview')) {
      return {
        type: 'publish-mq',
        topic: 'preview-tab-open',
        payload: {
          kind: 'plugin-page',
          plugin: plugin.id,
          page: cmd.view,
          title: view?.title || plugin.name,
        },
      }
    }
    return { type: 'open-tabs', viewKey: `plugin:${plugin.id}:${cmd.view}`, instanceKey: null }
  }
  return null
}

/** 返回某个区（left/center/right）的插件状态栏条目。 */
export function pluginBarItems(zone) {
  const { state } = usePlugins()
  const out = []
  for (const p of state.plugins) {
    if (!p || p.enabled === false) continue
    for (const s of p.statusbar || []) {
      if ((s.zone || 'right') !== zone) continue
      const command = normalizeCommand(p, s.command)
      if (!command) continue
      out.push({
        id: `plugin_${p.id}_${s.id}`,
        kind: 'button',
        zone,
        icon: s.icon || 'tool',
        label: s.tooltip || p.name,
        tooltip: s.tooltip || p.name,
        style: 'icon',
        command,
      })
    }
  }
  return out
}
