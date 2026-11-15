# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉞：**任务状态语义 + 提示词优化流式回环**
（迁移自 run_sem_task_status_loop T1–T4、run_sem_prompt_optimise O1–O3）。

* `tasks.started(running)` → 节点 `.status-icon[title]` ==「执行中」+ `.node-stop` 存在；
* `tasks.updated(done/error)` → 「已完成」/「执行失败」，停止入口消失；
* `task-close` → 节点保留 + `.node-closed-tag` ==「已关闭」（逻辑删除只读保留）；
* `gui.prompt-optimise` → `optimize-token` 增量逐字拼接 == `optimize-done.prompt`，含唯一哨兵，
  mock `/last.n_requests >= 1`，token/done 载荷均带 `instance_id`。
"""

import json
import time
import urllib.request

import harness as _h
from .framework import case


def _remote(env, typ, payload):
    env.js("window.mq.emitRemote({type: %s, payload: %s}); 'ok'"
           % (json.dumps(typ), json.dumps(json.dumps(payload))))


def _icon(env, nid):
    return str(env.js("(()=>{const i=document.querySelector('[data-node-id=%s] .status-icon');"
                      "return i?i.getAttribute('title'):''})()" % json.dumps(nid)) or "")


def _stops(env, nid):
    return int(env.js("document.querySelectorAll('[data-node-id=%s] .node-stop').length"
                      % json.dumps(nid)) or 0)


@case("S-TASK-STATUS-01", "任务树", "任务节点状态文案迁移（执行中/已完成/执行失败/已关闭只读）",
      stage="seeded", order=362, fp="34 任务 · run_sem_task_status_loop T1–T4",
      desc="tasks.started running → 图标 title「执行中」+ 停止入口；updated done →「已完成」且停止"
           "入口消失；updated error →「执行失败」；task-close → 节点仍在 + `.node-closed-tag`"
           "「已关闭」（逻辑删除、只读保留）。")
def s_task_status_01(env):
    env.wait(lambda: True if _h.ensure_task_panel_open(env.client, timeout=30) else None,
             "任务面板展开", timeout=35)
    top = "e2e-taskstat-%s" % env.run_id
    nid = "task-e2e-%s" % env.run_id
    env.emit("session-changed", {"session_id": top})
    time.sleep(0.5)
    _remote(env, "tasks.started", {
        "task_id": nid, "kind": "tool", "tool_name": "command_execute",
        "name": "echo status", "purpose": "echo status", "state": "running",
        "session_id": top, "top_session": top,
        "started_at": "2026-09-01T00:00:00Z"})
    env.wait(lambda: True if int(env.js(
        "document.querySelectorAll('[data-node-id=%s]').length" % json.dumps(nid)) or 0) >= 1
        else None, "任务节点渲染", timeout=20)
    run = env.wait(lambda: (lambda t: t if t else None)(_icon(env, nid)),
                   "running 图标文案", timeout=15)
    env.check("T1 running →「执行中」+ 停止入口",
              run == "执行中" and _stops(env, nid) >= 1, {"icon": run, "stop": _stops(env, nid)})

    _remote(env, "tasks.updated", {"task_id": nid, "kind": "tool", "state": "done",
                                   "session_id": top, "top_session": top})
    done = env.wait(lambda: (lambda t: t if t == "已完成" else None)(_icon(env, nid)),
                    "done 图标文案", timeout=15)
    env.check("T2 done →「已完成」+ 停止入口消失",
              done == "已完成" and _stops(env, nid) == 0,
              {"icon": done, "stop": _stops(env, nid)})

    _remote(env, "tasks.updated", {"task_id": nid, "kind": "tool", "state": "error",
                                   "session_id": top, "top_session": top})
    err = env.wait(lambda: (lambda t: t if t == "执行失败" else None)(_icon(env, nid)),
                   "error 图标文案", timeout=15)
    env.check("T3 error →「执行失败」", err == "执行失败", err)

    env.emit("task-close", {"node_id": nid})
    tag = env.wait(lambda: str(env.js(
        "(()=>{const e=document.querySelector('[data-node-id=%s] .node-closed-tag');"
        "return e?e.textContent.trim():''})()" % json.dumps(nid)) or "") or None,
        "关闭标记", timeout=20)
    still = int(env.js("document.querySelectorAll('[data-node-id=%s]').length"
                       % json.dumps(nid)) or 0) >= 1
    env.check("T4 task-close → 节点保留 +「已关闭」只读",
              still and tag == "已关闭", {"present": still, "tag": tag})


@case("S-PROMPT-OPT-01", "配置", "提示词优化：token 增量拼接==终态、哨兵内容、mock 真调用、instance_id",
      stage="seeded", order=398, fp="gui.prompt-optimise · run_sem_prompt_optimise O1–O3",
      desc="req gui.prompt-optimise{useCase,prompt} → optimize-token 增量 join == "
           "optimize-done.prompt；结果含唯一哨兵；mock /last.n_requests ≥1；token/done 载荷均带 "
           "instance_id。")
def s_prompt_opt_01(env):
    sent = "E2E-OPT-%s" % env.run_id
    prompt = "请优化这条提示词：" + sent
    # 前序配置测试可能改走 usr llms/defaultLLM/llm.promptOptimise → 明确把提示词优化子系统
    # 指向本机 mock（activeLLM 读 llm.promptOptimise；started:false = 未解析到 provider）。
    cfg = (env.req("data-user-config-load", {}) or {}).get("data") or {}
    base = {k: cfg.get(k) for k in ("llms", "defaultLLM", "llm.promptOptimise")}
    llms = cfg.get("llms") or []
    if isinstance(llms, str):
        try:
            llms = json.loads(llms)
        except Exception:
            llms = []
    llms = [e for e in llms if isinstance(e, dict)]
    mock_entry = next((e for e in llms if e.get("name") == "mock"), None)
    if mock_entry is None:
        mock_entry = {"name": "mock", "protocol": "openai", "model": "mock-model",
                      "baseUrl": "http://127.0.0.1:%d/v1" % env.mock_port,
                      "apiKey": "sk-test-key", "temperature": 0.7, "maxOutputToken": 4096}
        llms.append(mock_entry)
    idx = llms.index(mock_entry)
    env.req("data-user-config-save", {"data": {"llms": llms, "defaultLLM": idx,
                                               "llm.promptOptimise": "mock"}})
    env.js("window.mq.emit('config-refresh'); window.mq.emit('chat-select-llm', {name: 'mock'}); 'ok'")
    time.sleep(0.8)
    env.capture(["optimize-token", "optimize-done", "optimize-error"])
    r = {}
    for _ in range(6):
        r = env.req("gui.prompt-optimise", {"useCase": "e2e-sem-loop", "prompt": prompt}) or {}
        if r.get("ok") is not False and r.get("started") is not False:
            break
        time.sleep(3.0)   # 上一次优化可能仍在飞；等其结束再发（接口 started:false 表示已在跑）
    env.check("gui.prompt-optimise 回执 ok/started", r.get("ok") is not False, r)
    def _done():
        return (lambda d: d if d else None)(
            str(((env.events("optimize-done") or [{}])[0].get("payload") or {})
                .get("prompt") or ""))
    try:
        done = env.wait(_done, "optimize-done", timeout=60)
    except Exception:
        # 批跑负载下首次可能被排队/丢弃；重发一次（幂等只读优化，不写盘）
        env.req("gui.prompt-optimise", {"useCase": "e2e-sem-loop", "prompt": prompt})
        done = env.wait(_done, "optimize-done（重发）", timeout=90)
    errs = env.events("optimize-error")
    env.check("无 optimize-error", not errs, errs[:2])
    toks = [str((e.get("payload") or {}).get("content") or "")
            for e in env.events("optimize-token")]
    streamed = "".join(toks)
    env.check("O1 token 增量非空", bool(toks) and bool(streamed), len(toks))
    env.check("O1 增量拼接 == optimize-done.prompt", streamed == done,
              {"stream": streamed[:80], "done": done[:80]})
    env.check("O2 optimize-done 含哨兵", sent in done, done[:120])
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/last" % env.mock_port,
                                    timeout=5) as f:
            last = json.loads(f.read().decode("utf-8") or "{}")
    except Exception as e:
        last = {"error": str(e)}
    env.check("O2 mock LLM 真收到请求（n_requests ≥1）",
              int(last.get("n_requests") or 0) >= 1, last.get("n_requests"))
    evs = env.events("optimize-token") + env.events("optimize-done")
    missing = [e.get("type") for e in evs if not (e.get("payload") or {}).get("instance_id")]
    env.check("O3 token/done 事件均带 instance_id", not missing, missing)

    # 还原前序 LLM 配置（避免把测试夹具留给后续测试点）
    try:
        env.req("data-user-config-delete", {"id": "llm.promptOptimise"})
        if base.get("llm.promptOptimise") not in (None, ""):
            env.req("data-user-config-save", {"data": {
                "llm.promptOptimise": base["llm.promptOptimise"]}})
        env.js("window.mq.emit('config-refresh'); 'ok'")
    except Exception:
        pass