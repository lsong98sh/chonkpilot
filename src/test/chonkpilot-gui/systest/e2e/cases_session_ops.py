# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑯：**会话消息面契约**（迁移自 test_session_bridge.py S1–S9）。

共享单实例中「空库」断言改为**按库内不存在语义**验证：锚点会话提供 dialog_id，
其余会话/节点 id 使用不存在的哨兵；这样既保留契约强度，又不依赖空工作区。
"""

import os

from .framework import case


@case("S-SESS-BRIDGE-01", "会话", "会话消息面：latest/active 回读 / get-null / title 错误码 / delete 幂等 / content 空",
      stage="seeded", order=110, fp="61 §3.2 data-session-*（test_session_bridge）",
      desc="latest 返回对象；active-set → active-get 原样回读；list 非空且含锚点；get 不存在 → "
           "data=null；title 不存在 → E_SESSION_NOT_FOUND；delete 不存在幂等；content 未知 key → "
           "空 contents；history 缺 session_id → 报错。")
def s_sess_bridge_01(env):
    anchor = "e2e-bridge-%s" % env.run_id
    a = env.req("data-session-ensure-session", {"data": {"session_id": anchor}}) or {}
    dlg = a.get("dialog_id") or ""
    env.check("锚点会话 dialog_id", bool(dlg), dlg)

    latest = env.req("data-session-latest", {}) or {}
    env.check("latest 返回对象", isinstance(latest, dict), latest)

    active_id = "e2e-bridge-active-%s" % env.run_id
    env.req("data-session-ensure-session", {"data": {"session_id": active_id}})
    env.req("data-session-active-set", {"data": {"session_id": active_id}})
    got = env.req("data-session-active-get", {}) or {}
    env.eq("active 回读写入值（不校验存在性）", got.get("session_id"), active_id)

    rows = (env.req("data-session-list", {}) or {}).get("list") or []
    env.check("list 为数组且含锚点",
              any((x or {}).get("session_id") == anchor for x in rows), rows[:5])

    r = env.req("data-session-get", {"id": "e2e-no-such-%s" % env.run_id,
                                     "dialog_id": dlg}) or {}
    env.check("get 不存在返回 data=null", r.get("data") is None, r)

    raised = ""
    try:
        env.req("data-session-title",
                {"data": {"id": "e2e-no-such-%s" % env.run_id, "title": "x",
                          "dialog_id": dlg}})
    except Exception as e:
        raised = str(e)
    env.check("title 不存在 → E_SESSION_NOT_FOUND", "E_SESSION_NOT_FOUND" in raised,
              raised[:120])

    try:
        env.req("data-session-delete", {"id": "e2e-no-such-%s" % env.run_id,
                                        "dialog_id": dlg})
        env.check("delete 不存在幂等成功", True)
    except Exception as e:
        env.check("delete 不存在幂等成功", False, str(e)[:120])

    r = env.req("data-session-content",
                {"data": {"session_id": anchor, "keys": ["message:none"], "dialog_id": dlg}}) or {}
    env.eq("content 未知 key → 空 contents", r.get("contents"), {})

    raised = ""
    try:
        env.req("data-session-history", {"data": {}})
    except Exception as e:
        raised = str(e)
    env.check("history 缺 session_id 报错", "session_id" in raised, raised[:120])

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("会话契约组无前端错误", len(errs) == 0, errs[:3])

# ── 对话库自举 + 重启幂等（迁移自 run_data_shard.py S1/S2） ─────────────────

_SHARD = {}


@case("S-SHARD-01", "会话", "对话库自举：ensure-session 生成并回带 dialog_id，session-get 可按 id 回读",
      stage="seeded", order=112, fp="42 一树一库 · run_data_shard S1",
      desc="唯一会话 ensure-session → 返回非空 dialog_id；data-session-get 回读同一 session_id；"
           "记录该 dialog_id 供重启阶段验证不重复建库。")
def s_shard_01(env):
    sid = "e2e-shard-%s" % env.run_id
    r = env.req("data-session-ensure-session", {"data": {"session_id": sid}}) or {}
    dlg = str(r.get("dialog_id") or "")
    env.check("自举回带 dialog_id", bool(dlg), dlg)
    got = (env.req("data-session-get", {"id": sid}) or {}).get("data") or {}
    env.eq("session-get 回读 session_id", got.get("session_id"), sid)
    _SHARD.update({"sid": sid, "dlg": dlg})


@case("R-SHARD-01", "会话", "重启幂等：同一会话 ensure 不重复建库（dialog_id 不变）",
      stage="restart", order=113, fp="42 一树一库 · run_data_shard S2",
      desc="重启后对同一 session_id 再次 ensure-session → dialog_id 与重启前一致；会话可正常回读；"
           "随后删除该会话。")
def r_shard_01(env):
    if not _SHARD.get("sid"):
        env.check("重启前夹具存在（需与 S-SHARD-01 同次运行）", False, _SHARD)
        return
    sid, dlg = _SHARD["sid"], _SHARD["dlg"]
    r = env.req("data-session-ensure-session", {"data": {"session_id": sid}}) or {}
    env.check("重启后 ensure 不重复建库（dialog_id 不变）",
              str(r.get("dialog_id") or "") == dlg, {"before": dlg, "after": r.get("dialog_id")})
    got = (env.req("data-session-get", {"id": sid}) or {}).get("data") or {}
    env.eq("重启后会话可回读", got.get("session_id"), sid)
    try:
        env.req("data-session-delete", {"id": sid})
    except Exception:
        pass

