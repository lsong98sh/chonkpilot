# -*- coding: utf-8 -*-
"""MW-12：多窗口测试矩阵（24-多窗口模型设计方案 §9 MW-T01…MW-T28 + MW-T29）。

唯一准则 = `docs/spec/60-reference/61-消息一览.md`：全部用例经 **发送 mq 消息** 驱动、
经 **监听 mq 消息** 断言（50-测试体系 §1）；必要时辅以 DOM/OS 观测（24 §9 标注「半自动」
的条目，本文以 MQ 证据 + DOM/窗口标题双证据自动化）。

驱动通道 = `--test-port`：本套件对**对话窗口**的驱动/断言经请求体 `window_id` 路由
（`gui.window.list` 的 window_id；缺省 = 主窗口）——见 `src/lib/gui/testserver.go`。

用例对账（24 §9）：MW-T01…MW-T28 逐条一条一函数、函数名带 MW-Txx；MW-T29 为 2026-09-26
新增（会话标题变更跨窗口广播，61 §3.2 · G-48 ⑦ / #12）。
运行：python src\\test\\chonkpilot-gui\\systest\\run_mw.py（自起自收：独立 work-dir + 独立 HOME）
"""

import base64
import json
import os
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case  # noqa: E402
import harness as _h  # noqa: E402  按需加载 + 结束即回收（51-FP与测试映射 §测试资源规范）

# 独立工作目录（含文件 → 对话窗 filesys 能力断言用）+ 独立 HOME（usr/prjusr 库均隔离）。
WS = _h.tmp_dir("ck-mw-ws-")
HOME = _h.tmp_home()
for _n in ("mw-a.txt", "mw-b.txt"):
    with open(os.path.join(WS, _n), "w", encoding="utf-8") as _f:
        _f.write("mw\n")

_g = None   # GUIHandle（主窗口）
_m = None   # 主窗口客户端（缺省目标）
_insts = {}  # window_id → 该窗口实例 id（前端 mq 注入口径）


# ══════════════════════════════════════════════════════════════════
# 基础工具
# ══════════════════════════════════════════════════════════════════

def _plain(v):
    return _h._plain(v)


def cl(wid=""):
    """某窗口的测试客户端（缺省 = 主窗口）。"""
    return ChonkClient(base=_m.base, timeout=60, window_id=wid)


def inst(wid=""):
    """该窗口的前端实例 id（`window.__chonkpilotInstanceId`，首屏注入；与契约 §0 同源）。

    窗口刚建、页面尚未导航完成时取不到 → 返回 ''（gui.* 类消息不依赖 instance_id；
    需要 instance_id 的 data-* 用例请先 `wait_win_ready`）。
    """
    if _insts.get(wid):
        return _insts[wid]
    try:
        v = str(_plain(cl(wid).eval("window.__chonkpilotInstanceId", 15000)) or "")
    except Exception:
        return ""
    if v:
        _insts[wid] = v
    return v


def pub(wid, typ, payload=None, timeout=30000):
    """以某窗口的「前端身份」发布消息。

    payload 自动补 `instance_id`（与前端 `mq.emit` 的 `withInstanceId` 同口径，61 §0）——
    测试通道直连桥，不经前端注入，故此处显式补；与真实前端逐字同形。
    """
    p = dict(payload) if payload else {}
    i = inst(wid)
    if i and "instance_id" not in p:
        p["instance_id"] = i
    return cl(wid).publish(typ, p, timeout)


def req(wid, typ, payload=None, timeout=30000):
    """发布并校验成功（信封 ok + result.ok），返回 result。"""
    r = pub(wid, typ, payload, timeout)
    if not isinstance(r, dict) or r.get("ok") is False:
        raise TestError("%s 失败: %s" % (typ, (r or {}).get("errors") or (r or {}).get("error")))
    res = r.get("result")
    if isinstance(res, dict) and res.get("ok") is False:
        raise TestError("%s 失败: %s" % (typ, res.get("error")))
    return res


def wins():
    """gui.window.list（只含对话窗口，61 §1）。"""
    return (_m.req("gui.window.list", {}) or {}).get("windows") or []


def poll(fn, ok, timeout=25.0, interval=0.3):
    """有界轮询（只改「何时读」，不改「读什么」）。"""
    deadline = time.time() + timeout
    v = fn()
    while not ok(v) and time.time() < deadline:
        time.sleep(interval)
        v = fn()
    return v


def sid(tag):
    return "mw-%s-%d" % (tag, int(time.time() * 1000))


def ensure_session(s):
    return req("", "data-session-ensure-session", {"session_id": s})


def open_chat(s, wid=""):
    return req(wid, "gui.window.open-chat", {"session_id": s})


def open_chat_result(s, wid=""):
    """open-chat 的**原始 result**（ok 可能为 false，如达上限 → 不能用 req 的成败校验）。"""
    env = pub(wid, "gui.window.open-chat", {"session_id": s})
    return (env or {}).get("result") or {}


def close_window(wid):
    """关闭目标窗口（gui.window.status{command:close}）；返回请求是否成功。"""
    try:
        r = pub(wid, "gui.window.status", {"command": "close"}, 10000)
        return isinstance(r, dict) and r.get("ok") is not False
    except Exception:
        return False


def close_all_chats(rounds=3):
    """关闭全部对话窗口（每轮重试 + 有界等待；仍残留则打印告警，由用例自行断言）。"""
    for _ in range(max(1, int(rounds))):
        left = wins()
        if not left:
            return
        for w in left:
            close_window(w["window_id"])
        poll(wins, lambda v: not v, timeout=20.0)
    left = wins()
    if left:
        print("[mw] 警告：仍有对话窗口未关闭: %s" % left, flush=True)


def wait_win_ready(wid, timeout=60.0):
    """等对话窗口测试通道就绪（导航完成 → 本窗口 /eval 可用）。"""
    def probe():
        try:
            return cl(wid).eval("1+1", 10000) is not None
        except Exception:
            return False
    if not poll(probe, lambda v: v, timeout=timeout, interval=0.5):
        raise TestError("对话窗口 %s 测试通道未就绪（%ss）" % (wid, timeout))
    return True


def open_and_wait(s, wid=""):
    """开对话窗口并等其通道就绪，返回 window_id。"""
    w = open_chat(s, wid)
    if not w.get("ok"):
        raise TestError("open-chat 失败: %s" % w)
    wait_win_ready(w["window_id"])
    return w["window_id"]


