# -*- coding: utf-8 -*-
"""systest 统一资源管理：**按需加载 + 结束即回收**。

规范见 `docs/spec/50-testing/51-FP与测试映射.md` §测试资源规范。要点：

1. **按需加载**：套件用到什么才拉起什么（GUI / mock LLM / 夹具），不再要求外部先手工
   常驻一个底座实例。
2. **复用优先**：`acquire_*` 若发现目标端口已有就绪实例 → 直接复用（`owned=False`），
   **绝不回收**；端口空闲才自起（`owned=True`）。
3. **结束即回收**：本模块自起的资源一律登记在册，三条路径都会回收：① 套件显式 `stop()`
   （`finally`）；② 进程正常退出（`atexit`）；③ Ctrl+C / 关闭（SIGINT / SIGTERM / SIGBREAK
   处理器）。**GUI 实例优先优雅退出**（`POST /quit` → 宿主走托盘「退出」同路径），超时才
   `taskkill /F /T` 兜底 —— 直接杀进程不会摘托盘图标（系统托盘残影堆积）。
4. **隔离**：`free_port()` 取空闲端口；`tmp_dir()` / `tmp_home()` 给独立数据根与独立
   `USERPROFILE`（usr 主库不读不写机器 `~/.chonkpilot`）。
5. **配置快照-还原**：写真实配置（usr 主库 / work-dir prj 库 / `gui.ui.save`）的用例一律
   「快照 → 写入 → `finally` 还原」，复用 `snapshot_user_config` / `restore_user_config`
   （或 `with user_config_guard(c, keys)`）与 `snapshot_prj_config` / `prj_config_guard`；
   **套件级**整体兜底（usr + prj 两层一次快照/还原）用 `snapshot_config` / `restore_config`。

用法：
    from harness import acquire_gui, acquire_mock_llm, free_port, tmp_dir, user_config_guard

    g = acquire_gui(free_port(), work_dir=tmp_dir("ck-ws-"))
    g.client.req("data-session-list", {})
    with user_config_guard(g.client, ["theme", "locale"]):   # 写真实配置 → 退出即还原
        g.client.req("gui.ui.save", {"ui": {"locale": "en-US"}})
    g.stop()                      # 只回收自起实例；复用的实例不受影响
"""

import atexit
import contextlib
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

from chonk_client import ChonkClient  # noqa: E402
from chonk_client import TestError  # noqa: E402
from chonk_client import TRANSIENT_ERRORS as TRANSIENT_PROBE_ERRORS  # noqa: E402

_REPO_ROOT = os.path.abspath(os.path.join(_HERE, "..", "..", "..", ".."))

# GUI 产物候选（顺序 = 优先级）：dist（2026-11-02 目录扁平化后的发行根）→ dist/desktop /
# src/gui/dist（历史路径回落）。产物名（2026-09-21，D-27 第二刀）：`chonkpilot-gui.exe` →
# **`chonkpilot.exe`**（桌面单体宿主）。
GUI_EXE_CANDIDATES = (
    os.path.join(_REPO_ROOT, "dist", "chonkpilot.exe"),
    os.path.join(_REPO_ROOT, "dist", "desktop", "chonkpilot.exe"),
    os.path.join(_REPO_ROOT, "src", "gui", "dist", "chonkpilot.exe"),
)

HERE = _HERE
DEFAULT_WS = os.path.join(_HERE, "ws")
MOCK_LLM = os.path.join(_HERE, "mock_llm.py")
MOCK_SLOW_MCP = os.path.join(_HERE, "mock_slow_mcp.py")
DEFAULT_MOCK_LLM_PORT = 8901
CREATE_NO_WINDOW = getattr(subprocess, "CREATE_NO_WINDOW", 0)
# 被测进程**优先级**（`start_gui`）：并机负载（并发跑其它 L1/L2/后台编译）下，WebView2 UI 线程被
# 普通优先级的大量争用者饿死 → `--test-port` `/eval` 的 ExecuteScript（派发+执行+回调全程，
# 见 `chonkpilot-webview2/webview.go:675`）在预算内拿不到调度 → `ExecuteScript timed out`
# 使套件崩溃（实测 2026-09-23：负载 5 连跑第 4 轮 `test_toolbar` + `test_filetree` 首个断言 eval 双双超时）。
# 给**被测应用**一个「高于普通」的调度份额，消除与本轮断言无关的饥饿型假失败；
# 不改任何断言、不改变被测逻辑（只影响调度）。
ABOVE_NORMAL_PRIORITY_CLASS = getattr(subprocess, "ABOVE_NORMAL_PRIORITY_CLASS", 0x00008000)


def resolve_gui_exe():
    """按候选顺序取首个存在的 GUI 产物；均不存在时返回首个候选（由启动报错）。

    `CHONK_GUI_EXE` 可显式指定被测产物（如 `dist/gui/chonkpilot-flash.exe` 以跑 flash e2e）；
    未设或该路径不存在时按 `GUI_EXE_CANDIDATES` 顺序回落。
    """
    override = os.environ.get("CHONK_GUI_EXE", "").strip()
    if override and os.path.isfile(override):
        return override
    for p in GUI_EXE_CANDIDATES:
        if os.path.isfile(p):
            return p
    return GUI_EXE_CANDIDATES[0]


# ══════════════════════════════════════════════════════════
# 进程 / 端口工具
# ══════════════════════════════════════════════════════════

def kill_tree(pid, timeout=10):
    """taskkill /F /T：连 WebView2、引擎等子进程一并回收（失败静默）。"""
    if not pid:
        return
    try:
        subprocess.run(["taskkill", "/F", "/T", "/PID", str(pid)],
                       capture_output=True, timeout=timeout,
                       creationflags=CREATE_NO_WINDOW)
    except Exception:
        pass


def free_port():
    """取一个当前空闲的本地端口（动态端口，避免套件间/机器上固定端口冲突）。"""
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]
    finally:
        s.close()


def port_open(port, timeout=0.4):
    """TCP 探测：端口是否已有监听（不保证是 HTTP 就绪）。"""
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(timeout)
    try:
        return s.connect_ex(("127.0.0.1", int(port))) == 0
    finally:
        s.close()


def wait_port(port, timeout=60):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if port_open(port):
            return True
        time.sleep(0.3)
    return False


def ping_ready(port, timeout=3):
    """GET /ping → ready=True 才算「可复用的就绪实例」。"""
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/ping" % int(port), timeout=timeout) as r:
            return b'"ready":true' in r.read().replace(b" ", b"")
    except Exception:
        return False


def stable_ready(port, probes=3, gap=0.6, timeout=3):
    """连续 probes 次 `/ping ready`（间隔 gap）才算**稳定**就绪实例 → 排除「正在退出/被回收中」的实例。

    起因（2026-09-16 实测复现）：上一套件/上一个脚本 `stop()` 若以 `taskkill /F /T` 收尾，**端口释放
    与进程真正消失是异步的** → 紧随其后的套件 `ping_ready()` 仍可能成功（端口在监听、HTTP 还能答），
    于是 `acquire_gui` **复用一个即将消失的实例**（owned=False，永不回收）→ 实例在套件跑到一半时
    消失 → 该套件余下用例全线 `URLError 10061 目标计算机积极拒绝`（= 批内偶发红、单跑绿的真因之一；
    实跑证据：`run_preview_ui` 首行 `复用既有 GUI 实例 127.0.0.1:2345` + P1..P9 全 URLError）。
    """
    for i in range(max(1, int(probes))):
        if not ping_ready(port, timeout):
            return False
        if i < probes - 1:
            time.sleep(gap)
    return True


