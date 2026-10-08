# -*- coding: utf-8 -*-
"""B9 · 语义/回环级：**会话生命周期回环**（33-对话 · 会话导航/重命名/删除/活动态）。

覆盖（**拒绝"存在级"：断言字段值经持久层与重启往返一致**）：
  SL1 建会话 → 列表/详情回环：`chonk.session-start`（§4.2 `llm-start` 总线相对主题，幂等建会话）
      → `data-session-list` 含该会话 + `data-session-get{id}` 详情 `session_id`/标题一致。
  SL2 重命名传播回环：`data-session-title{id,title}` → **写库成功后**广播
      `data-session-title-changed{session_id,title}`（§3.2，不带 instance_id）→ `data-session-get`
      标题 == 新标题 + `data-session-list` 该项标题 == 新标题（三处一致）。
  SL3 活动会话切换回环：`data-session-active-set{session_id}` → `data-session-active-get` == 该 id
      （选择会话落库、再回读）。
  SL4 删除闭合回环：`data-session-delete{id}` → `data-session-list` **不含**该 id
      + `data-session-get{id}` 返回 `data` 为 null（§3.2 不存在 → `{data:null}`）。
  SL5 跨重启回环：同参重启 → `data-session-list` 仍含保留会话且标题不变
      + `data-session-active-get` 仍 == SL3 选中的会话（持久化跨进程存活）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(lambda: c)`（callable：中途重启换 client 后仍生效）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §3.2 `data-session-{list,get,title,title-changed,delete,active-set,active-get}` · §4.2 `chonk.session-start`
  · DOM（`--test-port` /eval，`.chat-session-tag` 选中态落点）。

运行：python run_sem_session_loop.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

WS = _h.tmp_dir("ck-semsess-ws-")
DD = _h.tmp_dir("ck-semsess-dd-")
HOME = _h.tmp_home()
PORT = _h.free_port()

_g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(lambda: c)
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, PORT), flush=True)

A = "sem-sess-a-1"
B = "sem-sess-b-2"
TITLE_A = "Sem 会话 A"
TITLE_B = "Sem 会话 B"
TITLE_A2 = "Sem 会话 A 重命名"


def wait_for(pred, desc, max_wait=20, interval=0.3):
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = pred()
        if last:
            return last
        time.sleep(interval)
    raise TestError("%s 超时（%ss），末次=%r" % (desc, max_wait, last))


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def sess_list():
    r = c.req("data-session-list", {}) or {}
    return [x for x in (r.get("list") or []) if isinstance(x, dict)]


def sess_get(sid):
    r = c.req("data-session-get", {"id": sid}) or {}
    return r.get("data")


def active_get():
    r = c.req("data-session-active-get", {}) or {}
    return r.get("session_id") or ""


def seed_session(sid, title):
    """建会话（§4.2 llm-start 的总线相对主题 session-start；不跟 send → 不触发 LLM）。"""
    r = c.req("chonk.session-start", {"session": sid, "turn": "t-" + sid + "-1", "llm": "mock"})
    if not isinstance(r, dict) or r.get("accepted") is not True:
        raise TestError("session-start 未受理: %r" % (r,))
    c.req("data-session-title", {"data": {"id": sid, "title": title}})
    return sid


def title_in_list(sid):
    for x in sess_list():
        if x.get("session_id") == sid:
            return x.get("title")
    return None


def restart(tag):
    global _g, c
    if not getattr(_g, "owned", False):
        raise TestError("GUI 非自起（owned=False）→ 不自动重启")
    print("[restart] %s：停止 pid=%s" % (tag, _g.pid()), flush=True)
    _g.stop()
    time.sleep(1)
    _g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
    c = _g.client
    _h.ensure_locale(c, "zh-CN")
    time.sleep(1.2)
    print("[restart] %s：新实例 pid=%s ready" % (tag, _g.pid()), flush=True)


def case_sl1_create_list_get():
    """SL1：建两会话 → 列表含两者 + 详情字段一致。"""
    seed_session(A, TITLE_A)
    seed_session(B, TITLE_B)
    lst = wait_for(lambda: sess_list() if {A, B} <= {x.get("session_id") for x in sess_list()} else None,
                   "会话未出现在 data-session-list")
    ga = sess_get(A)
    if not isinstance(ga, dict) or ga.get("session_id") != A:
        raise TestError("data-session-get(A) 详情不符：%r" % (ga,))
    if title_in_list(A) != TITLE_A or title_in_list(B) != TITLE_B:
        raise TestError("列表标题不符：A=%r B=%r" % (title_in_list(A), title_in_list(B)))
    evi("SL1 建会话→列表/详情回环", ids=sorted(x.get("session_id") for x in lst),
        get_a_id=ga.get("session_id"), title_a=title_in_list(A))


def case_sl2_rename_broadcast_loop():
    """SL2：重命名 → title-changed 广播（全局）+ get/list 三处一致。"""
    c.mq_on_capture(["data-session-title-changed"])
    c.req("data-session-title", {"data": {"id": A, "title": TITLE_A2}})
    evs = c.wait_events("data-session-title-changed", n=1, max_wait=15)
    pl = evs[0].get("payload") if evs else None
    if not isinstance(pl, dict) or pl.get("session_id") != A or pl.get("title") != TITLE_A2:
        raise TestError("data-session-title-changed 载荷应为 {session_id:%r,title:%r}：%r" % (A, TITLE_A2, pl))
    if "instance_id" in pl:
        raise TestError("data-session-title-changed 不应带 instance_id（61 §3.2：全局）：%r" % (pl,))
    got = wait_for(lambda: sess_get(A) if (sess_get(A) or {}).get("title") == TITLE_A2 else None,
                   "data-session-get 未回读新标题")
    tl = wait_for(lambda: title_in_list(A) if title_in_list(A) == TITLE_A2 else None,
                  "data-session-list 未回读新标题")
    evi("SL2 重命名传播回环", event=pl, get_title=got.get("title"), list_title=tl)


def case_sl3_active_switch_loop():
    """SL3：切活动会话 → active-get 回读 == 该 id。"""
    c.req("data-session-active-set", {"session_id": B})
    got = wait_for(lambda: active_get() if active_get() == B else None, "active-get 未落到 B")
    evi("SL3 活动会话切换回环", expect=B, active_get=got)


def case_sl4_delete_closed():
    """SL4：删除 A（非活动）→ 列表不含 + get 返回 data 为 null。"""
    c.req("data-session-delete", {"id": A})
    lst = wait_for(lambda: sess_list() if A not in {x.get("session_id") for x in sess_list()} else None,
                   "删除后 data-session-list 仍含 A")
    g = wait_for(lambda: True if sess_get(A) is None else None, "删除后 data-session-get 未返回 null")
    if A in {x.get("session_id") for x in lst}:
        raise TestError("删除闭合失败：列表仍含 %r" % A)
    if sess_get(A) is not None:
        raise TestError("删除闭合失败：get 仍返回 %r" % (sess_get(A),))
    evi("SL4 删除闭合回环", remaining=sorted(x.get("session_id") for x in lst), get_after=g)


def case_sl5_restart_loop():
    """SL5：同参重启 → 保留会话 + 其标题 + 活动会话位均跨进程存活。"""
    restart("SL5")
    lst = wait_for(lambda: sess_list() if B in {x.get("session_id") for x in sess_list()} else None,
                   "重启后列表丢失保留会话 B")
    if A in {x.get("session_id") for x in lst}:
        raise TestError("重启后被删除的 A 又出现：%r" % (lst,))
    tl = wait_for(lambda: title_in_list(B) if title_in_list(B) == TITLE_B else None,
                  "重启后 B 标题未保持")
    act = wait_for(lambda: active_get() if active_get() == B else None, "重启后活动会话位未恢复为 B")
    evi("SL5 跨重启回环", listed=sorted(x.get("session_id") for x in lst), title_b=tl, active_get=act)


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = chonk.session-start / data-session-*（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("SL1 建会话 → data-session-list/get 往返一致", case_sl1_create_list_get),
        ("SL2 重命名 → title-changed 广播 + get/list 三处一致", case_sl2_rename_broadcast_loop),
        ("SL3 活动会话切换 → active-set/get 回环", case_sl3_active_switch_loop),
        ("SL4 删除闭合 → 列表不含 + get 返回 null", case_sl4_delete_closed),
        ("SL5 跨重启 → 会话/标题/活动位存活", case_sl5_restart_loop),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n会话生命周期回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
