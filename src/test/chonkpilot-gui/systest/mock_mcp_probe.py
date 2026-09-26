#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""可「回显自身运行态」的 stdio MCP server 夹具（纯标准库，无第三方依赖）。

用途：`run_mcp_fields.py` 验证 MCP 配置项 **cwd / env / args / isolate** 的**真实效果**——
经 usr `mcps` 条目（{runtime, args, cwd, env, hot_tools, isolate}）注册后由 gateway 拉起本进程，
`tools/call probe_info` 把**子进程真实可见的** cwd / pid / argv / 指定环境变量以 JSON 文本返回，
使配置值可被直接断言（不读任何产品内部状态，也不改产品代码）。

工具：
  probe_info {keys?}  → JSON 文本 {"cwd": ..., "pid": ..., "argv": [...], "env": {k: v}}
                        keys 缺省 = 全部 `PROBE_` 前缀环境变量；argv = 本进程完整命令行参数
                        （用于断言 args 逐个作为 exec 参数传递、含空格路径不被切碎）。
  probe_pid  {}       → JSON 文本 {"pid": ..., "cwd": ...}（isolate 同一性比对：同 work_dir 复用同一进程）

协议要点（与 mock_slow_mcp.py 一致）：
  - JSON-RPC 2.0 over stdin/stdout，**逐行**（一行一条消息）。
  - stdout **只**输出 JSON-RPC 帧；日志一律走 stderr（避免污染协议流）。
  - 支持 initialize / notifications/initialized / ping / tools/list / tools/call。
  - 不安装信号处理器：SIGTERM/SIGKILL 直接终止（网关 unregister 时 kill）。

用法：python mock_mcp_probe.py [--marker <任意值>]（多余参数忽略，供 args 传递断言）
"""

import json
import os
import sys

PROTOCOL_VERSION = "2024-11-05"
SERVER_NAME = "mock-mcp-probe"
SERVER_VERSION = "1.0.0"
ENV_PREFIX = "PROBE_"


def log(msg):
    """日志走 stderr（stdout 仅供 JSON-RPC）。"""
    print("[mock_mcp_probe] " + str(msg), file=sys.stderr, flush=True)


def send(obj):
    """向 stdout 写一条 JSON-RPC 帧（逐行 + flush）。"""
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def ok(rid, result):
    send({"jsonrpc": "2.0", "id": rid, "result": result})


def err(rid, code, message):
    send({"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}})


def tools():
    return [
        {
            "name": "probe_info",
            "description": "回显本子进程的 cwd / pid / argv / 指定环境变量（JSON 文本）",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "keys": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "要回显的环境变量名；缺省 = 全部 PROBE_ 前缀变量",
                    },
                },
            },
        },
        {
            "name": "probe_pid",
            "description": "回显本子进程 pid 与 cwd（isolate 同 work_dir 复用同一进程比对用）",
            "inputSchema": {"type": "object", "properties": {}},
        },
    ]


def pick_env(keys):
    """按 keys 回显环境变量；keys 缺省 = 全部 PROBE_ 前缀变量。"""
    if isinstance(keys, list) and keys:
        return {str(k): os.environ.get(str(k), "<unset>") for k in keys}
    return {k: v for k, v in os.environ.items() if k.startswith(ENV_PREFIX)}


def call_tool(rid, name, args):
    if name == "probe_info":
        payload = {
            "cwd": os.getcwd(),
            "pid": os.getpid(),
            "argv": list(sys.argv),
            "env": pick_env(args.get("keys")),
        }
        ok(rid, {"content": [{"type": "text", "text": json.dumps(payload, ensure_ascii=False)}],
                 "isError": False})
        return
    if name == "probe_pid":
        payload = {"pid": os.getpid(), "cwd": os.getcwd()}
        ok(rid, {"content": [{"type": "text", "text": json.dumps(payload, ensure_ascii=False)}],
                 "isError": False})
        return
    err(rid, -32602, "unknown tool: %s" % name)


def main():
    log("started pid=%d cwd=%s argv=%s" % (os.getpid(), os.getcwd(), sys.argv[1:]))

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except ValueError:
            log("skip non-JSON line: %r" % line[:200])
            continue
        if not isinstance(req, dict):
            continue

        method = req.get("method") or ""
        rid = req.get("id")

        # 通知（无 id）：不回帧
        if rid is None and method.startswith("notifications/"):
            if method == "notifications/initialized":
                log("client initialized")
            continue

        if method == "initialize":
            params = req.get("params") or {}
            ok(rid, {
                "protocolVersion": params.get("protocolVersion") or PROTOCOL_VERSION,
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": SERVER_NAME, "version": SERVER_VERSION},
            })
        elif method == "ping":
            ok(rid, {})
        elif method == "tools/list":
            ok(rid, {"tools": tools()})
        elif method == "tools/call":
            params = req.get("params") or {}
            cargs = params.get("arguments") or {}
            if not isinstance(cargs, dict):
                cargs = {}
            call_tool(rid, params.get("name") or "", cargs)
        elif method == "initialized":
            continue
        else:
            err(rid, -32601, "method not found: %s" % method)

    log("stdin closed, exit pid=%d" % os.getpid())


if __name__ == "__main__":
    main()
