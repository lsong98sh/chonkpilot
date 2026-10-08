# -*- coding: utf-8 -*-
"""L4 端到端：统一异步模型「超时交用户裁决」（2026-09-13）+ MCP 配置面打通。

前置（父会话启动）：
  1. 重建产物：`.\\build-desktop.ps1`（本次不实跑）。
  2. IDE：`dist-desktop\\chonkpilot.exe --test-port=2345 --work-dir <大写盘符路径>`。
  3. mock LLM：`python mock_llm.py 8901`（经 llm-start 触发工具调用）。
  4. 用户配置（usr 库，经 data-user-config-load/save 消息面）的 llms 含 mock 条目
     （与 run_llm.py 同前置；%USERPROFILE%/.chonkpilot/config.json 为死文件，不再读写）。
运行：`python run_tool_async.py`

────────────────────────────────────────────────────────────────────────────
【核实结论（read bridge/gateway/llm-server + 实测，2026-09-13）】

A. 超时配置来源（gateway doCall 有效值）：
   **工具 `_meta.timeout` 显式 0 / -1（无上限）绝对优先、不可被覆盖**（用户口径 2026-09-27：
   「无上限」必须是绝对的）—— 此时调用级 / 条目级 / 全局超时均**不得覆盖**（不设裁决点，永远等、可取消）。
   否则按优先级高→低：调用级 `timeout`/`_timeout`（args 内，执行前剥离） > 条目级 `mcp_server.timeout`
   （ServerEntry.TimeoutSec） > 工具 `_meta.timeout`（正数） > 全局 `CallTimeout`（GUI 内嵌 60s）；
   键缺失 = 回落全局 CallTimeout。
   本用例用**调用级**最稳：mock LLM 回 tool_call 时带 `timeout:1` + `async:"never"`（该第三方工具 `_meta`
   无 timeout 键 → 不触发「绝对无上限」，调用级正数照常生效）。

B. 可达性（GUI test-port ≤ 桥 `PublishEvent`）：
   - `data-<domain>-*` 前缀 → 经总线 persist 服务应答（桥 dataViaPersist）→ **可达**。
     MCP 配置面即走此路：`data-mcp-save {data:{name,runtime,args,...}}` → 落四级文件化配置
     （`<级别>/capability/mcps/<名>.json`）。
   - 桥 `frontMethodSubjects` 白名单含 `mcp-tools-wait`（超时裁决「等待完成」）与 `task-stop`
     （**统一停止入口**：2026-09-18 起取代已移除的 `mcp-tasks-cancel`）。**已修**：`mcp-tools-wait`
     映射到 gateway 方法面**同名**相对主题 `mcp-tools-wait`（bridge.go:312；wiring_test.go 锁定）
     ——**非** `tools/wait`。
   - **2026-09-18 方法面移除**：`mcp-tasks-status|result|list|cancel`、`mcp-tools-background`、
     HTTP `/mcp/tasks` 已删；状态查询改读**层权威行**（`data-tasktree-tasks` 行 `state`，属 data 面
     可达）；取消改走 `task-stop`（工具行只发 `tool_call_id`，服务端反归一）。
   - `servers/register` 非前端通道；第三方 spawned server 的**正确接入路径** = 四级文件化 MCP
     （装配层在 **llm server 启动期** 读四级视图 → `ServerEntry.Runtime` → gateway spawned 拉起；
     D-18：保存后**需重启生效**，v1 不热重载）。

C. 夹具 `mock_slow_mcp.py`：stdio MCP server（纯标准库，逐行 JSON-RPC）。经四级文件 MCP 注册
   （`runtime` = 可执行/解释器单 token、`args` = 参数数组，**逐个作为 exec 参数**传递——路径含空格
   不再被切碎）后由 gateway 拉起；暴露名带节点前缀 = `slow3p_slow_sleep` / `slow3p_fast_echo`。
   取消 = kill 该 provider 子进程 + 按 restart 策略 respawn（第三方案例 D 用 pid 变化确权）。

D. 配置⑤ 超时自动取消（2026-10-07，spec 18-工具异步超时与取消 §7 B1–B4）：
   usr `tool_async.<工具暴露名>.cancel_on_timeout`（秒，> 0 生效、默认 0 = 不取消）→ gateway `doCall`
   到超时点**直接取消**（不发 mcp-tools-timeout、不等用户裁决）→ `onTaskCancel` 按执行线终止
   （spawned = kill + respawn；内嵌执行器 = 协作式，真停在执行体侧）。用例 I（第三方 stdio：层行
   cancelled + 快工具 pid 变化）、J（内置 `script_run`：层行 cancelled + 无孤儿 ping 进程）。
────────────────────────────────────────────────────────────────────────────
"""

import json
import os
import re
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import run_llm
import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
from chonk_client import ChonkClient, TestError, run_case

c: ChonkClient = run_llm.c

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURE = os.path.join(HERE, "mock_slow_mcp.py")
PY = sys.executable or "python"
# 产物 GUI + 工作目录（systest/ws）。仓库根 = systest 上溯四级（systest → chonkpilot-gui → test → src → 仓库根）。
_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(HERE))))
EXE = os.path.join(_ROOT, "dist", "desktop", "chonkpilot.exe")
if not os.path.exists(EXE):
    EXE = r"E:\BizWorks\chonkpilot\dist\desktop\chonkpilot.exe"
WS = os.path.join(HERE, "ws")
PORT = 2345

SLOW_NAME = "slow3p"           # 四级文件 MCP 条目名（节点前缀 = slow3p_）
SLOW_ECHO = "slow3p_fast_echo"  # 快工具（回显 pid，供 respawn 比对）
SLOW_SLEEP = "slow3p_slow_sleep"  # 慢工具（第三方软缺省 never）
CALL_TIMEOUT_S = 1              # 调用级 timeout（秒）

EVENTS = ["turn-start", "llm-tool-call", "tool-pair", "tool-result", "llm-complete",
          "mcp-tools-timeout", "tasks.started", "tasks.updated", "tasks.done"]


def _canon(tool):
    """工具名归一：剥离网关自节点前缀 self_。"""
    t = tool or ""
    return t[5:] if t.startswith("self_") else t


