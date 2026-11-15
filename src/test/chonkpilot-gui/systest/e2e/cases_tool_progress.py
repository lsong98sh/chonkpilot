# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉖：**工具进度 title 三态**（迁移自 run_tool_progress.py）。

mock 两轮产生 3 个工具条目（2×task_run + 1×file_read），经后端→前端同一入口
`mq.emitRemote('mcp-tools-progress')` 投递：
* `progress=2,total=5` → 该条 title = 「进度 40%」；
* 无进度事件的条目 → 无 title；
* `progress=3`（缺 total）→ 不显示百分比。
"""

import json
import time

from .framework import case
from .cases_chat_ops import _open_session, _select, _send

PAIRS_JS = """JSON.stringify((()=>{const root=document.querySelector('.chat-panel .message-list');
  if(!root)return [];
  return [...root.querySelectorAll('.message-item.tool_pair .toolpair-section')].map(el=>{
    const item=el.closest('.message-item');
    return {label:((el.querySelector('.tool-name-label')||{}).textContent||'').trim(),
            title:el.getAttribute('title'),
            message_id:item?item.getAttribute('data-id'):''};});})())"""


def _pairs(env):
    return env.jsj(PAIRS_JS) or []


def _inject(env, tool_call_id, progress, total=None):
    body = {"instance_id": str(env.js("window.__chonkpilotInstanceId || ''") or ""),
            "tool_call_id": tool_call_id, "progress": progress}
    if total is not None:
        body["total"] = total
    env.js("window.mq.emitRemote({type:'mcp-tools-progress', payload: JSON.stringify(%s)}); 'ok'"
           % json.dumps(body, ensure_ascii=False))


@case("S-TOOL-PROGRESS-01", "任务树", "工具条目进度 title：有 total→进度 N% / 无事件→无 / 缺 total→无",
      stage="seeded", order=442, fp="61 §5.3 mcp-tools-progress（run_tool_progress）",
      desc="mock「please call batch」→ 2×task_run、「please call tool」→ file_read，DOM 恰好 3 条且顺序"
           "与 tool-call 一致、初始 title 全空；注入 2/5 → 首条 title「进度 40%」；缺 total → 不显示。")
def s_tool_progress_01(env):
    _select(env, "mock")
    sid = "e2e-prog-%s" % env.run_id
    _open_session(env, sid)
    env.capture(["llm-complete", "mcp-tools-progress"])

    _send(env, sid, "please call batch", llm="mock")
    env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                    "batch 轮完成", timeout=120)
    two = env.wait(lambda: _pairs(env) if len(_pairs(env)) == 2 else None,
                   "第一轮 2 个工具条目", timeout=60)
    env.check("第一轮 2×task_run 条目", len(two) == 2, two)

    _send(env, sid, "please call tool", llm="mock")
    env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                    "tool 轮完成", timeout=120)
    three = env.wait(lambda: _pairs(env) if len(_pairs(env)) == 3 else None,
                     "第二轮累计 3 个工具条目", timeout=60)
    env.check("DOM 恰好 3 条工具条目", len(three) == 3, three)
    labels = [p.get("label") or "" for p in three]
    env.check("条目顺序 2×task_run + file_read",
              sum("task_run" in x for x in labels) == 2
              and any("file_read" in x for x in labels), labels)
    env.check("注入前进度 title 全空", [p.get("title") for p in three] == [None, None, None],
              [p.get("title") for p in three])

    ids = [str(p.get("message_id") or "").replace("tp_", "", 1) for p in three]
    env.check("条目带 tool_call_id（tp_）", all(ids), ids)

    _inject(env, ids[0], 2, total=5)
    ev = env.wait_events("mcp-tools-progress",
                         lambda p: (p or {}).get("tool_call_id") == ids[0],
                         "进度事件到达前端", timeout=15)
    env.check("进度载荷逐字段对齐",
              (ev or {}).get("progress") == 2 and (ev or {}).get("total") == 5, ev)
    title0 = env.wait(lambda: (lambda v: v if v == "进度 40%" else None)(
        (_pairs(env)[0] or {}).get("title") if _pairs(env) else None),
        "首条 title 进度 40%", timeout=20)
    env.check("有 total → title「进度 40%」", title0 == "进度 40%", title0)
    cur = _pairs(env)
    env.check("无进度事件的条目无 title",
              len(cur) == 3 and cur[1].get("title") is None and cur[2].get("title") is None,
              [p.get("title") for p in cur])

    _inject(env, ids[2], 3)
    ev2 = env.wait_events("mcp-tools-progress",
                          lambda p: (p or {}).get("tool_call_id") == ids[2],
                          "无 total 进度事件", timeout=15)
    env.check("缺 total 的载荷不带 total 键", "total" not in (ev2 or {}), ev2)
    time.sleep(1.0)
    cur2 = _pairs(env)
    env.check("缺 total → 不显示百分比 title", (cur2[2] or {}).get("title") is None,
              [p.get("title") for p in cur2])
    env.check("三态终值 [进度 40%, None, None]",
              [p.get("title") for p in cur2] == ["进度 40%", None, None],
              [p.get("title") for p in cur2])

    try:
        env.req("data-session-delete", {"id": sid})
    except Exception:
        pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("工具进度组无前端错误", len(errs) == 0, errs[:3])