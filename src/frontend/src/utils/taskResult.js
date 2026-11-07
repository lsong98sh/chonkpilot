/**
 * 任务详情「最终结果 / 运行输出」分区判定（纯逻辑）。
 *
 * 后端 TaskInfo（`tasks.done` 载荷）新增 `result_summary`（终态结果全文），
 * 同时仍保留 `output`（流式/部分输出）。展示口径：
 *   - 仅有 result_summary → 只显示最终结果；
 *   - 仅有 output        → 只显示运行输出；
 *   - 两者都有且内容相同 → 只显示一次（优先最终结果）；
 *   - 两者都有且不同     → 最终结果 + 部分输出两块。
 */
export function taskResultSections(task) {
  const summaryRaw = task && typeof task.result_summary === 'string' ? task.result_summary : ''
  const outputRaw = task && typeof task.output === 'string' ? task.output : ''
  const summary = summaryRaw.trim()
  const output = outputRaw.trim()
  return {
    finalResult: summary ? summaryRaw : '',
    // 与最终结果内容一致（忽略首尾空白）时去重，避免同一段文字展示两遍
    partialOutput: output && output !== summary ? outputRaw : '',
  }
}