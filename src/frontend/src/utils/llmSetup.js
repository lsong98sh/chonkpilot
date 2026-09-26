/**
 * 发送失败提示的判定（用户视角缺陷批 1 – 问题 A）。
 *
 * 背景：默认 LLM = GUI 启动参数（chonkpilot-gui/main.go:512-513 默认
 * `-llm-base http://127.0.0.1:8901/v1` + `-llm-model mock`）；新用户 usr 配置无 llms 时
 * resolveDefaultLLM('') 落到「系统默认（启动参数）」（ChatPanel.SYSTEM_DEFAULT_LLM），
 * 首发消息可能失败。
 *
 * 口径变更（2026-09-20）：不再以「usr `llms` 是否为空」作门槛。
 * 依据（用户 2026-09-20 明确决定）：**兜底可用**（当时 = 内置项 `echo`，不发 HTTP、确实可用；
 * D-30（2026-09-22）后 = router 内置兜底 echo，无可用 provider 时启用）也视为可用 LLM，故
 * usr `llms` 为空 **不等于**「未配置」——原 `isLLMSetupMissing` 判据会误判「只用兜底」的用户，
 * 且用「usr llms 是否为空」区分措辞亦属脆弱判据（已删除该导出）。
 * 现行口径 = **本回合失败即提示**：一次发送以 `llm-complete{status:error}` 收尾就给
 * 「去配置 LLM」可行动提示（空回复除外），文案**同时覆盖**「未配置可用 LLM」与
 * 「当前 provider 调用失败」两种情形（同一 CTA，见 `chat.llm_setup_failed_hint`）。
 */

/**
 * 一次发送失败后，是否需要给出「去配置 LLM」的可行动提示。
 * 仅认 LLM 终态错误（`llm-complete{status:error}`）；空回复（code=EMPTY_REPLY）非配置问题，不提示。
 * @param {object} evt - llm-complete 终态载荷 {status, code?, ...}
 * @returns {boolean}
 */
export function needsLLMConfigHint(evt) {
  if (!evt || evt.status !== 'error') return false
  if (evt.code === 'EMPTY_REPLY') return false
  return true
}
