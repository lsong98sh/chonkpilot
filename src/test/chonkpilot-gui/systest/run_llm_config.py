# -*- coding: utf-8 -*-
"""testplan.md 三.2 LLM 生效验证（含外部修改热重读）结合测试。

前置：IDE 以 --test-port=2345 启动；mock_llm.py 监听 127.0.0.1:8901（含 GET /last）。

会话/轮次改 mq-only（无 window.go、无 llm-started）。**旧 IDE test-port 契约
（llm-started 附加 llm_config）已废弃**（61 §6）：受理信息不再随事件下发，故改以
「mock 服务端收到的请求配置」确权（GET /last 返回最近一次 chat/completions 的
model/temperature/max_tokens）——server 每轮经 data-user-config-load 读取所选 LLM
条目（loadLLMConfig），据此可验证：
  三.2a 按所选 LLM 发起调用：请求 model == **该条目配置的 model**（P1-1 起 body.model 取配置
        model，**非** provider name；name 仅用于命中条目）、temperature == 该条目配置值。
  三.2b 配置热重读：改写用户的 mock 条目 temperature → 下一轮请求即用新值（无需重启）。

注（测试资产修正 2026-09-15）：`confirm_turn`/`wait_events` 依赖 `window.__chonkEvents`
的 mq 监听注册（本脚本原先未注册，仅靠前序脚本（如 run_llm.py）残留注册才可通过 →
单独运行必失败）；main 内显式 `mq_on_capture` 注册，消除脚本间隐式依赖。
"""

import json
import os
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345)  # 复用优先；无实例则自起并在结束时回收
_M = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)  # mock LLM 按需起（8901），结束回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：usr（llms/temperature 等）退出前回滚

MOCK_BASE = "http://127.0.0.1:8901"
_SEQ = [0]


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def create_session():
    _SEQ[0] += 1
    return "cfg-sess-%d-%03d" % (int(time.time() * 1000), _SEQ[0])


def start_turn(sid, q, llm="mock"):
    c.mq_emit("llm-start", {
        "session_id": sid, "turn": "t-" + sid, "q": q, "llm": llm,
        "think": "", "effort": "", "scenario_id": "",
    })


def confirm_turn(sid, max_wait=60):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("turn-start", clear=False):
            p = e.get("payload") or {}
            if p.get("session") == sid and not p.get("parents"):
                return p
        time.sleep(0.3)
    raise TestError("未收到主会话 turn-start")


def run_turn(sid, q, llm="mock"):
    """发一轮并等终态（llm-complete），返回其 payload。"""
    start_turn(sid, q, llm=llm)
    confirm_turn(sid, max_wait=60)
    ev = c.wait_events("llm-complete", n=1, max_wait=90, clear=True)
    return ev[0].get("payload") or {}


def last_req():
    """mock 最近一次 chat/completions 的配置字段。"""
    with urllib.request.urlopen(MOCK_BASE + "/last", timeout=5) as r:
        return json.loads(r.read().decode("utf-8"))


def _user_cfg():
    res = c.req("data-user-config-load", {})
    data = res.get("data") if isinstance(res, dict) else None
    return data if isinstance(data, dict) else {}


def _llms():
    return list(_user_cfg().get("llms") or [])


def _ensure_mock_entry():
    """确保用户配置 llms 含指向本套件 mock 端口的 name="mock" 条目（server 每轮按名读取）。

    2026-10-23：**始终覆盖**同名条目（其余套件可能遗留指向随机端口的 mock → 直接复用会连不通，
    表现为 model=None / LLM_REQUEST_FAILED）；并触发 `data-user-config-refresh` 让 router 对账。
    """
    llms = [l for l in _llms() if l.get("name") != "mock"]
    entry = {"name": "mock", "protocol": "openai", "apiKey": "sk-test-key",
             "model": "mock-model", "baseUrl": "http://127.0.0.1:%d/v1" % _h.DEFAULT_MOCK_LLM_PORT,
             "temperature": 0.7, "maxOutputToken": 4096}
    llms.append(entry)
    c.req("data-user-config-save", {"data": {"llms": llms}})
    # 2026-10-23：usr `llms` 需经 `data-user-config-refresh` 触发 router 对账
    #（reconcileLLMProviders）才可被 llm-start 的 provider name 命中；否则回落到 exe 默认 provider。
    c.mq_emit("config-refresh")
    time.sleep(0.5)
    return entry


