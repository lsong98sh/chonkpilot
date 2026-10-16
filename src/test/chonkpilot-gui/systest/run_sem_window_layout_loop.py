# -*- coding: utf-8 -*-
"""B9 · 语义/回环级：**窗口与布局持久化回环**（31-窗口与布局）。

覆盖（**拒绝"存在级"：断言字段值 / 生效态经持久层与重启往返一致**）：
  WL1 布局尺寸回环：`gui.ui.save{layout}` 写尺寸 → `data-prj-config-list` **精确回读** `layout.*`
      → 同参重启 → DOM 各面板宽度**语义对应**保存值（非产品默认值，故非"碰巧"）。
  WL2 三区常显 + 旧显隐键忽略回环（2026-10-16 用户裁决，**替代原「面板显隐落库+重启 DOM 回灌」**）：
      三区（chat / task / filetree）显隐能力与 UI 入口已移除 → 恒定渲染。本用例写入旧 `layout.*Open=false`
      三键（`gui.ui.save{layout{filetreeOpen,chatOpen,taskOpen}}`）→ prj **精确回读**（键确已落库）
      → 同参重启 → 三区**仍挂载**（旧键被忽略、**不回灌**；`applyLayout` 不再读 `layout.*Open`，
      原 `parseBool` 兼容逻辑已随显隐能力一并移除）。**原 WL3（三开关事件驱动 DOM 显隐 + 落库）
      已删除**：`chat-toggle` / `filetree-toggle` / `tasks-toggle` 三主题仅保留定义、前端已无订阅，
      不再驱动显隐（收到即 no-op）。
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

# WL1 写盘的**占比**哨兵值（≠ 默认文档的 body 占比 [0.5517, 0.4483]，便于区分"读到保存值"与"回落默认"）
BODY_SIZES = [0.70, 0.30]


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
    """WL1（**P4⑤ free 口径**）：改 `layout.free` 的 body 占比 → prj 精确回读 → 重启后 DOM
    宽度语义对应保存占比（非默认）。

    原口径 = 写旧尺寸键 `layout.{filetreeWidth,chatWidth,sessiontreeWidth}` 并靠 legacy
    `applyLayout` 回灌；切默认 free 后旧键**不被读取**（尺寸由 `layout.free` 文档占比决定）
    → 本用例改为直接改 `layout.free` 文档（等价于「拖分割条 → saveLiveLayout」的持久化路径）。
    """
    doc = ev("JSON.stringify((window.__ckLayout&&window.__ckLayout.engine)?"
             "window.__ckLayout.engine.state.config.live:null)")
    if not isinstance(doc, dict) or not doc.get("layout"):
        raise TestError("未取到 layout.free 文档：%r" % (doc,))
    body = doc["layout"]
    kids = body.get("children") or []
    if len(kids) == 2:
        body["sizes"] = list(BODY_SIZES)
    else:
        for nd in kids:
            if (isinstance(nd, dict) and nd.get("type") == "split"
                    and nd.get("direction") == "row" and len(nd.get("children") or []) == 2):
                nd["sizes"] = list(BODY_SIZES)
                break
    free_json = json.dumps(doc, ensure_ascii=False)
    c.req("gui.ui.save", {"layout": {"free": free_json}})

    def _readback_ok():
        s = prj().get("layout.free")
        return s if isinstance(s, str) and "[0.7, 0.3]" in s.replace("0.70", "0.7") else None

    lst = wait_for(_readback_ok, "layout.free 未落库回读一致")
    got = {"layout.free.sizes": BODY_SIZES}
    restart("WL1")
    # 保存占比 chat=0.30 → chat 面板宽 ≈ 0.30 × 内容宽（1280 上下）；默认 0.4483 → 约 573。
    wait_width(".chat-panel", 200, 460, "chat 宽度未对应保存占比 0.30")
    evi("WL1 尺寸持久化回环（layout.free 占比）", prj=got, chat=width(".chat-panel"),
        filetree=width(".filetree-panel"))


def case_wl2_always_visible_legacy_keys_ignored():
    """WL2：三区常显 + 旧 `layout.*Open` 键忽略回环（2026-10-16 用户裁决）。

    替代原「面板显隐落库 + 重启 DOM 回灌」：三区（chat / task / filetree）显隐能力与 UI 入口已移除
    → 恒定渲染。本用例写入旧 `layout.*Open=false` 三键（证明确已落库）→ 同参重启 → 三区**仍挂载**
    （旧键被忽略、**不回灌**；`applyLayout` 不再读 `layout.*Open`）。
    """
    c.req("gui.ui.save", {"layout": {"filetreeOpen": False, "chatOpen": False, "taskOpen": False}})
    lst = wait_for(lambda: prj() if prj().get("layout.filetreeOpen") == "false"
                   and prj().get("layout.chatOpen") == "false"
                   and prj().get("layout.taskOpen") == "false" else None, "旧显隐键未落库回读一致")
    got = {k: lst.get(k) for k in ("layout.filetreeOpen", "layout.chatOpen", "layout.taskOpen")}
    restart("WL2")
    # 三区常显：旧 `*Open=false` 被忽略 → 面板仍挂载（applyLayout 不再读 layout.*Open）
    wait_for(lambda: exists(".filetree-panel"), "重启后文件树未挂载（旧 filetreeOpen=false 被误生效）", 15)
    wait_for(lambda: exists(".chat-panel"), "重启后 chat 未挂载（旧 chatOpen=false 被误生效）", 15)
    wait_for(lambda: exists(".session-chat"), "重启后任务区未挂载（旧 taskOpen=false 被误生效）", 20)
    evi("WL2 三区常显（旧显隐键被忽略）", legacy_keys=got,
        filetree=exists(".filetree-panel"), chat=exists(".chat-panel"), task=exists(".session-chat"))


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
        ("WL2 三区常显：旧显隐键落库回读→重启后仍挂载（旧键被忽略）", case_wl2_always_visible_legacy_keys_ignored),
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