def _loads(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


# ── 夹具自检（spawn + stdio JSON-RPC 逐行）─────────────────────────────

def _rpc(proc, obj, timeout=10):
    """写一条 JSON-RPC 帧并读回一条（夹具逐行应答）。"""
    proc.stdin.write(json.dumps(obj) + "\n")
    proc.stdin.flush()
    line = proc.stdout.readline()
    if not line:
        raise TestError("夹具 stdout 无应答（进程疑似退出）")
    return json.loads(line)


def _fixture_selftest():
    """启动夹具 → initialize → tools/list → fast_echo → slow_sleep(1s) → kill。"""
    proc = subprocess.Popen([PY, FIXTURE, "--sleep", "3"],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True, encoding="utf-8")
    try:
        r = _rpc(proc, {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                        "params": {"protocolVersion": "2024-11-05",
                                   "capabilities": {}, "clientInfo": {"name": "l4", "version": "1"}}})
        if (r.get("result") or {}).get("serverInfo", {}).get("name") != "mock-slow-mcp":
            raise TestError("initialize 应答异常: %s" % r)
        if not (r.get("result") or {}).get("capabilities", {}).get("tools"):
            raise TestError("initialize 缺 tools 能力: %s" % r)
        r = _rpc(proc, {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
        names = [t.get("name") for t in (r.get("result") or {}).get("tools", [])]
        if "slow_sleep" not in names or "fast_echo" not in names:
            raise TestError("tools/list 缺 slow_sleep/fast_echo: %s" % names)
        r = _rpc(proc, {"jsonrpc": "2.0", "id": 3, "method": "tools/call",
                        "params": {"name": "fast_echo", "arguments": {"text": "ping"}}})
        txt = ((r.get("result") or {}).get("content") or [{}])[0].get("text", "")
        if "echo: ping" not in txt:
            raise TestError("fast_echo 结果异常: %s" % r)
        t0 = time.time()
        r = _rpc(proc, {"jsonrpc": "2.0", "id": 4, "method": "tools/call",
                        "params": {"name": "slow_sleep", "arguments": {"seconds": 1}}}, timeout=15)
        txt = ((r.get("result") or {}).get("content") or [{}])[0].get("text", "")
        if "slept 1" not in txt:
            raise TestError("slow_sleep 结果异常: %s" % r)
        if time.time() - t0 < 0.9:
            raise TestError("slow_sleep 未按参数睡眠（耗时 %.2fs）" % (time.time() - t0))
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


# ── A 经四级文件化 MCP 注册第三方 spawned server（MCP 配置面打通）────────

def _load_mcps():
    res = c.req("data-mcp-list", {})
    return list((res or {}).get("list") or [])


def _snapshot_mcp(name):
    """快照同名 user 级 MCP 条目（data-mcp-load；不存在 → None），供套件退出还原。"""
    try:
        res = c.req("data-mcp-load", {"name": name})
    except Exception:
        return None
    d = (res or {}).get("data")
    return d if isinstance(d, dict) and d.get("name") else None


def _restore_mcp(name, snap):
    """按快照还原：原本存在 → 写回；原本不存在 → 按名删（回原状）。失败仅告警。"""
    try:
        if snap is None:
            c.req("data-mcp-delete", {"name": name}, timeout=15000)
        else:
            c.req("data-mcp-save", {"data": snap}, timeout=15000)
    except Exception as e:
        print("[run_tool_async] MCP 文件还原失败（%s）: %s" % (name, e), flush=True)


# ── usr `tool_async` 快照-还原（配置⑤ 用例写 cancel_on_timeout，须收敛回原状）────────

TOOL_ASYNC_KEY = "tool_async"


def _snapshot_tool_async():
    """快照 usr `tool_async`（data-user-config-load；不存在 → None），供套件退出还原。"""
    try:
        res = c.req("data-user-config-load", {})
    except Exception:
        return None
    return ((res or {}).get("data") or {}).get(TOOL_ASYNC_KEY)


def _save_tool_async(value):
    c.req("data-user-config-save", {"data": {TOOL_ASYNC_KEY: value}}, timeout=15000)


def _restore_tool_async(snap):
    """按快照还原 usr `tool_async`：原本存在 → 写回；原本不存在 → 删键。失败仅告警。"""
    try:
        if snap is None:
            c.req("data-user-config-delete", {"id": TOOL_ASYNC_KEY}, timeout=15000)
        else:
            c.req("data-user-config-save", {"data": {TOOL_ASYNC_KEY: snap}}, timeout=15000)
    except Exception as e:
        print("[run_tool_async] usr tool_async 还原失败: %s" % e, flush=True)


def _wait_layer_state(sid, tcid, states, max_wait=25):
    """等该 tool_call_id 的层权威行落至 state ∈ states（data-tasktree-tasks）。"""
    return _wait_layer_row(sid, tcid, tuple(states), max_wait=max_wait)


def _restart_gui(max_wait=90):
    """重启本套件**自起**的 GUI → 同参数重启 → 等就绪（D-18：保存后重启生效）。

    只回收自起实例（run_llm._G.owned）；复用的外部实例绝不按端口扫杀（避免误杀他套件/他人实例）。
    重启后的新实例交回 run_llm._G，进程退出/信号时由 harness 兜底回收。
    """
    h = getattr(run_llm, "_G", None)
    if h is None or not getattr(h, "owned", False):
        raise TestError("GUI 为外部复用实例（owned=False）→ 不自动重启（按端口扫杀会误杀他人实例）")
    h.stop()
    time.sleep(1)
    run_llm._G = _h.start_gui(port=PORT, work_dir=WS, ready_timeout=max_wait)
    time.sleep(2)
    return run_llm._G.proc


def case_register_thirdparty():
    """A：经四级文件化 MCP（`<级别>/capability/mcps/<名>.json`）注册 stdio server → 重启 GUI →
    装配期 spawned 拉起 → tools/list 暴露。

    接入路径 = `data-mcp-save`（含 `runtime`/`args`）落 user 级文件，装配层在 **llm server 启动期**
    读四级文件视图 → `ServerEntry.Runtime` → gateway spawned 拉起（D-18：保存后需重启生效，
    v1 不热重载）。

    快照-还原（51 §6-8）：同名 user 级文件由 `main()` 的套件级 finally 还原（原本存在 → 写回；
    原本不存在 → 删除），故此处只写不还原（文件须留存至 D/E 用例使用）。
    """
    entry = {"name": SLOW_NAME, "level": "user",
             "runtime": PY,
             "args": [FIXTURE, "--sleep", "3"],
             "enabled": True, "transport": "stdio", "timeout": 5}
    save = c.req("data-mcp-save", {"data": entry}, timeout=15000)
    if not (isinstance(save, dict) and save.get("ok")):
        raise TestError("data-mcp-save 写入 MCP 文件失败：%r" % (save,))
    back = [m for m in _load_mcps() if m.get("name") == SLOW_NAME]
    if not back or not back[0].get("runtime") or not back[0].get("args"):
        raise TestError("data-mcp-list 未回读 slow3p/runtime+args：%r" % (_load_mcps(),))

    # D-18：保存后需重启生效。
    _restart_gui()
    tools = c.req("tools-list", {}) or {}
    names = [t.get("name") or "" for t in (tools.get("tools") or [])]
    if SLOW_SLEEP not in names:
        raise TestError("重启后 tools/list 未暴露 %s（装配期未拉起/连接）：实际 %s" % (SLOW_SLEEP, names))
    if SLOW_ECHO not in names:
        raise TestError("重启后 tools/list 缺 %s：实际 %s" % (SLOW_ECHO, names))


# ── 触发一次 never 模式工具调用，等 mcp-tools-timeout ───────────────────

def _start_call(q, extra_topics=()):
    """发一轮 turn（mock LLM 按关键词回 tool_call）并等待主会话确权。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(EVENTS + list(extra_topics))
    sid = run_llm.create_session()
    run_llm.activate_session(sid)
    run_llm.start_turn(sid, q)
    run_llm.confirm_turn(sid, max_wait=60)
    return sid


def _wait_timeout_event(expected_tool, max_wait=30, options=None):
    """等待 mcp-tools-timeout 事件并做精确字段断言，返回 payload。

    options 缺省 ["wait","cancel"]（never）；manual 用例传 ["detach","cancel"]。
    """
    want = list(options or ["wait", "cancel"])
    ev = c.wait_events("mcp-tools-timeout", n=1, max_wait=max_wait, clear=False)
    p = ev[-1].get("payload") or {}
    if p.get("reason") != "timeout":
        raise TestError("mcp-tools-timeout.reason=%r，期望 timeout" % p.get("reason"))
    if _canon(p.get("tool")) != expected_tool:
        raise TestError("mcp-tools-timeout.tool=%r，期望 %s（可带 self_ 前缀）" % (p.get("tool"), expected_tool))
    opts = list(p.get("options") or [])
    if opts != want:
        raise TestError("mcp-tools-timeout.options=%s，期望 %s（manual=[detach,cancel] / never=[wait,cancel]）" % (opts, want))
    for k in ("instance_id", "tool_call_id", "task_id"):
        if not p.get(k):
            raise TestError("mcp-tools-timeout 缺 %s: %s" % (k, p))
    if abs(float(p.get("timeout_s") or 0) - CALL_TIMEOUT_S) > 0.01:
        raise TestError("mcp-tools-timeout.timeout_s=%r，期望 %s（调用级 timeout）" % (p.get("timeout_s"), CALL_TIMEOUT_S))
    return p


def _drain_turn(max_wait=40):
    """兜底收敛：等本轮到 llm-complete（失败不阻塞，取消已断言场景）。"""
    try:
        c.wait_events("llm-complete", n=1, max_wait=max_wait, clear=False)
    except TestError:
        try:
            c.mq_emit("message-cancel")
        except Exception:
            pass


def _layer_node(sid, tool_call_id, want_state=None, max_wait=20):
    """读**层权威行**（`data-tasktree-tasks`，prjusr 库 tasktree 表）按 tool_call_id 定位节点。

    2026-09-18：`mcp-tasks-status` 方法面已移除 → 状态以层为权威（行 `state` 字段；`status`
    仅前端可见口径，detached/awaiting 被门控成 running）。层行读取**统一为 `data-tasktree-tasks`
    视图**（与前端任务区同源；`tool_call_id` 命中）——`tasks` 视图带 `task_id`/`state`/
    `tool_call_id`/`exec_json`，与 `data-tasktree-list` 口径一致，断言强度不变。
    want_state 非空 → 轮询至命中，超时返回末次命中行（未命中 → None）。
    """
    deadline = time.time() + max_wait
    row = None
    while True:
        res = c.req("data-tasktree-tasks", {"top_session": sid}, timeout=8000)
        for n in ((res or {}).get("list") or []):
            if n.get("tool_call_id") == tool_call_id:
                row = n
                break
        if row is not None and (want_state is None or row.get("state") == want_state):
            return row
        if time.time() >= deadline:
            return row
        time.sleep(0.5)


def _wait_layer_row(sid, tool_call_id, states, max_wait=15):
    """有界轮询层权威行至 `state ∈ states`（I-106）：取消/完成**异步落定**，紧贴动作的单次
    快照会与请求竞态（读到动作前的旧态）。只改「何时读」，判定范围由调用方给出（不放宽）。"""
    deadline = time.time() + max_wait
    row = None
    while True:
        row = _layer_node(sid, tool_call_id, max_wait=0)
        if row is not None and row.get("state") in states:
            return row
        if time.time() >= deadline:
            return row
        time.sleep(0.3)


def case_never_timeout():
    """B：never 到达超时点 → 断言收到 mcp-tools-timeout（options=[wait,cancel]）。

    断言即「不自动失败/不自动转后台」的可观测面：事件带 tool_call_id/task_id/timeout_s，
    且 options 恰为 wait+cancel（never 无 detach）。
    """
    _start_call("please call slow-never")
    _wait_timeout_event("script_run", max_wait=30)
    _drain_turn()


def case_wait_path():
    """C：never 裁决「等待完成」→ 前端 type `mcp-tools-wait` → 回执 {waiting:true} 后拿到工具结果。

    断言点：桥把前端 `mcp-tools-wait` 映射到 gateway 方法面相对主题 `mcp-tools-wait`
    （bridge.go:289-291，**已修**，原误写 `tools/wait` 被静默丢弃）；handleToolsWait 写回
    v.Result {waiting:true, task_id} → 撤销超时、继续等原调用交付。
    """
    _start_call("please call slow-never")
    p = _wait_timeout_event("script_run", max_wait=30)
    res = c.req("mcp-tools-wait",
                {"tool_call_id": p["tool_call_id"], "task_id": p["task_id"], "instance_id": p["instance_id"]},
                timeout=8000)
    if not (isinstance(res, dict) and res.get("waiting") is True and res.get("task_id")):
        raise TestError("未收到 tools/wait 回执 {waiting:true, task_id}（result=%r）" % (res,))
    tr = run_llm.poll_find("tool-result", lambda x: _canon(x.get("tool")) == "script_run", max_wait=30)
    if not tr:
        raise TestError("「等待完成」撤销超时后未收到工具结果（tool-result script_run）")
    _drain_turn()


def _echo_pid():
    """触发一次第三方快工具调用，返回 (pid, tool-result payload)。"""
    tr = run_llm.poll_find("tool-result", lambda x: (x.get("tool") or "") == SLOW_ECHO, max_wait=40)
    if not tr:
        raise TestError("未收到第三方夹具快工具结果（%s）" % SLOW_ECHO)
    m = re.search(r"pid=(\d+)", str(tr.get("result") or ""))
    if not m:
        raise TestError("快工具结果无 pid：%r" % (tr,))
    return m.group(1), tr


def case_cancel_path():
    """D：第三方 spawned server 超时待裁决 → `task-stop`（**只发 tool_call_id**，服务端反归一）
    取消 → 层权威行 state=cancelled（exec_json 保留）+ 任务取消 + respawn 生效。

    取消入口 = **统一停止入口 `task-stop`**（61 消息一览 §4.2；前端工具行裁决「停止」同路径：
    只有 LLM tool_call_id → 服务端 `tm.findByToolCall` 反查归一到任务节点 → 层 → 进程内 sink
    `CancelExec(gw_task_id)` 真打断）。2026-09-18 起取代**已移除**的 gateway 方法面
    `mcp-tasks-cancel`（原断言的是 gateway 任务 id 空间，已不再存在）。
    取消后按 restart 策略 kill provider 子进程 + respawn → 再调一次快工具成功且 pid 变化。
    """
    sid = _start_call("please call slow3p-echo")
    pid1, tr1 = _echo_pid()
    print("    [I-63] 夹具首调 slow3p_fast_echo pid=%s（tool-result：%s）"
          % (pid1, str(tr1.get("result"))[:120]))
    _drain_turn()

    sid = _start_call("please call slow3p-never")
    p = _wait_timeout_event(SLOW_SLEEP, max_wait=30)
    print("    [I-62] mcp-tools-timeout payload: %s" % json.dumps(p, ensure_ascii=False))
    # 前端工具行裁决「停止」同路径：只带 tool_call_id（无 task_id）→ 服务端反归一。
    res = c.req("task-stop", {"tool_call_id": p["tool_call_id"]}, timeout=8000)
    if not (isinstance(res, dict) and res.get("cancelled") is True and res.get("task_id")):
        raise TestError("取消未生效：task-stop(tool_call_id) → %r（期望 {cancelled:true, task_id}）" % (res,))
    print("    [I-62] task-stop(tool_call_id) 回执: %s" % json.dumps(res, ensure_ascii=False))

    # 层权威（取代已移除的 mcp-tasks-status）：该 tool_call_id 节点 state=cancelled，且
    # exec_json 保留（gateway 执行态只增不减 → 取消不清空执行明细）。
    row = _layer_node(sid, p["tool_call_id"], want_state="cancelled", max_wait=20)
    if not row:
        raise TestError("层权威行未命中该 tool_call_id=%s（data-tasktree-tasks）" % p["tool_call_id"])
    print("    [I-62] 层权威行（data-tasktree-tasks）: %s" % json.dumps(row, ensure_ascii=False))
    if row.get("state") != "cancelled":
        raise TestError("层权威 state=%r，期望 cancelled" % row.get("state"))
    if row.get("task_id") != res["task_id"]:
        raise TestError("层行 task_id=%r ≠ task-stop 回执 task_id=%r（反归一不一致）"
                        % (row.get("task_id"), res["task_id"]))
    if "gw_task_id" not in str(row.get("exec_json") or ""):
        raise TestError("取消后层行 exec_json 未保留执行明细（gw_task_id）：%r" % row.get("exec_json"))

    # 证据链：层取消 → 节点终态广播 tasks.done{state:cancelled,success:false}。
    node_done = run_llm.poll_find(
        "tasks.done",
        lambda x: x.get("tool_call_id") == p["tool_call_id"], max_wait=20)
    if not node_done:
        raise TestError("取消后未收到该节点的 tasks.done（tool_call_id=%s）" % p["tool_call_id"])
    print("    [I-62] 取消后 server 节点终态 tasks.done payload: %s"
          % json.dumps(node_done, ensure_ascii=False))
    if node_done.get("state") != "cancelled":
        raise TestError("取消后节点终态 = %r，期望 cancelled（payload=%s）"
                        % (node_done.get("state"), node_done))
    if node_done.get("success") is not False:
        raise TestError("取消后节点 success = %r，期望 false（payload=%s）"
                        % (node_done.get("success"), node_done))
    _drain_turn()

    # respawn 生效：快工具再调成功且换进程（pid 变化）。
    _start_call("please call slow3p-echo")
    pid2, tr2 = _echo_pid()
    print("    [I-63] 取消后夹具重调 slow3p_fast_echo pid=%s（tool-result：%s）"
          % (pid2, str(tr2.get("result"))[:120]))
    if pid2 == pid1:
        raise TestError("取消后夹具进程未 respawn（pid 仍为 %s）" % pid1)
    _drain_turn()


def case_cancel_during_async():
    """E：manual 到达超时点 → 转异步（解绑转后台）→ **后台运行中取消** → cancelled。

    用户硬约束：异步/后台运行中仍可随时取消。链路依据（读 bridge/server/gateway）：
      - 裁决「转异步」前端 = `task-background{tool_call_id}`（bridge.go:282 白名单 → server
        onTaskBackground（G-17 门控 AsyncMode=="manual" 放行）→ **进程内 sink `DetachExec`**；
        2026-09-18 起不再经 gateway `tools/background`，该方法面已移除），回执 {task_id}；
        **不是**直发 `mcp-tools-background`——该方法面已不存在。
      - 取消 = `task-stop{task_id|tool_call_id}`（**统一停止入口**；2026-09-18 起取代已移除的
        `mcp-tasks-cancel`）；服务端恒按子树级联，含「已 detached 的后台任务」。
      - 终态：detached 任务（asyncReport=true）由执行 goroutine 终态 → mcp-tasks-report
        {state:cancelled, tool_call_id} → server 落节点 cancelled 并广播 tasks.done。
    """
    sid = _start_call("please call slow-manual")
    p = _wait_timeout_event("script_run", max_wait=30, options=["detach", "cancel"])
    print("    [I-64] manual mcp-tools-timeout payload: %s" % json.dumps(p, ensure_ascii=False))

    # 1) 转异步（解绑转后台）→ 回执带 task_id
    bg = c.req("task-background", {"tool_call_id": p["tool_call_id"]}, timeout=8000)
    if not (isinstance(bg, dict) and bg.get("task_id")):
        raise TestError("转后台未返回 task_id：task-background → %r（期望 {task_id}）" % (bg,))
    print("    [I-64] task-background 回执: %s" % json.dumps(bg, ensure_ascii=False))

    # 2) 断言确已转后台（层权威 = detached，非终态）——读**层权威行**（data-tasktree-tasks，
    #    prjusr 库 tasktree 表；取代已移除的 mcp-tasks-status 方法面）。
    #    P3 口径（控制面以 Task 层为权威）：exec_json 只增不减 + 状态优先级
    #    「终态 > 执行态(detached/awaiting) > llm 粗粒度(pending/running)」，
    #    故层写入的 state=detached 不再被 llm 快照降级为 pending（旧断言编码的是缺陷行为）。
    row = _layer_node(sid, p["tool_call_id"], want_state="detached", max_wait=20)
    if not row:
        raise TestError("层权威行未命中该 tool_call_id=%s（data-tasktree-tasks）" % p["tool_call_id"])
    print("    [I-64] 转后台后层权威行（data-tasktree-tasks）: %s" % json.dumps(row, ensure_ascii=False))
    if row.get("state") != "detached":
        raise TestError("转后台后任务非层权威 detached（state=%r）" % row.get("state"))

    # 3) 后台运行中取消（**task_id** = 层权威节点 id，走统一停止入口 task-stop）
    node_id = row.get("task_id")
    res = c.req("task-stop", {"task_id": node_id}, timeout=8000)
    if not (isinstance(res, dict) and res.get("cancelled") is True and res.get("task_id") == node_id):
        raise TestError("后台取消未生效：task-stop → %r（期望 {cancelled:true, task_id=%s}）" % (res, node_id))
    print("    [I-64] task-stop 回执: %s" % json.dumps(res, ensure_ascii=False))
    row2 = _layer_node(sid, p["tool_call_id"], want_state="cancelled", max_wait=20)
    if not row2 or row2.get("state") != "cancelled":
        raise TestError("后台取消后层权威 state=%r，期望 cancelled" % (row2 or {}).get("state"))
    print("    [I-64] 后台取消后层权威行: %s" % json.dumps(row2, ensure_ascii=False))

    # 4) server 任务树节点终态 = cancelled（tasks.done）
    node_done = run_llm.poll_find(
        "tasks.done",
        lambda x: x.get("tool_call_id") == p["tool_call_id"], max_wait=25)
    if not node_done:
        raise TestError("后台取消后未收到该节点的 tasks.done（tool_call_id=%s）" % p["tool_call_id"])
    print("    [I-64] 后台取消后 tasks.done payload: %s" % json.dumps(node_done, ensure_ascii=False))
    if node_done.get("state") != "cancelled":
        raise TestError("后台取消后节点终态 = %r，期望 cancelled（payload=%s）"
                        % (node_done.get("state"), node_done))
    if node_done.get("success") is not False:
        raise TestError("后台取消后节点 success = %r，期望 false（payload=%s）"
                        % (node_done.get("success"), node_done))
    _drain_turn()

    # 5) respawn/可用性：取消后既有快路径仍可用（第三方夹具 fast_echo 成功；本用例取消的是
    #    builtin script_run 的 provider，夹具 pid 不因此变化，故只断言链路仍可用）。
    _start_call("please call slow3p-echo")
    pid3, tr3 = _echo_pid()
    print("    [I-64] 后台取消后 slow3p_fast_echo pid=%s（tool-result：%s）"
          % (pid3, str(tr3.get("result"))[:120]))
    _drain_turn()


def _ev(js, timeout=6000):
    """eval + 循环解包（测试通道可能双重编码）。"""
    return _loads(c.eval(js, timeout))


def _arb_row_dom():
    """裁决中（message.arbitration 存在）的工具行 DOM 快照。

    选择器取自 MessageItem.vue：`.toolpair-section`（工具对）→ `.status-badge.async`
    （_meta.async 徽标）/ `.background-btn`（转异步·转后台箭头）/ `.arbitration-hint`。
    """
    return _ev("""
(function(){
  const rows=[...document.querySelectorAll('.toolpair-section')].filter(e=>e.getBoundingClientRect().width>0);
  const r=rows.find(e=>e.querySelector('.arbitration-hint'));
  if(!r) return {found:false, rows:rows.length};
  const btn=r.querySelector('.background-btn');
  const badge=r.querySelector('.status-badge.async');
  const label=r.querySelector('.tool-name-label');
  return {found:true, rows:rows.length,
          hasBtn:!!btn, title:btn?btn.getAttribute('title'):null,
          badge:badge?badge.textContent.trim():'', badgeTitle:badge?badge.getAttribute('title'):null,
          label:label?label.textContent.trim():''};
})()""")


def _click_arb_arrow():
    """点击裁决行上的「转异步」箭头（v-mq → tool-background → backgroundTool）。"""
    return _ev("""
(function(){
  const rows=[...document.querySelectorAll('.toolpair-section')].filter(e=>e.getBoundingClientRect().width>0);
  const r=rows.find(e=>e.querySelector('.arbitration-hint'));
  if(!r) return 'no-row';
  const btn=r.querySelector('.background-btn');
  if(!btn) return 'no-btn';
  btn.click(); return 'ok';
})()""")


def case_manual_icon_dom():
    """F：manual 工具 in-flight → 工具行出现「转异步」图标（DOM）→ 点击 → task-background 收敛。

    用户口径：「前端会在 tool 消息上出现『转异步』图标，点击转」。本用例断言**前端可观测面**
    （MessageItem.vue:80-99：`_meta.async` 徽标 + `canDetach` → 箭头；点击 → 本地 tool-background
    事件 → backgroundTool → `task-background{tool_call_id}`）；**配置化后仍正确**的依据 = 图标显示
    条件读的是消息/能力面的 `_meta.async`（`useToolAsyncMode.js` 取 `tools-list`），后端把 usr
    `tool_async` 覆盖写进 `_meta` 后前端自动跟随，无需前端分支。

    断言链（全部经既有消息/事件面 + DOM，无新增主题）：
      1) `slow-manual`（script_run，async=manual + timeout=1s）→ mcp-tools-timeout options=[detach,cancel]
      2) DOM：裁决行 `.background-btn` 可见且 title = 「转异步」；`.status-badge.async` = manual 徽标
      3) 点击箭头 → 捕获前端内部事件 `tool-background`（payload.id 命中该消息）
      4) 收敛：`.b-message--success` 轻提示（chat.tool_background_ok）+ 裁决提示消失
      5) 层权威确权：`data-tasktree-tasks` 该 tool_call_id 行 `state=detached`（非终态）→ `task-stop`
         （只发 tool_call_id，与前端工具行同路径）收尾（不留后台任务）。P3 口径：控制面以 Task
         层为权威，detached 不被 llm 快照降级；已移除的 `mcp-tasks-status`/`mcp-tasks-cancel` 不再使用。
    """
    sid = _start_call("please call slow-manual", extra_topics=("tool-background",))
    p = _wait_timeout_event("script_run", max_wait=30, options=["detach", "cancel"])
    dom = _arb_row_dom()
    print("    [G-17] 裁决行 DOM: %s" % json.dumps(dom, ensure_ascii=False))
    if not dom.get("found"):
        raise TestError("未找到裁决中的工具行（.toolpair-section 含 .arbitration-hint；可见行数=%s）" % dom.get("rows"))
    if "script_run" not in (dom.get("label") or ""):
        raise TestError("裁决行工具名不含 script_run：%r" % dom.get("label"))
    if not dom.get("hasBtn"):
        raise TestError("manual 裁决行未出现「转异步」图标（.background-btn）")
    if dom.get("title") != "转异步":
        raise TestError("「转异步」图标 title=%r，期望「转异步」（chat.timeout_detach）" % dom.get("title"))
    if dom.get("badge") != "可转异步":
        raise TestError("工具行异步徽标=%r，期望「可转异步」（_meta.async=manual）" % dom.get("badge"))

    if _click_arb_arrow() != "ok":
        raise TestError("点击「转异步」图标失败（DOM 未命中 .background-btn）")

    # 3) 前端内部事件 tool-background（点击 → MessageItem 本地 handler）
    evs = c.wait_events("tool-background", n=1, max_wait=10, clear=False)
    print("    [G-17] tool-background 事件原始: %s" % json.dumps(evs, ensure_ascii=False)[:600])
    print("    [G-17] console: %s" % json.dumps(
        [e for e in (c.console().get("entries") or []) if e.get("level") != "log"][-4:],
        ensure_ascii=False)[:600])
    fp = evs[-1].get("payload")
    if not isinstance(fp, dict):
        raise TestError("点击后 tool-background 事件 payload=%r（期望 {id}；原始事件 %r）" % (fp, evs))
    if not fp.get("id"):
        raise TestError("tool-background 事件 payload 缺 id：%r（期望命中该消息）" % (fp,))

    # 4) 收敛：成功提示 + 裁决提示消失
    end = time.time() + 8
    toast = ""
    while time.time() < end:
        toast = _ev("(function(){const e=document.querySelector('.b-message--success');"
                    "return e?e.textContent.trim():'';})()") or ""
        if toast:
            break
        time.sleep(0.3)
    if "已转后台" not in toast:
        err = _ev("(function(){const e=document.querySelector('.b-message--error');"
                  "return e?e.textContent.trim():'';})()") or ""
        raise TestError("点击「转异步」后无成功提示（toast=%r，error toast=%r）" % (toast, err))
    if _arb_row_dom().get("found"):
        raise TestError("转异步后裁决提示未清除（.arbitration-hint 仍在）")

    # 5) 层权威确权 + 收尾取消（后台任务）——与 E 同口径：读层行（data-tasktree-tasks），
    #    取消走**统一停止入口 task-stop**（工具行只发 tool_call_id → 服务端反归一）。
    row = _layer_node(sid, p["tool_call_id"], want_state="detached", max_wait=15)
    print("    [G-17] 转异步后层权威行: %s" % json.dumps(row, ensure_ascii=False))
    if not row:
        raise TestError("转异步后层权威行未命中 tool_call_id=%s（data-tasktree-tasks）" % p["tool_call_id"])
    if row.get("state") != "detached":
        raise TestError("转异步后任务非层权威 detached（state=%r）" % row.get("state"))
    res = c.req("task-stop", {"tool_call_id": p["tool_call_id"]}, timeout=8000)
    if not (isinstance(res, dict) and res.get("cancelled") is True and res.get("task_id")):
        raise TestError("转异步后取消失败：task-stop → %r（期望 {cancelled:true, task_id}）" % (res,))
    print("    [G-17] task-stop 回执: %s" % json.dumps(res, ensure_ascii=False))
    node_done = run_llm.poll_find(
        "tasks.done",
        lambda x: x.get("tool_call_id") == p["tool_call_id"], max_wait=25)
    if not node_done:
        raise TestError("取消后未收到该节点的 tasks.done（tool_call_id=%s）" % p["tool_call_id"])
    if node_done.get("state") != "cancelled":
        raise TestError("取消后节点终态 = %r，期望 cancelled" % node_done.get("state"))
    _drain_turn()


def _wait_arb_dom(max_wait=10):
    """轮询等待「裁决中」工具行出现（切会话补挂有异步延迟），返回末次 DOM 快照。"""
    deadline = time.time() + max_wait
    dom = _arb_row_dom()
    while not dom.get("found") and time.time() < deadline:
        time.sleep(0.3)
        dom = _arb_row_dom()
    return dom


def _await_bar_dom():
    """任务区「待裁决裁决条」DOM 快照（I-103；SessionTreeNode.vue `.node-awaiting`）。

    选择器取自 SessionTreeNode.vue：`.node-awaiting`（裁决条）→ `.node-awaiting-text`（提示，含
    工具名 + timeout_s 秒数）＋ `.await-btn`（动作按钮）→ `.await-btn-danger`（取消）。按钮文案
    经 i18n（chat.timeout_wait / chat.timeout_detach / chat.tool_stop），此处按**类名**判定，
    不硬编码文案。
    """
    return _ev("""
