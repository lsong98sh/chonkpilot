/**
 * sandboxTrust — executor 级沙箱「信任目录为空」判定（纯函数，可直接单测）。
 *
 * 背景（用户视角缺陷批 2 · 设置面 ②）；2026-09-26 沙箱由工具级改 executor 级：开启 usr
 * `tool_sandbox`（按被 spawn 的执行体）后，本应注入 agentbox 目录策略（内容 = prj `security-*`
 * 信任目录）。**2026-10-21（GATEWAY-2 L3/L5）重构后**：应用层 `SecurityDirs` 全拒校验已删除 ——
 *   - 外部 MCP（stdio）：开关开 + 信任目录非空才注入策略；**空目录 = 不下发（不施加隔离）**；
 *   - 内建 executor：进程级沙箱**待 L6**（当前 spawn 不注入 `CHONKPILOT_SANDBOX`）。
 * 故「开而目录为空」仍需**显著内联预警**：此时沙箱开关不会带来任何目录限制。
 * 见 `src/lib/gateway/gateway/serverslist.go`（SandboxPolicyJSON）· `src/lib/llm/server/server.go`。
 *
 * 判定口径：
 *   - 二者同时成立才告警：至少一个工具开关已开启（"已开启沙箱"）且信任目录有效条目为 0。
 *   - 「有效条目」= dir 非空白的条目（空目录名不计入允许集）。
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
