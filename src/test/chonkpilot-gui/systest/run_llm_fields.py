# -*- coding: utf-8 -*-
"""64-配置项一览 §5「usr llms」字段端到端：**A 落库确认 + B 效果确认**。

覆盖键（原 run_llm_config.py 只覆盖 model/temperature）：
  protocol / thinking / reasoningEffort / maxToolIterations / apiKey / baseUrl /
  maxOutputToken / temperature（含 **0 值**）· usr 标量 defaultLLM / defaultScenario。

A（落库确认）= `data-user-config-load` 逐字段回读（写 → 读一致）。
B（效果确认）= **可观测证据**，一律来自真实链路：
  - mock LLM GET /last：path/protocol（区分 /chat/completions 与 /responses）·
    max_tokens / max_output_tokens · reasoning_effort · authorization · system（system 原文）·
    has_tool_result（是否发生工具回喂续轮）· n_requests（本进程累计请求数，用于
    「打到了哪个端点」「第二轮是否发生」）；
  - `llm-complete` 终态（status/code/text）；
  - 前端 DOM（defaultScenario 星标）+ 前端驱动真实发送（`.richtext-input` → chat-send）。

为什么新建而非扩 run_llm_config：run_llm_config 以**复用 2345 实例**为前提，其断言只依赖
mock /last 的 model/temperature；本套件需要三件它给不了的东西——① **隔离实例**（独立
work-dir/data-dir/HOME：写场景（独立根 scenarios/）、写 usr llms 都不碰机器 ~/.chonkpilot）；
② **两个 mock 端点做 baseUrl 对照**（"请求确实打到配置端点"）；③ 前端驱动的真实发送链路。
故按 run_s21_matrix 先例自起隔离实例（动态端口 + tmp 目录，结束由 harness 回收）。

约束：配置写入/还原走 harness 快照-还原（`suite_config_guard` + 逐用例 `user_config_guard`，
见 51 §6-8；`data-user-config-delete` 带 `{"id": key}`）；**零新增 MQ 主题**（仅用 61 既有主题
与前端既有内部事件）。运行：python run_llm_fields.py
"""

import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

# 捕获的既有主题（61-消息一览 §1/§3 + 前端内部事件，均为既有）
EVENTS = ["llm-start", "llm-complete", "llm-error", "turn-start",
          "optimize-done", "optimize-error", "optimize-token"]

SENT = "SENT-LLMFIELDS-%d" % int(time.time())
SC_ID = "llmfields-sc-%d" % int(time.time())
SC_NAME = "LLM字段哨兵场景"
SC_PROMPT = SENT + "-SCENARIO-PROMPT"


