/**
 * sandboxTrust — 工具级沙箱「信任目录为空」判定（纯函数，可直接单测）。
 *
 * 背景（用户视角缺陷批 2 · 设置面 ②）：开启 usr `tool_sandbox` 后，executor 会注入 agentbox
 * 策略；策略内容 = prj `security-*`（信任目录）集合。**开而目录为空 → 空允许集 = 全拒**
 * （chonkpilot-mcp-server/server/config.go SandboxPolicyFor 返回 "[]"；
 *  chonkpilot-lib/agentbox/agentbox.go New："空允许集 = 全拒" 严格语义）。
 * → 该工具的所有文件操作会被拒绝。前端须显著内联预警并引导去配置信任目录。
 *
 * 判定口径：
 *   - 二者同时成立才告警：至少一个工具开关已开启（"已开启沙箱"）且信任目录有效条目为 0。
 *   - 「有效条目」= dir 非空白的条目（空目录名会被 agentbox 归一化丢弃，不计入允许集）。
 */

/** 统计有效信任目录条目数（兼容数组/对象 map 两种载荷）。 */
export function countTrustDirs(entries) {
  if (Array.isArray(entries)) {
    return entries.filter((e) => e && String(e.dir || '').trim() !== '').length
  }
  if (entries && typeof entries === 'object') {
    return countTrustDirs(Object.values(entries))
  }
  return 0
}

/**
 * 是否需要「信任目录为空」告警。
 * @param {{ sandboxOn: boolean, trustDirCount: number }} o
 */
export function needsTrustDirsWarning({ sandboxOn, trustDirCount } = {}) {
  return !!sandboxOn && !(Number(trustDirCount) > 0)
}
