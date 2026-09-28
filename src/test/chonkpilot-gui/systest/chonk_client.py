# -*- coding: utf-8 -*-
"""ChonkPilot 测试通道客户端（--test-port 模式）。

协议（见 chonkpilot/testserver.go / arch-notes/测试模式.md）：
  GET  /ping       → {ok, app, ready}
  POST /eval       → {js, timeout}   → {ok, result}
  POST /click      → {selector, timeout}
  POST /input      → {selector, value, timeout}
  POST /text       → {selector, timeout}   → result=纯文本
  POST /html       → {selector, timeout}   → result=outerHTML
  POST /exists     → {selector, timeout}   → result={count, visible}
  POST /console    → {clear}               → result={entries, truncated}
  GET  /screenshot → PNG
"""

import functools
import json
import time
import urllib.request
import urllib.error


class TestError(AssertionError):
    """断言失败。"""


# ── 瞬态错误有界重试（**仅**驱动/取数层；不包裹任何断言）────────────────────
#
# 背景（已知抖动，用户核准加重试）：满负载批量跑时 WebView2 UI 线程偶发 starvation →
# 驱动指令（`/eval` 及其之上的 `/click` `/input` `/text` `/html` `/exists` `/console`；服务端
# 统一走 `doEval`→`EvalWithResult`，见 `src/lib/gui/testserver.go:226`）派发的 ExecuteScript
# 在预算内拿不到调度 → 抛 `ExecuteScript timed out`（`src/lib/go-webview2/webview.go:645`：
# 派发到 UI 线程 + 等回调，超时即报错）→ 断言脚崩溃（批内偶发红、单跑全绿）。
# 就绪探测早有 `harness.wait_probe` 容忍瞬时错误（[42 §2 (148)]），**断言期**的驱动调用此前没有。
#
# 口径（守住「不掩盖产品缺陷」的底线）：
#   * **只**重试「瞬态/超时类」错误（见 TRANSIENT_ERRORS）：ExecuteScript/网络超时、连接类瞬态；
#     业务失败——元素不存在（`not found: …`）、脚本自身抛错、`/publish` 信封 `ok=false` 等——
#     **立即上抛，绝不重试**；
#   * **有界**：每次调用额外重试 ≤ `RETRY_MAX` 次（总尝试 = RETRY_MAX+1），退避 `RETRY_BACKOFF`；
#   * **可观测**：每次重试打印一行 `[retry] <调用>: <错误>, attempt n/m, backoff Xs`
#     （便于区分「真绿」与「重试后绿」）；
#   * **作用域**：仅驱动/取数指令（`.eval/.click/.input/.text/.html/.exists/.console`）；
#     `/publish`（`req`/`publish`，消息面请求-响应，可能触发落库/LLM/工具等**副作用**）**不重试**，
#     断言（`Checker`/`run_case`/`assert_*`）**不重试**。
#
# 关于「重试是否可能重复施加副作用」（如 `eval` 里 `el.click()`）：`EvalWithResult` 的超时
# 覆盖「派发 + 执行 + 回调」全程；UI 线程饥饿时派发闭包**尚未执行**即超时（回调若已触发，
# 结果会先入 `ch` 缓冲区）——故超时通常意味着该 JS 未运行，重试等价于「再派发一次」。
RETRY_MAX = 2
RETRY_BACKOFF = (0.5, 1.0)
TRANSIENT_ERRORS = ("ExecuteScript timed out", "timed out", "10061", "10054", "refused",
                    "reset", "RemoteDisconnected", "connection aborted")


def is_transient_error(exc):
    """错误是否属「瞬态/超时类」（可由重试消除，非产品缺陷）。"""
    msg = str(exc)
    return any(t in msg for t in TRANSIENT_ERRORS)


def retryable(name):
    """装饰驱动/取数指令方法：**仅**对其抛出的瞬态错误做有界重试，其余立即上抛。

    只包裹「取数 / 派发」这一层；不得用于断言（断言失败必须原样暴露）。
    """
    def deco(fn):
        @functools.wraps(fn)
        def wrapper(*args, **kwargs):
            failures = 0
            while True:
                try:
                    return fn(*args, **kwargs)
                except Exception as e:
                    if failures >= RETRY_MAX or not is_transient_error(e):
                        raise
                    backoff = RETRY_BACKOFF[min(failures, len(RETRY_BACKOFF) - 1)]
                    failures += 1
                    print("[retry] %s: %s, attempt %d/%d, backoff %.1fs"
                          % (name, e, failures + 1, RETRY_MAX + 1, backoff), flush=True)
                    time.sleep(backoff)
        return wrapper
    return deco


