# -*- coding: utf-8 -*-
"""B9 · 语义/回环级：**窗口与布局持久化回环**（31-窗口与布局）。

覆盖（**拒绝"存在级"：断言字段值 / 生效态经持久层与重启往返一致**）：
  WL1 布局尺寸回环：`gui.ui.save{layout}` 写尺寸 → `data-prj-config-list` **精确回读** `layout.*`
      → 同参重启 → DOM 各面板宽度**语义对应**保存值（非产品默认值，故非"碰巧"）。
  WL2 面板显隐落库+重启 DOM 回灌：`gui.ui.save{layout{filetreeOpen,chatOpen,taskOpen}}` 写显隐
      → prj **精确回读** → 同参重启 → prj 仍保持该值 **且 DOM 面板显隐按保存值回灌**（I-173：
      `applyLayout` 经 `parseBool` 兼容持久层读回的字符串 `"true"/"false"`（亦兼容布尔），故重启后
      显隐可回灌）。
  WL3 显隐切换回环（WIN-008-S02/WIN-013-S01）：既有本地事件 `filetree-toggle` / `chat-toggle` /
      `tasks-toggle` 驱动（等价点工具栏/任务开关）→ DOM 面板即时显隐 + prj `layout.*Open` 同步落库。
  WL4 主题回环（WIN-014/021）：`data-user-config-save{theme}` → 捕获 `data-user-config-changed
      {data:{theme}}`（**不带 instance_id** → 全局）→ `documentElement[data-theme]` 即时应用
      → `data-user-config-load` 回读一致。
  WL5 语言回环（WIN-015/021）：`data-user-config-save{locale}` → 同上广播 → 本窗口 locale 置位
      （localStorage `chonkpilot-locale`）→ `data-user-config-load` 回读一致。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
套件级快照-还原 `_h.suite_config_guard(lambda: c)`（callable：中途重启换 client 后仍生效）。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §1 `gui.ui.save` · §3.1 `data-prj-config-list` / `data-user-config-{save,load,changed}` · DOM（`--test-port` /eval）。

运行：python run_sem_window_layout_loop.py
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

WS = _h.tmp_dir("ck-semlay-ws-")
DD = _h.tmp_dir("ck-semlay-dd-")
HOME = _h.tmp_home()
PORT = _h.free_port()

_g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(lambda: c)     # callable → 重启后仍指向当前 client
_h.ensure_locale(c, "zh-CN")
print("[env] ws=%s data=%s home=%s gui=%d（临时目录，结束即删）" % (WS, DD, HOME, PORT), flush=True)

# WL1 写盘的尺寸哨兵值（≠ 产品默认 320/520/400，便于区分"读到保存值"与"回落默认"）
FT_W, CHAT_W, TREE_W = 333, 404, 355


def ev(js, timeout=6000):
    """eval 并循环解包 JSON 字符串（测试通道可能双重编码）。"""
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
    """有界轮询（只改"何时读"，不改断言条件）。"""
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = pred()
        if last:
            return last
        time.sleep(interval)
    raise TestError("%s 超时（%ss），末次=%r" % (desc, max_wait, last))


def prj():
    r = c.req("data-prj-config-list", {}) or {}
    return r.get("list") or {}


def user_cfg():
    r = c.req("data-user-config-load", {}) or {}
    return r.get("data") or {}


def width(sel):
    """面板实际渲染宽（不存在 → 0）。"""
    v = ev("(document.querySelector(%s)?.getBoundingClientRect().width)||0" % json.dumps(sel))
    return v or 0


def exists(sel):
    return bool(ev("!!document.querySelector(%s)" % json.dumps(sel)))


def restart(tag):
    """同参重启（同端口 / work-dir / data-dir / HOME）→ 同一数据根回读。"""
    global _g, c
    if not getattr(_g, "owned", False):
        raise TestError("GUI 非自起（owned=False）→ 不自动重启")
    print("[restart] %s：停止 pid=%s" % (tag, _g.pid()), flush=True)
    _g.stop()
    time.sleep(1)
    _g = _h.start_gui(port=PORT, work_dir=WS, data_dir=DD, home=HOME)
    c = _g.client
    _h.ensure_locale(c, "zh-CN")
    time.sleep(1.0)
    print("[restart] %s：新实例 pid=%s ready" % (tag, _g.pid()), flush=True)


def wait_width(sel, lo, hi, desc, max_wait=15):
    return wait_for(lambda: width(sel) if lo <= width(sel) <= hi else 0, desc, max_wait)


def case_wl1_sizes_persist_and_restore():
    """WL1：写尺寸 → prj 精确回读 → 重启后 DOM 宽度语义对应保存值（非默认）。"""
    c.req("gui.ui.save", {"layout": {"filetreeWidth": FT_W, "chatWidth": CHAT_W,
                                     "sessiontreeWidth": TREE_W}})
    lst = wait_for(lambda: prj() if str(prj().get("layout.filetreeWidth")) == str(FT_W) else None,
                   "layout.* 未落库回读一致")
    got = {k: lst.get(k) for k in ("layout.filetreeWidth", "layout.chatWidth", "layout.sessiontreeWidth")}
    if got != {"layout.filetreeWidth": str(FT_W), "layout.chatWidth": str(CHAT_W),
               "layout.sessiontreeWidth": str(TREE_W)}:
        raise TestError("布局落库回读不符：%r" % (got,))
    # 重启 → 各面板宽度应按保存值（clamp 后）渲染；333/404/355 与默认 320/520/400 相差 >8
    restart("WL1")
    wait_width(".filetree-panel", FT_W - 6, FT_W + 6, ".filetree-panel 宽度未对应保存值 %d" % FT_W)
    wait_width(".chat-panel", CHAT_W - 6, CHAT_W + 6, ".chat-panel 宽度未对应保存值 %d" % CHAT_W)
    evi("WL1 尺寸持久化回环", prj=got, filetree=width(".filetree-panel"),
        chat=width(".chat-panel"))


def case_wl2_visibility_persist_and_restore():
    """WL2：写显隐 → prj 精确回读 → 重启后 prj 保持 + **DOM 显隐回灌**（I-173）。"""
    c.req("gui.ui.save", {"layout": {"filetreeOpen": False, "chatOpen": False, "taskOpen": True}})
    lst = wait_for(lambda: prj() if prj().get("layout.filetreeOpen") == "false"
                   and prj().get("layout.chatOpen") == "false"
                   and prj().get("layout.taskOpen") == "true" else None, "显隐未落库回读一致")
    got = {k: lst.get(k) for k in ("layout.filetreeOpen", "layout.chatOpen", "layout.taskOpen")}
    restart("WL2")
    lst2 = wait_for(lambda: prj() if prj().get("layout.filetreeOpen") == "false"
                    and prj().get("layout.chatOpen") == "false"
                    and prj().get("layout.taskOpen") == "true" else None,
                    "重启后显隐落库值未保持")
    # 重启后 DOM 显隐按保存值回灌（applyLayout 经 parseBool 兼容持久层字符串布尔）
    wait_for(lambda: not exists(".filetree-panel"), "重启后文件树面板未按保存值隐藏", 15)
    wait_for(lambda: not exists(".chat-panel"), "重启后 chat 面板未按保存值隐藏", 15)
    wait_for(lambda: exists(".session-chat"), "重启后任务区未按保存值展开", 20)
    evi("WL2 面板显隐落库+重启 DOM 回灌", before=got,
        after={k: lst2.get(k) for k in ("layout.filetreeOpen", "layout.chatOpen", "layout.taskOpen")},
        dom_filetree=False, dom_chat=False, dom_task=True)
    # 复位为默认（filetree/chat 显示、task 收起）：经开关事件同时回灌 DOM 与落库，
    # 保证后续 WL3 的切换起点为默认可见态。
    c.mq_emit("filetree-toggle")
    wait_for(lambda: exists(".filetree-panel") and prj().get("layout.filetreeOpen") == "true",
             "WL2 复位文件树显示失败", 15)
    c.mq_emit("chat-toggle")
    wait_for(lambda: exists(".chat-panel") and prj().get("layout.chatOpen") == "true",
             "WL2 复位 chat 显示失败", 15)
    c.mq_emit("tasks-toggle")
    wait_for(lambda: not exists(".session-chat") and prj().get("layout.taskOpen") == "false",
             "WL2 复位任务收起失败", 20)


def case_wl3_toggle_dom_and_persist():
    """WL3：三个既有开关事件驱动 → DOM 面板即时显隐 + prj `layout.*Open` 同步落库。"""
    if not exists(".filetree-panel"):
        raise TestError("前置不满足：文件树面板未显示")
    # ① filetree-toggle → 隐藏 + 落库 false
    c.mq_emit("filetree-toggle")
    wait_for(lambda: not exists(".filetree-panel"), "filetree-toggle 后文件树未隐藏", 15)
    wait_for(lambda: prj().get("layout.filetreeOpen") == "false",
             "filetree-toggle 未持久化 layout.filetreeOpen=false", 15)
    c.mq_emit("filetree-toggle")               # 复位
    wait_for(lambda: exists(".filetree-panel") and prj().get("layout.filetreeOpen") == "true",
             "filetree-toggle 复位失败", 15)
    # ② chat-toggle → 隐藏 + 落库 false
    c.mq_emit("chat-toggle")
    wait_for(lambda: not exists(".chat-panel"), "chat-toggle 后 chat 未隐藏", 15)
    wait_for(lambda: prj().get("layout.chatOpen") == "false",
             "chat-toggle 未持久化 layout.chatOpen=false", 15)
    c.mq_emit("chat-toggle")                    # 复位
    wait_for(lambda: exists(".chat-panel") and prj().get("layout.chatOpen") == "true",
             "chat-toggle 复位失败", 15)
    # ③ tasks-toggle → 展开（默认收起）+ 落库 true
    c.mq_emit("tasks-toggle")
    wait_for(lambda: exists(".session-chat"), "tasks-toggle 后任务区未展开", 20)
    wait_for(lambda: prj().get("layout.taskOpen") == "true",
             "tasks-toggle 未持久化 layout.taskOpen=true", 15)
    c.mq_emit("tasks-toggle")                    # 复位为收起
    wait_for(lambda: not exists(".session-chat") and prj().get("layout.taskOpen") == "false",
             "tasks-toggle 复位失败", 20)
    evi("WL3 显隐切换回环", filetree="hide→show", chat="hide→show", task="open→close")


def case_wl4_theme_loop():
    """WL4：usr theme 落库 → `data-user-config-changed` 广播（全局）→ DOM 即时应用 → load 回读。"""
    c.mq_on_capture(["data-user-config-changed"])
    c.req("data-user-config-save", {"data": {"theme": "dark"}})
    evs = c.wait_events("data-user-config-changed", n=1, max_wait=15)
    pl = evs[0].get("payload") if evs else None
    if not isinstance(pl, dict) or not isinstance(pl.get("data"), dict) or pl["data"].get("theme") != "dark":
        raise TestError("data-user-config-changed 载荷应为 {data:{theme:'dark'}}：%r" % (pl,))
    if "instance_id" in pl:
        raise TestError("data-user-config-changed 不应带 instance_id（61 §3.1：全局）：%r" % (pl,))
    applied = wait_for(lambda: ev("document.documentElement.getAttribute('data-theme')") == "dark" and "dark",
                       "本窗口未即时应用 theme=dark", 15)
    loaded = wait_for(lambda: user_cfg() if user_cfg().get("theme") == "dark" else None,
                      "usr theme 未落库回读一致", 15)
    evi("WL4 主题落库+广播+应用回环", event_payload=pl, data_theme=applied, load_theme=loaded.get("theme"))


def case_wl5_locale_loop():
    """WL5：usr locale 落库 → 广播 → 本窗口 locale 置位 → load 回读一致。"""
    c.mq_on_capture(["data-user-config-changed"])
    c.req("data-user-config-save", {"data": {"locale": "en-US"}})
    evs = c.wait_events("data-user-config-changed", n=1, max_wait=15)
    pl = evs[0].get("payload") if evs else None
    if not isinstance(pl, dict) or (pl.get("data") or {}).get("locale") != "en-US":
        raise TestError("data-user-config-changed 载荷应含 {data:{locale:'en-US'}}：%r" % (pl,))
    loc = wait_for(lambda: ev("localStorage.getItem('chonkpilot-locale')") == "en-US" and "en-US",
                   "本窗口未即时应用 locale=en-US", 15)
    loaded = wait_for(lambda: user_cfg() if user_cfg().get("locale") == "en-US" else None,
                      "usr locale 未落库回读一致", 15)
    evi("WL5 语言落库+广播+应用回环", event_payload=pl, local_storage=loc, load_locale=loaded.get("locale"))


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = gui.ui.save / data-user-config-save（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("WL1 布局尺寸：写→落库回读→重启后 DOM 宽度对应保存值", case_wl1_sizes_persist_and_restore),
        ("WL2 面板显隐：写→落库回读→重启后 prj 保持 + DOM 显隐回灌", case_wl2_visibility_persist_and_restore),
        ("WL3 显隐切换：三开关事件 → DOM 即时显隐 + 落库同步", case_wl3_toggle_dom_and_persist),
        ("WL4 主题回环：save→changed 广播→data-theme 应用→load 回读", case_wl4_theme_loop),
        ("WL5 语言回环：save→changed 广播→locale 置位→load 回读", case_wl5_locale_loop),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n窗口与布局持久化回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