def _mock_entry():
    for l in _llms():
        if l.get("name") == "mock":
            return l
    raise TestError("用户配置 llms 中缺少 name=mock 条目")


def _restore_llms(snap):
    """按快照还原 usr llms（51 §6-8 配置快照-还原：原本为空/缺省 → 清空集合，有值 → 写回）。"""
    _h.restore_user_config(c, snap)


def _set_mock_temperature(temp):
    """改写用户配置里 mock 条目的 temperature（data-user-config-save 消息面）。"""
    llms = _llms()
    for l in llms:
        if l.get("name") == "mock":
            l["temperature"] = temp
    c.req("data-user-config-save", {"data": {"llms": llms}})
    # 热生效同样依赖 router 对账（见 _ensure_mock_entry 注释）
    c.mq_emit("config-refresh")
    time.sleep(0.5)


def case_llm_active_verify():
    """三.2a 按所选 LLM 发起调用：请求 model/temperature 与所选 LLM 配置一致。"""
    snap = _h.snapshot_user_config(c, ["llms"])
    try:
        _ensure_mock_entry()
        entry = _mock_entry()
        sid = create_session()
        done = run_turn(sid, "hello, please reply with mock-reply", llm="mock")
        last = last_req()
        print("[evidence] entry=%r last=%r llm-complete=%r retries=%d"
              % (entry, last, done, len(c.events_of("llm-retry", clear=False))), flush=True)
        if last.get("model") != entry.get("model"):
            raise TestError(f"请求 model={last.get('model')!r}，期望 {entry.get('model')!r}"
                            "（P1-1 起 body.model = 所选条目的 model 配置，非 provider name）")
        if entry.get("temperature") is not None and last.get("temperature") != entry.get("temperature"):
            raise TestError(f"请求 temperature={last.get('temperature')!r}，"
                            f"期望 {entry.get('temperature')!r}（mock 条目配置）")
    finally:
        _restore_llms(snap)


def case_hot_reload():
    """三.2b 配置热重读：改 mock 条目 temperature → 下一轮请求生效（无需重启），双向验证。"""
    snap = _h.snapshot_user_config(c, ["llms"])
    try:
        _ensure_mock_entry()
        orig = _mock_entry().get("temperature")
        _set_mock_temperature(0.33)
        time.sleep(0.3)
        sid = create_session()
        run_turn(sid, "hello, please reply with mock-reply", llm="mock")
        last = last_req()
        if last.get("temperature") != 0.33:
            raise TestError(f"热重读失败：请求 temperature={last.get('temperature')!r}，期望 0.33")
        # 改回原值 → 再次生效
        _set_mock_temperature(orig)
        time.sleep(0.3)
        sid2 = create_session()
        run_turn(sid2, "hello, please reply with mock-reply", llm="mock")
        last2 = last_req()
        if last2.get("temperature") != orig:
            raise TestError(f"恢复后重读失败：请求 temperature={last2.get('temperature')!r}，期望 {orig!r}")
    finally:
        _restore_llms(snap)


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    # 事件捕获注册（mq-only 确权前置）：缺此注册则 events_of 恒空（脚本间隐式依赖已消除）
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["turn-start", "llm-complete", "llm-token", "llm-error", "error"])
    total += 1; ok += run_case("三.2a 按所选 LLM 发起调用（请求配置一致）", case_llm_active_verify)
    total += 1; ok += run_case("三.2b 外部修改热重读（改 temperature 无需重启生效）", case_hot_reload)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\nLLM 生效验证：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
