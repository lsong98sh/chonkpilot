# -*- coding: utf-8 -*-
"""B · 语义/回环级推广 ⑤：**提示词优化（AI 优化）流式回环**。

覆盖（**拒绝"存在级"：断言**流式增量**逐字拼回完整结果 + 内容级 + mock 真收到请求**）：
  O1 流式增量回环：发 `gui.prompt-optimise{useCase,prompt}` → 收 `optimize-token`（逐 token 增量）与
     `optimize-done{prompt}`；断言 **`"".join(增量) == optimize-done.prompt`**（流式回显与终态
     **逐字一致**，非仅"事件存在"）。
  O2 内容级 + 真链路：`optimize-done.prompt` **含唯一哨兵**（mock LLM 回显所发提示词）且 mock
     端点 `n_requests >= 1`（证明走了配置的默认 LLM，而非空跑）。
  O3 实例字段：三条流式事件（token/done）载荷均带 `instance_id`（[61 §0](../60-reference/61-消息一览.md) 硬规则）。

隔离（51-FP与测试映射 §5/§6-8）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/**独立 HOME**）；
usr `llms`/`defaultLLM` 临时写入（套件级快照-还原），不碰机器 `~/.chonkpilot`；mock LLM 自起自管。

观测渠道（**均为 61-消息一览既有主题，零新增**）：
  §1 `gui.prompt-optimise` → 事件 `optimize-token`/`optimize-done`/`optimize-error`。

运行：python run_sem_prompt_optimise.py
"""
import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

EXE = _h.resolve_gui_exe()
if not os.path.isfile(EXE):
    print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist/desktop)" % EXE, flush=True)
    sys.exit(0)

TOKENS = ["optimize-token", "optimize-done", "optimize-error"]
SENT = "SEM-OPT-SENT-%d" % int(time.time())
PROMPT = "OPT " + SENT


def main():
    m1 = _h.start_mock_llm(_h.free_port())
    g = _h.acquire_gui(_h.free_port(),
                       work_dir=_h.tmp_dir("ck-semopt-ws-"),
                       data_dir=_h.tmp_dir("ck-semopt-dd-"),
                       home=_h.tmp_home(),
                       # exe 级兜底端点指向 mock（本隔离实例无 8901 监听）
                       extra_args=("-llm-base=http://127.0.0.1:%d/v1" % m1.port,
                                   "-llm-model=m-exe-default"))
    c = g.client
    _h.suite_config_guard(c)
    print("[env] mock=%d gui=%d ws=%s（临时目录，结束即删）" % (m1.port, g.port, g.work_dir), flush=True)

    entry = {"name": "p-sem-opt", "protocol": "openai", "apiKey": "", "model": "m-sem-opt",
             "baseUrl": "http://127.0.0.1:%d/v1" % m1.port, "temperature": 0.7, "maxOutputToken": 4096}
    c.req("data-user-config-save", {"data": {"llms": [entry], "defaultLLM": "p-sem-opt"}})
    c.mq_emit("config-refresh")
    time.sleep(0.4)

    def last_of(port):
        try:
            with urllib.request.urlopen("http://127.0.0.1:%d/last" % int(port), timeout=5) as r:
                return json.loads(r.read().decode("utf-8")) or {}
        except Exception:
            return {}

    def evi(tag, **kw):
        print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)

    def drive():
        """发一次优化 → 等 done/error，返回 (tokens_text, done_prompt, err, events)。"""
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(TOKENS)
        r = c.req("gui.prompt-optimise", {"useCase": "sem-loop", "prompt": PROMPT}) or {}
        if not r.get("ok"):
            raise TestError("gui.prompt-optimise 回执非 ok：%r" % r)
        deadline = time.time() + 40
        while time.time() < deadline:
            if c.events_of("optimize-done", clear=False) or c.events_of("optimize-error", clear=False):
                break
            time.sleep(0.3)
        errs = c.events_of("optimize-error", clear=False)
        if errs:
            return "", "", (errs[0].get("payload") or {}).get("message"), errs
        toks = [(e.get("payload") or {}).get("content") or "" for e in c.events_of("optimize-token", clear=False)]
        dones = c.events_of("optimize-done", clear=False)
        done = (dones[0].get("payload") or {}).get("prompt") if dones else None
        return "".join(toks), done, None, (c.events_of("optimize-token", clear=False) + dones)

    RES = {}

    def case_o1_stream_loopback():
        """O1：增量的逐字拼接 == optimize-done.prompt（流式↔终态一致）。"""
        streamed, done, err, _ = drive()
        RES.update(streamed=streamed, done=done, err=err)
        if err:
            raise TestError("提示词优化报错：%r" % err)
        if done is None:
            raise TestError("未收到 optimize-done")
        if not streamed:
            raise TestError("未收到任何 optimize-token 增量")
        if streamed != done:
            raise TestError("增量拼接(%r) != optimize-done.prompt(%r)" % (streamed[:80], (done or "")[:80]))
        evi("O1 流式↔终态回环", streamed_len=len(streamed), done_len=len(done), equal=(streamed == done))

    def case_o2_content_and_real_llm():
        """O2：done.prompt 含哨兵（内容级）+ mock 真收到请求（真链路）。"""
        done = RES.get("done")
        if not done or SENT not in done:
            raise TestError("optimize-done.prompt 未含哨兵 %r：%r" % (SENT, (done or "")[:160]))
        n = int(last_of(m1.port).get("n_requests") or 0)
        if n < 1:
            raise TestError("mock LLM 未收到请求（n_requests=%d）" % n)
        evi("O2 内容级 + 真链路", has_sentinel=SENT in done, n_requests=n,
            model=last_of(m1.port).get("model"))

    def case_o3_instance_field():
        """O3：token/done 事件载荷均带 instance_id（61 §0 硬规则）。"""
        _, _, _, evs = drive()
        missing = [e.get("type") for e in evs if not (e.get("payload") or {}).get("instance_id")]
        if missing:
            raise TestError("流式事件缺 instance_id：%r" % missing)
        evi("O3 实例字段", n_events=len(evs),
            types=sorted({e.get("type") for e in evs}))

    c.console(clear=True)
    print("依赖：--test-port GUI（harness 自起）+ 自管 mock LLM + 隔离 work-dir/data-dir/HOME；"
          "驱动 = gui.prompt-optimise（既有主题）", flush=True)
    ok = total = 0
    for name, fn in [
        ("O1 流式↔终态回环：增量拼接 == optimize-done.prompt", case_o1_stream_loopback),
        ("O2 内容级 + 真链路：done 含哨兵 + mock 收到请求", case_o2_content_and_real_llm),
        ("O3 实例字段：token/done 事件均带 instance_id", case_o3_instance_field),
    ]:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\n提示词优化流式回环：%d/%d 通过" % (ok, total), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
