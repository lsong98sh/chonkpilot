# -*- coding: utf-8 -*-
"""llm-error-handling.md 规划项落地回归（§一 网络连接预检 / S21 空回复 / S23 握手分类 / S25 发送前校验）。

前置：IDE --test-port=2345；mock_llm.py 监听 127.0.0.1:8901；
      用户配置（usr 库，经 data-user-config-load/save 消息面）的 llms 含 mock 条目；
      本脚本临时改写 llms/defaultLLM/retryCount/retryDelay：按跑前**快照**在 finally 还原
      （51 §6-8 配置快照-还原；原本缺省 → 删键回落系统默认）。
      （原读写 %USERPROFILE%/.chonkpilot/config.json 为死文件——全仓无加载点，改动不生效。）

迁移（2026-09-15）：
  - 会话一律 mq-only（客户端自分配 session id + `session-changed` 激活）：`window.go.CreateSession`
    / `SetActiveSessionID` 已随 RPC 面移除（禁止直调 window.go.*），见 create_session/activate_session。
  - 输入区为 contenteditable 富文本（`.richtext-input`）：S25 用 innerHTML + input 事件驱动，
    不再用已移除的 `.b-textarea--autosize` textarea value 写入。
  - 轮次受理确权：`llm-started` 已无事件源 → 改等主会话 `turn-start`（§4.3
    `session-turn-start` → 前端 `turn-start`），与 run_llm.confirm_turn 同口径；
    llm-start 载荷补 `turn` 且 `scenario_id` 为字符串（空串）。

用例：
  S21  空回复实时提示：mock "call empty" → llm-complete status=completed 且无输出
       → 前端出现"空回复"提示 +"继续"按钮（不再静默无提示）
  §一  网络连接预检：LLM 端点不可达（127.0.0.1:9）+ retryCount=3 → 预检命中不重试
       （无 llm-retry 事件）、首次错误快速到达
  S23  错误分类单元测试见 chonkpilot/pkg/executor/llm/netprobe_test.go（go test）
  S25  发送前校验 LLM 配置：llms 置空 → 发送被拦截（无 llm-start）+ warning 提示
"""

import copy
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345)  # 复用优先；无实例则自起并在结束时回收
_M = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)  # mock LLM 按需起（8901），结束回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：usr（llms/retryCount 等）退出前回滚

EVENTS = ["llm-started", "llm-complete", "llm-retry", "llm-error", "llm-start", "turn-start"]

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


def read_cfg():
    """读取用户级配置（usr 库）：data-user-config-load 消息面 → data 对象。

    原实现读写 ~/.chonkpilot/config.json（死文件，全仓无加载点、改动不生效），
    改走配置消息面（与 run_config.py / run_llm_config.py 同路）。
    """
    res = c.req("data-user-config-load", {})
    return (res.get("data") or {}) if isinstance(res, dict) else {}


def write_cfg(cfg):
    """增量保存用户级配置（usr 库）：data-user-config-save 消息面（载荷 data = 增量对象）。"""
    c.req("data-user-config-save", {"data": cfg})


def create_session():
    """生成会话 id（mq-only）。

    迁移（2026-09-15）：新架构已无 window.go 绑定（禁止直调 window.go.*）——
    ChatPanel.ensureSessionId 客户端自分配 session uuid，llm-start 经桥 session-start
    幂等落库（21-llm-server §4.2），无需 CreateSession RPC 预建；与 run_llm.py 同口径。
    """
    _SEQ[0] += 1
    return "llm-err-%d-%03d" % (int(time.time() * 1000), _SEQ[0])


def activate_session(sid):
    """激活会话（前端内部事件 session-changed；新架构无 window.go.SetActiveSessionID）。"""
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.3)


def start_turn(sid, q, llm="mock"):
    """发布 llm-start（等效真实输入；session/turn 客户端分配，scenario_id 为字符串 key）。"""
    c.mq_emit("llm-start", {
        "session_id": sid, "turn": "t-" + sid, "q": q, "llm": llm,
        "think": "", "effort": "", "scenario_id": "",
    })


def wait_event(topic, n=1, max_wait=60):
    return c.wait_events(topic, n=n, max_wait=max_wait, clear=True)


def confirm_turn(sid, max_wait=60):
    """主轮次受理确权（mq-only，与 run_llm 同口径）。

    新构建已无 `llm-started` 兼容事件源（受理改由 §4.3 `session-turn-start`
    → 前端 `turn-start` 确认）→ 本脚本原「等待 llm-started」已过时，改等主会话 turn-start。
    """
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("turn-start", clear=False):
            p = e.get("payload") or {}
            if p.get("session") == sid and not p.get("parents"):
                return p
        time.sleep(0.3)
    raise TestError(f"未收到主会话 turn-start（session={sid}）")


