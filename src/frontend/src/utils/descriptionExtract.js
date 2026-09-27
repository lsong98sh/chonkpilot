// 描述提取纯函数（2026-09-27 用户口径）：从原语「正文」本地确定性提取一版描述，不依赖 LLM。
// 规则：取正文**首个非空、非 markdown 标题**（`#`/`>` 开头）的段落 → 去行内 markdown 标记
// （图片/链接语法取文字、去 `*`/`` ` ``/`_`）→ 折叠多余空白 → 超长按字数截断并追加省略号。
// 无可用段落 → 返回空串（调用方据此提示）。

// extractDescriptionFromContent 提取描述文本；limit = 截断字数上限（超出加省略号）。
export function extractDescriptionFromContent(content, limit = 200) {
  const text = String(content === undefined || content === null ? '' : content).replace(/\r\n?/g, '\n')
  for (const para of text.split(/\n\s*\n/)) {
    const block = para.trim()
    if (!block) continue
    if (block.startsWith('#') || block.startsWith('>')) continue // markdown 标题/引用不作为描述来源
    const cleaned = block
      .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1') // 图片 → alt 文本
      .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1') // 链接 → 锚文本
      .replace(/[*`_]/g, '') // 去行内强调/代码标记
      .replace(/\s+/g, ' ') // 折叠多行/多余空白为单空格
      .trim()
    if (!cleaned) continue
    return cleaned.length > limit ? cleaned.slice(0, limit) + '…' : cleaned
  }
  return ''
}
