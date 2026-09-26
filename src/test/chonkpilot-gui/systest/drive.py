"""chonkpilot-gui 回归测试公共驱动（--test-port 通道）。

用法（各 test_*.py）：
    from drive import GUI
    gui = GUI(port=2345, work_dir=..., exe=...)
    gui.start()          # 拉起 GUI 进程并等待 ready
    gui.eval(js)         # POST /eval 原始结果（已解一层包装 ok/result）
    gui.click(sel)       # POST /click
    gui.input(sel, v)    # POST /input
    gui.text(sel)        # POST /text
    gui.html(sel)        # POST /html
    gui.exists(sel)      # POST /exists → {count, visible}
    gui.console(clear)   # POST /console → entries
    gui.publish(t, p)    # POST /publish（前端事件透传，不校验结果）
    gui.req(t, p)        # POST /publish 消息面请求-响应（data-*/gui.* 等；返回解析后 result）
    gui.stop()           # 结束进程
"""
import json
import sys
import time
import urllib.request
import urllib.error
import os

_HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, _HERE)
import harness  # noqa: E402  （统一的起停/回收：按需加载 + 结束即关）

# GUI 产物候选（顺序 = 优先级）：dist/desktop（主力发行目录）→ src/gui/dist（旧路径，仅兼容回落）。
EXE_CANDIDATES = harness.GUI_EXE_CANDIDATES


def resolve_exe():
    p = harness.resolve_gui_exe()
    if os.path.isfile(p):
        print(f"[drive] GUI 产物: {p}", flush=True)
    else:
        print(f"[drive] 未找到 GUI 产物，尝试: {p}（候选: {' | '.join(EXE_CANDIDATES)}）", flush=True)
    return p


DEFAULT_EXE = resolve_exe()
DEFAULT_WORK = os.path.join(_HERE, "ws")