def eval_js(wid, js, timeout=30000):
    return _plain(cl(wid).eval(js, timeout))


def eval_obj(wid, js, timeout=30000):
    """执行「返回 JSON 字符串」的 JS 并解析为对象（测试通道结果可能多重编码 → 循环解包）。"""
    v = cl(wid).eval(js, timeout)
    for _ in range(4):
        if isinstance(v, (dict, list)):
            return v
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def prj_cfg():
    return _h.prj_config_load(_m)


# ── OS 级窗口标题（MW-T12/T13/T25：标题断言；同一进程内多窗口 → 按进程枚举）──
_PS_TITLES = r'''
Add-Type @"
using System;using System.Text;using System.Collections.Generic;using System.Runtime.InteropServices;
public class MwWinEnum {
  [DllImport("user32.dll")] static extern bool EnumWindows(EnumWindowsProc cb, IntPtr p);
  delegate bool EnumWindowsProc(IntPtr h, IntPtr p);
  [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetWindowTextW(IntPtr h, StringBuilder s, int n);
  [DllImport("user32.dll")] static extern bool IsWindowVisible(IntPtr h);
  public static List<string> Titles(uint target) {
    var list = new List<string>();
    EnumWindows((h,p)=>{ uint pid; GetWindowThreadProcessId(h, out pid);
      if(pid==target && IsWindowVisible(h)){ var sb=new StringBuilder(512); GetWindowTextW(h,sb,512);
        var t=sb.ToString(); if(t.Length>0) list.Add(t); }
      return true; }, IntPtr.Zero);
    return list;
  }
}
"@
[Console]::OutputEncoding=[System.Text.Encoding]::UTF8
[MwWinEnum]::Titles(%d) | ConvertTo-Json -Compress
'''


def os_titles():
    """本进程的可见顶层窗口标题（PowerShell + user32 EnumWindows）。"""
    try:
        r = subprocess.run(["powershell", "-NoProfile", "-Command", _PS_TITLES % int(_g.proc.pid)],
                           capture_output=True, encoding="utf-8", errors="replace", timeout=40)
        out = [ln.strip() for ln in (r.stdout or "").splitlines() if ln.strip()]
        if not out:
            return []
        v = json.loads(out[-1])
        return v if isinstance(v, list) else [v]
    except Exception as e:
        raise TestError("枚举窗口标题失败: %s" % e)


# ── /show/ 取回（页面上下文 fetch；fileserver 白名单可达）──
def show_fetch(wid, abs_path, max_wait=10.0):
    url = json.dumps("/show/" + str(abs_path).replace("\\", "/"))
    js = """(() => {
  if (!window.__mwShowProbe) {
    window.__mwShowProbe = { status: 0, body: '' };
    fetch(%s).then(r => r.text().then(t => { window.__mwShowProbe = { status: r.status, body: t }; }))
             .catch(e => { window.__mwShowProbe = { status: -1, body: String(e) }; });
  }
  return JSON.stringify(window.__mwShowProbe);
})()""" % url
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        raw = eval_js(wid, js, 8000)
        last = raw if isinstance(raw, dict) else {"status": -2, "body": str(raw)}
        for _ in range(4):
            if not isinstance(last, dict):
                break
            if isinstance(last.get("body"), str) and last["body"].startswith("{"):
                try:
                    last = json.loads(last["body"])
                except Exception:
                    break
            else:
                break
        if isinstance(last, dict) and last.get("status") not in (0, None):
            return int(last.get("status")), str(last.get("body", ""))
        time.sleep(0.3)
    raise TestError("/show/ 取回超时: %r" % (last,))


# ── 会话导航「会话」页签（T-2/T-3/T-4 入口落点；与真实点击同一消息面）──
def open_sessions_pane():
    _m.mq_emit("filetree-mode-select", {"mode": "sessions"})
    poll(lambda: bool(eval_js("", "!!document.querySelector('.sp-new-chat')")), bool, timeout=15.0)


def session_card_disabled(s):
    """该会话行「用新窗口打开」按钮是否置灰（T-3 / MW-T14）。"""
    js = """(()=>{
  const cards=[...document.querySelectorAll('.session-card')];
  const c=cards.find(x=>x.textContent.includes('#%s'));
  if(!c) return JSON.stringify({found:false});
  const b=c.querySelector('.open-chat-window-btn');
  if(!b) return JSON.stringify({found:true, hasButton:false});
  const dis = !!(b.disabled || b.getAttribute('aria-disabled')==='true' || b.classList.contains('is-disabled'));
  return JSON.stringify({found:true, hasButton:true, disabled: dis, title: b.getAttribute('title')||''});
})()""" % s[:8]
    v = eval_obj("", js)
    if not isinstance(v, dict):
        raise TestError("会话行探测结果异常 %s: %r" % (s[:8], v))
    return v


def click_session_card(s):
    """点击会话行（T-4 / T-5 推论：已开窗 → 激活对应窗口、主窗不切换）。"""
    js = """(()=>{
  const cards=[...document.querySelectorAll('.session-card')];
  const c=cards.find(x=>x.textContent.includes('#%s'));
  if(!c) return 'no-card';
  c.click(); return 'ok';
})()""" % s[:8]
    return eval_js("", js)


def click_chat_entry_of(s):
    """点击该会话行的 T-3 按钮（用新窗口打开）。"""
    js = """(()=>{
  const cards=[...document.querySelectorAll('.session-card')];
  const c=cards.find(x=>x.textContent.includes('#%s'));
  if(!c) return 'no-card';
  const b=c.querySelector('.open-chat-window-btn');
  if(!b) return 'no-btn';
  b.click(); return 'ok';
})()""" % s[:8]
    return eval_js("", js)


# ══════════════════════════════════════════════════════════════════
# 9.1 多窗口基础
# ══════════════════════════════════════════════════════════════════

def case_t01():
    """MW-T01 C1/C3 同进程多窗口：open-chat ×2 → list 2 项；新窗 status{query} 有应答。"""
    close_all_chats()
    s1, s2 = sid("t01a"), sid("t01b")
    ensure_session(s1)
    ensure_session(s2)
    r1, r2 = open_chat(s1), open_chat(s2)
    if not (r1.get("ok") and r2.get("ok")):
        raise TestError("open-chat 失败: %s / %s" % (r1, r2))
    w1, w2 = r1["window_id"], r2["window_id"]
    if w1 == w2:
        raise TestError("两个窗口 window_id 应不同: %s" % w1)
    lst = wins()
    if len(lst) != 2:
        raise TestError("gui.window.list 应 2 项，实际 %s" % lst)
    if {w["session_id"] for w in lst} != {s1, s2}:
        raise TestError("窗口会话与请求不符: %s" % lst)
    for w in (w1, w2):
        st = req(w, "gui.window.status", {"command": "query"}, 10000)
        if not isinstance(st, dict) or "maximized" not in st or "minimized" not in st:
            raise TestError("新窗 %s status{query} 应答异常: %r" % (w, st))
    close_all_chats()


