/**
 * 插件 UI 动作面（PLUGIN-UI-1 第二段）：`POST /plugins/<id>/api/<action>`。
 *
 * 前端**必须**注入 `X-CK-Plugin-Id`（= 来源视图的插件 id）：宿主据此校验「请求来源视图 id ==
 * 路径 <id>」，不一致 → 403（防跨插件串用）。宿主不解析业务：成功 = 200 + 插件结果 JSON **原样**，
 * 失败 = 4xx/5xx + `{ok:false, errors:[...]}` 信封。
 */
export async function pluginAction(pluginId, action, payload = {}) {
  const res = await fetch(
    `/plugins/${encodeURIComponent(pluginId)}/api/${encodeURIComponent(action)}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-CK-Plugin-Id': pluginId },
      body: JSON.stringify(payload || {}),
    },
  )
  let body = null
  try {
    body = await res.json()
  } catch (_) {
    /* 非 JSON 响应体：按状态码报错 */
  }
  if (!res.ok) {
    const errors = (body && Array.isArray(body.errors) && body.errors) || [`HTTP ${res.status}`]
    throw new Error(errors.join('; '))
  }
  return body
}
