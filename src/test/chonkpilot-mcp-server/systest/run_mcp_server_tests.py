#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""chonkpilot-mcp-server 全量回归测试（Streamable HTTP MCP client）。

用法（默认输入全部**以本脚本位置为基准**解析成绝对路径 → 不带参数即可直接跑通）:
    python run_mcp_server_tests.py

默认输入（四项均可经同名参数覆盖；**传入的相对路径按当前工作目录**解析）:
    --server-exe  <repo>\\dist\\other\\chonkpilot-mcp-server.exe              被测产物
    --root        <repo>\\src\\initdata\\capability                        skills/prompts/resources 契约源（扁平）
    --tools-dir   <repo>\\src\\initdata\\capability\\tools                  `<cat>/*.tool.md` 工具契约源
    --exec-dir    <repo>\\dist\\other\\capability\\executors                executor 产物（扁平 `chonkpilot-<cat>-executor.exe`）

换产物目录（如部署在别处）:
    python run_mcp_server_tests.py --server-exe=D:\\deploy\\chonkpilot-mcp-server.exe \\
                                   --exec-dir=D:\\deploy\\capability\\executors

输入缺失 → **启动 server 前即报错退出**（不静默跳过、不用旧路径兜底），并提示先构建：
    .\\build-mcp-server.ps1（或 .\\build-desktop.ps1）→ dist\\other\\

现行 CLI 契约（`chonkpilot-mcp-server/main.go:3-6,66-94`；spec 25-mcp-server §2）：
    `--http[=<addr>]`（裸 `--http` → 默认 `127.0.0.1:5700`；端点 `/mcp`）· `--stdio` ·
    `--service install|remove|run`（服务内部 = HTTP）。其它 flag：`-root` / `-timeout` / `-config`。
    **无 `--addr`（旧 flag）与 `--exec-dir`（`server/server.go:78` 已移除；executor 按契约文件
    所在目录相对解析 `server/executor.go resolveRuntime`）**。
    （脚本自身的 `--exec-dir` 仅用于**定位构建产物 exe**，不传给 server。）

自管理两个 server 实例：
    5702 默认配置；5703 自定义 --config（skip_dirs=["vendor"]）验证默认参数注入。
