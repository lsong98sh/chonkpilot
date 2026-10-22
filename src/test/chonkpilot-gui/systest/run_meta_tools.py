# -*- coding: utf-8 -*-
"""L4 套件：gateway 元工具**进程外**验收（BRIDGE-2 T7 ①）。

背景：`mcp_find` / `mcp_load` / `mcp_invoke` 原先由 gateway 进程内注册；现为一工具一 exe
（`chonkpilot-meta-*-executor.exe`，契约 `core-only: true`），由 core-mcp-server 注册并经
`ExecTool.Call` 执行；gateway 在 `CHONKPILOT_EXTERNAL_TOOLS=1`（桌面单体默认）时不再自注册。

覆盖（mq 驱动 + mq 断言，50-测试体系 §1）：
  M1 工具面：tools-list 含 `self_mcp_find` / `self_mcp_load` / `self_mcp_invoke`（外部注册可见）
  M2 `mcp_find(query=script)` → 命中 `self_script_run`
  M3 `mcp_load(name=self_script_run, kind=tool)` → 返回描述 + inputSchema
  M4 `mcp_invoke(name=self_script_run, echo META-INVOKE-OK)` → 结果含哨兵（真实执行）

隔离：自起 GUI（临时 work-dir/data-dir/独立 HOME）+ 自管 mock LLM；结束即回收。
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

TS = str(int(time.time()))[-6:]
PROVIDER = "p-meta-tools-" + TS

_g = None


def main():
    global _g
    m1 = _h.start_mock_llm(_h.free_port())
    _g = _h.acquire_gui(_h.free_port(),
                        work_dir=_h.tmp_dir("ck-meta-ws-"),
                        data_dir=_h.tmp_dir("ck-meta-dd-"),
                        home=_h.tmp_home(),
                        extra_args=("-llm-base=http://127.0.0.1:%d/v1" % m1.port,
                                    "-llm-model=m-exe-default"))
    c = _g.client
    _h.suite_config_guard(c)
    print("[env] mock=%d gui=%d ws=%s" % (m1.port, _g.port, _g.work_dir), flush=True)

    entry = {"name": PROVIDER, "protocol": "openai", "apiKey": "", "model": "m-meta",
             "baseUrl": "http://127.0.0.1:%d/v1" % m1.port, "temperature": 0.7, "maxOutputToken": 4096}
    c.req("data-user-config-save", {"data": {"llms": [entry], "defaultLLM": PROVIDER}})
    c.mq_emit("config-refresh")
    time.sleep(0.4)

    def run_turn(q, tool_suffix):
        sid = "meta-%d" % int(time.time() * 1000)
        turn = "t-" + sid
        c.mq_on_capture(["turn-start", "tool-result", "llm-complete"])
        c.mq_emit("session-changed", {"session_id": sid})
        c.mq_emit("llm-start", {"session_id": sid, "turn": turn, "q": q,
                                "llm": PROVIDER, "think": "", "effort": "", "scenario_id": ""})
        deadline = time.time() + 90
        payload = None
        while time.time() < deadline:
            for e in c.events_of("tool-result", clear=False):
                p = e.get("payload") or {}
                if isinstance(p, str):
                    try:
                        p = json.loads(p)
                    except Exception:
                        p = {}
                if not isinstance(p, dict) or p.get("turn_id") != turn:
                    continue
                if (p.get("tool") or "").endswith(tool_suffix) and p.get("status") == "done":
                    payload = p
            if payload is not None:
                break
            time.sleep(0.3)
        if payload is None:
            raise TestError("未收到 %s 的 tool-result（turn=%s）" % (tool_suffix, turn))
        # 等轮次终态，避免下一轮与该轮排队
        deadline = time.time() + 60
        while time.time() < deadline:
            done = [e for e in c.events_of("llm-complete", clear=False)
                    if turn in json.dumps(e.get("payload") or {}, ensure_ascii=False)]
            if done:
                break
            time.sleep(0.3)
        return payload.get("result") or ""

    def case_m1_surface():
        res = (c.req("tools-list", {}) or {})
        if isinstance(res, str):
            res = json.loads(res)
        tools = res.get("tools") or []
        names = {((t or {}).get("name") or "") for t in tools}
        for want in ("self_mcp_find", "self_mcp_load", "self_mcp_invoke"):
            if want not in names:
                raise TestError("工具面缺 %s（外部元工具注册未生效）：%s" % (want, sorted(names)))
        return "n=%d" % len(names)

    def case_m2_find():
        txt = run_turn("call meta-find", "mcp_find")
        if "self_script_run" not in txt:
            raise TestError("mcp_find 未命中 self_script_run：%r" % txt[:400])
        return txt[:160]

    def case_m3_load():
        txt = run_turn("call meta-load", "mcp_load")
        if "script_run" not in txt or "inputSchema" not in txt:
            raise TestError("mcp_load 返回缺描述/schema：%r" % txt[:400])
        return txt[:160]

    def case_m4_invoke():
        txt = run_turn("call meta-invoke", "mcp_invoke")
        if "META-INVOKE-OK" not in txt:
            raise TestError("mcp_invoke 未真实执行目标工具：%r" % txt[:400])
        return txt[:160]

    total = 0
    ok = 0
    cases = [
        ("M1 工具面：三个元工具经外部注册可见", case_m1_surface),
        ("M2 mcp_find → 命中 self_script_run", case_m2_find),
        ("M3 mcp_load → 描述 + inputSchema", case_m3_load),
        ("M4 mcp_invoke → 目标工具真实执行", case_m4_invoke),
    ]
    for name, fn in cases:
        total += 1
        ok += run_case(name, fn)
    print("\n元工具进程外验收：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