def _plain(v):
    """解包 eval 结果的 JSON 字符串（测试通道可能双重编码）。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            p = json.loads(v)
        except Exception:
            return v
        if p == v:
            return v
        v = p
    return v


def _evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def main():
    # 两个 mock 端点：M1 = 主端点（多数用例的 provider baseUrl）；M2 = baseUrl 对照端点
    # （未配置时不得收到任何请求 → 证明"请求打到配置端点"）。均自起（owned）→ 结束回收。
    m1 = _h.start_mock_llm(_h.free_port())
    m2 = _h.start_mock_llm(_h.free_port())
    # 隔离实例：独立 work-dir/data-dir/HOME（usr 主库全新，不碰机器 ~/.chonkpilot）
    g = _h.acquire_gui(_h.free_port(),
                       work_dir=_h.tmp_dir("llmf-ws-"),
                       data_dir=_h.tmp_dir("llmf-dd-"),
                       home=_h.tmp_home(),
                       # exe 级兜底端点显式指向 M1（默认 8901 在隔离实例下无监听）：
                       # 07 用例确权"llm-start 不带 llm 时回落 usr defaultLLM"（→ M2/m-opt-sent），
                       # exe flags 仅为末级兜底（本实例指向 M1/m-exe-default）。
                       extra_args=("-llm-base=http://127.0.0.1:%d/v1" % m1.port,
                                   "-llm-model=m-exe-default"))
    c = g.client
    _h.suite_config_guard(c)  # 套件级快照-还原（51 §6-8）
    print("[setup] mock M1=%d M2=%d gui=%d work_dir=%s" % (m1.port, m2.port, g.port, g.work_dir), flush=True)

    # ── mock / 配置 / 会话 助手 ─────────────────────────────

    def last_of(port):
        """mock 最近一次请求证据（无请求 → {}）。"""
        try:
            with urllib.request.urlopen("http://127.0.0.1:%d/last" % int(port), timeout=5) as r:
                return json.loads(r.read().decode("utf-8")) or {}
        except Exception:
            return {}

    def req_count(port):
        return int(last_of(port).get("n_requests") or 0)

    def reset_mock(port):
        """清 mock 一次性状态 + 请求记录/计数（跨用例确定性）。"""
        try:
            urllib.request.urlopen("http://127.0.0.1:%d/reset" % int(port), timeout=5).read()
        except Exception:
            pass
        time.sleep(0.2)

    def user_cfg():
        res = c.req("data-user-config-load", {})
        return (res.get("data") or {}) if isinstance(res, dict) else {}

    def mk_entry(name, **kw):
        """provider 条目基线（各用例只覆盖关心的字段）。baseUrl 默认指向 M1。"""
        e = {"name": name, "protocol": "openai", "apiKey": "", "model": "m-" + name,
             "baseUrl": "http://127.0.0.1:%d/v1" % m1.port, "temperature": 0.7, "maxOutputToken": 4096}
        e.update(kw)
        return e

    def save_llms(lst, default=None):
        """写 usr llms（可选 defaultLLM）→ 同步前端（config-refresh，既有前端事件）。"""
        data = {"llms": lst}
        if default is not None:
            data["defaultLLM"] = default
        c.req("data-user-config-save", {"data": data})
        c.mq_emit("config-refresh")
        time.sleep(0.4)

    def entry(name):
        """A：回读 usr llms 中该 provider 条目（缺失 → 失败）。"""
        for l in (user_cfg().get("llms") or []):
            if l.get("name") == name:
                return l
        raise TestError("A 回读失败：usr llms 缺 %r 条目" % name)

    _SEQ = [0]

    def new_session():
        _SEQ[0] += 1
        return "llmf-%d-%03d" % (int(time.time() * 1000), _SEQ[0])

    def begin(extra=()):
        """新一轮事件捕获（mq_on_capture 会清空旧事件 + 重注册）。"""
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(list(EVENTS) + list(extra))

    def wait_complete(sid, max_wait=90, n=1):
        """等本会话 llm-complete 至少 n 条，返回 payload 列表。"""
        deadline = time.time() + max_wait
        while time.time() < deadline:
            ours = [e.get("payload") or {} for e in c.events_of("llm-complete", clear=False)
                    if (e.get("payload") or {}).get("session") == sid]
            if len(ours) >= n:
                c.events_of("llm-complete", clear=True)
                return ours
            time.sleep(0.4)
        raise TestError("等待 llm-complete 超时（session=%s）" % sid)

    def wait_turn_start(max_wait=60):
        """等主会话（parents 空）turn-start，返回 session id。"""
        deadline = time.time() + max_wait
        while time.time() < deadline:
            for e in c.events_of("turn-start", clear=False):
                p = e.get("payload") or {}
                if not p.get("parents") and p.get("session"):
                    c.events_of("turn-start", clear=True)
                    return p["session"]
            time.sleep(0.3)
        raise TestError("未收到主会话 turn-start")

    def run_turn(q, llm, scenario=""):
        """mq 直接驱动一轮（llm-start）→ 返回 (llm-complete payload, session)。"""
        begin()
        sid = new_session()
        c.mq_emit("llm-start", {"session_id": sid, "turn": "t-" + sid, "q": q, "llm": llm,
                               "think": "", "effort": "", "scenario_id": scenario})
        wait_turn_start()
        return wait_complete(sid)[0], sid

    # ── 用例 ─────────────────────────────────────────────

    def case_protocol():
        """① protocol：responses → 命中 /responses 且用 max_output_tokens；openai → /chat/completions。"""
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            save_llms([mk_entry("p-resp", protocol="responses", apiKey="k-resp",
                                temperature=0.5, maxOutputToken=777)], default="p-resp")
            back = entry("p-resp")
            if back.get("protocol") != "responses" or back.get("maxOutputToken") != 777:
                raise TestError("A 回读失败: %r" % back)
            reset_mock(m1.port)
            p, _ = run_turn("hello proto responses", "p-resp")
            last = last_of(m1.port)
            _evi("protocol=responses", status=p.get("status"), code=p.get("code"),
                 text=(p.get("text") or "")[:40], path=last.get("path"), protocol=last.get("protocol"),
                 max_output_tokens=last.get("max_output_tokens"), max_tokens=last.get("max_tokens"),
                 n_requests=last.get("n_requests"))
            if p.get("status") != "complete" or "mock-responses-reply" not in (p.get("text") or ""):
                raise TestError("/responses 分支未正常收尾: %r" % p)
            if not str(last.get("path", "")).endswith("/responses") or last.get("protocol") != "responses":
                raise TestError("B 未命中 /responses: %r" % last)
            if last.get("max_output_tokens") != 777 or last.get("max_tokens") is not None:
                raise TestError("responses 应用 max_output_tokens（不得用 max_tokens）: %r" % last)
            # 对照：protocol=openai → /chat/completions（同 provider 只改 protocol）
            save_llms([mk_entry("p-resp", protocol="openai", apiKey="k-resp",
                                temperature=0.5, maxOutputToken=777)], default="p-resp")
            reset_mock(m1.port)
            p2, _ = run_turn("hello proto openai", "p-resp")
            last2 = last_of(m1.port)
            _evi("protocol=openai", status=p2.get("status"), path=last2.get("path"),
                 max_tokens=last2.get("max_tokens"), n_requests=last2.get("n_requests"))
            if not str(last2.get("path", "")).endswith("/chat/completions") or last2.get("max_tokens") != 777:
                raise TestError("B openai 分支应命中 /chat/completions 且带 max_tokens: %r" % last2)

    def case_thinking_effort():
        """② thinking / reasoningEffort：false → 不发 reasoning_effort；true → 发送配置值。"""
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            save_llms([mk_entry("p-think", thinking=False, reasoningEffort="high")], default="p-think")
            back = entry("p-think")
            if back.get("thinking") is not False or back.get("reasoningEffort") != "high":
                raise TestError("A 回读失败: %r" % back)
            reset_mock(m1.port)
            run_turn("hello think off", "p-think")
            last = last_of(m1.port)
            _evi("thinking=false", reasoning_effort=last.get("reasoning_effort"))
            if last.get("reasoning_effort") is not None:
                raise TestError("thinking=false 仍发送 reasoning_effort=%r" % last.get("reasoning_effort"))
            save_llms([mk_entry("p-think", thinking=True, reasoningEffort="high")], default="p-think")
            reset_mock(m1.port)
            run_turn("hello think high", "p-think")
            last = last_of(m1.port)
            _evi("thinking=true/effort=high", reasoning_effort=last.get("reasoning_effort"))
            if last.get("reasoning_effort") != "high":
                raise TestError("thinking=true 应发送 reasoning_effort=high: %r" % last)
            # 值维度：reasoningEffort=low → 请求体 effort=low（证明取值生效而非"仅发送"）
            save_llms([mk_entry("p-think", thinking=True, reasoningEffort="low")], default="p-think")
            if entry("p-think").get("reasoningEffort") != "low":
                raise TestError("A reasoningEffort 回读失败")
            reset_mock(m1.port)
            run_turn("hello think low", "p-think")
            last = last_of(m1.port)
            _evi("thinking=true/effort=low", reasoning_effort=last.get("reasoning_effort"))
            if last.get("reasoning_effort") != "low":
                raise TestError("reasoningEffort=low 未生效: %r" % last)

    def case_api_key():
        """③ apiKey：A 回读；B 请求头 Authorization: Bearer <该值>（空值 → 不带头，对照）。"""
        key = "sk-" + SENT + "-KEY"
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            save_llms([mk_entry("p-key", apiKey=key)], default="p-key")
            if entry("p-key").get("apiKey") != key:
                raise TestError("A 回读失败: %r" % entry("p-key"))
            reset_mock(m1.port)
            run_turn("hello apikey", "p-key")
            last = last_of(m1.port)
            _evi("apiKey", authorization=last.get("authorization"), expect="Bearer " + key)
            if last.get("authorization") != "Bearer " + key:
                raise TestError("B Authorization 头不符: %r" % last.get("authorization"))
            save_llms([mk_entry("p-key", apiKey="")], default="p-key")
            reset_mock(m1.port)
            run_turn("hello apikey empty", "p-key")
            last2 = last_of(m1.port)
            _evi("apiKey=''", authorization=last2.get("authorization"))
            if last2.get("authorization") != "":
                raise TestError("空 apiKey 不应带 Authorization: %r" % last2.get("authorization"))

    def case_base_url():
        """④ baseUrl：请求确实打到配置端点（M2）；对照端点 M1 不得收到任何请求。"""
        bu = "http://127.0.0.1:%d/v1" % m2.port
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            save_llms([mk_entry("p-base", baseUrl=bu, model="m-base-sent")], default="p-base")
            if entry("p-base").get("baseUrl") != bu:
                raise TestError("A 回读失败: %r" % entry("p-base"))
            reset_mock(m1.port)
            reset_mock(m2.port)
            p, _ = run_turn("hello baseurl", "p-base")
            l2, l1 = last_of(m2.port), last_of(m1.port)
            _evi("baseUrl", status=p.get("status"), m2_path=l2.get("path"), m2_model=l2.get("model"),
                 m2_requests=l2.get("n_requests"), m1_requests=l1.get("n_requests"))
            if p.get("status") != "complete" or l2.get("model") != "m-base-sent":
                raise TestError("请求未按 baseUrl 落到 M2: %r" % l2)
            if not str(l2.get("path", "")).endswith("/chat/completions"):
                raise TestError("M2 命中路径异常: %r" % l2.get("path"))
            if req_count(m1.port) != 0:
                raise TestError("对照端点 M1 也收到了 %d 个请求（未按配置端点发送）" % req_count(m1.port))

    def case_zero_values():
        """⑤ maxOutputToken / temperature 含 0 值：存在性判定 → 请求体确实为 0（非"不发送"）。"""
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            save_llms([mk_entry("p-zero", temperature=0, maxOutputToken=0)], default="p-zero")
            back = entry("p-zero")
            if back.get("temperature") != 0 or back.get("maxOutputToken") != 0:
                raise TestError("A 回读失败（0 值）: %r" % back)
            reset_mock(m1.port)
            run_turn("hello zero", "p-zero")
            last = last_of(m1.port)
            _evi("zero-values", temperature=last.get("temperature"), max_tokens=last.get("max_tokens"))
            if last.get("temperature") != 0 or last.get("max_tokens") != 0:
                raise TestError("0 值未按存在性生效: %r" % last)

    def case_max_tool_iterations():
        """⑥ maxToolIterations：=1 截断第二轮 LLM 调用（TOOL_LOOP_LIMIT）；=2 第二轮发生并收尾。"""
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            save_llms([mk_entry("p-iter", maxToolIterations=1)], default="p-iter")
            if entry("p-iter").get("maxToolIterations") != 1:
                raise TestError("A 回读失败: %r" % entry("p-iter"))
            reset_mock(m1.port)
            p, _ = run_turn("please call loop-limit", "p-iter")
            last = last_of(m1.port)
            _evi("maxToolIterations=1", status=p.get("status"), code=p.get("code"),
                 message=(p.get("message") or "")[:40], n_requests=last.get("n_requests"),
                 has_tool_result=last.get("has_tool_result"))
            if p.get("status") != "error" or p.get("code") != "TOOL_LOOP_LIMIT":
                raise TestError("maxToolIterations=1 应以 TOOL_LOOP_LIMIT 收尾: %r" % p)
            if req_count(m1.port) != 1:
                raise TestError("第二轮 LLM 调用未被截断（mock 收到 %d 个请求）" % req_count(m1.port))
            # 对照：=2 → 第二轮确实发生（请求上下文含工具结果）并正常收尾
            save_llms([mk_entry("p-iter", maxToolIterations=2)], default="p-iter")
            reset_mock(m1.port)
            p2, _ = run_turn("please call loop-limit", "p-iter")
            last2 = last_of(m1.port)
            _evi("maxToolIterations=2", status=p2.get("status"), n_requests=last2.get("n_requests"),
                 has_tool_result=last2.get("has_tool_result"))
            if req_count(m1.port) != 2 or last2.get("has_tool_result") is not True:
                raise TestError("maxToolIterations=2 应发生第 2 轮（含工具结果）: %r" % last2)
            if p2.get("status") != "complete":
                raise TestError("maxToolIterations=2 应正常收尾: %r" % p2)

    def case_default_llm():
        """⑦ defaultLLM：A 回读；B 默认 LLM 决定实际调用端点（顶层轮次 + 提示词优化）。

        `llm-start` 不带 llm 时 server 逐级回落 → 命中 usr `defaultLLM`（AG-C1 / G-45 ④(b) 有意
        行为变更，原为 exe flags）；`gui.prompt-optimise` 同为 defaultLLM 消费方 → B 用两条路径确权。
        """
        with _h.user_config_guard(c, ["llms", "defaultLLM"]):
            opt = mk_entry("p-opt", apiKey="sk-" + SENT + "-OPT", model="m-opt-sent",
                           temperature=0.25, maxOutputToken=123, baseUrl="http://127.0.0.1:%d/v1" % m2.port)
            save_llms([opt], default="p-opt")
            if user_cfg().get("defaultLLM") != "p-opt":
                raise TestError("A 回读失败: defaultLLM=%r" % user_cfg().get("defaultLLM"))
            begin()
            reset_mock(m2.port)
            c.req("gui.prompt-optimise", {"useCase": "field-probe", "prompt": "optimize " + SENT})
            deadline = time.time() + 40
            done = err = None
            while time.time() < deadline:
                if c.events_of("optimize-done", clear=False):
                    done = True
                    break
                ev = c.events_of("optimize-error", clear=False)
                if ev:
                    err = (ev[0].get("payload") or {}).get("message")
                    break
                time.sleep(0.5)
            l2 = last_of(m2.port)
            _evi("defaultLLM=p-opt", optimize_done=bool(done), optimize_error=err,
                 m2_path=l2.get("path"), m2_model=l2.get("model"), m2_auth=l2.get("authorization"),
                 m2_temperature=l2.get("temperature"), m2_max_tokens=l2.get("max_tokens"))
            if err or not done:
                raise TestError("提示词优化未走默认 LLM: err=%r" % err)
            if l2.get("model") != "m-opt-sent" or l2.get("authorization") != "Bearer sk-" + SENT + "-OPT":
                raise TestError("默认 LLM 的 model/apiKey 未生效: %r" % l2)
            # 行为确权（AG-C1 / G-45 ④(b) 有意变更）：llm-start **不带 llm** 时 server 回落
            # usr `defaultLLM`（本用例 = p-opt；端点 M2 / model=m-opt-sent），exe flags 仅为末级兜底。
            reset_mock(m1.port)
            reset_mock(m2.port)
            p3, _ = run_turn("hello no-llm-field", "")
            l2b = last_of(m2.port)
            _evi("llm-start 无 llm 字段（回落 usr defaultLLM=p-opt）", status=p3.get("status"),
                 m2_model=l2b.get("model"), m2_path=l2b.get("path"),
                 m1_requests=req_count(m1.port), default_llm="p-opt")
            if p3.get("status") != "complete" or l2b.get("model") != "m-opt-sent" or req_count(m1.port) != 0:
                raise TestError("不带 llm 字段应回落 usr defaultLLM（p-opt → M2/m-opt-sent）: %r" % l2b)
            # 对照：defaultLLM = ""（系统默认（启动参数））→ 明确报错、不发请求
            save_llms([opt], default="")
            begin()
            reset_mock(m2.port)
            c.req("gui.prompt-optimise", {"useCase": "field-probe", "prompt": "optimize again"})
            deadline = time.time() + 20
            err2 = None
            while time.time() < deadline:
                ev = c.events_of("optimize-error", clear=False)
                if ev:
                    err2 = (ev[0].get("payload") or {}).get("message")
                    break
                time.sleep(0.5)
            _evi("defaultLLM=''", optimize_error=err2, m2_requests=req_count(m2.port))
            if not err2:
                raise TestError("defaultLLM='' 应明确报错（不猜测端点）")
            if req_count(m2.port) != 0:
                raise TestError("defaultLLM='' 仍发出请求（%d 个）" % req_count(m2.port))

    def case_default_scenario():
        """⑧ defaultScenario：A 回读；B1 前端星标消费；B2/B3 送 LLM 的 system 含场景哨兵。"""
        with _h.user_config_guard(c, ["llms", "defaultLLM", "defaultScenario"]):
            try:
                # 场景 = 独立根 scenarios/<id>/（scenario.json + main.agent.md；示例化 user 级）。
                # 载荷外形对齐前端 dataClient.save（`{data: {...}}`，id 在 data 内）。
                c.req("data-scenario-save", {"data": {
                    "id": SC_ID, "name": SC_NAME, "level": "user",
                    "agents": [{"name": "主", "roleTag": "主", "isMain": True, "prompt": SC_PROMPT}],
                }})
                rec = (c.req("data-scenario-load",
                             {"data": {"id": SC_ID, "level": "user"}}) or {}).get("data") or {}
                if SC_PROMPT not in (rec.get("systemPrompt") or ""):
                    raise TestError("场景落盘失败（systemPrompt 未派生哨兵）: %r" % rec)
                # A：经前端"设为默认选中场景"（scenario-set-default，既有前端事件）落库 → 回读
                c.mq_emit("scenario-reload", {})
                time.sleep(0.6)
                c.mq_emit("scenario-set-default", {"id": SC_ID})
                time.sleep(1.0)
                got = user_cfg().get("defaultScenario")
                if got != SC_ID:
                    raise TestError("A 回读失败: defaultScenario=%r" % got)
                # B1：前端消费（场景 popover 的星标 .on）
                if c.exists(".scenario-item").get("count", 0) == 0:
                    c.click(".panel-header .b-tag", 5000)
                    time.sleep(1.0)
                cls = _plain(c.eval("""(() => {
                  const it = [...document.querySelectorAll('.scenario-item')].find(x =>
                    ((x.querySelector('.scenario-item-name')||{}).textContent||'').trim() === %s);
                  return it ? ((it.querySelector('.scenario-item-star')||{}).className || '') : 'NOT_FOUND';
                })()""" % json.dumps(SC_NAME)))
                _evi("defaultScenario DOM", star_class=cls, default_scenario=got)
                if "on" not in str(cls):
                    raise TestError("defaultScenario 未被前端消费（星标 class=%r）" % cls)
                c.click(".panel-header .b-tag", 5000)  # 收起 popover
                # B2：server 注入 —— 直接 llm-start 带该场景 id → 送 LLM 的 system 含哨兵
                save_llms([mk_entry("p-sc", model="m-sc")], default="p-sc")
                reset_mock(m1.port)
                p2, _ = run_turn("hello scenario", "p-sc", scenario=SC_ID)
                l2 = last_of(m1.port)
                _evi("defaultScenario→system（mq 驱动）", status=p2.get("status"),
                     system_has_sentinel=SC_PROMPT in (l2.get("system") or ""),
                     system_head=(l2.get("system") or "")[:60])
                if p2.get("status") != "complete" or SC_PROMPT not in (l2.get("system") or ""):
                    raise TestError("场景 systemPrompt 未注入 LLM: %r" % l2.get("system"))
                # B3：前端驱动的真实发送（无显式 scenario_id）→ 默认场景生效 → system 含哨兵
                c.mq_emit("chat-select-llm", {"name": "p-sc"})
                c.mq_emit("scenario-set-default", {"id": SC_ID})
                time.sleep(1.0)
                r = _plain(c.eval("""(() => {
                  const el = document.querySelector('.richtext-input');
                  if (!el) return 'no-el';
                  el.innerHTML = %s;
                  el.dispatchEvent(new Event('input', { bubbles: true }));
                  return 'ok';
                })()""" % json.dumps("hello default scenario " + SENT)))
                if r != "ok":
                    raise TestError("未找到输入区 .richtext-input: %r" % r)
                time.sleep(0.4)
                reset_mock(m1.port)
                begin()
                c.mq_emit("chat-send")
                sid = wait_turn_start()
                p3 = wait_complete(sid)[0]
                l3 = last_of(m1.port)
                _evi("defaultScenario→system（前端驱动）", status=p3.get("status"), session=sid,
                     system_has_sentinel=SC_PROMPT in (l3.get("system") or ""),
                     model=l3.get("model"))
                if SC_PROMPT not in (l3.get("system") or ""):
                    raise TestError("前端按默认场景发送时 system 未含哨兵: %r" % (l3.get("system") or "")[:120])
            finally:
                try:
                    c.req("data-scenario-delete", {"data": {"id": SC_ID, "level": "user"}})
                except Exception as e:
                    print("[cleanup] 场景删除失败: %s" % e, flush=True)

    # ── 执行 ─────────────────────────────────────────────

    ok = 0
    total = 0
    c.console(clear=True)
    cases = [
        ("① protocol=responses / openai（路径 + max_output_tokens）", case_protocol),
        ("② thinking / reasoningEffort（发不发 + 取值）", case_thinking_effort),
        ("③ apiKey（Authorization: Bearer）", case_api_key),
        ("④ baseUrl（请求打到配置端点，对照端点零请求）", case_base_url),
        ("⑤ maxOutputToken / temperature 含 0 值", case_zero_values),
        ("⑥ maxToolIterations=1 截断第二轮 LLM 调用", case_max_tool_iterations),
        ("⑦ defaultLLM（默认 LLM 决定实际调用端点）", case_default_llm),
        ("⑧ defaultScenario（前端选中 + system 注入哨兵）", case_default_scenario),
    ]
    for name, fn in cases:
        total += 1
        ok += run_case(name, fn)
    for e in (c.console() or {}).get("entries", []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)
    print("\nLLM 配置字段端到端：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
