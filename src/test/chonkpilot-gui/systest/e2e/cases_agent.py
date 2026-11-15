# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑥：ask_user 待办、子代理/单聊、群聊/多聊。

* ask_user：真实 LLM 轮次触发 `ask_user` 工具 → Ask 中心**待办**出现 → 用户作答 → 轮次继续、
  待办移除；另测**超时自动回复**（推荐值回填，轮次不中断）。
* 子代理（单聊）：`subchat_open` 真实工具 → `subchat-opened` → 子会话行（kind/parent/固定参数）
  → `subchat-list` 归属 → 用户 `subchat-finish` 结束。
* 群聊（多聊）：数据面（participants / round-targets / sender_agent+mentions 历史）+ 真实调度
  （`subchat-opened{mode:group}` → participant-state → 展示板消息 → 终止）。

Ask 中心是**同进程独立窗口**（`gui.window.list.ask_window_id`，用 window_id 路由 `/eval`）；
不是第二个进程，也不改变「一次启动」约束。
"""

import json
import time

import harness as _h
import ask_center as ac_mod

from .framework import case
from .workspace import SCENARIO_ID, SCENARIO_MAIN, SCENARIO_PEERS, ensure_scenario


def _payloads(env, topic):
    return [e.get("payload") for e in env.events(topic) if isinstance(e.get("payload"), dict)]


def _start(env, sid, q):
    """发一轮 llm-start（不等待完成；调用方自行等待 ask/子会话/终态）。返回 ensure 应答。"""
    res = env.req("data-session-ensure-session", {"data": {"session_id": sid}}) or {}
    env.emit("llm-start", {"session_id": sid, "turn": "t-" + sid, "q": q,
                           "llm": "mock", "think": "", "effort": "", "scenario_id": ""})
    return res


def _history_rows(env, sid, dialog_id=""):
    """读会话历史并归一为消息行列表（兼容 `{messages:{messages:[]}}` / 字符串包装）。"""
    payload = {"session_id": sid, "target_messages": 50, "target_bytes": 262144}
    if dialog_id:
        payload["dialog_id"] = dialog_id
    hist = env.req("data-session-history", payload) or {}
    if isinstance(hist, str):
        try:
            hist = json.loads(hist)
        except Exception:
            return []
    msgs = hist.get("messages") if isinstance(hist, dict) else None
    if isinstance(msgs, str):
        try:
            msgs = json.loads(msgs)
        except Exception:
            return []
    if isinstance(msgs, dict):
        msgs = msgs.get("messages")
    return msgs if isinstance(msgs, list) else []


def _dialog_for(env, sid, fallback=""):
    """解析会话所在对话库 id（一树一库；子会话不在 data-session-list 顶层，需父库）。"""
    for it in ((env.req("data-session-list", {}) or {}).get("list") or []):
        if it.get("session_id") == sid and it.get("dialog_id"):
            return it["dialog_id"]
    return fallback or ((getattr(env.client, "_dlg_cache", {}) or {}).get(sid, ""))


def _sess_get(env, sid, dialog_id=""):
    p = {"id": sid}
    if dialog_id:
        p["dialog_id"] = dialog_id
    return (env.req("data-session-get", p) or {}).get("data") or {}


def _open_ask(env, ask, timeout=30):
    """幂等打开/激活 Ask 中心（`gui.window.open-ask`：已开 → 激活并直推 ask-center-add）。

    必要性：Ask 窗口挂载时 `ask-center-subscribe` 的**快照应答**可能晚于首次 push 到达，
    覆盖本地列表（实测「左列条目短暂消失」）→ 显式再推一次，确保交互期间条目在列。
    """
    env.req("gui.window.open-ask", {"ask": ask})
    return ac_mod.ask_client(env.client, timeout=timeout)


def _answer_custom(env, ac, ask, answer):
    """选中待办 → 填自定义文本 → 提交（对「快照刷新清空选中/条目」做短重试）。"""
    ask_id = ask.get("ask_id")
    res = ""
    for _ in range(30):
        if not ac_mod.has_item(ac, ask_id):
            try:
                env.req("gui.window.open-ask", {"ask": ask})
            except Exception:
                pass
            ac_mod.wait_item(ac, ask_id, timeout=5)
            continue
        ac_mod.select_item(ac, ask_id)
        time.sleep(0.15)
        ac_mod.set_custom(ac, answer)
        time.sleep(0.15)
        res = ac_mod.click_submit(ac)
        if _h._plain(res) == "ok":
            return "ok"
        time.sleep(0.2)
    return _h._plain(res)


def _ask_list(env):
    r = env.req("gui.ask.list", {}) or {}
    return r.get("asks") or []


@case("S-ASK-01", "ask_user", "Ask 待办闭环：工具提问 → 待办出现 → 用户作答 → 轮次继续 → 待办移除",
      stage="seeded", order=190, fp="61 §4.3 ask-user/ask-center-* · 33 CHAT-014",
      desc="llm-start 触发 ask_user（自由文本）→ Ask 中心窗口自动打开、左列待办出现（问题/输入框）→ "
           "输入并提交 → 主轮 llm-complete 正常、ask-center-remove、gui.ask.list 不再含该 ask。")
def s_ask_01_todo_roundtrip(env):
    sid = "e2e-ask-%s" % env.run_id
    env.capture(["turn-start", "ask-user", "ask-center-add", "ask-center-remove",
                 "llm-complete", "tool-result"])
    _start(env, sid, "please call ask")
    env.wait_events("turn-start", lambda p: p.get("session") == sid, "主轮 turn-start", timeout=30)
    ask = env.wait_events("ask-user",
                          lambda p: p.get("ask_id") and p.get("session") == sid,
                          "ask-user(待办) 事件", timeout=40)
    ask_id = ask.get("ask_id")
    env.check("ask 携带 ask_id", bool(ask_id), ask)
    env.check("ask 携带问题文本", bool((ask.get("question") or "").strip()), ask.get("question"))
    env.check("ask 记录归属会话", ask.get("session") == sid, ask.get("session"))
    # `ask-center-add` 只投递给 Ask 中心窗口（宿主按 window 定向），主窗口不捕获；
    # 待办入列以下方 `ask_client` + 左列 DOM 为准。

    wid, ac = _open_ask(env, ask, timeout=30)
    env.check("Ask 中心窗口已自动打开", bool(wid), wid)
    env.check("待办项出现在左列", ac_mod.wait_item(ac, ask_id, timeout=20), ask_id)
    qtext = ac_mod.question_text(ac)
    env.check("待办详情显示问题", (ask.get("question") or "")[:8] in (qtext or ""),
              "detail=%r event=%r" % (qtext, ask.get("question")))

    answer = "E2E-ASK-ANSWER-%s" % env.run_id
    before_turns = len(_payloads(env, "turn-start"))
    res = _answer_custom(env, ac, ask, answer)
    env.note("提交返回", res)
    if res != "ok":
        env.note("Ask 详情按钮", ac.eval(
            "JSON.stringify([...document.querySelectorAll('.ask-detail button')]"
            ".map(b=>({cls:b.className,dis:b.disabled,txt:b.textContent.trim()})))"))
        env.note("Ask 详情文本", ac.eval("document.querySelector('.ask-detail')?.innerText || ''"))
    env.check("提交待办（自由文本）", res == "ok", res)
    env.check("待办项已从列表移除", ac_mod.wait_no_item(ac, ask_id, timeout=20), ask_id)

    # 回答以「后续用户消息」投递 → 触发续轮（本轮在提问后即完成，不阻塞）
    env.wait(lambda: len(_payloads(env, "turn-start")) > before_turns,
             "作答触发后续轮次", timeout=30)
    dlg = ask.get("dialog_id") or ""
    rows = _history_rows(env, sid, dlg)
    env.check("历史含用户作答内容",
              any(m.get("role") == "user" and answer in (m.get("content") or "")
                  for m in rows),
              [m.get("content")[:40] for m in rows if m.get("role") == "user"])
    env.wait(lambda: not any((a or {}).get("ask_id") == ask_id for a in _ask_list(env)),
             "gui.ask.list 不再含该待办", timeout=15)
    env.note("作答回填", "answer=%s" % answer)
    ac_mod.close_center(ac, env.client, timeout=10)


@case("S-ASK-02", "ask_user", "Ask 超时自动回复：推荐值回填 → 轮次不中断 → 待办移除",
      stage="seeded", order=200, fp="61 §4.3 ask-user(timeout) · 33 CHAT-014",
      desc="ask_user 携带 timeout=8 + recommended=[B] → 不作答，到点后自动以推荐值回复："
           "主轮 llm-complete 正常、tool-result 保留超时占位、待办移除。")
def s_ask_02_timeout_auto(env):
    sid = "e2e-askto-%s" % env.run_id
    # 超时来源 = **项目配置 `ask_timeout_sec`**（工具参数 `timeout` 被 gateway 作为调用级
    # 超时消费并从 args 剥离，不再能传给 ask_user）。
    env.req("data-prj-config-save", {"data": {"key": "ask_timeout_sec", "value": "8"}})
    env.capture(["turn-start", "ask-user", "llm-complete", "tool-result"])
    _start(env, sid, "please call ask-timeout")
    env.wait_events("turn-start", lambda p: p.get("session") == sid, "主轮 turn-start", timeout=30)
    ask = env.wait_events("ask-user",
                          lambda p: p.get("ask_id") and p.get("session") == sid,
                          "ask-user(超时待办)", timeout=40)
    ask_id = ask.get("ask_id")
    env.check("ask 带推荐值", bool(ask.get("recommended")), ask.get("recommended"))
    env.check("ask 选项为 A/B", ask.get("options") == ["A", "B"], ask.get("options"))

    _, ac = _open_ask(env, ask, timeout=30)
    env.check("待办入列（Ask 中心）", ac_mod.wait_item(ac, ask_id, timeout=20), ask_id)
    item = next((a for a in _ask_list(env) if (a or {}).get("ask_id") == ask_id), {})
    exp = item.get("expires_at")
    env.check("待办带超时截止时刻（expires_at 非空）",
              bool(exp) and str(exp) != "0", exp)

    # 不点任何按钮：到点后 server 以推荐值自动回复并续轮
    env.check("超时后待办自动移除", ac_mod.wait_no_item(ac, ask_id, timeout=30), ask_id)
    env.wait(lambda: not any((a or {}).get("ask_id") == ask_id for a in _ask_list(env)),
             "gui.ask.list 不再含该待办", timeout=15)

    tr = [p for p in _payloads(env, "tool-result")
          if (p.get("tool") or "").endswith("ask_user")]
    env.check("ask_user 工具结果已回填", len(tr) >= 1, [p.get("tool") for p in tr])
    env.eq("超时自动答复 = 推荐值 B", (tr[-1] or {}).get("result"), "B")

    # 自动答复同样以「后续用户消息」投递（推荐值 B）
    dlg = ask.get("dialog_id") or ""
    def auto_answer():
        rows = _history_rows(env, sid, dlg)
        return next((m for m in rows
                     if m.get("role") == "user"
                     and ("答：B" in (m.get("content") or "")
                          or (m.get("content") or "").strip() == "B")), None)

    hit = env.wait(auto_answer, "历史出现推荐值自动答复（答：B）", timeout=30)
    env.check("自动答复内容 = 推荐值 B",
              ("答：B" in (hit or {}).get("content", "")
               or (hit or {}).get("content", "").strip() == "B"), hit)

    done = env.wait_events("llm-complete", lambda p: p.get("session") == sid,
                           "自动答复后续轮完成", timeout=60)
    env.eq("后续轮终态", (done or {}).get("status"), "complete")
    try:
        env.req("data-prj-config-delete", {"id": "ask_timeout_sec"})
    except Exception:
        pass
    try:
        ac_mod.close_center(ac, env.client, timeout=10)
    except Exception:
        pass


@case("S-SUBCHAT-01", "子代理", "子代理/单聊：subchat_open 建子会话 → 归属/固定参数回读 → 结束",
      stage="seeded", order=210, fp="61 §4.3 subchat-open/state/list · 33 CHAT-024",
      desc="场景团队夹具 → mock 决策 subchat_open（agent+scenario）→ subchat-opened 父会话归属、"
           "data-session-get kind=sub_chat/固定 agent/lifecycle=chatting → subchat-list 归属 → "
           "subchat-finish 结束（finished），主轮不被阻塞。")
def s_subchat_01_subagent(env):
    sc = ensure_scenario(env)
    _h.wait_tools(env.client, ("subchat_open",), max_wait=60)
    sid = "e2e-subparent-%s" % env.run_id
    env.capture(["turn-start", "subchat-opened", "subchat-state", "tool-result", "llm-complete"])
    pres = _start(env, sid, "【T-E2E-SUBCHAT】t-e2e-subchat e2escenario=%s e2eagent=%s"
                  % (sc, SCENARIO_MAIN))
    dlg = pres.get("dialog_id") or _dialog_for(env, sid)
    env.check("父会话 dialog_id 可解析（子会话同一对话库）", bool(dlg), dlg)
    env.wait_events("turn-start", lambda p: p.get("session") == sid, "主轮 turn-start", timeout=30)
    opened = env.wait_events("subchat-opened",
                             lambda p: p.get("parent_session_id") == sid and p.get("session_id"),
                             "subchat-opened（子代理会话）", timeout=60)
    sub = opened.get("session_id")
    env.check("subchat-opened 归属父会话", opened.get("parent_session_id") == sid, opened)
    env.note("子会话", "sub=%s title=%r" % (sub, opened.get("title")))

    row = env.wait(lambda: _sess_get(env, sub, dlg)
                   if _sess_get(env, sub, dlg).get("kind") == "sub_chat" else None,
                   "子会话行 kind=sub_chat", timeout=20)
    env.eq("子会话 parent_id", row.get("parent_id"), sid)
    env.eq("子会话固定 agent", row.get("agent"), SCENARIO_MAIN)
    env.eq("子会话固定场景", row.get("scenario_id"), sc)
    env.eq("子会话初始 lifecycle", row.get("lifecycle"), "chatting")

    lst = env.wait(lambda: [x for x in ((env.req("subchat-list", {"parent_id": sid}) or {})
                                        .get("list") or [])
                            if x.get("session_id") == sub] or None,
                   "subchat-list 含该子会话", timeout=20)
    env.check("subchat-list 归属父会话", len(lst) == 1, lst)

    done = env.wait_events("llm-complete", lambda p: p.get("session") == sid,
                           "主轮不被子代理阻塞（llm-complete）", timeout=120)
    env.eq("主轮终态", (done or {}).get("status"), "complete")

    env.req("subchat-finish", {"session_id": sub})
    fin = env.wait(lambda: _sess_get(env, sub, dlg)
                   if _sess_get(env, sub, dlg).get("lifecycle") in ("finished", "ready") else None,
                   "子会话 lifecycle 结束", timeout=25)
    env.check("子会话已结束（finished/ready）",
              fin.get("lifecycle") in ("finished", "ready"), fin.get("lifecycle"))


@case("S-GROUP-01", "群聊", "群聊数据面：参与者名册 seq / 大轮次投递对象 / 群聊消息 sender_agent+mentions",
      stage="seeded", order=220, fp="61 §3.2c data-session-group-* · §3.2a sender_agent/mentions",
      desc="建 group_chat 会话 → participants 整表替换（seq 按序 1..N）→ round-targets 幂等写读（非法轮次失败）"
           "→ append-message 带 sender_agent/mentions → history 回读同字段。")
def s_group_01_data_plane(env):
    sc = ensure_scenario(env)
    parent = "e2e-group-parent-%s" % env.run_id
    gid = "e2e-group-%s" % env.run_id
    pres = env.req("data-session-ensure-session", {"data": {"session_id": parent}}) or {}
    dlg = pres.get("dialog_id") or _dialog_for(env, parent)
    env.req("data-session-ensure-session", {"data": {
        "session_id": gid, "dialog_id": dlg, "parent_session_id": parent,
        "kind": "group_chat", "lifecycle": "chatting", "agent": SCENARIO_MAIN,
        "scenario_id": sc, "llm": "mock", "purpose": "E2E 群聊",
        "goal": "E2E-GOAL-%s" % env.run_id, "playbook": "E2E-PLAYBOOK",
        "convergence": "E2E-CONVERGE",
    }})
    row = _sess_get(env, gid, dlg)
    env.eq("群聊会话 kind", row.get("kind"), "group_chat")
    env.eq("群聊会话 parent_id", row.get("parent_id"), parent)
    env.note("群聊行字段（lifecycle 由调度器路径覆盖；get 面未全部暴露）",
             {k: row.get(k) for k in ("kind", "parent_id", "agent", "lifecycle",
                                      "goal", "playbook", "convergence")})

    order = [SCENARIO_PEERS[0], SCENARIO_MAIN, SCENARIO_PEERS[1]]
    env.req("data-session-group-participants-save", {"data": {
        "session_id": gid,
        "list": [{"agent": a, "role": "role-%d" % i} for i, a in enumerate(order, 1)],
    }})
    plist = (env.req("data-session-group-participants-load", {"session_id": gid}) or {}).get("list") or []
    env.check("参与者名单数量", len(plist) == 3, plist)
    env.eq("参与者顺序 = 传入顺序", [x.get("agent") for x in plist], order)
    env.eq("参与者 seq = 1..N", [x.get("seq") for x in plist], [1, 2, 3])
    env.eq("参与者角色保留", [x.get("role") for x in plist],
           ["role-1", "role-2", "role-3"])

    agents = [SCENARIO_MAIN, SCENARIO_PEERS[0]]
    env.req("data-session-group-round-targets-save", {"data": {
        "session_id": gid, "round": 1, "agents": agents}})
    rt = (env.req("data-session-group-round-targets-load",
                  {"session_id": gid, "round": 1}) or {}).get("list") or []
    env.eq("轮次 1 投递对象", (rt[0] or {}).get("agents"), agents)
    allrt = (env.req("data-session-group-round-targets-load", {"session_id": gid}) or {}).get("list") or []
    env.check("缺省读全部轮次", any(x.get("round") == 1 for x in allrt), allrt)
    try:
        env.req("data-session-group-round-targets-save",
                {"data": {"session_id": gid, "round": 0, "agents": agents}})
        bad = False
    except Exception:
        bad = True
    env.check("非法轮次（round=0）被拒绝", bad)

    mark = "E2E-GROUP-MSG-%s" % env.run_id
    # 历史按「轮次」装配：先幂等建轮次，消息才会出现在 data-session-history
    env.req("data-session-ensure-turn",
            {"turn_id": "t-" + gid, "session_id": gid, "dialog_id": dlg})
    ap = env.req("data-session-append-message", {"turn_id": "t-" + gid, "dialog_id": dlg, "msg": {
        "role": "assistant", "content": mark + " @%s" % SCENARIO_PEERS[0],
        "session_id": gid, "sender_agent": SCENARIO_MAIN,
        "mentions": [SCENARIO_PEERS[0]],
    }})
    env.note("append-message 回执", ap)
    raw_hist = env.req("data-session-history",
                       {"session_id": gid, "dialog_id": dlg,
                        "target_messages": 50, "target_bytes": 262144}) or {}
    env.note("history 原始形态", str(raw_hist)[:300])
    rows = _history_rows(env, gid, dlg)
    hit = next((m for m in rows if mark in (m.get("content") or "")), None)
    env.check("历史含群聊消息", hit is not None, "rows=%d" % len(rows))
    env.eq("消息 sender_agent", (hit or {}).get("sender_agent"), SCENARIO_MAIN)
    env.eq("消息 mentions", (hit or {}).get("mentions"), [SCENARIO_PEERS[0]])


@case("S-GROUP-02", "群聊", "群聊真实调度：subchat_open(participants) → 依次发言 → 终止",
      stage="seeded", order=230, fp="61 §4.3 subchat-opened/participant-state/state · 33 CHAT-025",
      desc="mock 决策 subchat_open（2 名参与者）→ subchat-opened{mode:group} → subchat-state=chatting → "
           "subchat-participant-state（thinking/idle/spoken）→ 群聊历史出现 sender_agent → "
           "subchat-finish 终止（finished）。")
def s_group_02_scheduler(env):
    sc = ensure_scenario(env)
    _h.wait_tools(env.client, ("subchat_open",), max_wait=60)
    sid = "e2e-groupmain-%s" % env.run_id
    env.capture(["turn-start", "subchat-opened", "subchat-state", "subchat-participant-state",
                 "llm-complete", "tool-result"])
    pres = _start(env, sid, "【T-E2E-GROUP】t-e2e-group e2escenario=%s e2eparticipants=%s,%s"
                  % (sc, SCENARIO_PEERS[0], SCENARIO_PEERS[1]))
    dlg = pres.get("dialog_id") or _dialog_for(env, sid)
    env.wait_events("turn-start", lambda p: p.get("session") == sid, "主轮 turn-start", timeout=30)
    opened = env.wait_events("subchat-opened",
                             lambda p: p.get("parent_session_id") == sid and p.get("session_id"),
                             "subchat-opened（群聊）", timeout=60)
    gid = opened.get("session_id")
    env.eq("群聊标题 = purpose", opened.get("title"), "E2E 群聊")
    grow = _sess_get(env, gid, dlg)
    env.eq("群聊会话 kind", grow.get("kind"), "group_chat")
    env.check("群聊会话 participant 名册可读",
              len(((env.req("data-session-group-participants-load",
                            {"session_id": gid}) or {}).get("list") or [])) >= 2,
              (env.req("data-session-group-participants-load", {"session_id": gid}) or {})
              .get("list"))

    st = env.wait_events("subchat-state", lambda p: p.get("session_id") == gid,
                         "subchat-state（群聊生命周期）", timeout=60)
    env.check("群聊生命周期 chatting/finished",
              (st or {}).get("lifecycle") in ("chatting", "finished", "ready"), st)

    ps = env.wait_events("subchat-participant-state", lambda p: p.get("session_id") == gid,
                         "participant-state（参与者依次发言）", timeout=90)
    env.check("参与者状态合法",
              (ps or {}).get("state") in ("thinking", "idle", "spoken"), ps)

    def group_history_has_sender():
        rows = _history_rows(env, gid, dlg)
        return next((m for m in rows if m.get("sender_agent")), None)

    hit = env.wait(group_history_has_sender, "群聊历史出现 sender_agent", timeout=60)
    env.check("群聊消息带发言者", bool(hit.get("sender_agent")), hit)

    env.req("subchat-finish", {"session_id": gid})
    fin = env.wait_events("subchat-state",
                          lambda p: p.get("session_id") == gid
                          and p.get("lifecycle") in ("finished", "ready"),
                          "终止后 subchat-state", timeout=30)
    env.check("群聊已终止", fin.get("lifecycle") in ("finished", "ready"), fin)


def _answer_multi(env, ac, ask, labels):
    """多选作答：选中待办 → 依次勾选选项 → 提交（对快照刷新导致的选中丢失做短重试）。"""
    ask_id = ask.get("ask_id")
    res = ""
    for _ in range(12):
        if not ac_mod.has_item(ac, ask_id):
            try:
                env.req("gui.window.open-ask", {"ask": ask})
            except Exception:
                pass
            ac_mod.wait_item(ac, ask_id, timeout=5)
            continue
        ac_mod.select_item(ac, ask_id)
        time.sleep(0.2)
        for label in labels:
            ac_mod.click_option(ac, label)
            time.sleep(0.15)
        time.sleep(0.3)
        res = ac_mod.click_submit(ac)
        if _h._plain(res) == "ok":
            return "ok"
        time.sleep(0.2)
    return _h._plain(res)


@case("S-ASK-03", "ask_user", "Ask 多选作答：勾选多项 → 顿号拼接回填 → 待办移除",
      stage="seeded", order=205, fp="61 §4.3 ask-user(multi) · 33 CHAT-014",
      desc="ask_user multi=true（构建/测试/部署，推荐「测试」）→ 勾选「构建」「部署」提交 → "
           "tool-result = 「构建、部署」、历史用户答复同值、待办移除。")
def s_ask_03_multi_select(env):
    sid = "e2e-askmulti-%s" % env.run_id
    env.capture(["turn-start", "ask-user", "llm-complete", "tool-result"])
    _start(env, sid, "please call ask-multi")
    env.wait_events("turn-start", lambda p: p.get("session") == sid, "主轮 turn-start", timeout=30)
    ask = env.wait_events("ask-user",
                          lambda p: p.get("ask_id") and p.get("session") == sid,
                          "ask-user(多选待办)", timeout=40)
    ask_id = ask.get("ask_id")
    env.check("ask 为多选", ask.get("multi") is True, ask.get("multi"))
    env.eq("ask 选项", ask.get("options"), ["构建", "测试", "部署"])
    env.eq("ask 推荐值", ask.get("recommended"), ["测试"])

    _, ac = _open_ask(env, ask, timeout=30)
    env.check("待办入列（Ask 中心）", ac_mod.wait_item(ac, ask_id, timeout=20), ask_id)
    res = _answer_multi(env, ac, ask, ["构建", "部署"])
    env.check("多选提交", res == "ok", res)
    env.check("待办已移除", ac_mod.wait_no_item(ac, ask_id, timeout=20), ask_id)

    tr = env.wait(lambda: [p for p in _payloads(env, "tool-result")
                           if (p.get("tool") or "").endswith("ask_user")
                           and (p.get("result") is not None)] or None,
                  "ask_user 结果回填（异步：作答后）", timeout=30)
    env.check("ask_user 结果已回填", len(tr) >= 1, [p.get("tool") for p in tr])
    env.eq("多选答案顿号拼接", (tr[-1] or {}).get("result"), "构建、部署")

    dlg = ask.get("dialog_id") or ""

    def answer_row():
        rows = _history_rows(env, sid, dlg)
        return next((m for m in rows
                     if m.get("role") == "user"
                     and ("答：构建、部署" in (m.get("content") or "")
                          or (m.get("content") or "").strip() == "构建、部署")), None)

    hit = env.wait(answer_row, "历史出现多选答复（答：构建、部署）", timeout=30)
    env.check("历史用户答复含顿号拼接值", bool(hit), (hit or {}).get("content"))
    try:
        ac_mod.close_center(ac, env.client, timeout=10)
    except Exception:
        pass