class GUIClient:
    """GUI 驱动客户端。

    port=None → 每次取空闲端口（动态端口，避免与其它套件/机器上固定端口冲突）；
    start() 走 harness：自起即登记，`stop()` / 进程退出（atexit）/ Ctrl+C（信号）
    三条路径都会 taskkill /F /T 整棵进程树，不留 WebView2 残留。
    """

    def __init__(self, port=None, exe=None, work_dir=None, data_dir=None, wait_ready=30):
        self.port = int(port or harness.free_port())
        self.base = f"http://127.0.0.1:{self.port}"
        self.exe = exe or DEFAULT_EXE
        self.work_dir = work_dir or DEFAULT_WORK
        self.data_dir = data_dir
        self.wait_ready = wait_ready
        self.proc = None
        self._handle = None

    # ── 进程管理 ──
    def start(self):
        # 启动就绪栏栅（含 `.panel-inner` 有界等待 + 瞬时基础设施错误容忍）由 harness.GUIHandle
        # 统一提供（唯一起停入口）——见 harness.GUIHandle.__init__（[42 §2 (148)]）。
        self._handle = harness.start_gui(port=self.port, work_dir=self.work_dir,
                                         data_dir=self.data_dir, exe=self.exe,
                                         ready_timeout=self.wait_ready)
        self.proc = self._handle.proc

    def stop(self):
        if self._handle is not None:
            self._handle.stop()
            self._handle = None
            self.proc = None

    # ── HTTP 基础 ──
    def _post(self, path, body=None, timeout=60):
        data = json.dumps(body).encode() if body is not None else b"{}"
        req = urllib.request.Request(self.base + path, data=data, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def _get(self, path, timeout=60):
        req = urllib.request.Request(self.base + path)
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.read()

    def _result(self, resp):
        if not resp.get("ok"):
            raise RuntimeError(resp.get("error", "call failed"))
        return resp.get("result")

    # ── 驱动指令 ──
    # JS 执行预算（ms）默认 30s：`--test-port` 的 `/eval` 走
    # `webview2.EvalWithResult`（`src/lib/go-webview2/webview.go:639`）派发到 WebView2 UI 线程，
    # 其 timeout 覆盖「派发 + 执行 + 回调」全程；负载下首屏 bundle 执行期该线程被争用，
    # 10s 会误判为 `ExecuteScript timed out` 并**使套件崩溃**（实测 2026-09-23 负载批跑
    # `test_statusbar.py:48`）。预算提到 30s（test 通道自身钳制上限，见
    # `src/lib/gui/testserver.go:141`），HTTP 客户端超时同步提到 60s。
    # 就绪探测另有 `harness.wait_probe` 对瞬时超时**重试**（[42 §2 (148)]）。
    def eval(self, js, timeout=30000):
        return self._result(self._post("/eval", {"js": js, "timeout": timeout}))

    def click(self, selector, timeout=30000):
        return self._result(self._post("/click", {"selector": selector, "timeout": timeout}))

    def input(self, selector, value, timeout=30000):
        return self._result(self._post("/input", {"selector": selector, "value": value, "timeout": timeout}))

    def text(self, selector, timeout=30000):
        return self._result(self._post("/text", {"selector": selector, "timeout": timeout}))

    def html(self, selector, timeout=30000):
        return self._result(self._post("/html", {"selector": selector, "timeout": timeout}))

    def exists(self, selector, timeout=30000):
        return self._result(self._post("/exists", {"selector": selector, "timeout": timeout}))

    def console(self, clear=False, timeout=30000):
        return self._result(self._post("/console", {"clear": clear, "timeout": timeout}))

    def publish(self, typ, payload=None, timeout=30000):
        return self._result(self._post("/publish", {"type": typ, "payload": json.dumps(payload) if payload is not None else "", "timeout": timeout}))

    def req(self, typ, payload=None, timeout=30000):
        """消息面请求-响应（POST /publish）：body {type, payload: JSON 字符串}，与前端 mq.js
        publishToBackend 发送同构；响应信封 {ok, result, errors}（见 main.go writePublishResult）。
        返回解析后的 result 对象；请求失败（信封 ok=false / errors 非空 / 结果对象 {ok:false,error}）
        抛 RuntimeError，错误文本取 errors[0] / result.error——与前端 sessionReq/guiReq/dataClient
        判定一致。

        业务消息面：
          - data-session-* / data-tasktree-* / data-user-config-* … → envelope 字段 {req_id?, filter?, id?, data?}
            （internal/bridge/configmsg.go handleDataMsg 只解这四字段）
          - gui.* → payload 为扁平对象（guiDo 直接解析顶层）
        """
        body = {"type": typ, "payload": json.dumps(payload) if payload is not None else "", "timeout": timeout}
        resp = self._post("/publish", body)
        if not isinstance(resp, dict) or resp.get("ok") is False:
            msg = ""
            if isinstance(resp, dict):
                errs = resp.get("errors") or []
                if errs:
                    msg = errs[0]
                else:
                    res = resp.get("result")
                    if isinstance(res, dict):
                        msg = res.get("error") or ""
            raise RuntimeError(msg or ("publish failed: " + typ))
        result = resp.get("result")
        if isinstance(result, dict) and result.get("ok") is False:
            raise RuntimeError(result.get("error") or (typ + " failed"))
        return result

    # ── 等待 ──
    def wait_until_ready(self):
        deadline = time.time() + self.wait_ready
        last = None
        while time.time() < deadline:
            try:
                r = self._post("/ping", {})
                if r.get("ready"):
                    return True
                last = "not ready"
            except Exception as e:
                last = str(e)
            time.sleep(0.5)
        raise RuntimeError(f"GUI not ready in {self.wait_ready}s: {last}")

    def wait_selector(self, selector, timeout=15, visible=True):
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                info = self.exists(selector)
                if info.get("visible") == visible and info.get("count", 0) > 0:
                    return True
            except Exception:
                pass
            time.sleep(0.5)
        return False


def main_check(gui, name, cond, detail=""):
    """断言并打印结果（不抛异常，最后统计失败）。"""
    tag = "PASS" if cond else "FAIL"
    print(f"[{tag}] {name}{(' - ' + str(detail)) if detail else ''}")
    return bool(cond)


class Checker:
    def __init__(self):
        self.passed = 0
        self.failed = 0
        self.items = []

    def check(self, name, cond, detail=""):
        tag = "PASS" if cond else "FAIL"
        print(f"[{tag}] {name}{(' - ' + str(detail)) if detail else ''}")
        self.items.append((name, bool(cond), detail))
        if cond:
            self.passed += 1
        else:
            self.failed += 1
        return bool(cond)

    def summary(self):
        print(f"\n== {self.passed} passed, {self.failed} failed ==")
        return self.failed == 0


if __name__ == "__main__":
    # 快速冒烟：启动 → ping → 断言页面已渲染主要面板 → 退出（动态端口，结束即回收）
    gui = GUIClient()
    try:
        gui.start()
        c = Checker()
        c.check("ready", True)
        c.check("toolbar 存在", gui.wait_selector(".toolbar", 15))
        c.check("filetree 存在", gui.wait_selector(".filetree-panel", 5))
        c.check("chat 输入区存在", gui.wait_selector("textarea, [contenteditable]", 5))
        sys.exit(0 if c.summary() else 1)
    finally:
        gui.stop()