class ChonkClient:
    def __init__(self, base="http://127.0.0.1:2345", timeout=10, window_id=""):
        self.base = base
        self.timeout = timeout
        # 目标窗口（多窗口测试通道路由，见 24 §4.5 / main.go testServer）：
        # 空 = 主窗口（缺省，与单窗口行为逐字一致）；非空 = `gui.window.list` 的 window_id
        # → /eval、/publish、/wait-event、/screenshot 作用于该对话窗口。
        self.window_id = window_id

    def _post(self, path, payload):
        if self.window_id and isinstance(payload, dict) and "window_id" not in payload:
            payload = dict(payload, window_id=self.window_id)
        req = urllib.request.Request(
            self.base + path,
            data=json.dumps(payload).encode("utf-8"),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def _get(self, path):
        if self.window_id:
            path = path + ("&" if "?" in path else "?") + "window_id=" + self.window_id
        with urllib.request.urlopen(self.base + path, timeout=self.timeout) as resp:
            return resp.read()

    def ping(self):
        data = self._get("/ping")
        return json.loads(data)

    def wait_ready(self, max_wait=60):
        deadline = time.time() + max_wait
        while time.time() < deadline:
            try:
                p = self.ping()
                if p.get("ok") and p.get("ready"):
                    return p
            except Exception:
                pass
            time.sleep(1)
        raise TestError(f"IDE 测试通道 {max_wait}s 内未就绪")

    @retryable("eval")
    def eval(self, js, timeout=5000):
        """执行 JS，返回 EvalWithResult 解包后的原始结果（瞬态超时由装饰器有界重试）。"""
        r = self._post("/eval", {"js": js, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/eval 失败: {r.get('error')}")
        return r.get("result")

    @retryable("click")
    def click(self, selector, timeout=5000):
        r = self._post("/click", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/click 失败: {r.get('error')}")
        return r.get("result")

    @retryable("input")
    def input(self, selector, value, timeout=5000):
        r = self._post("/input", {"selector": selector, "value": value, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/input 失败: {r.get('error')}")
        return r.get("result")

    @retryable("text")
    def text(self, selector, timeout=5000):
        r = self._post("/text", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/text 失败: {r.get('error')}")
        return r.get("result")

    @retryable("html")
    def html(self, selector, timeout=5000):
        r = self._post("/html", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/html 失败: {r.get('error')}")
        return r.get("result")

    @retryable("exists")
    def exists(self, selector, timeout=5000):
        r = self._post("/exists", {"selector": selector, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/exists 失败: {r.get('error')}")
        return r.get("result")

    @retryable("console")
    def console(self, clear=False, timeout=5000):
        r = self._post("/console", {"clear": clear, "timeout": timeout})
        if not r.get("ok"):
            raise TestError(f"/console 失败: {r.get('error')}")
        return r.get("result")

    def screenshot(self, path=None):
        data = self._get("/screenshot")
        if path:
            with open(path, "wb") as f:
                f.write(data)
        return data

    # ── 消息面请求-响应（data-* / gui.*；publish + 收集 result）──

    def publish(self, typ, payload=None, timeout=30000):
        """POST /publish 原始信封 {ok, result, errors}（不校验成败；事件类/需读 ok=false 时用）。

        业务消息面 payload 传对象（本方法负责 JSON 编码，与前端 mq.js publishToBackend 同形）；
        跨窗口时由 `window_id` 指定目标窗口（见 `__init__`）。
        **不重试**：消息面请求-响应可能触发落库/LLM/工具等副作用（见模块头「瞬态重试」口径）。
        """
        body = {"type": typ,
                "payload": json.dumps(payload, ensure_ascii=False) if payload is not None else "",
                "timeout": timeout}
        if self.window_id and "window_id" not in body:
            body["window_id"] = self.window_id
        return self._post("/publish", body)

    def req(self, typ, payload=None, timeout=20000):
        """经 /publish 走消息面请求-响应（替代已移除的 window.go RPC）。

        桥 PublishEvent → persist/gateway 写回 result；返回 result 载荷；
        信封 ok=false / errors 非空 / result.ok=false → 抛 TestError。
        **不重试**（同 publish：请求-响应可能有副作用）。
        """
        body = {"type": typ,
                "payload": json.dumps(payload, ensure_ascii=False) if payload is not None else "",
                "timeout": timeout}
        r = self._post("/publish", body)
        if not isinstance(r, dict) or r.get("ok") is False:
            msg = ""
            if isinstance(r, dict):
                errs = r.get("errors") or []
                if errs:
                    msg = errs[0]
                else:
                    res = r.get("result")
                    if isinstance(res, dict):
                        msg = res.get("error") or ""
            raise TestError(msg or ("publish failed: " + typ))
        res = r.get("result")
        if isinstance(res, dict) and res.get("ok") is False:
            raise TestError(res.get("error") or (typ + " failed"))
        return res

    # ── mq 便捷方法（testplan 总纲：mq 驱动）──

    def mq_emit(self, topic, payload=None, timeout=5000):
        js = f"window.mq.emit({json.dumps(topic)}, {json.dumps(payload, ensure_ascii=False)})"
        return self.eval(js, timeout)

    def mq_on_capture(self, topics, timeout=5000):
        """注册监听，把事件 push 到 window.__chonkEvents，返回已注册的 topics 集合。

        每次调用前先注销上一轮注册的 handler：mq.on 按「回调引用」去重（mq.js:132），
        而本方法每次都是新闭包，若不注销会持续累积 → 同一事件被 push N 次（N = 调用次数），
        表现为测试侧「同一事件重复到达」。注销后每次捕获都是干净的一轮。
        """
        topics_js = json.dumps(topics, ensure_ascii=False)
        js = f"""
(() => {{
  if (!window.__chonkEvents) window.__chonkEvents = {{ events: [], map: {{}}, unsubs: [] }};
  const ev = window.__chonkEvents;
  const norm = (d) => {{
    if (typeof d === 'string' && d !== '') {{ try {{ return JSON.parse(d); }} catch (e) {{ return d; }} }}
    return d;
  }};
  (ev.unsubs || []).forEach(u => {{ try {{ u(); }} catch (e) {{}} }});  // 注销上一轮，避免 handler 累积
  ev.unsubs = [];
  ev.map = {{}};  // 强制重注册，确保使用最新回调
  ev.events = []; // 清空旧事件
  {topics_js}.forEach(t => {{
    if (ev.map[t]) return;
    ev.map[t] = true;
    ev.unsubs.push(window.mq.on(t, (d) => ev.events.push({{ topic: t, payload: norm(d), ts: Date.now() }})));
  }});
  return Object.keys(ev.map).length;
}})()
"""
        return self.eval(js, timeout)

    def events_of(self, topic, clear=True):
        """读取并（可选）清空 window.__chonkEvents 中某 topic 的事件。"""
        js = """
(() => {
  const ev = window.__chonkEvents || { events: [] };
  const out = ev.events.filter(e => e.topic === %s);
  if (%s) ev.events = ev.events.filter(e => e.topic !== %s);
  return JSON.stringify(out);
})()
""" % (json.dumps(topic), "true" if clear else "false", json.dumps(topic))
        res = self.eval(js, 5000)
        # EvalWithResult 双重 JSON 编码：循环解包直到非 JSON 字符串
        for _ in range(3):
            if not isinstance(res, str) or not res:
                break
            try:
                parsed = json.loads(res)
            except Exception:
                break
            if isinstance(parsed, str):
                res = parsed
                continue
            return parsed
        return []

    def wait_events(self, topic, n=1, max_wait=30, clear=True):
        """轮询等待某 topic 至少 n 条事件，超时抛错。"""
        deadline = time.time() + max_wait
        while time.time() < deadline:
            ev = self.events_of(topic, clear=False)
            if len(ev) >= n:
                if clear:
                    self.events_of(topic, clear=True)
                return ev
            time.sleep(0.5)
        raise TestError(f"等待事件 {topic} 超时（{max_wait}s），当前 {len(self.events_of(topic, clear=False))} 条")

    # ── 断言工具 ──

    def assert_exists(self, selector, visible=True, timeout=5000):
        info = self.exists(selector, timeout)
        if info.get("count", 0) == 0:
            raise TestError(f"断言失败：元素不存在 {selector}")
        if visible and not info.get("visible"):
            raise TestError(f"断言失败：元素不可见 {selector}")
        return info

    def assert_not_exists(self, selector, timeout=5000):
        info = self.exists(selector, timeout)
        if info.get("count", 0) > 0:
            raise TestError(f"断言失败：元素不应存在 {selector}")
        return info

    def assert_text_contains(self, selector, needle, timeout=5000):
        txt = self.text(selector, timeout)
        if needle not in txt:
            raise TestError(f"断言失败：{selector} 文本不含 {needle!r}，实际 {txt[:200]!r}")
        return txt


def run_case(name, fn):
    """执行单个用例并输出 PASS/FAIL。"""
    try:
        fn()
        print(f"  [PASS] {name}")
        return True
    except TestError as e:
        print(f"  [FAIL] {name}: {e}")
        return False
    except Exception as e:
        print(f"  [ERROR] {name}: {type(e).__name__}: {e}")
        return False