def case_t02():
    """MW-T02 关一窗不影响其它：关 w1 → 收 gui.window.closed；list 剩 1；w2 仍应答。"""
    close_all_chats()
    s1, s2 = sid("t02a"), sid("t02b")
    ensure_session(s1)
    ensure_session(s2)
    w1 = open_and_wait(s1)
    w2 = open_and_wait(s2)
    c = cl("")
    c.mq_on_capture(["gui.window.closed"])
    close_window(w1)
    evs = c.wait_events("gui.window.closed", 1, 20)
    if not any((e.get("payload") or {}).get("window_id") == w1 for e in evs):
        raise TestError("未收到 w1 的 gui.window.closed: %s" % evs)
    lst = poll(wins, lambda v: len(v) == 1, timeout=20.0)
    if len(lst) != 1 or lst[0]["window_id"] != w2:
        raise TestError("关闭 w1 后 list 应为 [%s]，实际 %s" % (w2, lst))
    st = req(w2, "gui.window.status", {"command": "query"}, 10000)
    if not isinstance(st, dict) or "maximized" not in st:
        raise TestError("w2 关闭后应仍应答: %r" % st)
    close_all_chats()


def case_t03():
    """MW-T03 主窗关闭 = 退出进程（半自动：观测 = 进程退出；广播侧随进程退出不可观测）。

    **必须是最后一个用例**（主窗关闭后测试通道随之关闭）。
    """
    close_all_chats()
    s = sid("t03")
    ensure_session(s)
    w = open_and_wait(s)
    cl(w).mq_on_capture(["gui.window.closed"])
    pub("", "gui.window.status", {"command": "close"}, 10000)  # 关主窗
    poll(lambda: _g.proc.poll(), lambda v: v is not None, timeout=30.0, interval=0.5)
    if _g.proc.poll() is None:
        raise TestError("主窗关闭后进程未退出（pid=%s）" % _g.proc.pid)


def case_t04():
    """MW-T04 窗口定向控制不串台：从 w2 最大化 → 仅 w2 maximized；事件只到 w2。"""
    close_all_chats()
    s1, s2 = sid("t04a"), sid("t04b")
    ensure_session(s1)
    ensure_session(s2)
    w1 = open_and_wait(s1)
    w2 = open_and_wait(s2)
    cm, c1, c2 = cl(""), cl(w1), cl(w2)
    for c in (cm, c1, c2):
        c.mq_on_capture(["window-maximized-changed"])
    st = req(w2, "gui.window.status", {"command": "maximize"}, 10000)
    if not st.get("maximized"):
        raise TestError("w2 maximize 后应 maximized=true: %r" % st)
    st1 = req(w1, "gui.window.status", {"command": "query"})
    if st1.get("maximized"):
        raise TestError("w1 不应被最大化（窗口控制串台）: %r" % st1)
    ev2 = c2.wait_events("window-maximized-changed", 1, 15)
    ev1 = c1.events_of("window-maximized-changed")
    evm = cm.events_of("window-maximized-changed")
    if not ev2:
        raise TestError("w2 未收到 window-maximized-changed")
    if ev1 or evm:
        raise TestError("window-maximized-changed 应只发往来源窗口: w1=%s main=%s" % (ev1, evm))
    req(w2, "gui.window.status", {"command": "maximize"}, 10000)  # 还原（切换回非最大化）
    close_all_chats()


def case_t05():
    """MW-T05 前端 → 前端不互通：主窗发布前端主题 → 对话窗不收到该事件回发。"""
    close_all_chats()
    s = sid("t05")
    ensure_session(s)
    w = open_and_wait(s)
    cm, cw = cl(""), cl(w)
    cm.mq_on_capture(["data-prj-config-refresh"])
    cw.mq_on_capture(["data-prj-config-refresh"])
    # 驱动：主窗发布（真实消息面：prj 配置保存 → 服务方广播 data-<domain>-refresh）
    req("", "data-prj-config-save", {"data": {"key": "mw.probe5", "value": "1"}})
    evm = cm.wait_events("data-prj-config-refresh", 1, 20)
    if not evm:
        raise TestError("主窗未收到本实例的 data-prj-config-refresh（后端事件未回本窗）")
    time.sleep(1.5)  # 给跨窗（若串台）足够到达窗口
    evw = cw.events_of("data-prj-config-refresh")
    if evw:
        raise TestError("前端之间应不互通，但对话窗收到了主窗实例的事件: %s" % evw)
    req("", "data-prj-config-delete", {"id": "mw.probe5"})
    close_all_chats()


def case_t06():
    """MW-T06 后端 → 各前端：每个窗口各得**本实例**的后端事件。"""
    close_all_chats()
    s = sid("t06")
    ensure_session(s)
    w = open_and_wait(s)
    cm, cw = cl(""), cl(w)
    cm.mq_on_capture(["data-prj-config-refresh"])
    cw.mq_on_capture(["data-prj-config-refresh"])
    # 主窗触发后端事件 → 主窗收
    req("", "data-prj-config-save", {"data": {"key": "mw.probe6a", "value": "1"}})
    if not cm.wait_events("data-prj-config-refresh", 1, 20):
        raise TestError("主窗未收到后端事件")
    # 对话窗触发后端事件 → 对话窗收（各前端各得本实例事件）
    pub(w, "data-prj-config-save", {"data": {"key": "mw.probe6b", "value": "1"}})
    if not cw.wait_events("data-prj-config-refresh", 1, 20):
        raise TestError("对话窗未收到本实例后端事件（后端 → 各前端不成立）")
    # 反向：对话窗事件不得投给主窗（实例隔离）
    evm = cm.events_of("data-prj-config-refresh")
    if any((e.get("payload") or {}).get("id") == "mw.probe6b" for e in evm):
        raise TestError("对话窗实例的后端事件被投给了主窗（隔离失效）: %s" % evm)
    for k in ("mw.probe6a", "mw.probe6b"):
        req("", "data-prj-config-delete", {"id": k})
    close_all_chats()