(function(){
  const all=[...document.querySelectorAll('.node-awaiting')];
  const bars=all.filter(e=>e.getBoundingClientRect().width>0);
  const nodes=document.querySelectorAll('.session-tree-node');
  const diag={all:all.length, nodes:nodes.length, scroll:document.querySelectorAll('.tree-scroll').length,
              titles:[...nodes].slice(0,6).map(n=>(n.textContent||'').trim().slice(0,24))};
  if(!bars.length) return Object.assign({found:false, bars:0}, diag);
  const b=bars[0];
  const txt=b.querySelector('.node-awaiting-text');
  const btns=[...b.querySelectorAll('.await-btn')];
  return Object.assign({found:true, bars:bars.length, text:txt?txt.textContent.trim():'',
          buttons:btns.map(x=>x.textContent.trim()),
          hasDetach:!!b.querySelector('.await-btn:not(.await-btn-danger)'),
          hasCancel:!!b.querySelector('.await-btn-danger')}, diag);
})()""")


def _wait_await_bar(max_wait=10):
    """轮询等待任务区裁决条出现（刷新后任务树载入 + 任务快照读入有异步延迟）。"""
    deadline = time.time() + max_wait
    dom = _await_bar_dom()
    while not dom.get("found") and time.time() < deadline:
        time.sleep(0.3)
        dom = _await_bar_dom()
    return dom


def _click_await_cancel():
    """点击任务区裁决条的「取消」按钮（→ 既有 task-stop{task_id}）。"""
    return _ev("""