def reset_mock_state():
    """重置 mock 一次性状态机（S21 空回复链确定性）。"""
    import urllib.request
    try:
        urllib.request.urlopen("http://127.0.0.1:8901/reset", timeout=5)
    except Exception:
        pass


# ── S21 空回复实时提示 ──────────────────────────────────────

def case_empty_reply():
    """S21：mock 空回复（stop 但 content 空）→ llm-complete{status:error, code:EMPTY_REPLY}
    （61-消息一览：错误随唯一终态携带，空回复也收敛 error）
    → 前端实时提示"空回复" +"继续"按钮（重发原用户消息），不再静默无提示。"""
    reset_mock_state()  # 确定性：首次 "call empty" 必为空回复
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS)
    sid = create_session()
    activate_session(sid)
    # 空回复 turn 无流式内容、完成极快：确保主 chat 会话切换完成（activeSessionId）再发，
    # 否则 onLlmComplete 的 session 过滤会把终态事件丢弃（hint 不显示）
    time.sleep(1.5)
    # 选中 mock LLM（"继续"按钮按当前所选 LLM 重发）
    c.mq_emit("chat-select-llm", {"name": "mock"})
    start_turn(sid, "please call empty")
    confirm_turn(sid, max_wait=60)
    done = wait_event("llm-complete", max_wait=60)
    payload = done[0]["payload"]
    if payload.get("status") != "error" or payload.get("code") != "EMPTY_REPLY":
        raise TestError(f"空回复应终结为 error+EMPTY_REPLY，实际: {payload}")
    # 实时提示：空回复 hint +"继续"按钮（底部操作区）
    deadline = time.time() + 8
    hint_found = False
    while time.time() < deadline:
        info = c.exists(".empty-reply-hint")
        if info.get("count", 0) > 0:
            hint_found = True
            break
        time.sleep(0.5)
    if not hint_found:
        raise TestError("空回复后未出现实时提示（.empty-reply-hint）")
    info = c.exists(".turn-actions .continue-btn")
    if info.get("count", 0) == 0:
        raise TestError("空回复后未出现'继续'按钮（.turn-actions .continue-btn）")
    # 点击"继续" → 重发原用户消息 → mock 第二次请求消费 _EMPTY_ONCE 返回正常回复
    c.click(".turn-actions .continue-btn", 5000)
    deadline = time.time() + 20
    while time.time() < deadline:
        txt = c.eval('document.body.innerText')
        if "mock-reply" in txt:
            return
        time.sleep(1)
    raise TestError("点击'继续'后未收到回复（mock-reply 未见）")


# ── §一 网络连接预检 ───────────────────────────────────────

def case_network_precheck():
    """§一/S14：LLM 端点不可达 + retryCount=3 → 预检命中，runner 不重试
    （全程无 llm-retry 事件）、首次错误快速终结；自动续写链结束即恢复配置。"""
    backup = read_cfg()
    snap = _h.snapshot_user_config(c, ["llms", "defaultLLM", "retryCount", "retryDelay"])
    try:
        cfg = copy.deepcopy(backup)
        cfg["llms"] = [l for l in cfg.get("llms", []) if l.get("name") == "mock"]
        cfg["llms"].append({
            "name": "dead-llm", "protocol": "openai", "apiKey": "dead-key",
            "model": "dead-model", "baseUrl": "http://127.0.0.1:9/v1",
            "temperature": 0.7, "maxOutputToken": 4096, "thinking": False,
        })
        cfg["defaultLLM"] = len(cfg["llms"]) - 1
        cfg["retryCount"] = 3
        cfg["retryDelay"] = 5
        write_cfg(cfg)
        c.mq_emit("config-refresh")
        c.mq_emit("chat-select-llm", {"name": "dead-llm"})
        time.sleep(1.5)

        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        sid = create_session()
        activate_session(sid)
        t0 = time.time()
        start_turn(sid, "hello", llm="dead-llm")
        # 受理确权：主会话 turn-start（旧 llm-started 已无源）
        started = confirm_turn(sid, max_wait=60)
        if not (started.get("turn") or started.get("turn_id")):
            raise TestError(f"turn-start 缺 turn 标识: {started}")
        done = wait_event("llm-complete", max_wait=60)
        elapsed = time.time() - t0
        if done[0]["payload"].get("status") not in ("error", "canceled", "cancelled"):
            raise TestError(f"网络错误应终结为 error: {done[0]['payload']}")
        if elapsed > 15:
            raise TestError(f"预检后首次错误耗时 {elapsed:.1f}s，期望 <15s（无重试延迟）")
        # 等待自动续写链结束（网络错误 retryable → 后端自动续写最多 3 次，每次预检快速失败）
        deadline = time.time() + 30
        last_new = time.time()
        while time.time() < deadline:
            ev = c.events_of("llm-complete", clear=False)
            if len(ev) > 1:
                last_new = time.time()
            if time.time() - last_new > 5:
                break
            time.sleep(1)
        # 全程不得出现 llm-retry（预检命中 → 方式 A 重试被跳过）
        retries = c.events_of("llm-retry", clear=True)
        if retries:
            raise TestError(f"网络未连接时不应重试（llm-retry 出现 {len(retries)} 次）")
        return
    finally:
        # 快照-还原（51 §6-8）：按跑前快照回原状（原本缺省 → 删键回落系统默认），
        # 不再整块写回 load 的合并视图（那会把系统默认固化成显式 usr 配置）。
        _h.restore_user_config(c, snap)
        c.mq_emit("config-refresh")


