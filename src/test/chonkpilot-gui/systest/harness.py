# -*- coding: utf-8 -*-
"""systest 统一资源管理：**按需加载 + 结束即回收**。

规范见 `docs/spec/50-testing/51-FP与测试映射.md` §测试资源规范。要点：

1. **按需加载**：套件用到什么才拉起什么（GUI / mock LLM / 夹具），不再要求外部先手工
   常驻一个底座实例。
2. **复用优先**：`acquire_*` 若发现目标端口已有就绪实例 → 直接复用（`owned=False`），
   **绝不回收**；端口空闲才自起（`owned=True`）。
3. **结束即回收**：本模块自起的资源一律登记在册，三条路径都会 `taskkill /F /T` 杀整棵
   进程树（含 WebView2 / 引擎子进程）：① 套件显式 `stop()`（`finally`）；② 进程正常退出
   （`atexit`）；③ Ctrl+C / 关闭（SIGINT / SIGTERM / SIGBREAK 处理器）。
4. **隔离**：`free_port()` 取空闲端口；`tmp_dir()` / `tmp_home()` 给独立数据根与独立
   `USERPROFILE`（usr 主库不读不写机器 `~/.chonkpilot`）。
5. **配置快照-还原**：写真实配置（usr 主库 / work-dir prj 库 / `gui.ui.save`）的用例一律
   「快照 → 写入 → `finally` 还原」，复用 `snapshot_user_config` / `restore_user_config`
   （或 `with user_config_guard(c, keys)`）与 `snapshot_prj_config` / `prj_config_guard`；
   **套件级**整体兜底（usr + prj 两层一次快照/还原）用 `snapshot_config` / `restore_config`。

用法：
    from harness import acquire_gui, acquire_mock_llm, free_port, tmp_dir, user_config_guard

    g = acquire_gui(free_port(), work_dir=tmp_dir("ck-ws-"), data_dir=tmp_dir("ck-dd-"))
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
import time
import urllib.request

_HERE = os.path.dirname(os.path.abspath(__file__))
if _HERE not in sys.path:
    sys.path.insert(0, _HERE)

from chonk_client import ChonkClient  # noqa: E402
from chonk_client import TRANSIENT_ERRORS as TRANSIENT_PROBE_ERRORS  # noqa: E402

_REPO_ROOT = os.path.abspath(os.path.join(_HERE, "..", "..", "..", ".."))

# GUI 产物候选（顺序 = 优先级）：dist/desktop（主力发行目录）→ src/gui/dist（旧路径回落）。
# 产物名（2026-09-21，D-27 第二刀）：`chonkpilot-gui.exe` → **`chonkpilot.exe`**（桌面单体宿主）。
GUI_EXE_CANDIDATES = (
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
# 见 `src/lib/go-webview2/webview.go:639`）在预算内拿不到调度 → `ExecuteScript timed out`
# 使套件崩溃（实测 2026-09-23：负载 5 连跑第 4 轮 `test_toolbar` + `test_filetree` 首个断言 eval 双双超时）。
# 给**被测应用**一个「高于普通」的调度份额，消除与本轮断言无关的饥饿型假失败；
# 不改任何断言、不改变被测逻辑（只影响调度）。
ABOVE_NORMAL_PRIORITY_CLASS = getattr(subprocess, "ABOVE_NORMAL_PRIORITY_CLASS", 0x00008000)


def resolve_gui_exe():
    """按候选顺序取首个存在的 GUI 产物；均不存在时返回首个候选（由启动报错）。"""
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

    起因（2026-09-16 实测复现）：上一套件/上一个脚本 `stop()` 发的是 `taskkill /F /T`，**端口释放
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
# `webview2.EvalWithResult`（`src/lib/go-webview2/webview.go:639`）把 ExecuteScript **派发到
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


def ensure_locale(client, locale="zh-CN", timeout=60):
    """把本实例前端语言固定为 locale（localStorage 置位 + 重载）——**语言确定性**（兜底）。

    必要性：`gui.ui.save{ui:{locale}}` 落 **usr 主库**（`bridge/local.go:207 callSaveUIState`
    → `data-user-config-save`；非 work-dir 项目库），`gui.init-data` 的 `ui.locale` 即取自此，
    `MainLayout` 在 localStorage 缺失时按它兜底 → 若某套件把 usr `locale` 改成 en-US 且不还原，
    同批"zh-CN 文案断言"就会随套件顺序漂移（实测：run_config_ui / run_project_cfg /
    run_fp_extra_ui / run_server_deps / run_explore_kb / run_ui_regressions 会话语言变 en-US 而 FAIL）。
    localStorage 属**本实例专属** WebView2 profile（I-68 每实例一份）→ 置位后重载即生效，
    不写 DB、不影响他套件；目标语言已就位时直接返回（幂等，不重载）。

    定位（2026-09-16 起）：本文为**兜底**；漂移源头已由各套件的「配置快照-还原」
    （见 `snapshot_user_config` / `user_config_guard`）消除，正常不应再被依赖。
    """
    try:
        cur = _plain(client.eval("localStorage.getItem('chonkpilot-locale')"))
    except Exception:
        return False
    if cur == locale:
        return True
    try:
        client.eval("localStorage.setItem('chonkpilot-locale', %s); 'ok'" % json.dumps(locale))
        client.eval("location.reload(); 'ok'")  # 重载期间连接可能中断 → 容错
    except Exception:
        pass
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            if _plain(client.eval("typeof document!=='undefined' && !!document.querySelector('.statusbar')")):
                return True
        except Exception:
            pass
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
    "responseTimeout": 120, "streamTimeout": 60, "retryCount": 2, "retryDelay": 5,
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
    """自起子进程资源：owned=True 才回收（复用的外部实例 owned=False 永不动）。"""

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

    def stop(self):
        if self.owned and self.alive():
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
    """GUI 实例句柄：`.client` 是 ChonkClient，`.port`/`.work_dir`/`.data_dir` 供套件复用。"""

    kind = "gui"

    def __init__(self, proc, port, work_dir, data_dir=None, owned=True, ready_timeout=90):
        ProcResource.__init__(self, proc=proc, name="gui:%d" % port, owned=owned, port=port)
        self.work_dir = work_dir
        self.data_dir = data_dir
        self.client = wait_ready(port, timeout=ready_timeout)
        # 启动就绪栏栅（2026-09-23，[42 §2 (148)]）：`/ping ready` 由**宿主**应答（首次导航完成），
        # **早于**前端首屏 bundle 执行完毕 —— 此刻 WebView2 UI 线程仍繁忙，紧随其后的首个 `/eval`
        # 可能瞬时超时（`ExecuteScript timed out`，见 `src/lib/go-webview2/webview.go:639`）→
        # 套件启动探测崩溃（实跑证据：负载批跑 `test_statusbar.py:48` → 21/22）。
        # 故在**唯一起停入口**处有界等待主视图挂载（`.panel-inner` = 主聊天面板，各 GUI 套件共用），
        # 并容忍瞬时基础设施错误（重试；非瞬时异常照旧上抛）。只影响「何时开始」，不改任何断言。
        # 追加「UI 线程空闲」栏栅：DOM 已挂载 ≠ UI 线程空闲（首屏渲染/文件树首扫/引擎调用仍在跑），
        # 否则套件启动后**首个断言 eval** 会撞上该窗口而超时崩溃（见 wait_idle 注释）。
        wait_probe(self.client, ["!!document.querySelector('.panel-inner')"], timeout=30.0)
        wait_idle(self.client, max_wait=30.0)


def start_gui(port=None, work_dir=None, data_dir=None, home=None, exe=None,
              ready_timeout=90, extra_args=(), no_window=True):
    """自起 GUI（未给 port 则取空闲端口）；登记为结束时回收。

    显式给 port 时先**等该端口真正空闲**（上一实例 taskkill 后的异步释放窗口），避免新实例与
    残留实例抢端口（I-74：同 work-dir/同端口双实例 → test-port 不就绪）。
    """
    port = int(port or free_port())
    if not wait_port_free(port):
        print("[harness] 警告：端口 %d 仍被占用（15s）→ 仍尝试自起（可能因端口冲突失败）" % port, flush=True)
    work_dir = work_dir or DEFAULT_WS
    os.makedirs(work_dir, exist_ok=True)
    cmd = [exe or resolve_gui_exe(), "--test-port=%d" % port, "--work-dir=%s" % work_dir]
    if data_dir:
        os.makedirs(data_dir, exist_ok=True)
        cmd.append("--data-dir=%s" % data_dir)
    cmd.extend(extra_args)
    env = None
    if home:
        os.makedirs(home, exist_ok=True)
        env = dict(os.environ)
        env["USERPROFILE"] = home   # Go os.UserHomeDir() 在 Windows 取 USERPROFILE
        env["HOME"] = home
    flags = (CREATE_NO_WINDOW if no_window else 0) | ABOVE_NORMAL_PRIORITY_CLASS
    proc = subprocess.Popen(cmd, cwd=os.path.dirname(cmd[0]), env=env, creationflags=flags)
    res = ProcResource(proc=proc, name="gui:%d" % port, port=port)
    register(res)
    try:
        h = GUIHandle(proc, port, work_dir, data_dir, owned=True, ready_timeout=ready_timeout)
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
        h = GUIHandle(None, int(port), work_dir or DEFAULT_WS, data_dir, owned=False)
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
    """通用自起子进程（夹具/引擎等），登记回收；返回 ProcResource。"""
    if "creationflags" not in kw:
        kw["creationflags"] = CREATE_NO_WINDOW
    proc = subprocess.Popen(cmd, cwd=cwd, **kw)
    return register(ProcResource(proc=proc, name=name))


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
