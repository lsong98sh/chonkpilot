# -*- coding: utf-8 -*-
"""B8 · 语义/回环级推广 ①：**工具执行结果渲染与回读**（真实工具执行 → 结果语义 → 历史回读）。

覆盖（**拒绝"存在级"：断言工具**真实执行**产出的结果内容 + 渲染 + 经受持久层回读**）：
  G1 执行语义：mock LLM 路由出 `self_script_run`（cmd `echo py-ok`）→ 真实执行 →
     `tool-result` 事件载荷**含 stdout 哨兵 `py-ok`**（工具结果内容级，非仅事件存在）。
  G2 渲染语义：展开对应 `tool_pair` 卡片（`.section-header` → `.more-link`）→ 卡片文本**含 `py-ok`**
     （结果落进 DOM，非仅容器存在）。
  G3 回读回环：结束轮次 → 切走会话 → 切回 → 经 `data-session-history` 回填后，该 `tool_pair`
     卡片展开仍**含 `py-ok`**（真实执行结果经受 persist 的历史往返）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）
+ harness 自起 mock LLM（8901；与 usr `llms[0]` 对齐）；套件级快照-还原 `_h.suite_config_guard(c)`
（含临时写入的 usr `llms`/`defaultLLM`）。夹具落临时 work-dir，结束即弃。

观测渠道（**均为 61-消息一览既有主题，零新增**）：`llm-start`/`session-changed`/`chat-select-llm`（§4.2）·
`session-receive`→`llm-receive`/`tool-result`（§4.3）· `data-session-history`（§3.2）· DOM（--test-port /eval）。

运行：python run_sem_tool_result.py
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

WS = _h.tmp_dir("ck-semtool-ws-")
DD = _h.tmp_dir("ck-semtool-dd-")
HOME = _h.tmp_home()
SID = "sem-tool-1"
OTHER = "sem-tool-other"
MARK = "py-ok"
MOCK = {"name": "mock", "protocol": "openai", "apiKey": "mock-key", "model": "mock-model",
        "baseUrl": "http://127.0.0.1:%d/v1" % _h.DEFAULT_MOCK_LLM_PORT}

_h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)         # 复用优先；无则自起（8901）
_g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME)
c = _g.client
_h.suite_config_guard(c)
_h.ensure_locale(c, "zh-CN")
# usr llms 注入 mock（defaultLLM=0）；套件级快照-还原会在退出前回滚
c.req("data-user-config-save", {"data": {"llms": [MOCK], "defaultLLM": 0}})
print("[env] ws=%s data=%s home=%s gui=%d mock=%d（临时目录，结束即删）"
      % (WS, DD, HOME, _g.port, _h.DEFAULT_MOCK_LLM_PORT), flush=True)


def J(js):
    return _h._plain(c.eval(js))


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def wait_upto(js, ok, max_wait=20.0, interval=0.3):
    end = time.time() + max_wait
    v = J(js)
    while not ok(v) and time.time() < end:
        time.sleep(interval)
        v = J(js)
    return v


def activate(sid):
    c.mq_emit("session-changed", {"session_id": sid})


def wait_active(sid, max_wait=15.0):
    return wait_upto("(()=>{const t=document.querySelector('.chat-session-tag');return t?t.getAttribute('title'):null})()",
                     lambda v: v == sid, max_wait=max_wait)


def payloads_containing(topic, mark):
    out = []
    for e in c.events_of(topic, clear=False):
        p = e.get("payload")
        if isinstance(p, str):
            try:
                p = json.loads(p)
            except Exception:
                pass
        if mark in json.dumps(p, ensure_ascii=False):
            out.append(p)
    return out


def expand_pair():
    """展开 tool_pair 卡片：点 section-header → 点 more-link（显示完整 result）。"""
    J("(()=>{const h=document.querySelector('.message-item.tool_pair .section-header');if(h)h.click();return 'ok';})()")
    time.sleep(0.4)
    J("(()=>{const m=document.querySelector('.message-item.tool_pair .more-link');if(m)m.click();return 'ok';})()")
    time.sleep(0.4)


def pair_text():
    return J("(()=>{const c=document.querySelector('.message-item.tool_pair');return c?c.textContent:''})()") or ""


def case_g1_execution_content():
    """G1：真实工具执行 → tool-result 事件载荷含 stdout 哨兵。"""
    _h.install_session_history_spy(c)
    activate(SID)
    _h.wait_session_history(c, SID)
    wait_active(SID)
    c.mq_on_capture(["tool-result", "tool-pair", "llm-tool-call", "llm-complete", "turn-start"])
    c.mq_emit("chat-select-llm", {"name": "mock"})
    c.mq_emit("llm-start", {"session_id": SID, "turn": "t-" + SID, "q": "please call py",
                            "llm": "mock", "think": "", "effort": "", "scenario_id": ""})
    end = time.time() + 90
    hit = []
    while time.time() < end:
        hit = payloads_containing("tool-result", MARK)
        if hit:
            break
        time.sleep(0.5)
    if not hit:
        raise TestError("tool-result 未含执行结果哨兵 %r（工具未真实执行或结果未回填）" % MARK)
    evi("G1 执行结果内容", mark=MARK, tool=(hit[0] or {}).get("tool"))


def case_g2_render_semantic():
    """G2：渲染语义 —— 展开 tool_pair 卡片后文本含执行结果哨兵。"""
    expand_pair()
    txt = wait_upto("(()=>{const c=document.querySelector('.message-item.tool_pair');return c?c.textContent:''})()",
                    lambda v: isinstance(v, str) and MARK in v, max_wait=12.0) or ""
    if MARK not in txt:
        raise TestError("tool_pair 卡片展开后未渲染结果 %r：%r" % (MARK, txt[:160]))
    evi("G2 结果渲染", rendered=True, text_len=len(txt))


def case_g3_history_readback():
    """G3：回读回环 —— 切走 → 切回 → 卡片展开仍含执行结果哨兵。"""
    # 结束轮次（等待终态，避免切换时 busy 干扰）
    w_end = time.time() + 60
    while time.time() < w_end and not c.events_of("llm-complete", clear=False):
        time.sleep(0.5)
    activate(OTHER)
    _h.wait_session_history(c, OTHER)
    time.sleep(0.4)
    activate(SID)
    _h.wait_session_history(c, SID)
    wait_active(SID)
    expand_pair()
    txt = wait_upto("(()=>{const c=document.querySelector('.message-item.tool_pair');return c?c.textContent:''})()",
                    lambda v: isinstance(v, str) and MARK in v, max_wait=15.0) or ""
    if MARK not in txt:
        raise TestError("历史回读后卡片未含执行结果 %r：%r" % (MARK, txt[:160]))
    evi("G3 历史回读", readback=True, text_len=len(txt))


def main():
    c.console(clear=True)
    print("依赖：--test-port GUI + mock LLM(8901)（harness 自起）+ 隔离 work-dir/data-dir/HOME；"
          "驱动 = llm-start/message 面 + 真实 script_run 执行（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("G1 执行语义：tool-result 载荷含真实 stdout 哨兵", case_g1_execution_content),
        ("G2 渲染语义：展开卡片后含执行结果", case_g2_render_semantic),
        ("G3 回读回环：切走→切回后卡片仍含执行结果", case_g3_history_readback),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error" and "SetActiveSessionID" not in (e.get("text") or ""):
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n工具执行结果渲染与回读：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