# ══════════════════════════════════════════════════════════════════
# 9.2 I-1 会话唯一（核心不变量）
# ══════════════════════════════════════════════════════════════════

def case_t07():
    """MW-T07 I-1 ② 重复开同一 session = 激活：第 2 次 activated=true；list 仍 1 项。"""
    close_all_chats()
    s = sid("t07")
    ensure_session(s)
    r1 = open_chat(s)
    r2 = open_chat(s)
    if not (r1.get("ok") and r2.get("ok")):
        raise TestError("open-chat 失败: %s / %s" % (r1, r2))
    if r1.get("activated") is not False:
        raise TestError("首次开窗应 activated=false: %s" % r1)
    if r2.get("activated") is not True:
        raise TestError("重复开同 session 应 activated=true: %s" % r2)
    if r2.get("window_id") != r1.get("window_id"):
        raise TestError("重复开应复用同一窗口: %s vs %s" % (r1, r2))
    lst = wins()
    if len(lst) != 1:
        raise TestError("list 应只有 1 个对话窗口: %s" % lst)
    close_all_chats()


def case_t08():
    """MW-T08 I-1 ① 主窗当前会话的开窗判定（**61 §1 口径**）。

    61-消息一览 §1 `gui.window.open-chat` 明载：「主窗口当前活动会话不得开新窗口」的判定
    **在前端**（宿主不追踪主窗会话），原「拒开①」**已删除** → 宿主侧应**接受**（ok=true），
    置灰由前端负责（见 MW-T14）。24 §9 原文记的 `{ok:false}` 与 61 冲突（已在报告中列出）。
    """
    close_all_chats()
    s = sid("t08")
    ensure_session(s)
    req("", "data-session-active-set", {"session_id": s})  # 主窗当前会话 = s
    before = len(wins())
    r = open_chat(s)
    if not r.get("ok"):
        raise TestError("宿主不应拒开（61 §1：拒开①已删除、判定在前端）: %s" % r)
    lst = poll(wins, lambda v: len(v) == before + 1, timeout=20.0)
    if len(lst) != before + 1:
        raise TestError("应新增 1 个对话窗口: %s → %s" % (before, lst))
    close_all_chats()


def case_t09():
    """MW-T09 T-4 点击已开 session = 激活（半自动→本文以 MQ + DOM 双证据自动化）。"""
    close_all_chats()
    s_a, s_b = sid("t09a"), sid("t09b")
    ensure_session(s_a)
    ensure_session(s_b)
    req("", "data-session-active-set", {"session_id": s_b})
    _m.mq_emit("session-changed", {"session_id": s_b})
    ensure_session(s_a)
    w = open_and_wait(s_a)
    open_sessions_pane()
    poll(lambda: len(wins()), lambda v: v == 1, timeout=10.0)
    r = click_session_card(s_a)
    if r != "ok":
        raise TestError("未找到会话行 %s: %r" % (s_a[:8], r))
    time.sleep(2.0)
    lst = wins()
    if len(lst) != 1 or lst[0]["window_id"] != w:
        raise TestError("点击已开窗会话应只激活（不新建）: %s" % lst)
    active = req("", "data-session-active-get", {})
    if (active or {}).get("session_id") == s_a:
        raise TestError("I-1 推论：主窗不应切换到已被对话窗绑定的会话（实际切了）: %s" % active)
    close_all_chats()


def case_t10():
    """MW-T10 I-1 推论 主窗禁切到已绑定 session（半自动→MQ + DOM 双证据自动化）。"""
    close_all_chats()
    s_x, s_y = sid("t10x"), sid("t10y")
    ensure_session(s_x)
    ensure_session(s_y)
    req("", "data-session-active-set", {"session_id": s_y})
    _m.mq_emit("session-changed", {"session_id": s_y})
    open_and_wait(s_x)
    open_sessions_pane()
    if click_session_card(s_x) != "ok":
        raise TestError("未找到会话行 %s" % s_x[:8])
    time.sleep(2.0)
    active = req("", "data-session-active-get", {})
    if (active or {}).get("session_id") == s_x:
        raise TestError("主窗不得切到已绑定对话窗的会话（I-1 推论）: %s" % active)
    if len(wins()) != 1:
        raise TestError("不得新建窗口（应为激活）: %s" % wins())
    close_all_chats()


def case_t11():
    """MW-T11 窗口关闭解除占用：关 w1 → 再 open-chat{S} → activated=false（新建成功）。"""
    close_all_chats()
    s = sid("t11")
    ensure_session(s)
    r1 = open_chat(s)
    close_window(r1["window_id"])
    poll(wins, lambda v: not v, timeout=20.0)
    r2 = open_chat(s)
    if not r2.get("ok"):
        raise TestError("窗口关闭后应可重新建窗: %s" % r2)
    if r2.get("activated") is not False:
        raise TestError("重新建窗应 activated=false: %s" % r2)
    if r2.get("window_id") == r1.get("window_id"):
        raise TestError("重建应分配新 window_id: %s vs %s" % (r1, r2))
    close_all_chats()


# ══════════════════════════════════════════════════════════════════
# 9.3 触发位置 T-1…T-5
# ══════════════════════════════════════════════════════════════════

def case_t12():
    """MW-T12 T-1 主 chat 头部图标：点击 → 发出 open-chat{新 sid}；新窗标题 = 新会话 + <workdir>。"""
    close_all_chats()
    base = os.path.basename(os.path.normpath(WS))
    if not eval_js("", "!!document.querySelector('.new-chat-window-btn')"):
        raise TestError("主窗口缺少 T-1 入口 .new-chat-window-btn")
    eval_js("", "document.querySelector('.new-chat-window-btn').click(); 'ok'")
    lst = poll(wins, lambda v: len(v) == 1, timeout=25.0)
    if len(lst) != 1:
        raise TestError("点击 T-1 后应有 1 个对话窗口: %s" % lst)
    # 新会话须已在 DB 中存在（session-ensure）
    s_new = lst[0]["session_id"]
    titles = os_titles()
    if ("新会话 " + base) not in titles:
        raise TestError("新窗标题应为「新会话 %s」，实际窗口标题 %s" % (base, titles))
    listed = [x.get("session_id") for x in (req("", "data-session-list", {}) or {}).get("list") or []]
    if s_new not in listed:
        raise TestError("T-1 新会话未落库（session-ensure 缺失）: %s not in %s" % (s_new, listed))
    close_all_chats()