# ── S25 发送前校验 LLM 配置 ─────────────────────────────────

def case_send_blocked_no_llm():
    """S25：llms 置空 → 发送被拦截（无 llm-start / llm-started）+ warning 提示去配置；
    textarea 文本保留（未发送未清空）。"""
    backup = read_cfg()
    snap = _h.snapshot_user_config(c, ["llms", "defaultLLM"])
    try:
        cfg = copy.deepcopy(backup)
        cfg["llms"] = []
        cfg["defaultLLM"] = 0
        write_cfg(cfg)
        c.mq_emit("config-refresh")
        time.sleep(1.5)
        # 输入文本（输入区为 contenteditable 富文本：写 innerHTML + input 事件 → onEdit 同步 text）
        r = _loads_deep(c.eval("""(() => {
          const el = document.querySelector('.richtext-input');
          if (!el) return 'no-el';
          el.innerHTML = 'hello without llm';
          el.dispatchEvent(new Event('input', { bubbles: true }));
          return 'ok';
        })()"""))
        if r != "ok":
            raise TestError(f"未找到输入区 .richtext-input: {r}")
        time.sleep(0.3)
        c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
        c.mq_on_capture(EVENTS)
        # 触发发送（chat-send → InputBox handleSend → serialize → ChatPanel.handleSend）
        c.mq_emit("chat-send")
        time.sleep(2)
        # 断言未发起 LLM 请求
        if c.events_of("llm-start", clear=True):
            raise TestError("LLM 未配置时仍发起了 llm-start")
        if c.events_of("llm-started", clear=True):
            raise TestError("LLM 未配置时仍收到了 llm-started")
        # warning 提示（b-message--warning，含"未配置 LLM"）
        deadline = time.time() + 5
        toast = ""
        while time.time() < deadline:
            info = c.exists(".b-message--warning")
            if info.get("count", 0) > 0:
                toast = c.text(".b-message--warning .b-message-text", 3000) or ""
                if toast:
                    break
            time.sleep(0.5)
        if not toast:
            raise TestError("未出现 LLM 未配置的 warning 提示")
        if "LLM" not in toast:
            raise TestError(f"warning 提示文案异常: {toast!r}")
        # 输入区文本保留（未被清空/发送；拦截后经 chat-queue-restore 写回）
        v = _loads_deep(c.eval("(() => { const t = document.querySelector('.richtext-input'); return t ? t.textContent : ''; })()")) or ""
        if "hello without llm" not in v:
            raise TestError(f"发送被拦截后输入区文本未保留: {v!r}")
        return
    finally:
        _h.restore_user_config(c, snap)  # 快照-还原（51 §6-8）：llms 原本为空/缺省 → 清空集合
        c.mq_emit("config-refresh")


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    total += 1; ok += run_case("S21 空回复实时提示（completed 无输出 → 提示 + 继续）", case_empty_reply)
    total += 1; ok += run_case("§一 网络连接预检（端点不可达不重试，无 llm-retry）", case_network_precheck)
    total += 1; ok += run_case("S25 发送前校验 LLM 配置（llms 空 → 拦截 + 提示）", case_send_blocked_no_llm)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\nLLM 错误处理规划项回归：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
