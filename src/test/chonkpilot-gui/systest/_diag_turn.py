# -*- coding: utf-8 -*-
"""诊断：server-starting + turn-start（主/子）事件。"""
import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


import harness as _h  # 按需加载 + 结束即回收
_G = _h.acquire_gui(2345)
c = _G.client


def main():
    # server-starting 在启动时已广播（此脚本后来才捕获不到，查事件历史）：
    evs = _loads_deep(c.eval("(() => { window.__ev = []; window.mq.on('server-starting', d => window.__ev.push('server-starting:' + JSON.stringify(d))); window.mq.on('turn-start', d => window.__ev.push('turn-start:' + JSON.stringify(d))); return 'ok'; })()"))
    time.sleep(1)
    c.eval('window.go.app.App.CreateSession({ workDir: "", title: "" }).then(s => { window.__sid = s; window.mq.emit("llm-start", { session_id: s && (s.session_id || s.id), q: "please call llm for sub task", llm: "mock", think: "", effort: "", scenario_id: 0 }); }).catch(e => { window.__sid = "ERR:" + String(e); });')
    time.sleep(14)
    raw = c.eval("JSON.stringify(window.__ev)")
    for line in (_loads_deep(raw) or []):
        print(line[:160])
    return 0


if __name__ == "__main__":
    sys.exit(main())