(function(){
  const bars=[...document.querySelectorAll('.node-awaiting')].filter(e=>e.getBoundingClientRect().width>0);
  if(!bars.length) return 'no-bar';
  const btn=bars[0].querySelector('.await-btn-danger');
  if(!btn) return 'no-btn';
  btn.click(); return 'ok';
})()""")


def _install_task_stop_probe(delay_ms=0):
    """仪器化（I-106 定位用）：劫持 `window.mq.emit`，捕获裁决条「取消」真正发出的
    `task-stop` 载荷与后端回执（不新增 MQ 主题、不改产品代码；仅测试侧观测）。

    背景：`SessionTreeNode.onAwaitCancel` 发出的 `task-stop` 回执不外泄到 DOM，
    失败时无法区分「未发出 / id 错 / 服务端拒绝 / 已生效但读得早」——本探针补上该观测面。
    必须在 `location.reload()` **之后**安装（刷新会重建 JS 上下文）。

    `delay_ms > 0` = 诊断杠杆（仅测试侧）：把 `task-stop` 的**发出**推迟 N ms，用来
    确定性放大「读快照 vs 请求在途」的竞态（等价批量下的网络/排队延迟），不修改任何产品代码。
    """
    return _ev("""
(function(){
  try{
    var mq = window.mq;
    if(!mq || !mq.emit) return 'no-mq';
    if(!mq.__origEmit){ mq.__origEmit = mq.emit.bind(mq); }
    var delayMs = %d;
    function capture(topic, payload, p){
      window.__lastTaskStop = {payload: payload, done: false, sent_at: Date.now()};
      Promise.resolve(p).then(function(env){
        window.__lastTaskStop.env = env;
        window.__lastTaskStop.ack_at = Date.now();
        window.__lastTaskStop.done = true;
      }, function(e){
        window.__lastTaskStop.err = String(e); window.__lastTaskStop.done = true;
      });
      return p;
    }
    mq.emit = function(topic, payload){
      if(topic !== 'task-stop') return mq.__origEmit(topic, payload);
      if(delayMs > 0){
        return new Promise(function(resolve, reject){
          setTimeout(function(){
            capture(topic, payload, mq.__origEmit(topic, payload)).then(resolve, reject);
          }, delayMs);
        });
      }
      return capture(topic, payload, mq.__origEmit(topic, payload));
    };
    return 'ok';
  }catch(e){ return 'err:' + e; }
})()""" % int(delay_ms))


def _read_task_stop_probe():
    """读回探针捕获的 `task-stop` 载荷/回执 + 前端错误 toast（诊断证据）。"""
    return _ev("""