--root 提供 skills/prompts/resources（capability 根，扁平）；--tools-dir 提供 tools 契约（src/initdata 唯一源）；
--exec-dir 提供**构建产物的 executor exe**（`capability/executors/`，扁平命名）。
脚本启动 server 前把三者合并到临时契约根（mcp-server 单根递归扫描四原语；tools/<cat>/*.tool.md 的
runtime 写 `../../executors/<exe>`，故 exe 合并到 `executors/` 才可解析）。
调用上下文：DSL 类工具（filesys_run / desktop_run / browser_run）需 `_meta.chonkpilot`
（`instance_id`/`work_dir`/`data_dir`，`server/server.go:63-69 CallContextMeta`），否则报
「缺少 instance」；本脚本 `tool_call(context=...)` 统一注入（等价 gateway 的注入路径）。
退出码: 0 = 全部通过；1 = 存在失败；2 = 输入缺失/参数错误。
"""
import argparse
import http.server
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent
REPO = ROOT.parents[3]  # systest → chonkpilot-mcp-server → test → src → chonkpilot
DIST_OTHER = REPO / "dist" / "other"  # build-mcp-server.ps1 / build-mcp-gateway.ps1 产物目录
DEFAULT_PORT = 5700  # `main.go:29 defaultAddr` = 127.0.0.1:5700（裸 --http / 服务形态默认端口）

# 默认输入：全部以**脚本位置**为基准解析成绝对路径（与 CWD 无关），故无参数直接运行即可跑通。
DEFAULT_SERVER_EXE = DIST_OTHER / "chonkpilot-mcp-server.exe"
DEFAULT_ROOT = REPO / "src" / "initdata" / "capability"
DEFAULT_TOOLS_DIR = REPO / "src" / "initdata" / "capability" / "tools"
DEFAULT_EXEC_DIR = DIST_OTHER / "capability" / "executors"
BUILD_HINT = "先执行 .\\build-mcp-server.ps1（或 .\\build-desktop.ps1）产出 dist/other\\，或用参数指向既有产物目录"

passed = failed = skipped = 0
failures = []


def _die(msg):
    """输入/参数不满足 → stderr 明确报错 + 退出码 2（不静默跳过、不用旧路径兜底）。"""
    print(f"错误: {msg}", file=sys.stderr)
    sys.exit(2)


def call_context(work):
    """调用上下文（与 gateway 注入同构；`server/server.go:63-69 CallContextMeta`）。

    DSL 类工具（filesys_run / desktop_run / browser_run）缺它即报「缺少 instance」。
    """
    return {"instance_id": "l3-systest", "work_dir": work, "data_dir": work}


class SkipTest(Exception):
    """测试跳过（如需要管理员权限）。"""


def check(name, fn):
    global passed, failed, skipped
    try:
        fn()
        passed += 1
        print(f"  PASS  {name}")
    except SkipTest as e:
        skipped += 1
        print(f"  SKIP  {name}: {e}")
    except AssertionError as e:
        failed += 1
        failures.append(f"{name}: {e}")
        print(f"  FAIL  {name}: {e}")
    except Exception as e:  # noqa: BLE001
        failed += 1
        failures.append(f"{name}: {e!r}")
        print(f"  FAIL  {name}: {e!r}")


# ─── 极简 MCP Streamable HTTP client ─────────────────────

_rpc_id = 0


class MCPClient:
    def __init__(self, url):
        self.url = url
        self.session = None
        self.protocol = None

    def rpc(self, method, params=None):
        global _rpc_id
        _rpc_id += 1
        body = {"jsonrpc": "2.0", "id": _rpc_id, "method": method}
        if params is not None:
            body["params"] = params
        req = urllib.request.Request(
            self.url, data=json.dumps(body).encode("utf-8"),
            headers={"Content-Type": "application/json",
                     "Accept": "application/json, text/event-stream"})
        if self.session:
            req.add_header("mcp-session-id", self.session)
        resp = urllib.request.urlopen(req, timeout=90)
        if self.session is None:
            self.session = resp.headers.get("mcp-session-id")
        data = resp.read().decode("utf-8", "replace")
        if "text/event-stream" in resp.headers.get("Content-Type", ""):
            for line in data.splitlines():
                if line.startswith("data:"):
                    frame = json.loads(line[5:].strip())
                    # 跳过单向通知（notifications/*，无 id）；取与请求 id 匹配的响应
                    if frame.get("id") == _rpc_id or "result" in frame or "error" in frame:
                        return frame
            return None
        return json.loads(data)

    def notify(self, method, params=None):
        """单向通知（无 id）。"""
        body = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            body["params"] = params
        req = urllib.request.Request(
            self.url, data=json.dumps(body).encode("utf-8"),
            headers={"Content-Type": "application/json",
                     "Accept": "application/json, text/event-stream"})
        if self.session:
            req.add_header("mcp-session-id", self.session)
        urllib.request.urlopen(req, timeout=90).read()

    def initialize(self):
        res = self.rpc("initialize", {"protocolVersion": "2025-03-26",
                                      "capabilities": {},
                                      "clientInfo": {"name": "ck-test", "version": "1.0"}})
        assert "result" in res, f"initialize failed: {res}"
        self.protocol = res["result"]["protocolVersion"]
        self.capabilities = res["result"].get("capabilities", {})
        self.notify("notifications/initialized")
        return res["result"]

    def call(self, method, params=None):
        res = self.rpc(method, params)
        assert "result" in res, f"{method} error: {res}"
        return res["result"]

    def tools_list(self):
        return self.call("tools/list").get("tools", [])

    def tool_call(self, name, arguments, context=None):
        """tools/call；context 非空 → 注入调用上下文 `_meta`（等价 gateway 注入路径）。"""
        params = {"name": name, "arguments": arguments}
        if context is not None:
            params["_meta"] = {"chonkpilot": context}
        return self.call("tools/call", params)

    def prompts_list(self):
        return self.call("prompts/list").get("prompts", [])

    def prompt_get(self, name, arguments=None):
        return self.call("prompts/get", {"name": name, "arguments": arguments or {}})

    def resources_list(self):
        return self.call("resources/list").get("resources", [])

    def resource_read(self, uri):
        return self.call("resources/read", {"uri": uri})

    def error_call(self, name, arguments=None):
        """期望 method 报错（JSON-RPC error），返回 error dict。"""
        global _rpc_id
        _rpc_id += 1
        body = {"jsonrpc": "2.0", "id": _rpc_id, "method": "tools/call",
                "params": {"name": name, "arguments": arguments or {}}}
        req = urllib.request.Request(
            self.url, data=json.dumps(body).encode("utf-8"),
            headers={"Content-Type": "application/json",
                     "Accept": "application/json, text/event-stream"})
        if self.session:
            req.add_header("mcp-session-id", self.session)
        resp = urllib.request.urlopen(req, timeout=90)
        data = resp.read().decode("utf-8", "replace")
        if "text/event-stream" in resp.headers.get("Content-Type", ""):
            for line in data.splitlines():
                if line.startswith("data:"):
                    return json.loads(line[5:].strip())
        return json.loads(data)

    def rpc_raw(self, method, params=None):
        """原始 JSON-RPC 请求，返回 (http_status, body_text)；HTTP 错误不抛异常。

        用途：断言「方法不受支持」——go-sdk 对未注册方法回 **HTTP 400** +
        body `JSON RPC not handled: "<method>" unsupported`（实测；本仓仅注册
        tools/prompts/resources 四原语，无 skills/*、tasks/* 方法）。
        """
        global _rpc_id
        _rpc_id += 1
        body = {"jsonrpc": "2.0", "id": _rpc_id, "method": method, "params": params or {}}
        req = urllib.request.Request(
            self.url, data=json.dumps(body).encode("utf-8"),
            headers={"Content-Type": "application/json",
                     "Accept": "application/json, text/event-stream"})
        if self.session:
            req.add_header("mcp-session-id", self.session)
        try:
            resp = urllib.request.urlopen(req, timeout=90)
            return resp.status, resp.read().decode("utf-8", "replace")
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode("utf-8", "replace")


# ─── 本地 HTTP echo server（fetch 测试） ─────────────────

class EchoHandler(http.server.BaseHTTPRequestHandler):
    def _send(self, code, body=b"ok"):
        data = body if isinstance(body, bytes) else body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/hello":
            self._send(200, "hello-mcp")
        else:
            self._send(404, "not-found")

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(n).decode("utf-8", "replace")
        self._send(200, f"echo:{body}")

    def log_message(self, *a):  # 静默
        pass


# ─── 测试套件 ───────────────────────────────────────────

def suite_core(core, work):
    """core 工具面 + 四原语（prompts/resources）+ 能力声明 + 调用上下文。

    工具名 = 契约文件名（spec 25-mcp-server §3.1「原语名 = 文件名去后缀」），现行 8 个 =
    `file_read/file_find/file_diff/filesys_run/script_run/web_fetch`（core）+ `desktop_run` +
    `browser_run`（与 L2 `chonkpilot-test/chonkpilot-mcp-server/unittest/server_test.go:141-142` 一致）。
    """
    names = {t["name"] for t in core.tools_list()}
    expect = {"file_read", "file_find", "file_diff", "filesys_run", "script_run",
              "web_fetch", "desktop_run", "browser_run"}
    # 平铺单实例：单根扫描 → 8 个工具全在同一端点（三类 category 不构成路由，spec 25 §7）
    assert names == expect, f"tools 集合不符：多 {names - expect} / 缺 {expect - names}"
    ctx = call_context(work)

    def t_capabilities():
        # 现行能力声明（实测）：logging + tools/prompts/resources 的 listChanged；
        # **无** resources.subscribe、**无** tasks、**无** extensions——
        # 依据 server/server.go:71-159 RegisterContracts 仅 AddTool/AddPrompt/AddResource。
        cap = core.capabilities
        assert cap.get("tools", {}).get("listChanged"), cap
        assert cap.get("prompts", {}).get("listChanged"), cap
        assert cap.get("resources", {}).get("listChanged"), cap
        assert "subscribe" not in cap.get("resources", {}), cap
        assert "tasks" not in cap, cap
        assert "extensions" not in cap, cap

    def t_skills_list():
        # skills 复用 prompt 通道，经 _meta.type 区分（spec 25 §3.1/§4.1/§7）：无 skills/list 方法
        # 集合 = 核心 3（skills/core/）+ 14 个 UX/前端设计技能（skills/ux/，来源 claude-ux/skills/）。
        skills = {p["name"] for p in core.prompts_list() if (p.get("_meta") or {}).get("type") == "skill"}
        expect = {
            "debug", "explore", "sandbox-escape",
            "wireframe", "polish-pass", "make-tweakable", "make-a-prototype", "make-a-deck",
            "interaction-states-pass", "hierarchy-rhythm-review", "generate-variations",
            "frontend-aesthetic-direction", "discovery-questions", "design-system-extract",
            "component-extract", "ai-slop-check", "accessibility-audit",
        }
        assert skills == expect, skills

    def t_skills_get():
        # 取 skill 正文 = prompts/get（skills 无独立方法）；未知名 → JSON-RPC error
        res = core.prompt_get("explore", {"goal": "了解结构"})
        txt = res["messages"][0]["content"]["text"]
        assert "探索项目" in txt, txt[:200]
        bad = core.rpc("prompts/get", {"name": "no_such", "arguments": {}})
        assert "error" in bad, bad

    def t_dir_list():
        # 目录结构（旧 directory_list）→ file_find output=tree（path 必填、须绝对路径）
        res = core.tool_call("file_find", {"path": work, "output": "tree"})
        txt = res["content"][0]["text"]
        assert "seed.txt" in txt, txt
        assert not res.get("isError"), res

    def t_write_read():
        # 写（旧 file_write）→ filesys_run INS DSL；读 → file_read（DSL 工具需调用上下文）
        target = work.replace("\\", "/") + "/a.txt"
        res = core.tool_call("filesys_run",
                             {"script": 'INS #"%s" "hello 中文\\nworld\\n"' % target}, context=ctx)
        assert not res.get("isError"), res
        res = core.tool_call("file_read", {"files": [{"path": os.path.join(work, "a.txt")}]})
        txt = res["content"][0]["text"]
        assert "hello 中文" in txt, txt

    def t_grep():
        # 内容检索（旧 grep）→ file_find grep（path 绝对路径）
        res = core.tool_call("file_find", {"path": work, "grep": "world", "output": "file"})
        txt = res["content"][0]["text"]
        assert "seed.txt" in txt, txt

    def t_script_shell():
        res = core.tool_call("script_run", {"runtime": "shell", "script": "echo mcp-ok"})
        txt = res["content"][0]["text"]
        assert "mcp-ok" in txt, txt

    def t_script_missing_runtime():
        # python 未配置解释器 → 明确报错（引导安装），不崩溃
        res = core.tool_call("script_run", {"runtime": "python", "script": "print(1)"})
        txt = res["content"][0]["text"]
        assert "python" in txt and "未配置" in txt, txt

    def t_fetch():
        # 旧 fetch → 现行 web_fetch（URL 参数 url）
        res = core.tool_call("web_fetch", {"url": f"http://127.0.0.1:{echo_port}/hello"})
        txt = res["content"][0]["text"]
        assert "hello-mcp" in txt, txt

    def t_unknown_tool():
        res = core.error_call("no_such_tool", {})
        assert "error" in res, res

    def t_prompts():
        names = {p["name"] for p in core.prompts_list()}
        assert "code_review" in names, names

    def t_prompt_render():
        res = core.prompt_get("code_review", {"path": "a.txt"})
        txt = res["messages"][0]["content"]["text"]
        assert "a.txt" in txt, txt

    def t_resources():
        uris = {r["uri"] for r in core.resources_list()}
        assert "mcp://chonkpilot/resources/overview" in uris, uris

    def t_resource_read():
        res = core.resource_read("mcp://chonkpilot/resources/overview")
        txt = res["contents"][0]["text"]
        assert "file_read" in txt, txt

    def t_skills_in_prompts():
        # prompts/list 同表含 skills（_meta.type=skill；spec 25 §9）
        names = {p["name"] for p in core.prompts_list()}
        assert {"explore", "debug"} <= names, names

    def t_skill_prompt():
        res = core.prompt_get("explore", {"goal": "了解结构"})
        txt = res["messages"][0]["content"]["text"]
        assert "探索项目" in txt, txt

    def t_skill_resource():
        # 旧 `skill://explore` 资源已不存在（skills 走 prompt 通道，spec 25 §3.1/§7）→
        # 改为经 prompts/get 取 skill 正文并断言其应然内容（含工具面指引）
        res = core.prompt_get("explore", {"goal": "了解结构"})
        txt = res["messages"][0]["content"]["text"]
        assert "探索项目" in txt and "directory_list" in txt, txt[:200]

    def t_task_param_ignored():
        # MCP tasks 不属 mcp-server 契约面（server.go:71-159 只注册四原语；capabilities 无 tasks）：
        # tools/call 带 task 字段不生效 → 同步返回 result、无 task 对象
        res = core.call("tools/call", {"name": "script_run",
                                       "arguments": {"runtime": "shell", "script": "echo task-ok"},
                                       "task": {"ttl": 120000}})
        assert "task" not in res, res
        assert "task-ok" in json.dumps(res, ensure_ascii=False), json.dumps(res, ensure_ascii=False)[:300]
        assert "tasks" not in core.capabilities, core.capabilities

    def t_tasks_list_unsupported():
        # 未注册方法 → HTTP 400 + `JSON RPC not handled: "tasks/list" unsupported`（实测）
        status, body = core.rpc_raw("tasks/list", {})
        assert status == 400 and "unsupported" in body, (status, body)

    def t_tasks_cancel_unsupported():
        status, body = core.rpc_raw("tasks/cancel", {"taskId": "x"})
        assert status == 400 and "unsupported" in body, (status, body)

    for fn in [t_capabilities, t_skills_list, t_skills_get,
               t_dir_list, t_write_read, t_grep, t_script_shell, t_script_missing_runtime,
               t_fetch, t_unknown_tool, t_prompts, t_prompt_render, t_resources,
               t_resource_read, t_skills_in_prompts, t_skill_prompt, t_skill_resource,
               t_task_param_ignored, t_tasks_list_unsupported, t_tasks_cancel_unsupported]:
        check(f"[core] {fn.__name__[2:]}", fn)


def suite_desktop(desk, work):
    """desktop 分类：现行**单工具** `desktop_run`（DSL 编排；spec 25 §3.1 meta `category=desktop`）。

    旧的 18 个细粒度工具（key_down/mouse_click/windows_list…）已收敛为 DSL 指令
    （`src/initdata/capability/tools/desktop/desktop_run.tool.md`：WIN/MOV/CLK/…）。
    """
    tools = {t["name"]: t for t in desk.tools_list()}
    assert "desktop_run" in tools, sorted(tools)
    assert (tools["desktop_run"].get("_meta") or {}).get("category") == "desktop", tools["desktop_run"]

    def t_windows_list():
        # 旧 windows_list 工具 → 现行 `WIN list`（文本输出经 `=> #"file"` 重定向后 file_read 读回）
        wins = work.replace("\\", "/") + "/wins.txt"
        res = desk.tool_call("desktop_run", {"script": 'WIN list => #"%s"' % wins},
                             context=call_context(work))
        assert not res.get("isError"), res
        res = desk.tool_call("file_read", {"files": [{"path": os.path.join(work, "wins.txt")}]})
        txt = res["content"][0]["text"]
        assert "[hwnd=" in txt, txt[:300]

    for fn in [t_windows_list]:
        check(f"[desktop] {fn.__name__[2:]}", fn)


def suite_defaults_injection(defaults_core, work):
    """自定义 --config（skip_dirs=["vendor"]）验证默认参数注入。

    合并口径：`Config.defaultsMap()` 注入 `skip_dirs`，客户端显式同名参数优先
    （`chonkpilot-mcp-server/server/config.go:17-18` + `server/executor.go:60-67`）。
    工具：旧 `grep` → 现行 `file_find`（`grep` 参数 + `skip_dirs` 参数）。
    """
    os.makedirs(os.path.join(work, "vendor"), exist_ok=True)
    with open(os.path.join(work, "vendor", "dep.txt"), "w", encoding="utf-8") as f:
        f.write("SENTINEL_DEFAULT\n")
    with open(os.path.join(work, "ok.txt"), "w", encoding="utf-8") as f:
        f.write("SENTINEL_DEFAULT\n")

    def t_injected():
        # 未显式传 skip_dirs：server 默认配置注入 vendor → 被跳过
        res = defaults_core.tool_call("file_find", {"path": work, "grep": "SENTINEL_DEFAULT", "output": "file"})
        txt = res["content"][0]["text"]
        assert "ok.txt" in txt and "vendor" not in txt, txt

    def t_explicit_override():
        # 客户端显式传 skip_dirs 覆盖默认（不再注入 vendor）→ vendor 出现
        res = defaults_core.tool_call("file_find", {"path": work, "grep": "SENTINEL_DEFAULT",
                                                    "output": "file", "skip_dirs": ["other"]})
        txt = res["content"][0]["text"]
        assert "vendor" in txt, txt

    for fn in [t_injected, t_explicit_override]:
        check(f"[defaults] {fn.__name__[2:]}", fn)


def suite_service(server_exe):
    """Windows service 全流程（install → start → 验证端口 → stop → remove）。
    非管理员环境自动 SKIP。

    注：`sc.exe` 的错误文本为**本机 ANSI（GBK）**，而 server 自身日志为 UTF-8 →
    一律 `encoding="utf-8", errors="replace"` 解码（否则 reader 线程 UnicodeDecodeError，
    stdout/stderr 变 None），SKIP 判定关键词 `OpenSCManager`（ASCII）不受影响。
    """

    def t_service_flow():
        p = subprocess.run([server_exe, "--service", "install"], capture_output=True,
                           text=True, encoding="utf-8", errors="replace")
        if p.returncode != 0:
            out = (p.stdout or "") + (p.stderr or "")
            if "Access is denied" in out or "OpenSCManager" in out:
                raise SkipTest("需要管理员权限（sc create 被拒），跳过 service 全流程")
            raise AssertionError(f"install failed: {out}")
        try:
            s = subprocess.run(["sc", "start", "chonkpilot-mcp-server"], capture_output=True,
                               text=True, encoding="utf-8", errors="replace", timeout=30)
            assert "RUNNING" in s.stdout, s.stdout
            time.sleep(2)
            # 服务已起 → 5700 端口可用（服务用默认配置，端点 /mcp）
            c = MCPClient(f"http://127.0.0.1:{DEFAULT_PORT}/mcp")
            c.initialize()
            names = {t["name"] for t in c.tools_list()}
            assert "file_read" in names, names
        finally:
            subprocess.run(["sc", "stop", "chonkpilot-mcp-server"], capture_output=True,
                           text=True, encoding="utf-8", errors="replace", timeout=30)
            subprocess.run([server_exe, "--service", "remove"], capture_output=True,
                           text=True, encoding="utf-8", errors="replace")

    check("[service] install/start/verify/stop/remove", t_service_flow)


def _wait_port(port, timeout=30):
    """等 TCP 端口可连（server 已监听）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1.0):
                return True
        except OSError:
            time.sleep(0.3)
    return False


def _spawn_server(exe, argv, tag):
    """启动 server 子进程（stdout/stderr 落临时日志，供失败时定位：如 CLI flag 变更）。"""
    log = tempfile.NamedTemporaryFile("w+", suffix=".log", delete=False, encoding="utf-8")
    p = subprocess.Popen([exe, *argv], stdout=log, stderr=subprocess.STDOUT)
    return p, log.name, tag


def _require_alive(entry):
    """server 提前退出 → 抛出（附日志尾部，便于定位 flag/契约不符）。"""
    p, log_path, tag = entry
    if p.poll() is None:
        return
    try:
        with open(log_path, encoding="utf-8", errors="replace") as f:
            tail = f.read()[-500:]
    except OSError:
        tail = "(日志不可读)"
    raise AssertionError(f"server {tag} 已退出（exit={p.returncode}）：{tail}")


def main():
    global echo_port
    ap = argparse.ArgumentParser()
    ap.add_argument("--server-exe", default=str(DEFAULT_SERVER_EXE),
                    help=f"被测 server exe（默认 {DEFAULT_SERVER_EXE}）")
    ap.add_argument("--root", default=str(DEFAULT_ROOT),
                    help=f"契约根（knowledge：skills/prompts/resources）（默认 {DEFAULT_ROOT}）")
    ap.add_argument("--tools-dir", default=str(DEFAULT_TOOLS_DIR),
                    help=f"tools 契约目录（src/initdata/capability/tools）（默认 {DEFAULT_TOOLS_DIR}）")
    ap.add_argument("--exec-dir", default=str(DEFAULT_EXEC_DIR),
                    help=f"执行器产物目录（capability/executors/chonkpilot-<cat>-executor.exe）（默认 {DEFAULT_EXEC_DIR}）")
    args = ap.parse_args()

    # 输入预检（起 server 前 fail-fast）：缺产物一律明确报错，不静默跳过、不用旧路径兜底。
    if not os.path.isfile(args.server_exe):
        _die(f"未找到被测 server exe：{args.server_exe}（--server-exe）\n       {BUILD_HINT}")
    if not os.path.isdir(args.root):
        _die(f"未找到契约根：{args.root}（--root）\n       {BUILD_HINT}")
    if not os.path.isdir(args.tools_dir):
        _die(f"未找到 tools 契约目录：{args.tools_dir}（--tools-dir）\n       {BUILD_HINT}")
    if not os.path.isdir(args.exec_dir):
        _die(f"未找到 executor 产物目录：{args.exec_dir}（--exec-dir）\n       {BUILD_HINT}")
    for prim in ("skills", "prompts", "resources"):
        if not os.path.isdir(os.path.join(args.root, prim)):
            _die(f"契约根缺少 {prim}/ 目录：{os.path.join(args.root, prim)}（--root）")
    cat_dirs = [c for c in sorted(os.listdir(args.tools_dir))
                if os.path.isdir(os.path.join(args.tools_dir, c))]
    if not cat_dirs:
        _die(f"--tools-dir={args.tools_dir} 下无分类目录 <cat>/（{BUILD_HINT}）")
    exe_names = [fn for fn in os.listdir(args.exec_dir) if fn.lower().endswith(".exe")]
    if not exe_names:
        _die(f"未在 --exec-dir={args.exec_dir} 找到 executor exe\n       {BUILD_HINT}")
    for cat in cat_dirs:
        want = f"chonkpilot-{cat}-executor.exe"
        if want not in exe_names:
            _die(f"--exec-dir 缺 {want}（与 --tools-dir 分类 {cat} 对应）\n       {BUILD_HINT}")
    print(f"被测 server: {args.server_exe}")
    print(f"契约源: --root={args.root} + --tools-dir={args.tools_dir}；executor 产物: {args.exec_dir}")

    # 本地 echo server
    echo = http.server.ThreadingHTTPServer(("127.0.0.1", 0), EchoHandler)
    echo_port = echo.server_address[1]
    threading.Thread(target=echo.serve_forever, daemon=True).start()

    # tools 契约在 src/initdata/capability/tools；与 skills/prompts/resources 源契约合并为单根（扁平），
    # 并把 executor exe 放进 tmp_root/executors/（tool.md runtime 相对契约文件写 ../../executors/<exe>）
    tmp_root = tempfile.mkdtemp(prefix="ck-contracts-")
    for prim in ("skills", "prompts", "resources"):
        shutil.copytree(os.path.join(args.root, prim), os.path.join(tmp_root, prim))
    shutil.copytree(args.tools_dir, os.path.join(tmp_root, "tools"))
    os.makedirs(os.path.join(tmp_root, "executors"), exist_ok=True)
    n_exe = 0
    for fn in exe_names:
        shutil.copy2(os.path.join(args.exec_dir, fn), os.path.join(tmp_root, "executors", fn))
        n_exe += 1
    merged_root = tmp_root
    cfg_path = None

    servers = []
    try:
        # 默认 server（5702）：--http 带值形态（main.go:31-46 optionalString，`--http=<addr>`）
        servers.append(_spawn_server(args.server_exe,
                                     ["--http=127.0.0.1:5702", f"--root={merged_root}"], "5702"))
        # 自定义 config server（5703）：skip_dirs 注入 vendor
        cfg = {"defaults": {"skip_dirs": ["vendor"]}}
        with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8") as f:
            json.dump(cfg, f)
            cfg_path = f.name
        servers.append(_spawn_server(args.server_exe,
                                     ["--http=127.0.0.1:5703", f"--root={merged_root}",
                                      f"--config={cfg_path}"], "5703"))
        for entry in servers:
            if not _wait_port(int(entry[2])):
                _require_alive(entry)
                raise AssertionError(f"server {entry[2]} 端口 30s 未就绪")
            _require_alive(entry)

        # 平铺单实例：core/desktop/browser 契约都在同一根端点（四原语全平铺；端点 /mcp）
        core = MCPClient("http://127.0.0.1:5702/mcp")
        desk = MCPClient("http://127.0.0.1:5702/mcp")
        dcore = MCPClient("http://127.0.0.1:5703/mcp")

        with tempfile.TemporaryDirectory(prefix="ck-mcp-") as work:
            # 夹具：seed.txt 供「目录/内容检索」用例（先于写用例，避免顺序耦合）
            with open(os.path.join(work, "seed.txt"), "w", encoding="utf-8") as f:
                f.write("hello 中文\nworld\n")
            core.initialize()
            desk.initialize()
            dcore.initialize()
            suite_core(core, work)
            suite_desktop(desk, work)
            suite_defaults_injection(dcore, work)

    finally:
        for entry in servers:
            entry[0].terminate()
        echo.shutdown()
        if tmp_root:
            shutil.rmtree(tmp_root, ignore_errors=True)
        if cfg_path and os.path.exists(cfg_path):
            os.remove(cfg_path)
        for entry in servers:
            try:
                os.remove(entry[1])
            except OSError:
                pass

    # service 用例独立于脚本自管 server（服务用默认 5700 端口）
    suite_service(args.server_exe)

    print(f"\n汇总: {passed} 通过 / {failed} 失败 / {skipped} 跳过")
    if failed:
        print("失败明细:")
        for f in failures:
            print(f"  - {f}")
        sys.exit(1)


if __name__ == "__main__":
    main()
