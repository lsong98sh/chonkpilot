# -*- coding: utf-8 -*-
"""P6 批次 L4 套件：活动会话 `active_session_id` 的 **A 落库回读 + B 重启恢复** 端到端。

背景（已审计缺口，2026-09-17）：`active_session_id` 只有隐式落盘（`data-session-active-set`
写 prj/prjusr config；`persist_session.go:296-330`），**无** "选择会话 → 重启 → 恢复选中"
的显式断言；既有 SD3（test_session_drawer）只断言"点了卡片之后 active-get 命中二者之一"。

覆盖（每条 = A 数据面回读 + B 可观测效果；B 恒以可观测证据收口）
  A1 选择会话（真实抽屉点击）→ `data-session-active-get` == 该 id（A 落库回读）
     + 前端选中态 `.chat-session-tag` == `#<id 前 8>`（DOM 落点，非内部状态）
  B1 **重启同一数据根** → `data-session-active-get` 仍 == 该 id（A 再次回读）
     + 前端选中态仍 == `#<id 前 8>`（首屏 initSession 消费活动会话位的效果证据）
  A2/B2 **对照**：切换会话 → 再次重启 → 恢复为**新选**的 id（证明 B1 不是"总是取最新会话"
     的偶然：第二次选择的是**更早创建**的会话，而 `LatestTopSession`（view.go:234）按
     `created_at` 倒序 → 回落分支必然给出**另一个** id，故该对照能区分"读了活动会话位"
     与"回落最新会话"）。

观测渠道（**全部为 61-消息一览既有主题，零新增**）
  §3.2 会话域：data-session-list / -active-get / -active-set（经前端抽屉真实点击驱动）
  §3.2a 会话运行时：session-start（= §4.2 llm-start 的总线相对主题，建会话；test_session_drawer 同法）
  §3.2 data-session-title（给会话可读标题，便于证据定位）
  前端内部既有事件：sessions-open / session-select（v-mq 绑定，见 SessionDrawer.vue）

隔离（51-FP与测试映射 §5/§6-8）：
  * 自起 GUI：动态端口 + 独立 work-dir + 独立 `--data-dir` + 独立 `HOME`（usr 主库全新）。
    套件级快照-还原用 `suite_config_guard(lambda: c)`（传 callable：中途重启换 client 后仍生效）。
  * 会话/活动位全落在临时数据根内 → 结束随 `harness.tmp_dir` 删除 → **零残留**。

运行：python run_session_active.py
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

WS = _h.tmp_dir("ck-sessact-ws-")
DD = _h.tmp_dir("ck-sessact-dd-")
HOME = _h.tmp_home()
PORT = _h.free_port()

_g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(lambda: c)   # 套件级快照-还原（中途重启 → 用当前 client 还原）
_h.ensure_locale(c)
print("[env] ws=%s data=%s home=%s mock=none gui=%d（临时目录，结束即删）"
      % (WS, DD, HOME, PORT), flush=True)


# ══════════════════════════════════════════════════════════
# 通用工具
# ══════════════════════════════════════════════════════════

def ev(js, timeout=6000):
    v = c.eval(js, timeout)
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def wait_for(pred, desc, max_wait=20, interval=0.3):
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = pred()
        if last:
            return last
        time.sleep(interval)
    raise TestError("%s 超时（%ss）" % (desc, max_wait))


def active_get():
    """A：prj/prjusr `active_session_id` 落库值回读（§3.2 data-session-active-get）。"""
    r = c.req("data-session-active-get", {}) or {}
    return r.get("session_id") or ""


def chat_tag():
    """前端选中态的 DOM 落点（ChatPanel.vue:6-12 `#{{ currentSessionId.slice(0,8) }}`）。"""
    txt = ev("(document.querySelector('.chat-session-tag')||{}).textContent||''")
    return (txt or "").strip()


def tag_of(sid):
    return "#" + sid[:8]


def seed_session(sid, title):
    """建会话（§4.2 llm-start 的总线相对主题 session-start；不跟 send → 不触发 LLM）。"""
    r = c.req("chonk.session-start", {"session": sid, "turn": "t-" + sid + "-1", "llm": "mock"})
    if not isinstance(r, dict) or r.get("accepted") is not True:
        raise TestError("session-start 未受理: %r" % (r,))
    c.req("data-session-title", {"data": {"id": sid, "title": title}})
    return sid


def open_drawer():
    c.mq_emit("sessions-open")
    wait_for(lambda: int(ev("document.querySelectorAll('.session-card').length") or 0) > 0,
             "会话抽屉卡片未出现")


def card_ids():
    return ev("JSON.stringify([...document.querySelectorAll('.session-card .session-id')]"
              ".map(e=>e.textContent.trim()))") or []


def click_card(sid):
    """真实 UI 路径：点会话卡片（SessionDrawer.handleSelect → setActiveSessionID + session-changed）。"""
    js = """(() => {
      const want = %s;
      const cards = [...document.querySelectorAll('.session-card')];
      const t = cards.find(x => (((x.querySelector('.session-id')||{}).textContent)||'').trim() === want);
      if (!t) return 'notfound';
      t.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'clicked';
    })()""" % json.dumps(tag_of(sid))
    return ev(js)


def restart(tag):
    """重启本套件自起实例（同端口 / 同 work-dir / 同 --data-dir / 同 HOME）→ 同一数据根。"""
    global _g, c
    if not getattr(_g, "owned", False):
        raise TestError("GUI 非自起（owned=False）→ 不自动重启")
    print("[restart] %s：停止 pid=%s" % (tag, _g.pid()), flush=True)
    _g.stop()
    time.sleep(1)
    _g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
    c = _g.client
    _h.ensure_locale(c)
    time.sleep(1.5)
    print("[restart] %s：新实例 pid=%s ready" % (tag, _g.pid()), flush=True)


S1 = "p6sess-a-001"
S2 = "p6sess-b-002"


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_a1_select_persist():
    """A1：真实选择会话（抽屉点击）→ 落库回读 + 前端选中态一致。"""
    seed_session(S1, "P6 会话A")
    time.sleep(1.2)                     # created_at 拉开（LatestTopSession 按 created_at 倒序 → S2 恒为"最新"）
    seed_session(S2, "P6 会话B")
    open_drawer()
    ids = card_ids()
    if (tag_of(S1) not in ids) or (tag_of(S2) not in ids):
        raise TestError("抽屉未列出全部会话：%r" % (ids,))
    r = click_card(S2)
    if r != "clicked":
        raise TestError("点击会话卡片失败：%r（卡片=%r）" % (r, ids))
    got = wait_for(lambda: active_get() == S2, "active-get 未落到 S2（选中后）", 15)
    wait_for(lambda: chat_tag() == tag_of(S2), "前端 .chat-session-tag 未显示 S2", 15)
    evi("A1 选择会话 → 落库回读", clicked_id=S2, active_get=active_get(), chat_tag=chat_tag(),
        drawer_ids=ids, old_active_before=got)
    if active_get() != S2:
        raise TestError("A 落库回读失败: active=%r 期望 %r" % (active_get(), S2))
    if chat_tag() != tag_of(S2):
        raise TestError("前端选中态不符: tag=%r 期望 %r" % (chat_tag(), tag_of(S2)))


def case_b1_restart_restore():
    """B1：重启同一数据根 → 仍选中 S2（落库回读 + 首屏选中态）。"""
    restart("B1")
    wait_for(lambda: chat_tag() != "", "重启后 .chat-session-tag 未出现", 40)
    act = active_get()
    evi("B1 重启后恢复选中", expect=S2, active_get=act, chat_tag=chat_tag(),
        tag_expect=tag_of(S2))
    if act != S2:
        raise TestError("重启后活动会话丢失: active=%r 期望 %r" % (act, S2))
    if chat_tag() != tag_of(S2):
        raise TestError("重启后前端选中态不符: tag=%r 期望 %r" % (chat_tag(), tag_of(S2)))


def case_a2_switch_other():
    """A2（对照前置）：切换到**更早创建**的 S1 → 落库与前端选中态同步改变。"""
    open_drawer()
    r = click_card(S1)
    if r != "clicked":
        raise TestError("点击 S1 卡片失败：%r" % (r,))
    wait_for(lambda: active_get() == S1, "active-get 未落到 S1", 15)
    wait_for(lambda: chat_tag() == tag_of(S1), "前端 .chat-session-tag 未切到 S1", 15)
    evi("A2 切换会话 → 落库回读", clicked_id=S1, active_get=active_get(), chat_tag=chat_tag())
    if active_get() != S1 or chat_tag() != tag_of(S1):
        raise TestError("切换未生效: active=%r tag=%r" % (active_get(), chat_tag()))


def case_b2_restart_restore_again():
    """B2（对照）：再次重启 → 恢复为 S1（而非最新会话 S2）→ 排除"回落最新会话"的偶然。"""
    restart("B2")
    wait_for(lambda: chat_tag() != "", "重启后 .chat-session-tag 未出现", 40)
    act = active_get()
    evi("B2 再次重启恢复（对照）", expect=S1, latest_fallback=S2, active_get=act,
        chat_tag=chat_tag(), tag_expect=tag_of(S1))
    if act == "":
        raise TestError("重启后活动会话为空（未恢复）")
    if act == S2:
        raise TestError("重启后回落成最新会话 %r（未读活动会话位）" % S2)
    if act != S1:
        raise TestError("重启后活动会话=%r 期望 %r" % (act, S1))
    if chat_tag() != tag_of(S1):
        raise TestError("重启后前端选中态不符: tag=%r 期望 %r" % (chat_tag(), tag_of(S1)))


def main():
    ok = total = 0
    c.console(clear=True)
    print("依赖：--test-port=%d 的 GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = 会话抽屉真实点击 + 既有消息面 data-session-*" % PORT, flush=True)
    for name, fn in [
        ("A1 选择会话（抽屉点击）→ active_session_id 落库回读 + 前端选中态", case_a1_select_persist),
        ("B1 重启同一数据根 → 仍恢复选中该会话（回读 + 首屏选中态）", case_b1_restart_restore),
        ("A2 切换会话（更早创建的 S1）→ 落库/前端同步改变（对照前置）", case_a2_switch_other),
        ("B2 再次重启 → 恢复为 S1（排除回落最新会话）", case_b2_restart_restore_again),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n活动会话 active_session_id（A 落库 + B 重启恢复）：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