def case_t13():
    """MW-T13 T-2 session 列表标题栏图标：点击 → 同上（新会话 + 新窗，标题同形）。"""
    close_all_chats()
    base = os.path.basename(os.path.normpath(WS))
    open_sessions_pane()
    eval_js("", "document.querySelector('.sp-new-chat').click(); 'ok'")
    lst = poll(wins, lambda v: len(v) == 1, timeout=25.0)
    if len(lst) != 1:
        raise TestError("点击 T-2 后应有 1 个对话窗口: %s" % lst)
    titles = os_titles()
    if ("新会话 " + base) not in titles:
        raise TestError("新窗标题应为「新会话 %s」，实际 %s" % (base, titles))
    close_all_chats()


def case_t14():
    """MW-T14 T-3 列表项按钮**置灰**：该 session = 主窗当前会话 → 按钮置灰、不可点。"""
    close_all_chats()
    s = sid("t14")
    ensure_session(s)
    req("", "data-session-active-set", {"session_id": s})
    _m.mq_emit("session-changed", {"session_id": s})
    open_sessions_pane()
    info = poll(lambda: session_card_disabled(s), lambda v: v.get("disabled"), timeout=15.0)
    if not info.get("disabled"):
        raise TestError("主窗当前会话的 T-3 按钮应置灰: %s" % info)
    # 置灰 → 点击不应建窗
    click_chat_entry_of(s)
    time.sleep(2.0)
    if wins():
        raise TestError("置灰按钮点击不应建窗: %s" % wins())


def case_t15():
    """MW-T15 T-3 列表项按钮可用：非主窗当前会话 → 点击建窗成功。"""
    close_all_chats()
    s_cur, s_other = sid("t15cur"), sid("t15oth")
    ensure_session(s_cur)
    ensure_session(s_other)
    req("", "data-session-active-set", {"session_id": s_cur})
    _m.mq_emit("session-changed", {"session_id": s_cur})
    open_sessions_pane()
    info = poll(lambda: session_card_disabled(s_other),
                lambda v: v.get("hasButton") and not v.get("disabled"), timeout=15.0)
    if not (info.get("hasButton") and not info.get("disabled")):
        raise TestError("非当前会话的 T-3 按钮应可用: %s" % info)
    if click_chat_entry_of(s_other) != "ok":
        raise TestError("未找到会话行 %s" % s_other[:8])
    lst = poll(wins, lambda v: len(v) == 1, timeout=25.0)
    if len(lst) != 1 or lst[0]["session_id"] != s_other:
        raise TestError("T-3 点击应建窗且绑定该会话: %s" % lst)
    close_all_chats()


def case_t16():
    """MW-T16 仅 chat 渲染 + 不持久化几何：无 toolbar/文件树/预览；不写 window.*/layout.*。"""
    close_all_chats()
    cfg_before = prj_cfg()
    s = sid("t16")
    ensure_session(s)
    w = open_and_wait(s)
    dom = eval_js(w, """(()=>{
  const q=s=>!!document.querySelector(s);
  return JSON.stringify({
    only: q('.chat-only-view'), toolbar: q('.toolbar'), filetree: q('.filetree-panel'),
    preview: q('.preview-panel'), statusbar: q('.statusbar'),
    chat: q('.panel-inner'), url_search: location.search, url_hash: location.hash
  });
})()""")
    dom = json.loads(dom) if isinstance(dom, str) else dom
    if not dom.get("only") or not dom.get("chat"):
        raise TestError("对话窗口应渲染 ChatOnlyView + chat 面板: %s" % dom)
    for k in ("toolbar", "filetree", "preview", "statusbar"):
        if dom.get(k):
            raise TestError("对话窗口不应有 %s: %s" % (k, dom))
    close_window(w)
    poll(wins, lambda v: not v, timeout=20.0)
    time.sleep(1.0)
    cfg_after = prj_cfg()
    added = {k: v for k, v in cfg_after.items() if k not in cfg_before}
    # C5（24 §3.3）：对话窗口**不写** `window.*` / `layout.*`（也不参与 filetree/opened 持久化）。
    bad = {k: v for k, v in added.items()
           if k.startswith("window.") or k.startswith("layout.") or k.startswith("window.chat.")
           or k.startswith("filetree-") or k.startswith("opened-")}
    if bad:
        raise TestError("对话窗口不得写 window.*/layout.* 等几何/布局键: %s" % bad)


def case_t17():
    """MW-T17 对话窗不写 active_session_id：开/交互对话窗后 prj 活动会话不变。"""
    close_all_chats()
    s_main, s_chat = sid("t17main"), sid("t17chat")
    ensure_session(s_main)
    ensure_session(s_chat)
    req("", "data-session-active-set", {"session_id": s_main})
    _m.mq_emit("session-changed", {"session_id": s_main})
    w = open_and_wait(s_chat)
    time.sleep(2.0)
    active = req("", "data-session-active-get", {})
    if (active or {}).get("session_id") != s_main:
        raise TestError("对话窗口不得改 active_session_id: 期望 %s 实际 %s" % (s_main, active))
    close_all_chats()


def case_t18():
    """MW-T18 对话窗可跑 tools（同能力）：对话窗经消息面驱动后端能力面（tools-list + filesys）。"""
    close_all_chats()
    s = sid("t18")
    ensure_session(s)
    w = open_and_wait(s)
    # 能力面（MCP tools 清单）：对话窗可向 gateway 取
    r = pub(w, "tools-list", {})
    if not isinstance(r, dict) or r.get("ok") is False:
        raise TestError("对话窗 tools-list 失败: %s" % r)
    tools = (r.get("result") or {}).get("tools")
    if not isinstance(tools, list):
        raise TestError("对话窗 tools-list 应答应含 tools 数组: %s" % r)
    # 工具/文件域能力（filesys.list）：对话窗读到工作目录子项
    fr = req(w, "filesys.list", {"work_dir": WS, "path": WS})
    children = [c.get("name") for c in (fr or {}).get("children") or []]
    if "mw-a.txt" not in children:
        raise TestError("对话窗 filesys.list 应读到工作目录子项: %s" % children)
    close_all_chats()


