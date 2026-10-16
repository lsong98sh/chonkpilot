import { defineStore } from 'pinia'
import { KEY_DEFINITIONS } from '../config/keys'

/**
 * 变量（key）store：预置变量由这里统一管理，响应式。
 * 多实例 view 的 instanceKey 指向这里的变量。
 */
export const useKeysStore = defineStore('keys', {
  state: () => {
    const values = {}
    KEY_DEFINITIONS.forEach((k) => {
      values[k.key] = k.defaultValue
    })
    return { values, definitions: KEY_DEFINITIONS }
  },
  getters: {
    /** 变量列表（含当前值），供下拉框展示 */
    list(state) {
      return state.definitions.map((d) => ({
        key: d.key,
        label: d.label,
        description: d.description,
        defaultValue: d.defaultValue,
        value: state.values[d.key] ?? '',
      }))
    },
  },
  actions: {
    /** 变量名 -> 当前值（响应式读取） */
    valueOf(key) {
      if (!key) return ''
      return this.values[key] ?? ''
    },
    setValue(key, value) {
      this.values[key] = value
    },
    reset() {
      this.definitions.forEach((d) => {
        this.values[d.key] = d.defaultValue
      })
    },
    /** 导出为快照，便于调试展示 */
    snapshot() {
      return { ...this.values }
    },
  },
})
