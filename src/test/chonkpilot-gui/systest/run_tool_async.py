# -*- coding: utf-8 -*-
"""L4 端到端：统一异步模型「超时交用户裁决」（2026-09-13）+ MCP 配置面打通。

前置（父会话启动）：
  1. 重建产物：`.\\build-desktop.ps1`（本次不实跑）。
  2. IDE：`dist/desktop\\chonkpilot.exe --test-port=2345 --work-dir <大写盘符路径>`。
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
   本用例用**调用级**最稳：mock LLM 回 tool_call 时带 `timeout:1` + `async:"manual"`（该第三方工具 `_meta`
   无 timeout 键 → 不触发「绝对无上限」，调用级正数照常生效）。

B. 可达性（GUI test-port ≤ 桥 `PublishEvent`）：
   - `data-<domain>-*` 前缀 → 经总线 persist 服务应答（桥 dataViaPersist）→ **可达**。
     MCP 配置面即走此路：`data-mcp-save {data:{name,runtime,args,...}}` → 落四级文件化配置
     （`<级别>/capability/mcps/<名>.json`）。
   - 桥 `frontMethodSubjects` 白名单含 `task-stop`（**统一停止入口**，取代已移除的
     `mcp-tasks-cancel`）；`mcp-tools-wait` 方法面已删除。
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
# 产物 GUI + 工作目录（与 `run_llm` 同源：42 (290) 起 run_llm 用独立临时 work-dir，
# 本套件复用其 GUI/客户端 → 重启也必须在同一 work-dir，否则实例与夹具路径不一致）。
WS = getattr(run_llm._G, "work_dir", None) or os.path.join(HERE, "ws")
PORT = 2345

SLOW_NAME = "slow3p"           # 四级文件 MCP 条目名（节点前缀 = slow3p_）
SLOW_ECHO = "slow3p_fast_echo"  # 快工具（回显 pid，供 respawn 比对）
SLOW_SLEEP = "slow3p_slow_sleep"  # 慢工具
CALL_TIMEOUT_S = 1              # 调用级 timeout（秒）

EVENTS = ["turn-start", "llm-tool-call", "tool-pair", "tool-result", "llm-complete",
          "mcp-tools-timeout", "tasks.started", "tasks.updated", "tasks.done"]


def _canon(tool):
    """工具名归一（当前暴露名 = 原名）。"""
    t = tool or ""
    return t


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


# ── usr `tool_async` 快照-还原（套件写入的 mode 覆盖须收敛回原状）────────

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


def _wait_tool_meta_async(tool, mode, max_wait=20):
    """等 tools/list 暴露的 `_meta.async` 落至 mode（保存 → refresh → 覆盖表 → tools/list 刷新）。

    usr `tool_async.<暴露名>.mode` 经 data-user-config-refresh → applyToolAsync →
    gateway SetAsyncOverrides → tools/list `_meta.async`（gateway 侧统一施加）。该链路含
    gateway/reload + 重拉 list（异步后台执行）→ 必须**有界轮询**而非固定 sleep。
    返回末次命中工具的 `_meta`（未命中 → None）。
    """
    deadline = time.time() + max_wait
    meta = None
    while True:
        res = c.req("tools-list", {}) or {}
        for t in (res.get("tools") or []):
            if (t.get("name") or "") == tool:
                meta = t.get("_meta") or {}
                break
        if isinstance(meta, dict) and meta.get("async") == mode:
            return meta
        if time.time() >= deadline:
            return meta
        time.sleep(0.5)


def _restore_tool_async(snap):
    """按快照还原 usr `tool_async`：原本存在 → 写回；原本不存在 → 删键。失败仅告警。"""
    try:
        if snap is None:
            c.req("data-user-config-delete", {"id": TOOL_ASYNC_KEY}, timeout=15000)
        else:
            c.req("data-user-config-save", {"data": {TOOL_ASYNC_KEY: snap}}, timeout=15000)
    except Exception as e:
        print("[run_tool_async] usr tool_async 还原失败: %s" % e, flush=True)


def main():
    ok = 0
    total = 0
    # 可选：命令行给用例字母（如 `python run_tool_async.py F`）→ 只跑指定用例（默认全跑）
    only = [a.upper() for a in sys.argv[1:] if a and not a.startswith("-")]
    # 任务面板默认收起（2026-09-27 首屏减负：MainLayout taskOpen 默认 false）→
    # 用例断言任务区 DOM（.session-tree-node）→ 先经既有 tasks-toggle 展开。
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
    # usr `tool_async` 快照-还原：套件写入的 mode 覆盖退出前收敛回原状。
    ta_snap = _snapshot_tool_async()
    # 2026-10-23：驱动工具改为插件 `task_run` 后，其契约缺省 async=auto（原 script_run = manual）
    # → E/F 的「manual 裁决/转异步」语义需经 usr 配置显式打开（与产品「按工具可配」一致）。
    # 键 = `mode`（64-配置项一览 §3：{"<暴露名>":{"mode":"sync|async|auto|manual",...}}）。
    _save_tool_async({"task_run": {"mode": "manual"}})
    _meta = _wait_tool_meta_async("task_run", "manual")
    print("    [套件配置] usr tool_async.task_run.mode=manual → tools-list _meta=%s"
          % json.dumps(_meta, ensure_ascii=False))
    if not isinstance(_meta, dict) or _meta.get("async") != "manual":
        raise TestError("usr tool_async 覆盖未生效：task_run _meta.async=%r（期望 manual）"
                        % ((_meta or {}).get("async"),))
    try:
        rec("SELF", "夹具 mock_slow_mcp.py 自检（stdio JSON-RPC 协议）", _fixture_selftest)
        rec("A", "A 经四级文件化 MCP 注册第三方 stdio server（重启 spawned 拉起）", case_register_thirdparty)
        rec("D", "D 停止（task-stop 只发 tool_call_id → 层行 cancelled + 链路可用）", case_cancel_path)
        rec("E", "E 转后台后再取消（manual detach → 层行 detached → task-stop → cancelled）",
            case_cancel_during_async)
        rec("F", "F manual 工具「转异步」图标（DOM 点击 → tool-background → task-background 收敛）",
            case_manual_icon_dom)
        rec("H", "H 真实会话工具调用 → Task 层权威行（running→done + exec_json 含 gw_task_id；I-88）",
            case_layer_authority_row)
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