(function(){
  var out = {probe: window.__lastTaskStop || null, now: Date.now()};
  try{
    var e = document.querySelector('.b-message--error');
    out.error_toast = e ? e.textContent.trim() : '';
  }catch(_e){}
  return out;
})()""")


def _wait_task_stop_ack(max_wait=15):
    """有界轮询等探针拿到 `task-stop` 回执（不改断言：回执照原样进诊断串）。"""
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = _read_task_stop_probe()
        if last and (last.get("probe") or {}).get("done"):
            return last
        time.sleep(0.2)
    return last


def _toolcall_rows(sid, tool_call_id):
    """诊断（I-106）：同一 `tool_call_id` 在**任务快照**（data-tasktree-tasks）与**树**（
    data-tasktree-list）两侧的全部行/节点（用于判定是否存在「重复行 / 节点不在 tm.nodes」）。"""
    tasks = []
    try:
        res = c.req("data-tasktree-tasks", {"top_session": sid}, timeout=8000) or {}
        for n in (res.get("list") or []):
            if n.get("tool_call_id") == tool_call_id:
                tasks.append({k: n.get(k) for k in ("task_id", "state", "status", "tool_call_id")})
    except Exception as e:  # noqa: BLE001
        tasks.append({"error": str(e)})
    nodes = []
    try:
        res = c.req("data-tasktree-list", {"top_session": sid, "mode": "full"}, timeout=8000) or {}
        for n in (res.get("nodes") or []):
            if n.get("tool_call_id") == tool_call_id:
                nodes.append({k: n.get(k) for k in
                              ("node_id", "task_id", "state", "status", "parent_node_id", "title")})
    except Exception as e:  # noqa: BLE001
        nodes.append({"error": str(e)})
    return {"tasks_rows": tasks, "tree_nodes": nodes}


def case_awaiting_restore_dom():
    """G：待裁决恢复（I-99 数据面 + I-103 任务区裁决条）——manual 到超时点 → **刷新** → 可裁决。

    覆盖两条验收面（此前 `data-tasktree-tasks` **只产出 state/exec_json，无 awaiting 对象**
    → 前端恢复路径 `if (!t || !t.awaiting) continue` 恒命中 → 刷新后裁决条永不补挂）：
      1) **视图增补（I-99）**：层行（`data-tasktree-tasks`）在 `state=awaiting` 时**输出 `awaiting`
         对象**（含 `options` / `timeout_s`）—— I-99 生效判据；
      2) **恢复路径（I-99）**：`location.reload()`（真刷新；挂载时 `MessageList.onMounted` →
         `restoreAwaitingArbitration`）→ 工具卡从库重建后，DOM `.arbitration-hint`（裁决条）
         **复现**且带裁决按钮；
      2b) **任务区裁决条（I-103）**：`SessionTree.loadTreeFor` 并行读 `data-tasktree-tasks` →
         `SessionTreeNode` 对 `awaiting` 节点**直接渲染 `.node-awaiting` 裁决条**（**不依赖消息卡**，
         这正是 I-103 修的点）→ 刷新后**仍可见且可操作**；
      3) **操作证据（I-103）**：点击裁决条「取消」按钮 → 既有 `task-stop{task_id}` → 层行
         `cancelled`（不留待裁决任务）。

    注：**「切会话」不适用作恢复触发**（实测：切会话重载消息时在飞工具卡 `toolpair-section`
    未从库重建 → 无卡片可挂；**刷新**才会重建卡片）——故本用例用**刷新**（与 I-57 口径一致）。
    """
    sid = _start_call("please call slow-manual")
    p = _wait_timeout_event("script_run", max_wait=30, options=["detach", "cancel"])

    # 1) 视图增补 awaiting 对象（I-99）
    row = _layer_node(sid, p["tool_call_id"], want_state="awaiting", max_wait=20)
    if not row:
        raise TestError("层权威行未命中 tool_call_id=%s（data-tasktree-tasks）" % p["tool_call_id"])
    print("    [I-99] awaiting 层权威行（data-tasktree-tasks）: %s" % json.dumps(row, ensure_ascii=False))
    if row.get("state") != "awaiting":
        raise TestError("层权威 state=%r，期望 awaiting" % row.get("state"))
    aw = row.get("awaiting")
    if not isinstance(aw, dict):
        raise TestError("data-tasktree-tasks 未输出 awaiting 对象（I-99 未生效）: %r" % row)
    opts = list(aw.get("options") or [])
    if opts != ["detach", "cancel"]:
        raise TestError("awaiting.options=%s，期望 [detach,cancel]" % opts)
    if abs(float(aw.get("timeout_s") or 0) - CALL_TIMEOUT_S) > 0.01:
        raise TestError("awaiting.timeout_s=%r，期望 %s" % (aw.get("timeout_s"), CALL_TIMEOUT_S))
    # I-106 诊断：同一 tool_call_id 若在快照/树侧出现多行（或 node_id ≠ task_id）→ 打印留证。
    _diag1 = _toolcall_rows(sid, p["tool_call_id"])
    if len(_diag1["tasks_rows"]) != 1 or len(_diag1["tree_nodes"]) != 1:
        print("    [I-106] ⚠ 同 tool_call_id 多行/多节点: %s" % json.dumps(_diag1, ensure_ascii=False))
    else:
        print("    [I-106] 同 tool_call_id 行/节点唯一: %s"
              % json.dumps(_diag1["tree_nodes"][0], ensure_ascii=False))

    # 2) 真刷新：awaiting 数据经任务列表可达前端 → 恢复路径据此补挂裁决条。
    #    前置：**持久化活动会话** —— 真实 UI 新建/切换会话经 `data-session-active-set` 落库，
    #    `App.vue` 挂载时 `getActiveSessionID()` 恢复并 `session-changed` → 任务树定位到本会话。
    #    本用例经 mq-only 建会话（`session-changed` 仅本地事件、不落库），故此处补一次
    #    （等价真实点击会话），否则重载后任务树停在上一活动会话、看不到本会话的 awaiting 节点。
    c.req("data-session-active-set", {"session_id": sid}, timeout=8000)
    #    ⚠ 卡片路径的**次级阻塞（I-103）**：在飞/待裁决工具**尚无 role=tool 结果行**
    #    （`turn.go` 仅在结果到达时 `persistToolResult`）→ `data-session-history` 不产出
    #    `tool_pair`（`persist/view.go:355` 仅对 role=tool / legacy assistant tool_pair 展开）
    #    → 刷新后**无卡可挂**，`restoreAwaitingArbitration` 只能入待决缓存。故：卡片存在则断言
    #    补挂复现；不存在不算失败（改由 2b 的任务区裁决条承担**无卡也能裁决**）。
    c.eval("location.reload(); 'ok'")
    time.sleep(4)
    dom = _wait_arb_dom(max_wait=3)
    print("    [I-99] 刷新后消息卡裁决行 DOM: %s" % json.dumps(dom, ensure_ascii=False))
    if dom.get("found"):
        if "script_run" not in (dom.get("label") or ""):
            raise TestError("补挂裁决行工具名不含 script_run：%r" % dom.get("label"))
        if not dom.get("hasBtn"):
            raise TestError("补挂裁决行缺裁决按钮（.background-btn）")
    else:
        print("    [I-99] 刷新后无同 tool_call_id 的工具卡（在飞工具无 role=tool 结果行 → "
              "不产出 tool_pair）→ 卡片路径无卡可挂；**由 2b 任务区裁决条承担**。")

    # 2b) I-103：任务区（SessionTree/SessionTreeNode）**直接按 awaiting 渲染裁决条** ——
    #     刷新后**仍可见且可操作**（不依赖消息卡；manual options=[detach,cancel] → 转异步 + 取消）。
    bar = _wait_await_bar(max_wait=10)
    print("    [I-103] 刷新后任务区裁决条 DOM: %s" % json.dumps(bar, ensure_ascii=False))
    if not bar.get("found"):
        raise TestError("刷新后任务区未渲染裁决条（.node-awaiting；I-103 未生效）: %s" % bar)
    # 提示取节点标题（= mock 的 tool_call_display_name「转后台」）+ timeout_s 秒数（同工具行口径）。
    if "转后台" not in (bar.get("text") or ""):
        raise TestError("裁决条提示未含工具名（节点标题=转后台）: %r" % bar.get("text"))
    if "1s" not in (bar.get("text") or ""):
        raise TestError("裁决条提示未含超时秒数（timeout_s=%s）: %r" % (CALL_TIMEOUT_S, bar.get("text")))
    if not bar.get("hasDetach"):
        raise TestError("裁决条缺「转异步」按钮（manual options 含 detach）: %s" % bar)
    if not bar.get("hasCancel"):
        raise TestError("裁决条缺「取消」按钮（options 含 cancel）: %s" % bar)

    # 3) 操作证据（I-103）：点击裁决条「取消」→ 既有 task-stop{task_id} → 层行 cancelled。
    #    若工具已自行完成（终态 done）→ 无待裁决任务（容忍，不失败）。
    #
    #    I-106（2026-09-19 定性 = **测试资产时序**，非产品缺陷；证据见 42 §2 (120)）：
    #    原实现「点击后**紧贴**读一次层行快照」与前端 `task-stop` 的 /publish **并发** ——
    #    批量/负载下该读可先于取消落定返回 → 读到动作前的 `awaiting`（`I106_DELAY_MS` 诊断
    #    杠杆把取消发出推迟 400ms 即 100% 复现：紧贴快照=awaiting、回执 ok、复位读=cancelled）。
    #    故断言：① 探针**先断言取消被受理**（载荷 task_id 非空 + 回执 ok 无 errors —— 把
    #    「空 id 静默返回 / 服务端拒绝」堵死，属**加强**）；② 层行改**有界轮询**等终态
    #    （cancelled / done，判定范围与原口径一致、未放宽）；紧贴快照仅作竞态证据打印。
    _delay_ms = int(os.environ.get("I106_DELAY_MS") or 0)
    probe_install = _install_task_stop_probe(delay_ms=_delay_ms)
    print("    [I-106] task-stop 探针安装: %s（诊断延迟 %dms）" % (probe_install, _delay_ms))
    t_click = time.time()
    click = _click_await_cancel()
    print("    [I-103] 点击裁决条「取消」: %s" % click)
    if click != "ok":
        raise TestError("点击任务区裁决条「取消」失败（DOM 未命中 .node-awaiting .await-btn-danger）: %s" % click)
    t_snap = time.time()
    snap = _layer_node(sid, p["tool_call_id"], max_wait=0)  # 紧贴点击的第 1 次快照（仅证据）
    probe = _wait_task_stop_ack(max_wait=15)                 # 等 task-stop 回执（有界）
    print("    [I-106] task-stop 探针（click→snap=%dms）: %s"
          % (int((t_snap - t_click) * 1000), json.dumps(probe, ensure_ascii=False)))

    pr = (probe or {}).get("probe") or {}
    pl = pr.get("payload") or {}
    if not pl.get("task_id"):
        raise TestError("裁决条「取消」未发出有效 task-stop（载荷=%r；前端 onAwaitCancel 空 id 静默返回？）"
                        % (pl,))
    backend = ((pr.get("env") or {}).get("backend") or {})
    errs = backend.get("errors") or []
    if not backend.get("ok") or errs:
        raise TestError("task-stop 回执异常（取消被拒，非时序）：%s" % json.dumps(probe, ensure_ascii=False))

    row2 = _wait_layer_row(sid, p["tool_call_id"], ("cancelled", "done"), max_wait=15)
    if not row2 or row2.get("state") not in ("cancelled", "done"):
        diag = _toolcall_rows(sid, p["tool_call_id"])
        raise TestError("点击「取消」后层权威 state=%r，期望 cancelled（或已 done）；"
                        "紧贴快照=%s；task-stop 探针=%s；同 tool_call_id 行/节点=%s"
                        % ((row2 or {}).get("state"),
                           json.dumps({k: (snap or {}).get(k) for k in ("task_id", "state")},
                                      ensure_ascii=False),
                           json.dumps(probe, ensure_ascii=False),
                           json.dumps(diag, ensure_ascii=False)))
    print("    [I-103] 点击「取消」后层权威行: %s" % json.dumps(row2, ensure_ascii=False))
    _drain_turn()


def case_layer_authority_row():
    """H（I-88）：真实会话跑一次工具调用 → Task 层权威行落库（端到端 L4 断言）。

    背景（41 I-88 原文）：`run_task_ui.py` 断言的是**前端点分事件** `tasks.started/updated/done`
    （由 bridge 原样发布），而任务层订阅的是**连字符**主题 `task-started/task-updated/task-done`
    （`chonkpilot-task/layer.go`）→ 该套件不驱动层落库，故「真实会话产生的任务是否被 **Task 层**
    落库为权威行」此前缺 L4 级 e2e 断言（仅有 L1 集成 `tasks_layer_test.go`）。

    本用例断言链（全部经既有消息面，无新增 MQ 主题；不降低既有断言强度）：
      1) 真实会话（mock LLM 触发一次 script_run：cmd `ping -n 4`，约 3s）→ 捕获 `tasks.started`
         取该节点的 `tool_call_id`（服务器任务树节点 id 空间）；
      2) **层权威行出现**（`data-tasktree-tasks`，prjusr 库 tasktree 表）且 **state=running**
         —— 执行推进中的中间态（`llm task-started` → 层落 running）；
      3) 工具自然结束后同一行 **state=done**（终态推进；P3 口径终态优先级最高）；
      4) 该行 `exec_json` 含 `gw_task_id` —— gateway 执行层任务 id 已随执行态上报回填层
         （真实会话的 gateway 执行与层行确有关联，而非仅前端事件空转）。
    """
    sid = _start_call("please call task-layer")
    # 1) 服务器任务树节点（tool_call_id）——真实会话产生的任务节点（tasks.started 带 tool_call_id）。
    node = run_llm.poll_find(
        "tasks.started",
        lambda p: _canon(p.get("tool")) == "script_run" and p.get("top_session") == sid,
        max_wait=30)
    if not node:
        raise TestError("未收到 script_run 的 tasks.started（无法定位 tool_call_id）")
    tcid = node.get("tool_call_id")
    if not tcid:
        raise TestError("tasks.started 缺 tool_call_id：%s" % json.dumps(node, ensure_ascii=False))
    if node.get("state") != "running":
        raise TestError("tasks.started.state=%r，期望 running" % node.get("state"))
    print("    [I-88] script_run 任务节点 tasks.started: %s" % json.dumps(node, ensure_ascii=False))

    # 2) 层权威行出现 + state=running（执行推进中间态）。
    row_run = _layer_node(sid, tcid, want_state="running", max_wait=15)
    if not row_run:
        raise TestError("层权威行未命中 tool_call_id=%s（data-tasktree-tasks）" % tcid)
    print("    [I-88] 执行中层权威行（data-tasktree-tasks）: %s" % json.dumps(row_run, ensure_ascii=False))
    if row_run.get("state") != "running":
        raise TestError("执行中层权威 state=%r，期望 running（工具会自然完成落 done）" % row_run.get("state"))

    # 3) 工具自然结束 → 同一行 state=done（终态推进）。
    row_done = _layer_node(sid, tcid, want_state="done", max_wait=30)
    if not row_done or row_done.get("state") != "done":
        raise TestError("工具完成后层权威 state=%r，期望 done" % (row_done or {}).get("state"))
    print("    [I-88] 完成后层权威行: %s" % json.dumps(row_done, ensure_ascii=False))
    if row_done.get("task_id") != node.get("task_id"):
        raise TestError("层行 task_id=%r ≠ tasks.started task_id=%r"
                        % (row_done.get("task_id"), node.get("task_id")))

    # 4) exec_json 含 gw_task_id（gateway 执行层任务 id 已回填层）。
    if "gw_task_id" not in str(row_done.get("exec_json") or ""):
        raise TestError("层行 exec_json 未含 gw_task_id（gateway 执行态未回填层）：%r"
                        % row_done.get("exec_json"))
    # 证据链：服务器节点终态 tasks.done{state:done, success:true}。
    node_done = run_llm.poll_find(
        "tasks.done",
        lambda x: x.get("tool_call_id") == tcid, max_wait=20)
    if not node_done:
        raise TestError("未收到该节点的 tasks.done（tool_call_id=%s）" % tcid)
    if node_done.get("state") != "done":
        raise TestError("节点终态 = %r，期望 done（payload=%s）"
                        % (node_done.get("state"), node_done))
    if node_done.get("success") is not True:
        raise TestError("节点 success = %r，期望 true（payload=%s）"
                        % (node_done.get("success"), node_done))
    print("    [I-88] 节点终态 tasks.done: %s" % json.dumps(node_done, ensure_ascii=False))
    _drain_turn()


def _started_tcid(sid, tool, max_wait=30):
    """取本会话内某工具本轮任务的 tool_call_id（tasks.started；tool 名剥离 self_ 前缀后比对）。"""
    node = run_llm.poll_find(
        "tasks.started",
        lambda p: _canon(p.get("tool")) == tool and p.get("top_session") == sid,
        max_wait=max_wait)
    if not node:
        raise TestError("未收到 %s 的 tasks.started（无法定位 tool_call_id）" % tool)
    tcid = node.get("tool_call_id")
    if not tcid:
        raise TestError("tasks.started 缺 tool_call_id：%s" % json.dumps(node, ensure_ascii=False))
    return tcid


def _procs_with_cmdline(sentinel):
    """返回命令行含 sentinel 的**系统进程** [{ProcessId,Name,CommandLine}]（孤儿进程探针）。

    仅测试侧观测：经 PowerShell CIM 查询 Win32_Process（**不新增 MQ 主题、不改产品代码**）。
    sentinel 为唯一串（如 ping 目标地址 127.0.0.99）→ 精确圈定被测子进程。
    """
    ps = ("Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*%s*' } | "
          "Select-Object ProcessId,Name,CommandLine | ConvertTo-Json -Compress" % sentinel)
    try:
        out = subprocess.run(["powershell", "-NoProfile", "-Command", ps],
                             capture_output=True, text=True, encoding="utf-8", timeout=25)
    except Exception as e:  # noqa: BLE001
        print("[run_tool_async] 进程探针执行失败: %s" % e, flush=True)
        return []
    txt = (out.stdout or "").strip()
    if not txt:
        return []
    try:
        data = json.loads(txt)
    except Exception:
        return []
    return data if isinstance(data, list) else [data]


def case_cancel_on_timeout_thirdparty():
    """I（配置⑤）：第三方 stdio 工具配 `cancel_on_timeout` → 到超时点**自动取消**（不等裁决）
    → gateway `Terminate` = kill + respawn（下次快工具 pid 变化 = 「真被杀」）。

    驱动链：usr `tool_async.slow3p_slow_sleep.cancel_on_timeout=1` → gateway `doCall` 到超时点
    直接 `tm.cancelRef`（**不发 mcp-tools-timeout**）→ `onTaskCancel` → provider `Terminate`
    （spawned 线 = kill 子进程 + respawn）。断言：层权威行 state=cancelled + 快工具 pid 变化。
    """
    snap = _snapshot_tool_async()
    try:
        _save_tool_async({"slow3p_slow_sleep": {"cancel_on_timeout": 1}})
        time.sleep(0.5)
        # 1) 首调快工具记 pid
        sid = _start_call("please call slow3p-echo")
        pid1, _ = _echo_pid()
        print("    [cancel_on_timeout] 夹具首调 slow3p_fast_echo pid=%s" % pid1)
        _drain_turn()
        # 2) 慢工具（async=never + call-level timeout=1s）+ usr cancel_on_timeout → 自动取消
        sid = _start_call("please call cancel-on-timeout-3p")
        tcid = _started_tcid(sid, SLOW_SLEEP)
        row = _wait_layer_state(sid, tcid, ("cancelled",), max_wait=25)
        print("    [cancel_on_timeout] 层权威行（data-tasktree-tasks）: %s"
              % json.dumps(row, ensure_ascii=False))
        if not row or row.get("state") != "cancelled":
            raise TestError("配置⑤ 未自动取消：层权威 state=%r（期望 cancelled）" % (row or {}).get("state"))
        _drain_turn()
        # 3) respawn 生效：快工具再调成功且换进程（pid 变化 = 子进程真被杀后重建）
        sid = _start_call("please call slow3p-echo")
        pid2, _ = _echo_pid()
        print("    [cancel_on_timeout] 自动取消后慢工具重调 pid=%s" % pid2)
        if pid2 == pid1:
            raise TestError("配置⑤ 未 kill+respawn 夹具进程（pid 仍为 %s）" % pid1)
        _drain_turn()
    finally:
        _restore_tool_async(snap)


def case_cancel_on_timeout_no_orphan():
    """J（配置⑤）：内置执行器（self_script_run）超时自动取消 → **无孤儿进程**。

    驱动链：usr `tool_async.self_script_run.cancel_on_timeout=1` → 到超时点自动取消 →
    gateway 取消 ctx 经 in-memory 传输送达 mcp-server handler 的请求 ctx（18 §3.7 探针）→
    `callTool` 的 `exec.CommandContext` 杀掉执行体子进程（`cmd /c ping`）→ 无残留 `ping`。
    探针：系统进程命令行含唯一哨兵 `127.0.0.99` 者应为 0（取消后给 3s 收敛窗口）。
    """
    snap = _snapshot_tool_async()
    try:
        _save_tool_async({"self_script_run": {"cancel_on_timeout": 1}})
        time.sleep(0.5)
        sid = _start_call("please call cancel-on-timeout-self")
        tcid = _started_tcid(sid, "script_run")
        # 取消前：应有在跑的 ping（哨兵命中）——证据（可能因时序已不可见，仅打印不强断言）
        before = _procs_with_cmdline("127.0.0.99")
        print("    [cancel_on_timeout] 取消前哨兵进程: %s"
              % json.dumps(before, ensure_ascii=False)[:300])
        row = _wait_layer_state(sid, tcid, ("cancelled",), max_wait=25)
        print("    [cancel_on_timeout] 层权威行（data-tasktree-tasks）: %s"
              % json.dumps(row, ensure_ascii=False))
        if not row or row.get("state") != "cancelled":
            raise TestError("配置⑤ 未自动取消：层权威 state=%r（期望 cancelled）" % (row or {}).get("state"))
        # 取消后：给 3s 收敛窗口，哨兵进程应清零（无孤儿）。
        deadline = time.time() + 3
        leaked = _procs_with_cmdline("127.0.0.99")
        while leaked and time.time() < deadline:
            time.sleep(0.4)
            leaked = _procs_with_cmdline("127.0.0.99")
        if leaked:
            raise TestError("配置⑤ 自动取消后仍有孤儿进程（哨兵 127.0.0.99）: %s"
                            % json.dumps(leaked, ensure_ascii=False))
        _drain_turn()
    finally:
        _restore_tool_async(snap)


def main():
    ok = 0
    total = 0
    # 可选：命令行给用例字母（如 `python run_tool_async.py F`）→ 只跑指定用例（默认全跑）
    only = [a.upper() for a in sys.argv[1:] if a and not a.startswith("-")]
    # 任务面板默认收起（2026-09-27 首屏减负：MainLayout taskOpen 默认 false）→
    # B/G 等用例断言任务区 DOM（.session-tree-node / .node-awaiting）→ 先经既有 tasks-toggle 展开。
    _h.ensure_task_panel_open(c)
    c.console(clear=True)
    print("依赖：--test-port=2345 的 GUI + mock_llm(8901) 指向 llms[0]；夹具 = mock_slow_mcp.py")

    def rec(tag, name, fn):
        nonlocal ok, total
        if only and tag.upper() not in only:
            return
        total += 1
        if run_case(name, fn):
            ok += 1

    # 套件级 MCP 文件快照-还原（51 §6-8）：同名 user 级文件退出前回原状（原本存在 → 写回；
    # 原本不存在 → 删除）。窗口重装/复用实例下 client 仍可用，失败仅告警。
    mcp_snap = _snapshot_mcp(SLOW_NAME)
    # usr `tool_async` 快照-还原：I/J（配置⑤）会写 cancel_on_timeout → 退出前收敛回原状。
    ta_snap = _snapshot_tool_async()
    try:
        rec("SELF", "夹具 mock_slow_mcp.py 自检（stdio JSON-RPC 协议）", _fixture_selftest)
        rec("A", "A 经四级文件化 MCP 注册第三方 stdio server（重启 spawned 拉起）", case_register_thirdparty)
        rec("B", "B never 超时待裁决（mcp-tools-timeout options=[wait,cancel]）", case_never_timeout)
        rec("C", "C 等待完成（mcp-tools-wait → {waiting:true} + 结果交付）", case_wait_path)
        rec("D", "D 停止（task-stop 只发 tool_call_id → 层行 cancelled + respawn 生效）", case_cancel_path)
        rec("E", "E 转后台后再取消（manual detach → 层行 detached → task-stop → cancelled）",
            case_cancel_during_async)
        rec("F", "F manual 工具「转异步」图标（DOM 点击 → tool-background → task-background 收敛）",
            case_manual_icon_dom)
        rec("G", "G 待裁决恢复（I-99 层行带 awaiting 对象 + I-103 任务区裁决条刷新后可见可操作）",
            case_awaiting_restore_dom)
        rec("H", "H 真实会话工具调用 → Task 层权威行（running→done + exec_json 含 gw_task_id；I-88）",
            case_layer_authority_row)
        rec("I", "I 配置⑤ 第三方 stdio cancel_on_timeout → 自动取消（层行 cancelled + respawn/pid 变化）",
            case_cancel_on_timeout_thirdparty)
        rec("J", "J 配置⑤ 内置执行器 cancel_on_timeout → 自动取消无孤儿进程（ping 哨兵探针）",
            case_cancel_on_timeout_no_orphan)
    finally:
        _restore_mcp(SLOW_NAME, mcp_snap)
        _restore_tool_async(ta_snap)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print("  [CONSOLE-ERROR] %s" % e.get("text"))
    print("\n统一异步模型 L4（超时交裁决）：%d/%d 通过" % (ok, total))
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