def case_t25():
    """MW-T25 标题随摘要更新（set-title，**仅对话窗**）：对话窗 ok=true 且标题更新；主窗不变。

    主窗口标题固定 = `chonkpilot-<workdir 目录名>`（宿主建窗缺省且**不随会话变化**，见 main.go）；
    对话窗口标题可由 `gui.window.set-title` 更新（61 §1：执行对象 = 调用来源窗口）。
    """
    close_all_chats()
    base = os.path.basename(os.path.normpath(WS))
    s = sid("t25")
    ensure_session(s)
    w = open_and_wait(s)
    r_main = (pub("", "gui.window.set-title", {"title": "MW-T25-主窗不应变"}) or {}).get("result") or {}
    if r_main.get("ok") is not False:
        raise TestError("主窗口 set-title 应返回 ok=false（仅对话窗口可改标题）: %s" % r_main)
    r = req(w, "gui.window.set-title", {"title": "MW-T25-对话窗标题"})
    if r.get("ok") is not True:
        raise TestError("对话窗口 set-title 应 ok=true: %s" % r)
    titles = poll(os_titles, lambda v: "MW-T25-对话窗标题" in v, timeout=15.0)
    if "MW-T25-对话窗标题" not in titles:
        raise TestError("对话窗口标题未更新: %s" % titles)
    main_title = "chonkpilot-" + base
    if main_title not in titles:
        raise TestError("主窗口标题应固定为 %s（不应被改动）: %s" % (main_title, titles))
    if "MW-T25-主窗不应变" in titles:
        raise TestError("主窗口标题被改动（仅对话窗口可改）: %s" % titles)
    close_all_chats()


def case_t26():
    """MW-T26 独立窗口上限 5：连发 6 次（新 session）→ 前 5 次 ok=true；第 6 次 ok=false。"""
    close_all_chats()
    ids = []
    for i in range(6):
        s = sid("t26-%d" % i)
        ensure_session(s)
        r = open_chat_result(s)
        if i < 5:
            if not r.get("ok"):
                raise TestError("第 %d 次 open-chat 应成功: %s" % (i + 1, r))
            ids.append(r.get("window_id"))
        else:
            if r.get("ok"):
                raise TestError("第 6 次 open-chat 应被拒（上限 5）: %s" % r)
    lst = wins()
    if len(lst) != 5:
        raise TestError("list 中对话窗口数应 = 5，实际 %d: %s" % (len(lst), lst))
    # T-1/T-2 入口置灰（达上限）：先打开「会话」页签（load → gui.window.list → 前端刷新已开清单）
    open_sessions_pane()
    t1 = poll(
        lambda: eval_obj("", """(()=>{const b=document.querySelector('.new-chat-window-btn');
            return JSON.stringify({dis: !!(b&&(b.disabled||b.getAttribute('aria-disabled')==='true'))})})()"""),
        lambda v: v.get("dis"), timeout=15.0)
    if not t1.get("dis"):
        raise TestError("达上限时 T-1 入口应置灰: %s" % t1)
    t2 = poll(
        lambda: eval_obj("", """(()=>{const b=document.querySelector('.sp-new-chat');
            return JSON.stringify({dis: !!(b&&(b.disabled||b.getAttribute('aria-disabled')==='true'))})})()"""),
        lambda v: v.get("dis"), timeout=15.0)
    if not t2.get("dis"):
        raise TestError("达上限时 T-2 入口应置灰: %s" % t2)
    close_all_chats()


def case_t27():
    """MW-T27 主题 / 语言跨窗口**即时同步**：主窗保存 usr 配置 → 已开对话窗即时**收到并应用**。

    断言口径 = 61 §3.1 的 **`data-user-config-changed`**（服务方在 `data-user-config-save` /
    `-delete` 成功后**下行广播**；payload `{data:{…}}` 与 save 同形；**不带 `instance_id`**
    → 全局：每个窗口的桥各自转发 → **所有窗口都收到**）。
    ⚠️ 口径订正（2026-09-25）：`data-<domain>-refresh` 家族带 `instance_id` → 桥
    `acceptEventInstance` 过滤 → **只到来源窗口**（正是 MW-T05/T06 断言的隔离），故**不能**
    用它断言跨窗同步；本用例原口径（refresh 家族）与 61 §3 冲突，随实现落地改按 §3.1。
    """
    close_all_chats()
    s = sid("t27")
    ensure_session(s)
    w = open_and_wait(s)
    cw = cl(w)
    cw.mq_on_capture(["data-user-config-changed"])

    # ① 主窗改主题 → 对话窗即时收到广播（payload 形态按 61 §3.1）
    req("", "data-user-config-save", {"data": {"theme": "dark"}})
    evs = poll(lambda: cw.events_of("data-user-config-changed", clear=False),
               lambda v: bool(v), timeout=12.0)
    if not evs:
        raise TestError("对话窗未即时收到 usr 配置变更广播（跨窗口 theme/locale 同步未生效）")
    pl = evs[0].get("payload") if isinstance(evs[0], dict) else None
    if isinstance(pl, str):
        try:
            pl = json.loads(pl)
        except Exception:
            pl = None
    if not isinstance(pl, dict):
        raise TestError("data-user-config-changed 载荷形态异常: %r" % (evs[0],))
    if "instance_id" in pl:
        raise TestError("data-user-config-changed 不应带 instance_id（61 §3.1：全局投递）: %r" % pl)
    data = pl.get("data")
    if not isinstance(data, dict) or data.get("theme") != "dark":
        raise TestError("data-user-config-changed 载荷应为 {data:{theme:'dark'}}（61 §3.1）: %r" % pl)
    # ② 对话窗**已应用**（不只是收到）：本窗口主题属性跟随
    got = poll(lambda: eval_js(w, "document.documentElement.getAttribute('data-theme')", 8000),
               lambda v: v == "dark", timeout=12.0)
    if got != "dark":
        raise TestError("对话窗未应用 theme=dark（本窗口 data-theme=%r）" % (got,))

    # ③ 语言同理（WIN-021-S02）：对话窗即时跟随（应用标记 = 本窗口 locale 置位）
    req("", "data-user-config-save", {"data": {"locale": "en-US"}})
    loc = poll(lambda: eval_js(w, "localStorage.getItem('chonkpilot-locale')", 8000),
               lambda v: v == "en-US", timeout=12.0)
    if loc != "en-US":
        raise TestError("对话窗未应用 locale=en-US（本窗口 locale=%r）" % (loc,))

    # 还原（usr 主题 / 语言）
    req("", "data-user-config-save", {"data": {"theme": "light", "locale": "zh-CN"}})
    close_all_chats()