def wait_port_free(port, timeout=15):
    """等端口**真正空闲**（上一实例释放）。返回 True = 已空闲；超时返回 False（调用方决定是否继续）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if not port_open(port):
            return True
        time.sleep(0.3)
    return False


def wait_ready(port, timeout=90, client_timeout=10):
    """等待 GUI 测试通道就绪；就绪返回 ChonkClient，超时抛 RuntimeError。"""
    c = ChonkClient(base="http://127.0.0.1:%d" % int(port), timeout=client_timeout)
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            p = c.ping()
            if p.get("ok") and p.get("ready"):
                return c
            last = "not ready"
        except Exception as e:  # 连接未起来属正常
            last = str(e)
        time.sleep(0.5)
    raise RuntimeError("GUI(port=%s) 在 %ss 内未就绪: %s" % (port, timeout, last))


def tmp_dir(prefix):
    """建独立临时目录，登记为「结束时删除」（独立数据根 / 工作目录用）。"""
    d = tempfile.mkdtemp(prefix=prefix)
    _TMP_DIRS.append(d)
    return d


# ── 启动就绪探测（容忍 test 通道的瞬时基础设施错误）────────────────
#
# 起因（2026-09-23，`run_all.py` 负载批跑实测，[42 §2 (148)]）：`--test-port` 的 `/eval` 经
# `webview2.EvalWithResult`（`chonkpilot-webview2/webview.go:675`）把 ExecuteScript **派发到
# WebView2 UI 线程**执行，其 timeout 覆盖「派发 + 执行 + 回调」**全程**。并机负载（CPU/IO 压力）
# 下首屏 bundle 执行期该线程被争用 → 一次 10s 超时即抛 `ExecuteScript timed out`
# → 套件**启动就绪探测**直接崩溃（实测 `test_statusbar.py:48`；表现 = 批内偶发红、单独复跑全绿、
# 每次失败套件不同）。就绪探测属**基础设施**等待、非断言 → 对其**容忍并重试**；
# 断言脚本一律不重试（避免副作用脚本重复执行）。
# 瞬态分类与**驱动层**（`chonk_client.TRANSIENT_ERRORS`，驱动指令的有界重试同源）**共用一份**，
# 避免两处口径漂移。

# 与各套件原 `for _ in range(30): ... time.sleep(0.5)` 循环等价的默认窗口（15s）。
DEFAULT_PROBE_TIMEOUT = 15.0


def wait_probe(client, preds, timeout=DEFAULT_PROBE_TIMEOUT, interval=0.5):
    """有界轮询启动就绪探测（`preds` = 单个 JS 串或串列表，全部真值即返回 True）。

    **语义与各套件原「for _ in range(30) + sleep(0.5)」循环逐字等价**（含
    `querySelector` 结果为 `{}` 时判定为假、因而通常走满窗口的既有行为），
    唯一差别 = **瞬时基础设施错误**（`ExecuteScript timed out` / 连接类）**重试而不抛**。
    非瞬时异常（脚本自身抛错等）照旧上抛，不掩盖真实问题。
    """
    js_list = [preds] if isinstance(preds, str) else list(preds)
    deadline = time.time() + timeout
    while True:
        try:
            if all(_plain(client.eval(js)) for js in js_list):
                return True
        except Exception as e:
            if not any(t in str(e) for t in TRANSIENT_PROBE_ERRORS):
                raise
        if time.time() >= deadline:
            return False
        time.sleep(interval)


def wait_idle(client, max_wait=30.0, fast=0.4, probes=2, interval=0.2):
    """等被测 **UI 线程进入「可及时响应」**状态：连续 `probes` 次轻量 eval 往返耗时 < `fast` 秒。

    必要性（2026-09-23，[42 §2 (148)]）：「`/ping ready`」与「主视图已挂载」**都不代表 UI 线程空闲**
    —— 首屏渲染 / 文件树首扫 / 引擎调用期间，`--test-port` `/eval`
    （`webview.go:639` 派发 + 执行 + 回调**全程**受预算约束）会排队；套件紧随其后的**首个断言 eval**
    若撞上该窗口即 `ExecuteScript timed out` 崩溃（实跑证据：负载下 `test_toolbar.py:53` /
    `test_filetree.py:42` 双双崩在启动后首个断言 eval）。此处按「往返耗时」判定空闲，与具体 DOM 无关。
    """
    deadline = time.time() + max_wait
    fast_run = 0
    while time.time() < deadline:
        t0 = time.time()
        try:
            client.eval("1+1")
            dt = time.time() - t0
        except Exception as e:
            if not any(t in str(e) for t in TRANSIENT_PROBE_ERRORS):
                raise
            fast_run = 0
            continue
        fast_run = fast_run + 1 if dt < fast else 0
        if fast_run >= max(1, int(probes)):
            return True
        time.sleep(interval)
    return False


# ── 产品插件 / 工具面就绪栅栏（2026-11-07）────────────────────────
#
# 必要性（实跑定位）：GUI `/ping ready` 只代表**主视图可交互**，**产品插件随后异步拉起**
# （file/task/history 等 runtime autostart；实测 ready 瞬间 `tools-list` 仅 5 个内置工具
# `ask_user/next_run/subchat_finish/subchat_open/tool_result`，约 1–3s 后才补齐到 55）。
# 套件若只等 ready 就起轮，gateway 对尚未注册的插件工具返回 `unknown tool "<name>"`：
#   · run_hist_git H3/H1：`file_replace` → 文件未被修改（"真实工具未改到文件"）；
#   · run_sem_task_tree T1：`task_run` → 任务节点 state=error，永不 done。
# 故「真实工具用例」必须先等**插件台账 running** 且**对应工具出现在 `tools-list`**，
# 再开始驱动；这不是放宽断言（判据仍是真实产物），而是消除「插件冷启动 vs 起轮」竞争。
# 先例：run_sem_tool_result.wait_task_plugin（同因，2026-11-04 加固）。


def plugin_statuses(client):
    """`plugin-list` → {plugin_id: status}（兼容顶层 plugins / data.plugins；失败 → {}）。

    用 `publish` 而非 `req`：插件平台未就绪时 `req` 会抛（超时），此处按空表处理。
    """
    try:
        r = client.publish("plugin-list", {}, timeout=10000) or {}
    except Exception:
        return {}
    if not isinstance(r, dict):
        return {}
    # publish 返回信封 {ok,result,errors}；req 已解到 result。两种形态都兼容。
    res = r.get("result") if isinstance(r.get("result"), dict) else r
    plugins = res.get("plugins")
    if not isinstance(plugins, list) and isinstance(res.get("data"), dict):
        plugins = res["data"].get("plugins")
    out = {}
    for p in plugins or []:
        if isinstance(p, dict) and p.get("id"):
            out[p["id"]] = p.get("status") or ""
    return out


def wait_plugins_running(client, ids, max_wait=180, interval=1.0):
    """有界等待若干插件在台账中 status=running（插件注册 MCP server / 钩子后上报）。

    超时抛 TestError（末次状态随消息打印）——插件没起来就无法验证其工具行为，
    继续跑只会得到 `unknown tool` 的连锁假失败。
    """
    ids = tuple(ids)
    end = time.time() + max_wait
    last = {}
    while time.time() < end:
        last = plugin_statuses(client)
        if all(last.get(i) == "running" for i in ids):
            return last
        time.sleep(interval)
    raise TestError("插件 %r 在 %ss 内未就绪（末次台账=%r）→ 工具面缺其工具"
                    % (list(ids), max_wait, last))


def wait_tools(client, required, max_wait=120, interval=0.5):
    """有界等待 `tools-list` 含全部 required（按暴露名；`self_` 前缀自动归一）。

    返回末次工具名列表（去前缀后的判定集合命中即返回）。超时抛 TestError。
    用于消除「ready 早于插件工具注册」的竞争（见上方就绪栅栏说明）。
    """
    req = tuple(required)
    end = time.time() + max_wait
    last = []
    while time.time() < end:
        try:
            raw = (client.req("tools-list", {}) or {}).get("tools") or []
            last = sorted(t.get("name") or "" for t in raw if isinstance(t, dict))
        except Exception:
            last = []
        canon = {n[5:] if n.startswith("self_") else n for n in last}
        if all(r in canon for r in req):
            return last
        time.sleep(interval)
    raise TestError("工具面 %ss 内未含 %r（末次 %d 项：%r）"
                    % (max_wait, list(req), len(last), last[:20]))


def ensure_task_panel_open(client, timeout=DEFAULT_PROBE_TIMEOUT):
    """确保任务面板已展开（2026-09-27「首屏减负」后 `MainLayout.vue taskOpen` 默认 false）。

    断言任务区 DOM 的套件（`.session-tree-node` / `.session-chat` / `.node-awaiting` 等）须先展开：
    经**既有** `tasks-toggle` 事件（等价用户点顶部「任务」开关）→ 等 `.session-chat` 挂载。
    幂等：已展开直接返回 True；超时返回 False（由调用方决定是否判红）。
    """
    try:
        if bool(_plain(client.eval("!!document.querySelector('.session-chat')"))):
            return True
    except Exception:
        pass
    client.eval("window.mq.emit('tasks-toggle', {}); 'ok'")
    return wait_probe(client, ["!!document.querySelector('.session-chat')"], timeout=timeout)


# ── 会话历史加载「已完成」观测（`data-session-history` 应答）────────────
#
# 必要性（2026-09-23，[42 §2 (148)]）：`MessageList.onSessionChanged` → `loadMessages`
# **整体替换** `messages`（`src/frontend/src/utils/sessionMessages.js`「Replaces the entire
# messages array」）。若发送与该历史回填交错，回执后渲染的 user 气泡可能落在
# **不含该消息的历史快照**之后被替换掉（`isLoading` 仍为真 → 现象 = 「无 user 气泡但有 Cancel」，
# 实测批跑 CF1 `bubble='' cancel=True`）。
# 产品侧已按 **T5 方案 B** 修正（user 气泡不再乐观插入，改在「落库回执」后渲染；「加载中」
# 占位移出 `messages` 数组），本 spy 保留为**确定性辅助**：**先装 spy 记录已完成的
# `data-session-history` 请求**，`session-changed` 后等到目标会话的历史应答回来再发送
# → 消除时序交错（只改「何时发」，不改任何断言）。
HISTORY_SPY_JS = """(()=>{
  if (window.__ckHistSpy) return 'exists';
  window.__ckHistSpy = {done: []};
  const orig = window.fetch.bind(window);
  window.fetch = (...a) => {
    const p = orig(...a);
    try {
      const b = a[1] && a[1].body ? JSON.parse(a[1].body) : null;
      if (b && b.type === 'data-session-history') {
        const sid = (JSON.parse(b.payload || '{}') || {}).session_id || '';
        p.then(() => { window.__ckHistSpy.done.push(sid); }).catch(() => {});
      }
    } catch (e) {}
    return p;
  };
  return 'ok';
})()"""


def install_session_history_spy(client):
    """装 /publish spy：记录**已完成**的 `data-session-history` 请求的 session_id。"""
    return _plain(client.eval(HISTORY_SPY_JS))


def wait_session_history(client, sid, timeout=20.0):
    """有界等待：目标会话的**历史加载应答已回**（见 HISTORY_SPY_JS）。返回是否等到。"""
    return bool(wait_probe(
        client,
        ["(window.__ckHistSpy&&window.__ckHistSpy.done||[]).indexOf(%s)>=0" % json.dumps(sid)],
        timeout=timeout))


def _plain(v):
    """循环解包 JSON 字符串（测试通道 eval 结果可能被双重编码）。"""
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


# 语言生效探针：用顶栏首个菜单文案判定 UI **实际**语言（localStorage 里有值不等于已生效）。
_LOCALE_PROBE_TEXT = {"zh-CN": "打开", "en-US": "Open"}


def _locale_applied(client, locale):
    key = _LOCALE_PROBE_TEXT.get(locale)
    if not key:
        return True
    try:
        txt = (_plain(client.eval("document.body ? document.body.innerText : ''")) or "").strip()
        # 顶栏第一个菜单就是语言敏感文案（zh：打开 / en：Open）；用**前缀**判定，
        # 避免正文里偶发同名英文单词（文件名/模型名）造成假通过。
        return txt[:12].startswith(key)
    except Exception:
        return False


def _try_save_locale(client, locale, budget=20.0):
    """把 locale 写进 usr 主库并**回读确认**（写成功但广播携带旧值是隐性失败，必须对账）。"""
    deadline = time.time() + budget
    while time.time() < deadline:
        try:
            res = client.req("data-user-config-load", {}, timeout=6000)
            cur = ((res or {}).get("data") or {}).get("locale")
            if cur == locale:
                return True
        except Exception:
            cur = None
        try:
            client.req("gui.ui.save", {"ui": {"locale": locale}}, timeout=8000)
        except Exception:
            pass
        time.sleep(0.4)
    return False


def _wait_locale_applied(client, locale, wait=20.0):
    deadline = time.time() + wait
    while time.time() < deadline:
        try:
            if (_plain(client.eval("typeof document!=='undefined' && !!document.querySelector('.statusbar')"))
                    and _locale_applied(client, locale)):
                return True
        except Exception:
            pass
        time.sleep(0.5)
    return False


_STATUS_PROBE = {"zh-CN": "记忆", "en-US": "Memory"}


def _nudge_statusbar_locale(client, locale, wait=6.0):
    """状态栏（layoutFree 命名空间）语言**挂载后**对齐。

    真机现象（2026-11-07）：重载后顶栏已是目标语言，但 `.statusbar` 的
    `Memory · Idle` / `Compression · Idle` 仍是英文——状态栏组件在配置广播到达前
    先以 framework 默认语言挂载，且不随后续同值保存自动重渲。这里在重载完成后再
    下发一次 `gui.ui.save{ui:{locale}}`（应用层既有入口，不新增主题），等状态栏文案对齐。
    """
    key = _STATUS_PROBE.get(locale)
    if not key:
        return True

    def ok():
        try:
            t = _plain(client.eval(
                "document.querySelector('.statusbar') ? document.querySelector('.statusbar').innerText : ''")) or ""
            return key in t
        except Exception:
            return False

    if ok():
        return True
    try:
        client.req("gui.ui.save", {"ui": {"locale": locale}}, timeout=5000)
    except Exception:
        pass
    deadline = time.time() + wait
    while time.time() < deadline:
        if ok():
            return True
        time.sleep(0.3)
    return False


def ensure_locale(client, locale="zh-CN", timeout=60):
    """把本实例前端语言固定为 locale（DB + localStorage 双写 + 重载 + **生效校验**）。

    必要性（2026-11-07 全量批跑定位）：只写 localStorage 不够 —— 启动/保存窗口几何等
    `gui.ui.save` 会触发 `data-user-config-changed` 广播，`useUserConfigSync` 的
    `applyLocale` 用 **usr 主库** 的 `ui.locale` 覆盖本窗口 localStorage；若机器 usr 库
    停在另一语言（任一「中文断言」套件写成 zh-CN 后未还原），英文断言套件即使置了
    localStorage 也会被广播改回 zh-CN（实测：test_chat/test_richtext/test_task/
    test_filetree_ctxmenu 同时红，body 仍是中文、localStorage 回读 zh-CN）。

    修正：
      1) **先**把目标语言写 usr 主库（等数据面就绪后 `gui.ui.save{ui:{locale}}`，重试直至成功），
         使后续配置广播携带目标值；
      2) 再置 localStorage + 重载；
      3) 按 **DOM 实际文案** 校验（顶栏首菜单前缀），不通过则整轮重试（最多 3 轮）——
         负载下 persist/广播可能晚到，单次尝试会误判。
    幂等：DB/localStorage/DOM 三者一致时直接返回，不重载。
    """
    try:
        cur = _plain(client.eval("localStorage.getItem('chonkpilot-locale')"))
    except Exception:
        cur = None
    if cur == locale and _locale_applied(client, locale):
        return True

    per_try = max(15, timeout // 3)
    for _ in range(3):
        _try_save_locale(client, locale, budget=min(25, per_try))
        try:
            client.eval("localStorage.setItem('chonkpilot-locale', %s); 'ok'" % json.dumps(locale))
            client.eval("location.reload(); 'ok'")  # 重载期间连接可能中断 → 容错
        except Exception:
            pass
        if _wait_locale_applied(client, locale, wait=per_try):
            _nudge_statusbar_locale(client, locale)
            return True
    return False


_BASELINE_LOCALE_DONE = "CHONK_TEST_LOCALE_DONE"


def ensure_baseline_locale(client):
    """批跑基线语言归一（**每套件一次**；`run_all.py` 注入 `CHONK_TEST_LOCALE`，默认 zh-CN）。

    背景（2026-11-07）：usr `ui.locale` 是**机器级共享状态**，任一套件写 en-US 后若还原失败，
    后续「中文断言」套件会成批变红（实测 test_statusbar/test_config/test_queue 全英文 UI）。
    在**唯一起停入口**（`GUIHandle.__init__`）把首个实例统一到基线语言，使每套件起点确定；
    同一套件内中途重启（`CHONK_TEST_LOCALE_DONE` 已置位）**跳过**，以免破坏
    「语言持久化/跨重启」类用例（如 run_sem_window_layout_loop）。

    未设 `CHONK_TEST_LOCALE`（单跑）时不动 —— 保持既有直跑行为。
    """
    target = (os.environ.get("CHONK_TEST_LOCALE") or "").strip()
    if not target or os.environ.get(_BASELINE_LOCALE_DONE):
        return
    os.environ[_BASELINE_LOCALE_DONE] = "1"  # 同套件后续实例不再归一
    probe = _LOCALE_PROBE_TEXT.get(target)

    def applied():
        try:
            txt = (_plain(client.eval("document.body ? document.body.innerText : ''")) or "").strip()
            return probe is None or txt[:12].startswith(probe)
        except Exception:
            return False

    if applied():
        _nudge_statusbar_locale(client, target)
        return
    try:
        _try_save_locale(client, target, budget=20.0)
    except Exception:
        pass
    try:
        client.eval("localStorage.setItem('chonkpilot-locale', %s); 'ok'" % json.dumps(target))
        client.eval("location.reload(); 'ok'")
    except Exception:
        pass
    deadline = time.time() + 20
    while time.time() < deadline:
        if applied():
            _nudge_statusbar_locale(client, target)
            return
        time.sleep(0.4)


def reload_and_settle(client, max_wait=120):
    """刷新页面并等**新文档**渲染线程可用（返回 True/False）。

    **必要性（2026-10-27 全量批跑定位）**：共享工作区的 prjusr 会随套件累积（数百对话库 /
    会话列表），`location.reload()` 后首屏重建可让 WebView2 渲染线程**连续阻塞数十秒** ——
    期间宿主管线仍正常（`/ping` 2ms），但 `/eval` 一律 `ExecuteScript timed out`，导致
    紧随其后的断言整条误红（run_inbox INBOX-S1 · run_llm_errors RETRY-1 · run_tool_async F/G）。
    做法：先埋 `window.__ckReloadMark`，重载后等标记消失（= 新文档）+ 输入框出现 + **连续两次**
    评估成功（排除"旧文档尚可应答"的假就绪）。

    调用方无需再自行 sleep；超时返回 False（由调用方决定是否失败）。
    """
    try:
        client.eval("window.__ckReloadMark = '1'; location.reload(); 'ok'")
    except Exception:
        pass
    deadline = time.time() + max_wait
    stable = 0
    while time.time() < deadline:
        try:
            raw = client.eval(
                "JSON.stringify({m: typeof window.__ckReloadMark,"
                " i: document.querySelectorAll('[contenteditable=\"true\"]').length || 0})", 8000)
            st = json.loads(_plain(raw)) if isinstance(_plain(raw), str) else _plain(raw)
        except Exception:
            stable = 0
            continue
        if isinstance(st, dict) and st.get("m") == "undefined" and int(st.get("i") or 0) >= 1:
            stable += 1
            if stable >= 2:
                return True
        else:
            stable = 0
        time.sleep(0.5)
    return False


def tmp_home(prefix="ck-home-"):
    """独立 HOME：子实例 USERPROFILE/HOME 指向该目录 → usr 主库全新，不碰机器 ~/.chonkpilot。"""
    return tmp_dir(prefix)


# ══════════════════════════════════════════════════════════
# 配置快照-还原（写真实配置的用例必须配对）
# ══════════════════════════════════════════════════════════
#
# 口径（51-FP与测试映射 §6-8「配置快照-还原」）：
#   ① 凡写真实配置（usr 主库 `~/.chonkpilot`（data-user-config-*）、work-dir prj 库
#      （data-prj-config-*）、`gui.ui.save`（其 ui 段落 usr、layout/window 段落 prj））的用例，
#      一律「**快照 → 写入 → finally 还原**」，禁止写后不还原；
#   ② 还原语义 = **回原状**：原本缺省 → **删除**该键（回落系统默认/继承），原本有值 → 写回原值；
#   ③ 异常/中断路径（`finally` / 上下文管理器）同样还原。
#
# client 形参：`chonk_client.ChonkClient` 或 `drive.GUIClient` 均可（两者都有 `.req(typ, payload, timeout)`）。

# usr 库标量的**系统默认**（persist_userconfig.go:191 userConfigSystemDefaults）：
# 快照值 == 系统默认、自由键缺失、集合为空 → 视为「本就没显式配」→ 还原走**删除**，
# 避免把默认值固化成显式配置（也避免把机器偏好改成非默认值）。
# theme 缺省 2026-09-16 由 `system` 订正为 `light`（原值无实现，见 64-配置项一览 §3）。
USER_CONFIG_DEFAULTS = {
    "theme": "light",
    "locale": "zh-CN",
    "chromePath": "", "javaPath": "", "pythonPath": "", "nodePath": "",
    "goPath": "", "rustPath": "", "cCompilerPath": "",
    "responseTimeout": 120, "streamTimeout": 60, "retryCount": 2,
    "defaultScenario": "",
    "llms": [],
}

# 读取侧现算的缺省（键缺失才算，load 会给这两个值）→ 快照命中即视为「缺省」：
#   defaultLLM：有可用 llms → 0；无 → -1（persist_userconfig.go:247）。
USER_CONFIG_COMPUTED_DEFAULTS = {"defaultLLM": (0, -1)}


class ConfigSnapshot(dict):
    """配置快照（dict 子类）。

    `full=True` = **全量**快照（`keys=None`）→ 还原时额外把「快照里没有、跑完才出现」的键删除
    （回原状）；`full=False` = 部分键快照 → 只还原快照里列出的键，**绝不**触碰其它键
    （部分快照缺的键并不代表"原本没有"——usr load 恒补齐系统默认，故不得据此删除）。
    """

    def __init__(self, pairs=(), full=False):
        dict.__init__(self, pairs)
        self.full = full


def user_config_load(client):
    """读 usr 主库配置（data-user-config-load）→ 合并后的 data 对象（含系统默认兜底）。"""
    res = client.req("data-user-config-load", {})
    return (res.get("data") or {}) if isinstance(res, dict) else {}


_HANG_SRV = None


class _HangLLMHandler(BaseHTTPRequestHandler):
    """只收不回的 chat/completions 端点：连接保持到用例结束 → 该轮**不自行终态**。

    发送链路用例（CF/IT/T12/SD 等）需要「turn 起来但不自动完成」：前端在 `llm-start` 回执后
    才渲染 user 气泡 + 忙碌态，随后由用例自己注入 `llm-receive` / `session-complete`。
    指向死端口会**立刻连接失败**（无气泡、无忙碌），故用挂起端点。
    """

    protocol_version = 'HTTP/1.0'  # 与 mock_llm 一致：无 Content-Length → 读至连接关闭（我们永不关闭）

    def do_POST(self):
        n = int(self.headers.get('Content-Length', 0) or 0)
        if n:
            try:
                self.rfile.read(n)
            except Exception:
                pass
        # **先回 SSE 头 + 一个合法首 chunk，再挂起**：后端以「流已建立 / 首个 chunk」为
        # llm-start 受理时点（回执后前端才渲染 user 气泡 + 忙碌态）；chunk 形状照抄
        # mock_llm.py（只含 choices[].delta）——否则 router 判协议错误 → 立即失败终态。
        try:
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Cache-Control', 'no-cache')
            self.end_headers()
            chunk = {"choices": [{"delta": {"role": "assistant", "content": ""},
                                  "finish_reason": None}]}
            self.wfile.write(("data: %s\n\n" % json.dumps(chunk)).encode('utf-8'))
            self.wfile.flush()
        except Exception:
            return
        time.sleep(120)

    def log_message(self, *a):
        pass


def hang_llm_base():
    """进程内单例的挂起 LLM 端点 → baseUrl（`http://127.0.0.1:<port>/v1`）。

    守护线程 + 随机空闲端口，无需显式回收（进程退出即释放）。
    """
    global _HANG_SRV
    if _HANG_SRV is None:
        srv = ThreadingHTTPServer(('127.0.0.1', 0), _HangLLMHandler)
        srv.daemon_threads = True
        threading.Thread(target=srv.serve_forever, daemon=True).start()
        _HANG_SRV = srv
    return 'http://127.0.0.1:%d/v1' % _HANG_SRV.server_address[1]


def ensure_llm_configured(client, name="mock-llm", model="mock-model", base_url=None):
    """保证 usr 配置里至少有一个可用 LLM 记录（**发送链路套件的前置**，S25 口径）。

    `35-错误处理与恢复 S25` / `ChatPanel.llmConfigReady`：**无任何 LLM 选项 → 发送被拦截**
    （`message.warning` + `chat-queue-restore`），不发起 `message-send`。因此凡「断言发送成功」
    的套件必须先满足该前置；否则测到的是「被拦截」而不是发送。

    默认 baseUrl = `hang_llm_base()`（只收不回）：turn 会起来（渲染 user 气泡 + 忙碌态）但
    **不会自行完成**，用例仍可用 `llm-receive` / `session-complete` 精确控制时序。

    已有 `llms`（≥1 条）→ **不动**（不覆盖用户/他套件配置）；返回是否写入。
    记录形状对齐 `SettingsLLMPage` 保存的 usr 记录（`name`/`protocol`/`baseUrl`/`model`/`apiKey`）；
    `defaultLLM` 缺省（-1）时前端 `resolveDefaultLLM` 会回落到 `llms[0]`，无需另写。
    """
    cfg = user_config_load(client) or {}
    llms = cfg.get("llms")
    llms = llms if isinstance(llms, list) else []
    dflt = cfg.get("defaultLLM")
    has_default = isinstance(dflt, int) and dflt >= 0
    ours = [i for i, e in enumerate(llms) if isinstance(e, dict) and e.get("name") == name]
    if llms and not ours and has_default:
        return False  # 已有**真实** LLM 配置且默认项有效 → 不动
    patch = {}
    if ours:
        # 本套件自己的占位条目：baseUrl 指向**本进程**的挂起端点（跨进程会失效，必须改写）
        idx = ours[0]
        rec = dict(llms[idx])
        rec.update({"protocol": "openai", "baseUrl": base_url or hang_llm_base(),
                    "model": model, "apiKey": ""})
        new_llms = list(llms)
        new_llms[idx] = rec
        patch["llms"] = new_llms
        if not has_default:
            patch["defaultLLM"] = idx
    elif not llms:
        patch["llms"] = [{
            "name": name, "protocol": "openai", "baseUrl": base_url or hang_llm_base(),
            "model": model, "apiKey": "",
        }]
        if not has_default:
            # defaultLLM 是 llms 的下标：-1（缺省）在前端会回落到 llms[0]，但**后端**解析默认
            # provider 时依赖它 → 不写会出现「publish ok 但不起 turn」（无 user 气泡/无忙碌态）。
            patch["defaultLLM"] = 0
    if patch:
        client.req("data-user-config-save", {"data": patch})
    # ChatPanel 在**挂载时**一次性读 usr llms；本函数在 start() 之后调用 → 必须重载页面
    # 才能让新配置生效（否则 llmOptions 仍为空、发送仍被 S25 拦截）。
    # 2026-10-27（42 (290)）：重载会按 `active_session_id` 恢复上一活动会话——共享 work-dir 下
    # 可能是个 MB 级大历史，WebView2 渲染线程被首屏占满数十秒（`/eval` 超时 → 后续用例假红）。
    # 故先**建**一个空会话并把活动位指向它，再用 `reload_and_settle` 等「新文档 + 渲染线程真正可应答」。
    # （`active-set` 指向不存在的会话会被 `active-get` 判失效并回落「最近会话」，故必须先 ensure-session。）
    try:
        client.req("data-session-ensure-session", {"data": {"session_id": "llmcfg-bootstrap"}}, timeout=8000)
        client.req("data-session-active-set", {"session_id": "llmcfg-bootstrap"}, timeout=8000)
    except Exception:
        pass
    reload_and_settle(client)
    # 选中本函数保证可用的条目：usr 主库不受 work-dir 隔离，他套件遗留的默认 LLM
    # （如 8901 的 mock）可能已死 → 不显式选中会让 llm-start 静默失败（无忙碌态 / 断言串扰）。
    # 需要特定 LLM 的套件在 ensure 之后自行 `chat-select-llm` 覆盖（run_llm / test_queue 等）。
    try:
        time.sleep(0.2)
        client.eval("window.mq.emit('chat-select-llm', {name: %s}); 'ok'" % json.dumps(name))
    except Exception:
        pass
    return True


def _key_is_default(key, val):
    """快照值是否等价于「该键缺省」（→ 还原走删除）。"""
    if val is None:
        return True
    if key in USER_CONFIG_DEFAULTS and val == USER_CONFIG_DEFAULTS[key]:
        return True
    if key in USER_CONFIG_COMPUTED_DEFAULTS and val in USER_CONFIG_COMPUTED_DEFAULTS[key]:
        return True
    return False


def snapshot_user_config(client, keys):
    """快照 usr 配置的指定键（供 restore_user_config 还原）。

    keys=None → 快照 load 视图的**全部**键（含自由键如 recent_dirs）→ 允许回滚期间新增的键。
    """
    cfg = user_config_load(client)
    if keys is None:
        return ConfigSnapshot({k: v for k, v in cfg.items()}, full=True)
    return ConfigSnapshot({k: cfg.get(k) for k in keys})


def restore_user_config(client, snap):
    """按快照还原 usr 配置：缺省/空 → 删除该键；有值 → 写回原值；全量快照下「期间新增的键」→ 删除。
    （失败仅告警，不掩盖用例结果。）

    **已等于快照值的键不写回**：`data-user-config-save` 会给 `llms` 每条目重打
    `updated_at` 戳（persist 落库时生成），无谓写回会让「跑前/跑后配置一致」的校验出现纯时间戳
    差异（值语义未变）——故先读当前值，只写真正变化的键。
    """
    full = getattr(snap, "full", False)
    snap = dict(snap or {})
    cur = user_config_load(client)
    if full:
        for k in cur:
            if k not in snap:  # 跑完才出现的键 → 回原状 = 删除
                snap[k] = None
    writes, dels = {}, []
    for k, v in snap.items():
        if _key_is_default(k, v):
            dels.append(k)
        elif cur.get(k) != v:  # 已等于快照值 → 无需写回
            writes[k] = v
    if writes:
        try:
            client.req("data-user-config-save", {"data": writes})
        except Exception as e:
            print("[harness] usr 配置还原（写回 %r）失败: %s" % (sorted(writes), e), flush=True)
    for k in dels:
        try:
            # 删除必须用 **`id`** 字段（persist reqKey 只认 req.ID/req.Data["id"]/req.Data["key"]）：
            # 传顶层 `{"key":k}` 会被判为「无 key」→ 触发「清空整份 usr 配置」（把 theme/locale/
            # llms 一并清掉）；集合名（llms）→ 只清空该集合表。
            client.req("data-user-config-delete", {"id": k})
        except Exception as e:
            print("[harness] usr 配置还原（删除 %r）失败: %s" % (k, e), flush=True)


@contextlib.contextmanager
def user_config_guard(client, keys):
    """`with user_config_guard(c, ["locale","theme"]):` → 快照，退出（含异常）即还原。

    keys=None → 全量快照（见 snapshot_user_config）。
    """
    snap = snapshot_user_config(client, None if keys is None else list(keys))
    try:
        yield snap
    finally:
        restore_user_config(client, snap)


def prj_config_load(client):
    """读 prj 配置视图（data-prj-config-list）→ 平铺 map（key 含 `layout.` 等前缀）。"""
    res = client.req("data-prj-config-list", {})
    return (res.get("list") or {}) if isinstance(res, dict) else {}


def snapshot_prj_config(client, keys):
    """快照 prj 配置的指定键（不在表中 → None）。

    keys=None → 快照**全部**键（套件级整体兜底：把本套件期间新落的所有 prj 键一并回滚）。
    """
    lst = prj_config_load(client)
    if keys is None:
        return ConfigSnapshot(lst, full=True)
    return ConfigSnapshot({k: lst.get(k) for k in keys})


def restore_prj_config(client, snap):
    """按快照还原 prj 配置：快照值 None（原本无该键）→ 删除；有值 → 写回原值；
    **全量**快照下「快照里没有、跑完才出现」的键 → 删除（回原状）。

    **已等于快照值的键不写回**（同 `restore_user_config`：省掉无谓写，避免落库附加字段变动）。
    """
    full = getattr(snap, "full", False)
    snap = dict(snap or {})
    cur = prj_config_load(client)
    if full:
        for k in cur:
            if k not in snap:
                snap[k] = None
    for k, v in snap.items():
        try:
            if v is None:
                client.req("data-prj-config-delete", {"id": k})
            elif cur.get(k) != v:  # 已等于快照值 → 无需写回
                client.req("data-prj-config-save", {"data": {"key": k, "value": v}})
        except Exception as e:
            print("[harness] prj 配置还原（%s=%r）失败: %s" % (k, v, e), flush=True)


@contextlib.contextmanager
def prj_config_guard(client, keys):
    """`with prj_config_guard(c, ["memory.enabled"]):` → 快照，退出（含异常）即还原。

    keys=None → 套件级整体快照/回滚（覆盖前端交互隐式落盘的 prj 状态，如
    `layout.*`/`window.*`/`filetree-*`/`opened-files`/项目配置表单保存）。
    """
    snap = snapshot_prj_config(client, None if keys is None else list(keys))
    try:
        yield snap
    finally:
        restore_prj_config(client, snap)


def snapshot_config(client):
    """**套件级**全量快照（usr + prj **两层**，见 51 §6-8）→ 交给 restore_config 还原。

    用于 `test_*.py` 这类「套件体不重排、只加两条语句」的场景（无需包住整个 `with` 块）：
        snap = None
        try:
            gui.start()
            snap = snapshot_config(gui)
            ...
        finally:
            if snap is not None:
                restore_config(gui, snap)   # 必须在 gui.stop() 之前（要用 client）
            gui.stop()

    覆盖**前端交互隐式落盘**：prj 的 `opened-files`（文件页签）/`layout.*`/`window.*`
    （窗口位置尺寸）/`filetree-*`/项目配置表单保存；usr 的 theme/locale/llms 等偏好。
    """
    return {"usr": snapshot_user_config(client, None), "prj": snapshot_prj_config(client, None)}


def restore_config(client, snap):
    """还原 `snapshot_config` 的套件级快照（prj 先、usr 后；失败仅告警）。"""
    if not snap:
        return
    if snap.get("prj") is not None:
        restore_prj_config(client, snap["prj"])
    if snap.get("usr") is not None:
        restore_user_config(client, snap["usr"])


# ── 套件级「自动兜底」快照-还原（`run_*.py` 用；`test_*.py` 仍用 snapshot_config/restore_config）──
#
# 差别只在**还原时机**：`test_*.py` 在 `finally: restore_config(gui, snap)` 里手动还原；
# `run_*.py` 体量小、多为直线脚本（无统一 try/finally），故提供一个自动兜底：arm 一次即可，
# 还原在**退出前**自动执行——① 正常/异常/断言失败退出：`atexit`（本函数注册晚于
# `cleanup_all` → LIFO **先于** GUI 回收执行，client 仍可用）；② Ctrl+C / 关窗：信号处理器里
# 在 `cleanup_all()` **之前**执行。语义与 `finally` 等价（回原状：原本缺省 → 删除该键）。
_CONFIG_GUARDS = []


def suite_config_guard(client, keys=None):
    """arm 套件级配置快照-还原（立即快照 usr + prj 两层，退出前自动还原）；返回快照。

        _h.suite_config_guard(c)     # 一行接入，无需包 try/finally

    `client` 可传 `ChonkClient` / `drive.GUIClient`，或**零参可调用**（返回“当前 client”）
    ——供中途重启 GUI 的套件（如 `run_tool_async`）使用。keys 目前恒为全量（None）。
    """
    if keys is not None:
        raise ValueError("suite_config_guard 目前只支持全量快照（keys=None）")
    cl = client() if callable(client) else client
    snap = snapshot_config(cl)
    _CONFIG_GUARDS.append((client, snap))
    return snap


def flush_config_guards():
    """**立即**执行已登记的套件级还原（供「中途要停掉 client 所属实例」的套件在 stop 前调用）。

    背景：`suite_config_guard` 的还原挂在 `atexit`（先于 GUI 回收，client 仍可用）。但若套件在
    退出前**主动 `stop()` 了底座实例**（如本套件的 D/D2 走独立实例体例），退出期还原已无连接
    （`WinError 10061`）→ 必须先在有 client 时还原，再停实例。
    """
    _restore_config_guards()


def _restore_config_guards():
    """执行全部已登记套件级还原（幂等；后进先出）。"""
    while _CONFIG_GUARDS:
        client, snap = _CONFIG_GUARDS.pop()
        try:
            cl = client() if callable(client) else client
            restore_config(cl, snap)
        except Exception as e:
            print("[harness] 套件级配置还原失败: %s" % e, flush=True)


# ══════════════════════════════════════════════════════════
# 资源登记与回收（finally 之外的兜底：atexit + 信号）
# ══════════════════════════════════════════════════════════

_RESOURCES = []
_TMP_DIRS = []
_CLEANING = [False]


def register(res):
    _RESOURCES.append(res)
    return res


def unregister(res):
    if res in _RESOURCES:
        _RESOURCES.remove(res)


def cleanup_all(verbose=True):
    """回收全部**自起**资源（幂等）：先杀进程树，再删临时目录。"""
    if _CLEANING[0]:
        return
    _CLEANING[0] = True
    try:
        for res in list(reversed(_RESOURCES)):
            try:
                res.stop()
            except Exception as e:
                if verbose:
                    print("[harness] 回收失败 %r: %s" % (res, e), flush=True)
        for d in list(_TMP_DIRS):
            # `taskkill /F /T` 之后**文件句柄释放是异步的**（WebView2 profile / bbolt 库），
            # 立即 rmtree 会因占用而**静默失败**（ignore_errors）→ 临时目录残留（实测：
            # 自起实例结束后 `ck-home-*` 仍留 81 项）。故按短间隔重试若干次。
            # 窗口 6s（12 × 0.5s）：实测 3s 偶尔不足（`chonk-hist-*` / `ck-c8-*` 残留 2026-09-17；
            # 事后手工删除即成功 → 纯时序竞态、非永久占用）；6s 仍可能输给极慢释放（同批实测 1 例），
            # 此时按下方提示打印「仍存在」，由收尾清理兜底。
            for _try in range(12):
                shutil.rmtree(d, ignore_errors=True)
                if not os.path.isdir(d):
                    break
                time.sleep(0.5)
            if os.path.isdir(d) and verbose:
                print("[harness] 临时目录删除失败（仍存在）: %s" % d, flush=True)
        _TMP_DIRS[:] = []
    finally:
        _CLEANING[0] = False


class ProcResource:
    """自起子进程资源：owned=True 才回收（复用的外部实例 owned=False 永不动）。

    `port` 非空且是 GUI 实例（`--test-port`）时，收尾**优先优雅退出**：`POST /quit` 让宿主走
    与托盘「退出」完全相同的路径（关项目窗口 → 关日志窗口 → Shell_NotifyIcon 摘托盘图标 →
    进程结束）。直接 `taskkill /F` 不摘托盘图标 → 每跑一个套件系统托盘就多一个残影
    （2026-10-23 用户实测反馈）。优雅退出失败/超时才兜底 kill。
    """

    kind = "proc"

    def __init__(self, proc=None, name="proc", owned=True, port=None):
        self.proc = proc
        self.name = name
        self.owned = owned
        self.port = port

    def alive(self):
        return self.proc is not None and self.proc.poll() is None

    def pid(self):
        return self.proc.pid if self.proc is not None else None

    def _quit_gracefully(self, timeout=15.0):
        """请求 `POST /quit` 并等待进程退出；端口未开/未接线 → False（调用方兜底 kill）。"""
        if not self.port:
            return False
        import urllib.request
        try:
            req = urllib.request.Request(
                "http://127.0.0.1:%d/quit" % int(self.port), data=b"{}",
                headers={"Content-Type": "application/json"})
            with urllib.request.urlopen(req, timeout=3) as resp:
                resp.read()
        except Exception:
            return False
        deadline = time.time() + timeout
        while time.time() < deadline:
            if not self.alive():
                return True
            time.sleep(0.2)
        return False

    def stop(self):
        if self.owned and self.alive():
            print("[harness] 优雅退出 %s (pid=%s)" % (self.name, self.pid()), flush=True)
            if self._quit_gracefully():
                try:
                    self.proc.wait(timeout=10)
                except Exception:
                    pass
            if self.alive():
                print("[harness] 回收 %s (pid=%s)" % (self.name, self.pid()), flush=True)
                kill_tree(self.pid())
                try:
                    self.proc.wait(timeout=10)
                except Exception:
                    try:
                        self.proc.kill()
                    except Exception:
                        pass
        unregister(self)

    def __repr__(self):
        return "%s(%s, pid=%s, owned=%s)" % (self.kind, self.name, self.pid(), self.owned)


class GUIHandle(ProcResource):
    """GUI 实例句柄：`.client` 是 ChonkClient，`.port`/`.work_dir`/`.data_dir` 供套件复用。

    `.data_dir` = **prjusr 数据根**（DATAROOT-1 固定规则：`<work_dir>/.chonkpilot/users/<OS 用户名>`）；
    2026-10-25 起 GUI 无 `--data-dir` 启动参数，该值由 harness 按同一规则算出（供套件做落盘断言）。
    """

    kind = "gui"

    def __init__(self, proc, port, work_dir, data_dir=None, owned=True, ready_timeout=90,
                 plugin_uds=None):
        ProcResource.__init__(self, proc=proc, name="gui:%d" % port, owned=owned, port=port)
        self.work_dir = work_dir
        self.data_dir = data_dir
        self.plugin_uds = plugin_uds
        self.client = wait_ready(port, timeout=ready_timeout)
        # 启动就绪栏栅（2026-09-23，[42 §2 (148)]）：`/ping ready` 由**宿主**应答（首次导航完成），
        # **早于**前端首屏 bundle 执行完毕 —— 此刻 WebView2 UI 线程仍繁忙，紧随其后的首个 `/eval`
        # 可能瞬时超时（`ExecuteScript timed out`，见 `chonkpilot-webview2/webview.go:675`）→
        # 套件启动探测崩溃（实跑证据：负载批跑 `test_statusbar.py:48` → 21/22）。
        # 故在**唯一起停入口**处有界等待主视图挂载（`.panel-inner` = 主聊天面板，各 GUI 套件共用），
        # 并容忍瞬时基础设施错误（重试；非瞬时异常照旧上抛）。只影响「何时开始」，不改任何断言。
        # 追加「UI 线程空闲」栏栅：DOM 已挂载 ≠ UI 线程空闲（首屏渲染/文件树首扫/引擎调用仍在跑），
        # 否则套件启动后**首个断言 eval** 会撞上该窗口而超时崩溃（见 wait_idle 注释）。
        wait_probe(self.client, ["!!document.querySelector('.panel-inner')"], timeout=30.0)
        wait_idle(self.client, max_wait=30.0)
        # 批跑基线语言（每套件一次；`run_all.py` 注入 CHONK_TEST_LOCALE）——见函数注释。
        ensure_baseline_locale(self.client)


def ensure_project_spec(work_dir):
    """夹具：确保 `<workDir>/.chonkpilot/project_spec.md` 存在。

    用途：GUI 启动时桥检测该文件缺失 → 下发 `agent-wizard` + `wizard_required` → 前端**自动弹出**
    场景向导对话框（`docs/agents-wizard/01 §1`）。非向导套件应模拟「已初始化项目」→ 预置该文件，
    避免向导弹窗遮挡 UI（`elementFromPoint` 命中 / 未限定 `.dialog-shell` 的对话框选择均会误命中）。
    向导**自动弹出**行为本身另由专用套件覆盖。
    """
    spec = os.path.join(work_dir, ".chonkpilot", "project_spec.md")
    os.makedirs(os.path.dirname(spec), exist_ok=True)
    if not os.path.exists(spec):
        open(spec, "w", encoding="utf-8").close()


def prjusr_root(work_dir, home=None):
    """prjusr 数据根（DATAROOT-1 固定规则）= `<work_dir>/.chonkpilot/users/<OS 用户名>`。

    `<OS 用户名>` = home 目录 basename（Go `os.UserHomeDir()` 在 Windows 取 `USERPROFILE`）；
    harness 自起实例时 home 经 env 注入，故此处与 GUI 侧解析口径一致。
    """
    h = home or os.environ.get("USERPROFILE") or os.path.expanduser("~")
    user = os.path.basename(os.path.normpath(h)) or "default"
    return os.path.join(work_dir, ".chonkpilot", "users", user)


def start_gui(port=None, work_dir=None, data_dir=None, home=None, exe=None,
              ready_timeout=90, extra_args=(), no_window=True, plugin_addr=None):
    """自起 GUI（未给 port 则取空闲端口）；登记为结束时回收。

    `data_dir` 参数**已废弃**（GUI 无 `--data-dir` 启动参数，2026-10-25 删除）：传了也忽略，
    句柄上的 `.data_dir` 恒为按 DATAROOT-1 规则算出的 prjusr 数据根。

    显式给 port 时先**等该端口真正空闲**（上一实例 taskkill 后的异步释放窗口），避免新实例与
    残留实例抢端口（I-74：同 work-dir/同端口双实例 → test-port 不就绪）。
    """
    port = int(port or free_port())
    if not wait_port_free(port):
        print("[harness] 警告：端口 %d 仍被占用（15s）→ 仍尝试自起（可能因端口冲突失败）" % port, flush=True)
    work_dir = work_dir or DEFAULT_WS
    os.makedirs(work_dir, exist_ok=True)
    # 夹具：预置工程规格文件 → 抑制「场景向导」启动自动弹出（该特性仅在缺 project_spec.md 时触发；
    # 非向导套件应模拟「已初始化项目」）。向导自动弹出另由专用套件覆盖。
    ensure_project_spec(work_dir)
    cmd = [exe or resolve_gui_exe(), "--test-port=%d" % port, "--work-dir=%s" % work_dir]
    cmd.extend(extra_args)
    # 插件桥地址一实例一份（BRIDGE-2 测试隔离）：`CHONK_PLUGIN_UDS` 覆盖 UDS 路径；
    # 显式给 plugin_addr（如 tcp://127.0.0.1:PORT）→ `CHONK_PLUGIN_LISTEN`，用于 Python
    # 无 AF_UNIX 的平台（Windows）驱动插件面验收。
    if plugin_addr:
        plugin_uds = plugin_addr
        plugin_env = {"CHONK_PLUGIN_LISTEN": plugin_addr}
    else:
        plugin_uds = os.path.join(tempfile.gettempdir(), "ck-plugin-%d.sock" % port)
        plugin_env = {"CHONK_PLUGIN_UDS": plugin_uds}
    env = dict(os.environ)
    env.update(plugin_env)
    if home:
        os.makedirs(home, exist_ok=True)
        env["USERPROFILE"] = home   # Go os.UserHomeDir() 在 Windows 取 USERPROFILE
        env["HOME"] = home
    flags = (CREATE_NO_WINDOW if no_window else 0) | ABOVE_NORMAL_PRIORITY_CLASS
    proc = subprocess.Popen(cmd, cwd=os.path.dirname(cmd[0]), env=env, creationflags=flags)
    res = ProcResource(proc=proc, name="gui:%d" % port, port=port)
    register(res)
    try:
        h = GUIHandle(proc, port, work_dir, prjusr_root(work_dir, home), owned=True,
                      ready_timeout=ready_timeout, plugin_uds=plugin_uds)
    except Exception:
        res.stop()
        raise
    # GUIHandle 与登记项共享同一 proc/owned 语义：替换登记项，避免重复回收
    unregister(res)
    return register(h)


def acquire_gui(port=None, work_dir=None, data_dir=None, home=None, exe=None,
                ready_timeout=90, reuse=True, extra_args=()):
    """**复用优先**：端口已有**稳定就绪**实例 → 返回 owned=False 句柄（不回收）；否则自起。

    「稳定就绪」= `stable_ready()`（连续多次 /ping ready）——只复用真正活着的实例，避免把
    「正在退出/被上一脚本回收中」的实例当成可复用实例（见 stable_ready 注释里的实跑证据）。
    reuse=False 时强制自起（如 run_filetree 需要独占 work-dir 的场景）。
    """
    if port and reuse and stable_ready(port):
        print("[harness] 复用既有 GUI 实例 127.0.0.1:%d（不回收）" % int(port), flush=True)
        h = GUIHandle(None, int(port), work_dir or DEFAULT_WS,
                      prjusr_root(work_dir or DEFAULT_WS, home), owned=False)
        return h
    return start_gui(port, work_dir, data_dir, home, exe, ready_timeout, extra_args)


def start_mock_llm(port=DEFAULT_MOCK_LLM_PORT, ready_timeout=30):
    """自起 mock LLM（HTTP）；登记为结束时回收（不再需要手工常驻）。"""
    port = int(port)
    proc = subprocess.Popen([sys.executable, MOCK_LLM, str(port)],
                            cwd=HERE, creationflags=CREATE_NO_WINDOW)
    res = register(ProcResource(proc=proc, name="mock_llm:%d" % port, port=port))
    if not wait_port(port, ready_timeout):
        res.stop()
        raise RuntimeError("mock_llm 端口 %d 在 %ss 内未监听" % (port, ready_timeout))
    return res


def acquire_mock_llm(port=DEFAULT_MOCK_LLM_PORT, reuse=True):
    """复用优先的 mock LLM：已在监听则直接复用（owned=False）。"""
    port = int(port)
    if reuse and port_open(port):
        print("[harness] 复用既有 mock LLM 127.0.0.1:%d（不回收）" % port, flush=True)
        return ProcResource(proc=None, name="mock_llm:%d" % port, owned=False, port=port)
    return start_mock_llm(port)


def popen_own(cmd, name="proc", cwd=None, **kw):
    """通用自起子进程（夹具/引擎等），登记回收；返回 ProcResource。

    从命令行里的 `--test-port=NNNN` 自动识别 GUI 测试通道端口 → 收尾优先优雅退出（见
    ProcResource.stop），避免 taskkill 留下托盘图标残影。
    """
    if "creationflags" not in kw:
        kw["creationflags"] = CREATE_NO_WINDOW
    port = None
    for a in cmd:
        if isinstance(a, str) and a.startswith("--test-port="):
            try:
                port = int(a.split("=", 1)[1])
            except ValueError:
                port = None
    proc = subprocess.Popen(cmd, cwd=cwd, **kw)
    return register(ProcResource(proc=proc, name=name, port=port))


# ── 三条兜底路径：atexit + 信号 ────────────────────────────

atexit.register(cleanup_all)
# 注册**晚于** cleanup_all → LIFO 先跑：套件级配置还原在 GUI 回收之前（要用 client）
atexit.register(_restore_config_guards)


def _signal_cleanup(signum, _frame):
    print("\n[harness] 收到信号 %s → 回收自起资源" % signum, flush=True)
    _restore_config_guards()  # 先还原配置（client 尚在），再回收进程
    cleanup_all()
    try:
        signal.signal(signum, signal.SIG_DFL)
    except Exception:
        pass
    raise SystemExit(128 + int(signum))


for _sig_name in ("SIGINT", "SIGTERM", "SIGBREAK"):
    _sig = getattr(signal, _sig_name, None)
    if _sig is not None:
        try:
            signal.signal(_sig, _signal_cleanup)
        except Exception:
            pass


def main():
    """自检：起 GUI（动态端口）→ ping → 起 mock LLM → 全部回收 → 端口应释放。"""
    g = start_gui()
    m = start_mock_llm(free_port())
    print("gui port=%d pid=%s / mock port=%d pid=%s" % (g.port, g.pid(), m.port, m.pid()))
    assert ping_ready(g.port), "GUI 未就绪"
    assert port_open(m.port), "mock LLM 未监听"
    gport, mport = g.port, m.port
    g.stop()
    m.stop()
    time.sleep(1.5)
    assert not port_open(gport), "GUI 端口未释放"
    assert not port_open(mport), "mock 端口未释放"
    print("harness 自检通过：起停 + 端口释放 OK")


if __name__ == "__main__":
    main()
