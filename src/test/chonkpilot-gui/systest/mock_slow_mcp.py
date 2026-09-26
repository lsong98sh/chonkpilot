#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""慢速第三方 MCP server 夹具（stdio，纯标准库，无第三方依赖）。

用途：验证「统一异步模型：超时交用户裁决」中的**第三方 spawned server** 路径——
gateway 以 usr `mcps` 条目 `{runtime:<python>, args:[mock_slow_mcp.py, --sleep, N]}` 拉起本进程
（runtime = 可执行/解释器单 token，args = 逐个 exec 参数，不做 shell/引号解析），
`tools/call slow_sleep` 睡眠超过条目超时 → 到点发 `mcp-tools-timeout`（第三方软缺省 never
→ options=[wait,cancel]）；取消 = kill 本子进程 + 按 restart 策略 respawn。

协议要点：
  - JSON-RPC 2.0 over stdin/stdout，**逐行**（一行一条消息，Line-delimited）。
  - stdout **只**输出 JSON-RPC 帧；任何日志一律走 stderr（避免污染协议流）。
  - 支持 initialize / notifications/initialized / ping / tools/list / tools/call。
  - 不安装任何信号处理器：SIGTERM/SIGKILL 直接终止（便于测试 kill + respawn）。

用法：
  python mock_slow_mcp.py [--sleep 3] [--name slow_sleep] [--pid-file <path>]

工具：
  slow_sleep {seconds?}  → 睡眠（缺省用 --sleep）后返回文本 "slept Ns (pid=...)"
  fast_echo  {text?}     → 立即返回文本 "echo: <text> (pid=...)"（对照用，验证 respawn 后可用）
"""

import argparse
import json
import os
import sys
import time

PROTOCOL_VERSION = "2024-11-05"
SERVER_NAME = "mock-slow-mcp"
SERVER_VERSION = "1.0.0"


def log(msg):
    """日志走 stderr（stdout 仅供 JSON-RPC）。"""
    print("[mock_slow_mcp] " + str(msg), file=sys.stderr, flush=True)


def send(obj):
    """向 stdout 写一条 JSON-RPC 帧（逐行 + flush）。"""
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def ok(rid, result):
    send({"jsonrpc": "2.0", "id": rid, "result": result})


def err(rid, code, message):
    send({"jsonrpc": "2.0", "id": rid, "error": {"code": code, "message": message}})


def tools(default_sleep):
    return [
        {
            "name": "slow_sleep",
            "description": "睡眠 N 秒后返回文本（缺省 %.3g 秒；启动参数 --sleep 可改）" % default_sleep,
            "inputSchema": {
                "type": "object",
                "properties": {
                    "seconds": {"type": "number", "description": "睡眠秒数，缺省用启动参数"},
                },
            },
        },
        {
            "name": "fast_echo",
            "description": "立即回显 text 参数（对照用；验证服务可用/进程已 respawn）",
            "inputSchema": {
                "type": "object",
                "properties": {"text": {"type": "string", "description": "回显内容"}},
            },
        },
    ]


def call_tool(rid, name, args, default_sleep):
    if name == "slow_sleep":
        secs = args.get("seconds", default_sleep)
        try:
            secs = float(secs)
        except (TypeError, ValueError):
            secs = float(default_sleep)
        log("slow_sleep sleeping %ss (pid=%d)" % (secs, os.getpid()))
        time.sleep(secs)  # 被 kill 时直接终止（无信号处理）
        ok(rid, {"content": [{"type": "text", "text": "slept %ss (pid=%d)" % (secs, os.getpid())}],
                 "isError": False})
        return
    if name == "fast_echo":
        text = str(args.get("text", ""))
        ok(rid, {"content": [{"type": "text", "text": "echo: %s (pid=%d)" % (text, os.getpid())}],
                 "isError": False})
        return
    err(rid, -32602, "unknown tool: %s" % name)


def main():
    ap = argparse.ArgumentParser(description="慢速第三方 MCP server 夹具（stdio）")
    ap.add_argument("--sleep", "-s", type=float, default=3.0, help="slow_sleep 缺省睡眠秒数")
    ap.add_argument("--pid-file", default="", help="把本进程 pid 写入该文件（测试断言 kill/respawn 换进程用）")
    args = ap.parse_args()

    if args.pid_file:
        try:
            with open(args.pid_file, "w", encoding="utf-8") as f:
                f.write(str(os.getpid()))
        except OSError as e:
            log("write pid file failed: %s" % e)

    log("started pid=%d default_sleep=%s argv=%s" % (os.getpid(), args.sleep, sys.argv[1:]))

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
            ok(rid, {"tools": tools(args.sleep)})
        elif method == "tools/call":
            params = req.get("params") or {}
            name = params.get("name") or ""
            cargs = params.get("arguments") or {}
            if not isinstance(cargs, dict):
                cargs = {}
            call_tool(rid, name, cargs, args.sleep)
        elif method in ("initialized",):
            continue
        else:
            err(rid, -32601, "method not found: %s" % method)

    log("stdin closed, exit pid=%d" % os.getpid())


if __name__ == "__main__":
    main()
