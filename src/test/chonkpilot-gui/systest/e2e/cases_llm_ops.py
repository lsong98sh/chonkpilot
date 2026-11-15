# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉒：**LLM 可见重试与重试耗尽**（迁移自 run_llm_errors RETRY-1 / ERRUX-1）。

使用本地 mock LLM（8901）的两条确定性剧本：
* `please call retry-twice`  → 前两次 429（Retry-After: 2）→ 第三次成功；
* `please call retry-exhaust`→ 每次 500 → 重试耗尽（retryCount 次）。

断言：中间态 `llm-retry` 载荷（retry_attempt/retry_count/wait_ms）+ 页面提示条文案与逐秒倒计时；
成功路径无 `.error-bubble` / 无残留提示条；耗尽终态只在下方提示条（含「已重试 n/N」+ 原始详情）。
"""

import json
import time
import urllib.request

from .framework import case
from .cases_chat_ops import _open_session, _send, _select


def _reset_mock(env):
    try:
        urllib.request.urlopen("http://127.0.0.1:8901/reset", timeout=5).read()
    except Exception:
        pass


def _set_retry(env, n):
    env.req("data-user-config-save", {"data": {"retryCount": n}})
    env.js("window.mq.emit('config-refresh'); 'ok'")
    time.sleep(0.6)
    _select(env, "mock")


def _install_retry_recorder(env):
    env.js("""(()=>{window.__retryTexts=[];
      if(window.__retryObs)window.__retryObs.disconnect();
      const pick=()=>{const e=document.querySelector('.llm-retry-hint');
        const t=e?(e.textContent||'').trim():'';
        if(t&&window.__retryTexts.indexOf(t)<0)window.__retryTexts.push(t);};
      window.__retryObs=new MutationObserver(pick);
      window.__retryObs.observe(document.body,{subtree:true,childList:true,characterData:true});
      pick();return 'ok';})()""")


def _retry_texts(env):
    return env.jsj("JSON.stringify(window.__retryTexts||[])") or []


@case("S-LLM-RETRY-01", "会话消息", "可见重试：中间态 n/N + 等待倒计时 + 成功；耗尽终态只在下提示条",
      stage="seeded", order=310, fp="33 LLM 错误处理 RETRY-1/ERRUX-1（run_llm_errors）",
      desc="retryCount=3 + mock「retry-twice」：2 条 llm-retry（1/3、2/3，wait_ms>0）、提示条含"
           "「重试 1/3」与 2→1 秒倒计时、终态 complete 且无错误气泡/提示条；retryCount=2 + "
           "「retry-exhaust」：终态 error/retryable/2/2/wait=0，提示条含「已重试 2/2 次」，"
           "展开详情含原始错误，聊天流无错误气泡。")
def s_llm_retry_01(env):
    orig = env.usr().get("retryCount")
    sid = "e2e-llmretry-%s" % env.run_id
    try:
        _reset_mock(env)
        _set_retry(env, 3)
        _open_session(env, sid)

        env.capture(["llm-retry", "llm-complete"])
        _install_retry_recorder(env)
        _send(env, sid, "please call retry-twice", llm="mock")
        done = env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                               "retry-twice 成功终态", timeout=90)
        retries = env.events("llm-retry")
        rp = [e.get("payload") or {} for e in retries]
        texts = _retry_texts(env)
        bubbles = int(env.js("document.querySelectorAll('.message-item.assistant "
                             ".error-bubble').length") or 0)
        bars = int(env.js("document.querySelectorAll('.llm-fail-hint.llm-error-hint')"
                          ".length") or 0)
        env.check("中间态 llm-retry ≥2 条", len(rp) >= 2, len(rp))
        if rp:
            env.check("首条中间态计数 1/3", rp[0].get("retry_attempt") == 1
                      and rp[0].get("retry_count") == 3, rp[0])
            env.check("中间态 retry_wait_ms > 0", int(rp[0].get("retry_wait_ms") or 0) > 0,
                      rp[0].get("retry_wait_ms"))
        env.check("重试提示条可见且含「重试 1/3」+ 等待 2 秒",
                  any("重试" in t and "1/3" in t and "2" in t for t in texts), texts)
        env.check("提示条逐秒倒计时（2 秒 → 1 秒）",
                  any(("等待 2 秒" in t or "waiting 2s" in t) for t in texts)
                  and any(("等待 1 秒" in t or "waiting 1s" in t) for t in texts), texts)
        env.check("成功路径无错误气泡/提示条", bubbles == 0 and bars == 0,
                  {"bubbles": bubbles, "bars": bars})
        env.check("成功终态 retry 提示条已消失", int(env.js(
            "document.querySelectorAll('.llm-retry-hint').length") or 0) == 0)

        # 耗尽分支
        _reset_mock(env)
        _set_retry(env, 2)
        sid2 = sid + "-exhaust"
        _open_session(env, sid2)
        env.capture(["llm-retry", "llm-complete"])
        _send(env, sid2, "please call retry-exhaust", llm="mock")
        fail = env.wait_events("llm-complete", lambda p: (p or {}).get("status") == "error",
                               "retry-exhaust 错误终态", timeout=90)
        payload = fail or {}
        env.check("耗尽终态 status=error 且 retryable=true",
                  payload.get("status") == "error" and payload.get("retryable") is True, payload)
        env.check("耗尽计数 2/2 且 wait_ms=0",
                  payload.get("retry_attempt") == 2 and payload.get("retry_count") == 2
                  and payload.get("retry_wait_ms") == 0, payload)
        bar = str(env.js("document.querySelector('.llm-fail-hint.llm-error-hint')"
                         "?.textContent || ''") or "")
        env.check("终态提示条含「已重试 2/2 次」", "已重试 2/2 次" in bar, bar[:120])
        env.check("聊天流内无错误气泡", int(env.js(
            "document.querySelectorAll('.message-item.assistant .error-bubble').length")
            or 0) == 0)
        env.js("document.querySelector('.llm-fail-detail-toggle')?.click(); 'ok'")
        time.sleep(0.4)
        raw = str(env.js("document.querySelector('.llm-fail-raw')?.textContent || ''") or "")
        env.check("「查看错误信息」展开原始错误",
                  "retry-exhaust" in raw or "mock" in raw, raw[:120])
    finally:
        if orig is not None:
            env.req("data-user-config-save", {"data": {"retryCount": orig}})
        env.js("window.mq.emit('config-refresh'); 'ok'")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and all(k not in (e.get("text") or "")
                    for k in ("method not implemented", "SetActiveSessionID"))]
    env.check("重试组无前端错误", len(errs) == 0, errs[:3])

def _get_last(env, model=None, timeout=30):
    end = time.time() + timeout
    last = {}
    while time.time() < end:
        try:
            with urllib.request.urlopen("http://127.0.0.1:8901/last", timeout=5) as r:
                last = json.loads(r.read().decode("utf-8", "replace"))
        except Exception:
            last = {}
        if not model or last.get("model") == model:
            return last
        time.sleep(0.3)
    return last


@case("S-LLM-CONFIG-01", "配置", "所选 LLM 请求配置一致 + 外部修改热重读（无需重启）",
      stage="seeded", order=314, fp="64 配置项 · llms（run_llm_config 三.2a/2b）",
      desc="usr 写入唯一 provider（model/temperature/maxOutputToken 哨兵）→ 选中并发送 → mock /last"
           " 回显 model/temperature/max_tokens 一致；外部改写 temperature/model + config-refresh → "
           "再发送 → /last 直接读到新值（热重读）。结束还原 llms 并选回 mock。")
def s_llm_config_01(env):
    rid = env.run_id
    orig_llms = env.usr().get("llms")
    orig_default = env.usr().get("defaultLLM")
    name = "e2e-cfg-%s" % rid

    def _entry(model, temp):
        return {"name": name, "protocol": "openai", "apiKey": "sk-e2e",
                "model": model, "baseUrl": "http://127.0.0.1:8901/v1",
                "temperature": temp, "maxOutputToken": 1024,
                "maxContextToken": 128000, "thinking": False,
                "reasoningEffort": "", "maxToolIterations": 20}

    try:
        model1 = "cfg-model-1-%s" % rid
        env.req("data-user-config-save", {"data": {"llms": [_entry(model1, 0.3)],
                                                   "defaultLLM": name}})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        time.sleep(0.6)
        _select(env, name)
        sid = "e2e-llmcfg-%s" % rid
        _open_session(env, sid)
        env.capture(["llm-complete"])
        _send(env, sid, "llm config probe one", llm=name)
        env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                        "首轮完成", timeout=90)
        last1 = _get_last(env, model1)
        env.check("请求 model 一致", last1.get("model") == model1, last1.get("model"))
        env.check("请求 temperature 一致", abs(float(last1.get("temperature") or -1) - 0.3) < 1e-6,
                  last1.get("temperature"))
        env.check("请求 max_tokens 一致", int(last1.get("max_tokens") or 0) == 1024,
                  last1.get("max_tokens"))

        model2 = "cfg-model-2-%s" % rid
        env.req("data-user-config-save", {"data": {"llms": [_entry(model2, 0.9)]}})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        time.sleep(0.8)
        _select(env, name)
        env.capture(["llm-complete"])
        _send(env, sid, "llm config probe two", llm=name)
        env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                        "热重读轮完成", timeout=90)
        last2 = _get_last(env, model2)
        env.check("外部修改后热重读 model", last2.get("model") == model2, last2.get("model"))
        env.check("外部修改后热重读 temperature",
                  abs(float(last2.get("temperature") or -1) - 0.9) < 1e-6, last2.get("temperature"))
        try:
            env.req("data-session-delete", {"id": sid})
        except Exception:
            pass
    finally:
        if orig_llms is not None:
            restore = {"llms": orig_llms}
            if orig_default:
                restore["defaultLLM"] = orig_default
            env.req("data-user-config-save", {"data": restore})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        _select(env, "mock")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("LLM 配置组无前端错误", len(errs) == 0, errs[:3])

@case("S-INBOX-01", "会话消息", "工具执行中插话：llm-inbox-add → insert 分类 → 插话先出流 → 主轮后续终态",
      stage="seeded", order=312, fp="中途插话设计 §3.1 S1（run_inbox INBOX-S1）",
      desc="mock「please call slow-insert」强制异步 task_run → 工具 pending 期 req `llm-inbox-add"
           "{insert:true}` → 返回 type=insert；llm-receive 回显插话文本且主轮尚未 complete；"
           "工具回执后主轮最终 complete。")
def s_inbox_01(env):
    _select(env, "mock")
    sid = "e2e-inbox-%s" % env.run_id
    _open_session(env, sid)
    turn = "t-" + sid
    env.capture(["llm-tool-call", "llm-receive", "llm-complete"])
    env.js("window.mq.emit('llm-start', %s); 'ok'" % json.dumps({
        "session_id": sid, "turn": turn, "q": "please call slow-insert",
        "llm": "mock", "think": "", "effort": "", "scenario_id": ""}))
    env.wait_events("llm-tool-call", None, "工具调用已派发", timeout=40)
    env.check("工具派发后主轮尚未终态",
              not any((e.get("payload") or {}).get("turn") == turn
                      for e in env.events("llm-complete")))

    res = env.req("llm-inbox-add", {"session": sid, "llm": "mock",
                                    "content": "插话e2e", "insert": True}) or {}
    env.eq("工具执行期插话分类 = insert", str(res.get("type")), "insert")

    def _echo():
        for e in env.events("llm-receive"):
            p = e.get("payload") or {}
            if p.get("type") != "text":
                continue
            inner = p.get("payload") or {}
            txt = inner.get("text") if isinstance(inner, dict) else ""
            if txt and "插话e2e" in str(txt):
                return str(txt)
        return None

    echo = env.wait(_echo, "插话回显 llm-receive", timeout=40)
    env.check("插话文本先出流", "插话e2e" in echo, echo[:60])
    env.check("插话处理时主轮仍未终态",
              not any((e.get("payload") or {}).get("turn") == turn
                      for e in env.events("llm-complete")))
    done = env.wait_events("llm-complete", lambda p: (p or {}).get("turn") == turn,
                           "主轮最终 complete", timeout=120)
    env.check("工具回执后主轮终态", bool(done), (done or {}).get("status"))
    try:
        env.req("data-session-delete", {"id": sid})
    except Exception:
        pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("插话组无前端错误", len(errs) == 0, errs[:3])

@case("S-LLM-FIELDS-01", "配置", "LLM 字段：protocol 端点/max_output_tokens / thinking+reasoningEffort / apiKey / 落库回读",
      stage="seeded", order=315, fp="64 配置项 §5 llms（run_llm_fields）",
      desc="protocol=responses → mock /last path=/responses、max_output_tokens=777、max_tokens 为空；"
           "改 openai → /chat/completions 且 max_tokens=777；thinking=false 不发 reasoning_effort，"
           "thinking=true 发配置值 high；Authorization 携带 apiKey；A 面逐字段回读。")
def s_llm_fields_01(env):
    rid = env.run_id
    name = "e2e-fields-%s" % rid
    orig_llms = env.usr().get("llms")
    orig_default = env.usr().get("defaultLLM")

    def _entry(**kw):
        e = {"name": name, "protocol": "responses", "apiKey": "sk-fields-%s" % rid,
             "model": "fields-model-%s" % rid,
             "baseUrl": "http://127.0.0.1:8901/v1", "temperature": 0.4,
             "maxOutputToken": 777, "maxContextToken": 32000,
             "thinking": False, "reasoningEffort": "high", "maxToolIterations": 9}
        e.update(kw)
        return e

    def _save(entry):
        env.req("data-user-config-save", {"data": {"llms": [entry], "defaultLLM": name}})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        time.sleep(0.6)
        _select(env, name)

    def _send_wait(text):
        env.capture(["llm-complete"])
        _send(env, sid, text, llm=name)
        env.wait_events("llm-complete", lambda p: (p or {}).get("status") in ("complete", "completed"),
                        "回合完成：%s" % text, timeout=90)

    try:
        _save(_entry())
        back = (env.req("data-user-config-load", {}) or {}).get("data") or {}
        got = next((x for x in (back.get("llms") or []) if x.get("name") == name), {})
        for k in ("protocol", "apiKey", "maxOutputToken", "maxContextToken",
                  "thinking", "reasoningEffort", "maxToolIterations"):
            env.eq("A 回读 %s" % k, got.get(k), _entry().get(k))

        sid = "e2e-fields-%s" % rid
        _open_session(env, sid)
        _send_wait("fields probe responses")
        last = _get_last(env, _entry()["model"])
        env.check("responses → /responses 端点",
                  str(last.get("path") or "").endswith("/responses"), last.get("path"))
        env.check("responses 用 max_output_tokens=777 且不发 max_tokens",
                  last.get("max_output_tokens") == 777 and last.get("max_tokens") is None, last)
        env.check("Authorization 携带 apiKey",
                  "sk-fields-%s" % rid in str(last.get("authorization") or ""),
                  last.get("authorization"))
        env.check("thinking=false → 不发 reasoning_effort",
                  last.get("reasoning_effort") is None, last.get("reasoning_effort"))

        _save(_entry(protocol="openai"))
        _send_wait("fields probe openai")
        last2 = _get_last(env, _entry()["model"])
        env.check("openai → /chat/completions 端点",
                  str(last2.get("path") or "").endswith("/chat/completions"), last2.get("path"))
        env.check("openai 用 max_tokens=777", last2.get("max_tokens") == 777, last2.get("max_tokens"))

        _save(_entry(protocol="openai", thinking=True))
        _send_wait("fields probe thinking")
        last3 = _get_last(env, _entry()["model"])
        env.check("thinking=true → reasoning_effort=high",
                  last3.get("reasoning_effort") == "high", last3.get("reasoning_effort"))
        try:
            env.req("data-session-delete", {"id": sid})
        except Exception:
            pass
    finally:
        if orig_llms is not None:
            restore = {"llms": orig_llms}
            if orig_default:
                restore["defaultLLM"] = orig_default
            env.req("data-user-config-save", {"data": restore})
        env.js("window.mq.emit('config-refresh'); 'ok'")
        _select(env, "mock")

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error" and "method not implemented" not in (e.get("text") or "")]
    env.check("LLM 字段组无前端错误", len(errs) == 0, errs[:3])