def case_t28():
    """MW-T28「新建对话窗口」入口仅主窗口：对话窗内无 T-1 图标。"""
    close_all_chats()
    s = sid("t28")
    ensure_session(s)
    w = open_and_wait(s)
    in_main = eval_js("", "!!document.querySelector('.new-chat-window-btn')")
    in_chat = eval_js(w, "!!document.querySelector('.new-chat-window-btn')")
    if not in_main:
        raise TestError("主窗口应有 T-1 入口")
    if in_chat:
        raise TestError("对话窗口不应有 T-1 入口（仅主窗口）")
    close_all_chats()


def case_t29():
    """MW-T29 会话标题变更**跨窗口广播**（61 §3.2 · G-48 ⑦ / #12，2026-09-26 新增）：
    主窗侧改会话标题 → `data-session-title-changed`（`{session_id, title}`，**不带
    `instance_id`** = 全局）→ 已开**对话窗口**（另一 instance）标题跟随；
    **未在本窗打开的会话**改名不波及本窗（无副作用）。

    唯一准则 = 61 §3.2（本主题为该批新增）；断言 = mq 监听（载荷形态）+ OS 窗口标题双证据。
    """
    close_all_chats()
    s = sid("t29")
    ensure_session(s)
    w = open_and_wait(s)
    cw = cl(w)
    cw.mq_on_capture(["data-session-title-changed"])

    # ① 主窗侧改标题 → 对话窗即时收到下行广播（载荷形态按 61 §3.2）
    title = "MW-T29-标题跟随"
    req("", "data-session-title", {"id": s, "title": title})
    evs = poll(lambda: cw.events_of("data-session-title-changed", clear=False),
               lambda v: bool(v), timeout=12.0)
    if not evs:
        raise TestError("对话窗未即时收到 data-session-title-changed（跨窗口标题跟随未生效）")
    pl = evs[0].get("payload") if isinstance(evs[0], dict) else None
    if isinstance(pl, str):
        try:
            pl = json.loads(pl)
        except Exception:
            pl = None
    if not isinstance(pl, dict):
        raise TestError("data-session-title-changed 载荷形态异常: %r" % (evs[0],))
    if pl.get("session_id") != s or pl.get("title") != title:
        raise TestError("载荷应为 {session_id:%r, title:%r}（61 §3.2）: %r" % (s, title, pl))
    if "instance_id" in pl:
        raise TestError("data-session-title-changed 不应带 instance_id（61 §3.2：全局投递）: %r" % pl)

    # ② 对话窗**标题已跟随**（端到端）：OS 窗口标题出现新标题
    titles = poll(os_titles, lambda v: title in v, timeout=15.0)
    if title not in titles:
        raise TestError("对话窗口标题未跟随会话标题: %s" % titles)

    # ③ 未在本窗打开的会话改名 → 本窗标题**不变**（无副作用）。事件仍按全局投递到达本窗，
    #    但前端按 session_id 过滤 → **不应用**（否则标题会跳到其它会话的标题）。
    s2 = sid("t29-other")
    ensure_session(s2)
    cw.events_of("data-session-title-changed", clear=True)  # 清空前一轮，便于观察下一轮到达
    req("", "data-session-title", {"id": s2, "title": "MW-T29-其它会话"})
    poll(lambda: cw.events_of("data-session-title-changed", clear=False),
         lambda v: bool(v), timeout=12.0)  # 事件确已到达（全局可见）
    time.sleep(1.0)  # 给前端应用窗口（若误应用则本窗标题会变）
    titles = os_titles()
    if title not in titles:
        raise TestError("本窗标题被其它会话改名波及（应保持不变）: %s" % titles)
    if "MW-T29-其它会话" in titles:
        raise TestError("其它会话改名的标题不应出现在本窗: %s" % titles)
    close_all_chats()


# ══════════════════════════════════════════════════════════════════
# 9.4 数据层 B 方案与隔离
# ══════════════════════════════════════════════════════════════════

def _b_root():
    pid = (prj_cfg() or {}).get("project-id") or ""
    if not pid:
        raise TestError("prj 库缺 project-id（prjusr 数据根无法绑定）")
    return os.path.join(HOME, ".chonkpilot", "data", pid)


def case_t19():
    """MW-T19 C4 B 方案落点：prj 留项目内；prjusr 落 <home>/.chonkpilot/data/<prj-id>/。"""
    prj_db = os.path.join(WS, ".chonkpilot", "chonkpilot.db")
    if not os.path.isfile(prj_db):
        raise TestError("prj 库应在项目内: %s" % prj_db)
    root = _b_root()
    if not os.path.isfile(os.path.join(root, "chonkpilot.db")):
        raise TestError("prjusr 库应在 %s/chonkpilot.db" % root)
    if os.path.exists(os.path.join(WS, ".chonkpilot", "tmp")):
        raise TestError("项目数据根不应出现 tmp/（个人运行态落 prjusr 根）")


def case_t20():
    """MW-T20 日志跟随数据根（U-1 已决）：<prjusr 根>/logs/gui.log 存在；init-data.logDir 指向它。"""
    root = _b_root()
    log_dir = os.path.join(root, "logs")
    if not os.path.isfile(os.path.join(log_dir, "gui.log")):
        raise TestError("日志应在 %s/gui.log" % log_dir)
    init = req("", "gui.init-data", {})
    got = str((init or {}).get("logDir", ""))
    if os.path.normcase(os.path.normpath(got)) != os.path.normcase(os.path.normpath(log_dir)):
        raise TestError("gui.init-data.logDir 应 = %s，实际 %r" % (log_dir, got))


def case_t21():
    """MW-T21 截图 / 附件落点 + fileserver 白名单：**对话窗**上传 → 落 prjusr 根 tmp/uploads 且 /show/ 可达。"""
    close_all_chats()
    s = sid("t21")
    ensure_session(s)
    w = open_and_wait(s)
    payload = b"mw-t21-chat-upload\n"
    res = req(w, "gui.upload", {
        "name": "mw_t21.txt",
        "data": base64.b64encode(payload).decode("ascii"),
        "kind": "file",
    })
    path = (res or {}).get("path")
    if not path:
        raise TestError("对话窗 gui.upload 未返回落盘路径: %s" % res)
    root = _b_root()
    up_dir = os.path.normcase(os.path.normpath(os.path.join(root, "tmp", "uploads")))
    got_dir = os.path.normcase(os.path.normpath(os.path.dirname(str(path))))
    if got_dir != up_dir:
        raise TestError("附件应落 prjusr 根 tmp/uploads：期望 %s，实际 %s" % (up_dir, got_dir))
    status, body = show_fetch(w, path)
    if status != 200 or body != payload.decode("ascii"):
        raise TestError("/show/ 应取回对话窗上传的附件: status=%s body=%r" % (status, body))
    close_all_chats()


def case_t22():
    """MW-T22 URL 路由同页：GET `/?session-id=…#chat` 返回与 `/` 同一 index.html（query/hash 不进 Path）。"""
    close_all_chats()
    s = sid("t22")
    ensure_session(s)
    w = open_and_wait(s)
    dom = eval_js(w, """(()=>JSON.stringify({
      search: location.search, hash: location.hash,
      chatOnly: !!document.querySelector('.chat-only-view'),
      mainLayout: !!document.querySelector('.toolbar')
    }))()""")
    dom = json.loads(dom) if isinstance(dom, str) else dom
    if ("session-id=" + s) not in (dom.get("search") or ""):
        raise TestError("对话窗 URL 应含 session-id: %s" % dom)
    if dom.get("hash") != "#chat":
        raise TestError("对话窗 URL hash 应为 #chat: %s" % dom)
    if not dom.get("chatOnly") or dom.get("mainLayout"):
        raise TestError("同一 index.html 应按 URL 分派为 ChatOnlyView: %s" % dom)
    # 主窗口同一 index.html（主布局）——同页不同分派
    if not eval_js("", "!!document.querySelector('.toolbar')"):
        raise TestError("主窗口应由同一 index.html 分派为主布局")
    close_all_chats()


def case_t23():
    """MW-T23 Resolve 审计（MW-11）：多实例下核心数据场景 → 无 ErrInstanceIDRequired。"""
    close_all_chats()
    s = sid("t23")
    ensure_session(s)
    w = open_and_wait(s)  # 此时进程内 ≥2 个实例（主窗 + 对话窗）
    ops = [
        ("data-session-list", {}),
        ("data-session-ensure-session", {"session_id": s + "-b"}),
        ("data-session-history", {"session_id": s}),
        ("data-tasktree-list", {"top_session": s}),
        ("data-tasktree-tasks", {"session_id": s}),
        ("data-prj-config-list", {}),
        ("data-user-config-load", {}),
        ("data-scenario-list", {}),
    ]
    for wid in ("", w):
        for typ, payload in ops:
            r = pub(wid, typ, payload)
            if not isinstance(r, dict):
                raise TestError("%s(%s) 无应答: %r" % (typ, wid or "main", r))
            errs = " | ".join(r.get("errors") or [])
            if "instance_id required" in errs:
                raise TestError("多实例下 %s(%s) 触发 ErrInstanceIDRequired: %s" % (typ, wid or "main", errs))
            if r.get("ok") is False:
                raise TestError("%s(%s) 失败: %s" % (typ, wid or "main", errs or r.get("result")))
    close_all_chats()


# ══════════════════════════════════════════════════════════════════
# 主流程
# ══════════════════════════════════════════════════════════════════
CASES = [
    ("MW-T01 同进程多窗口（list 2 项 + 新窗 status 应答）", case_t01),
    ("MW-T02 关一窗不影响其它（closed 广播 + list 剩 1）", case_t02),
    ("MW-T04 窗口定向控制不串台（仅来源窗最大化/收事件）", case_t04),
    ("MW-T05 前端 → 前端不互通", case_t05),
    ("MW-T06 后端 → 各前端（每窗各得本实例事件）", case_t06),
    ("MW-T07 I-1② 重复开同 session = 激活（幂等）", case_t07),
    ("MW-T08 I-1① 主窗当前会话开窗（61 §1 口径：宿主不拒、前端置灰）", case_t08),
    ("MW-T09 T-4 点击已开 session = 激活（主窗不切换）", case_t09),
    ("MW-T10 I-1 推论 主窗禁切到已绑定 session", case_t10),
    ("MW-T11 窗口关闭解除占用（可重建）", case_t11),
    ("MW-T12 T-1 主 chat 头部图标（新会话 + 标题）", case_t12),
    ("MW-T13 T-2 会话列表标题栏图标（新会话）", case_t13),
    ("MW-T14 T-3 列表项按钮置灰（主窗当前会话）", case_t14),
    ("MW-T15 T-3 列表项按钮可用（非当前会话 → 建窗）", case_t15),
    ("MW-T16 仅 chat 渲染 + 不持久化几何", case_t16),
    ("MW-T17 对话窗不写 active_session_id", case_t17),
    ("MW-T18 对话窗可跑 tools（能力面 + filesys）", case_t18),
    ("MW-T19 C4 B 方案落点（prj 项目内 / prjusr 用户根）", case_t19),
    ("MW-T20 日志跟随数据根（logs/gui.log + init-data.logDir）", case_t20),
    ("MW-T21 对话窗附件落 prjusr tmp/uploads + /show/ 可达", case_t21),
    ("MW-T22 URL 路由同页（?session-id=…#chat 同 index.html）", case_t22),
    ("MW-T23 Resolve 审计：多实例核心数据场景无 ErrInstanceIDRequired", case_t23),
    ("MW-T25 set-title 仅对话窗（主窗 ok=false / 标题不变）", case_t25),
    ("MW-T26 独立窗口上限 5（第 6 次拒开 + 入口置灰）", case_t26),
    ("MW-T27 主题/语言跨窗口即时同步", case_t27),
    ("MW-T28「新建对话窗口」入口仅主窗口", case_t28),
    ("MW-T29 会话标题变更跨窗口广播（对话窗标题跟随）", case_t29),
    ("MW-T03 主窗关闭 = 退出进程（必须最后跑）", case_t03),
]


def main():
    global _g, _m
    # 可选筛选：`python run_mw.py T05 T06` → 只跑名字含该子串的用例（迭代提速；不带参 = 全跑）。
    flt = sys.argv[1:]
    cases = [(n, f) for n, f in CASES if not flt or any(a in n for a in flt)]
    ok = 0
    try:
        _g = _h.start_gui(port=_h.free_port(), work_dir=WS, home=HOME, ready_timeout=90)
        _m = _g.client
        for name, fn in cases:
            ok += 1 if run_case(name, fn) else 0
    finally:
        try:
            if _g is not None:
                _g.stop()
        except Exception:
            pass
    total = len(cases)
    print("\n== 多窗口 MW-T*：%d/%d 通过 ==" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
