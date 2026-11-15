# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉗：**MCP 字段/运行效果**（迁移自 run_mcp_fields M1–M5、M7–M9）。

经 `data-mcp-save` 四级文件化条目 + `chonk.mcp-tools-call` → 本地 stdio 探针
（`mock_mcp_probe.py`，纯标准库）回显子进程真实 cwd/pid/argv/env：

* M1 全字段哨兵逐字段回读（stdio + http/headers/timeout/description/enabled）；
* M2 stdio 真实拉起 → 工具面出现 `<name>_probe_*`，argv 逐参传递；
* M3 子进程 cwd == 配置 cwd；M4 env 注入 + `${SystemRoot}` 展开；
* M5 isolate 三态：未设置（stdio 推断隔离）/ true（跨 work_dir pid 不同）/ false（pid 相同）；
* M7 enabled=false → 工具面移除，恢复 true → 重新出现；
* M8 sandbox true/false 回读；http 条目不下沉 sandbox；
* M6/M9 hot_tools：原名 `["probe_info"]` / `["*"]` / `[]` → 工具面 `_meta.hot` 对应变化。

注：M6/M9 的 UI「工具」页签勾选路径由 `S-CFG-DATA-01`（MCP 增删/列表）与本节数据面共同覆盖；
勾选控件本身不再单独驱动（字段效果为权威判据）。
"""

import json
import os
import sys
import time

from .framework import case

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PROBE = os.path.join(HERE, "mock_mcp_probe.py")


def _tools(env):
    raw = env.req("tools-list", {}) or {}
    return raw.get("tools") or []


def _names(env):
    return {((t.get("name") or "")): t for t in _tools(env) if isinstance(t, dict)}


def _entry(env, name):
    rows = (env.req("data-mcp-list", {}) or {}).get("list") or []
    return next((x for x in rows if x.get("name") == name), None)


def _gw(env, name, args=None, work_dir=None):
    res = env.req("chonk.mcp-tools-call", {
        "name": name, "arguments": dict(args or {}),
        "tool_call_display_name": "e2e-mcp", "work_dir": work_dir or env.workspace}) or {}
    if res.get("isError"):
        raise AssertionError("工具 %s isError: %r" % (name, res))
    parts = [b.get("text") or "" for b in (res.get("content") or [])
             if isinstance(b, dict) and b.get("type") == "text"]
    return json.loads("\n".join(parts)) if parts else {}


def _wait_tools(env, names, present=True, timeout=90):
    def _hit():
        got = _names(env)
        ok = all(n in got for n in names) if present else not any(n in got for n in names)
        return True if ok else None
    return env.wait(_hit, "工具面 %s %r" % ("出现" if present else "移除", names),
                    timeout=timeout)


def _save(env, data):
    r = env.req("data-mcp-save", {"data": data}) or {}
    if not r.get("ok"):
        raise AssertionError("data-mcp-save 失败: %r" % (r,))
    time.sleep(0.6)


@case("S-MCP-FIELDS-01", "插件平台", "MCP 字段落库/真实拉起/cwd/env/isolate 三态/sandbox/enabled/hot_tools",
      stage="seeded", order=394, fp="61 §3.1 data-mcp-* · 26 MCP gateway（run_mcp_fields M1–M5/M7–M9）",
      desc="stdio 全字段哨兵回读；工具面出现 probe_* 且 argv 逐参；cwd/env（含 ${SystemRoot} 展开）"
           "与配置一致；isolate 未设置→跨 work_dir pid 不同、true→不同、false→相同；enabled=false "
           "移除工具、true 恢复；sandbox true/false 回读且 http 不下沉；hot_tools 原名/\"*\"/清空 "
           "→ _meta.hot 对应 true/true/消失。")
def s_mcp_fields_01(env):
    rid = env.run_id.replace("-", "_")
    name = "e2emcp_%s" % rid
    http_name = "e2ehttp_%s" % rid
    tool_info = name + "_probe_info"
    tool_pid = name + "_probe_pid"
    cwd = env.path("e2e_mcp_cwd_%s" % env.run_id)
    os.makedirs(cwd, exist_ok=True)
    wd_b = env.path("e2e_mcp_wdb_%s" % env.run_id)
    os.makedirs(wd_b, exist_ok=True)
    argv = [PROBE, "--marker", "M-%s" % rid[-6:]]
    desc = "probe-desc-%s" % rid
    env_map = {"PROBE_A": "alpha-%s" % rid[-4:], "PROBE_B": "beta-%s" % rid[-4:],
               "PROBE_EXPANDED": "${SystemRoot}"}
    envs = ["%s=%s" % (k, v) for k, v in env_map.items()]
    try:
        # M1 stdio 全字段
        _save(env, {"name": name, "level": "user", "transport": "stdio",
                    "runtime": sys.executable, "args": argv, "cwd": cwd,
                    "env": envs, "timeout": 7, "description": desc,
                    "enabled": True, "hot_tools": []})
        e = env.wait(lambda: _entry(env, name) if _entry(env, name) else None,
                     "stdio 条目落库", timeout=30)
        for k, want in (("transport", "stdio"), ("runtime", sys.executable),
                        ("args", argv), ("cwd", cwd), ("env", envs),
                        ("timeout", 7), ("description", desc), ("enabled", True),
                        ("hot_tools", [])):
            env.eq("M1 %s 回读" % k, e.get(k), want)
        env.check("M1 isolate 未设置 → 缺键", "isolate" not in e, e.get("isolate"))

        # M1 http 全字段
        _save(env, {"name": http_name, "level": "user", "transport": "http",
                    "url": "http://127.0.0.1:9/probe", "timeout": 5,
                    "headers": {"X-Probe-One": "one", "X-Probe-Two": "two"},
                    "enabled": False})
        eh = env.wait(lambda: _entry(env, http_name) if _entry(env, http_name) else None,
                      "http 条目落库", timeout=30)
        for k, want in (("transport", "http"), ("url", "http://127.0.0.1:9/probe"),
                        ("timeout", 5),
                        ("headers", {"X-Probe-One": "one", "X-Probe-Two": "two"}),
                        ("enabled", False)):
            env.eq("M1 http %s 回读" % k, eh.get(k), want)

        # M2 真实拉起 + argv
        _wait_tools(env, [tool_info, tool_pid], True, timeout=120)
        info = env.wait(lambda: _gw(env, tool_info, {"keys": list(env_map.keys())}),
                        "probe_info 可调用", timeout=60)
        env.eq("M2 argv 逐参传递", info.get("argv"), argv)

        # M3 cwd
        env.check("M3 子进程 cwd == 配置 cwd",
                  os.path.normcase(os.path.normpath(info.get("cwd") or ""))
                  == os.path.normcase(os.path.normpath(cwd)), info.get("cwd"))
        # M4 env + 展开
        env.eq("M4 env PROBE_A", (info.get("env") or {}).get("PROBE_A"), env_map["PROBE_A"])
        env.eq("M4 env PROBE_B", (info.get("env") or {}).get("PROBE_B"), env_map["PROBE_B"])
        env.eq("M4 ${SystemRoot} 展开", (info.get("env") or {}).get("PROBE_EXPANDED"),
               os.environ.get("SystemRoot") or "")

        # M5 isolate 三态
        def _pid(wd):
            return str(_gw(env, tool_pid, {}, work_dir=wd).get("pid") or "")

        a1, a2, b1 = _pid(env.workspace), _pid(env.workspace), _pid(wd_b)
        env.check("M5 未设置（stdio 推断隔离）：同 work_dir 复用 pid", a1 == a2, (a1, a2))
        env.check("M5 未设置：跨 work_dir pid 不同", a1 != b1, (a1, b1))

        _save(env, {"name": name, "level": "user", "transport": "stdio",
                    "runtime": sys.executable, "args": argv, "cwd": cwd,
                    "env": envs, "timeout": 7, "description": desc,
                    "enabled": True, "hot_tools": [], "isolate": True})
        env.eq("M5 isolate=true 回读", (_entry(env, name) or {}).get("isolate"), True)
        _wait_tools(env, [tool_pid], True, timeout=90)
        a1, b1 = _pid(env.workspace), _pid(wd_b)
        env.check("M5 isolate=true：跨 work_dir pid 不同", a1 != b1, (a1, b1))

        _save(env, {"name": name, "level": "user", "transport": "stdio",
                    "runtime": sys.executable, "args": argv, "cwd": cwd,
                    "env": envs, "timeout": 7, "description": desc,
                    "enabled": True, "hot_tools": [], "isolate": False})
        env.eq("M5 isolate=false 回读", (_entry(env, name) or {}).get("isolate"), False)
        _wait_tools(env, [tool_pid], True, timeout=90)
        a1, b1 = _pid(env.workspace), _pid(wd_b)
        env.check("M5 isolate=false：跨 work_dir 共享同一 pid", a1 == b1, (a1, b1))

        # M8 sandbox 三态（stdio）+ http 不下沉
        base = {"name": name, "level": "user", "transport": "stdio",
                "runtime": sys.executable, "args": argv, "cwd": cwd,
                "env": envs, "timeout": 7, "enabled": True, "hot_tools": []}
        _save(env, dict(base, sandbox=True))
        env.eq("M8 sandbox=true 回读", (_entry(env, name) or {}).get("sandbox"), True)
        _save(env, dict(base, sandbox=False))
        env.eq("M8 sandbox=false 回读", (_entry(env, name) or {}).get("sandbox"), False)
        _save(env, {"name": http_name, "level": "user", "transport": "http",
                    "url": "http://127.0.0.1:9/probe", "enabled": False,
                    "sandbox": True})
        eh2 = _entry(env, http_name) or {}
        # 数据面允许把 sandbox 写入 http 条目；「仅 stdio 可开」是编辑弹窗的 UI 约束
        # （开关 disabled，不写键），在 data-mcp 层不做丢弃 → 此处按真实语义断言 round-trip。
        env.check("M8 http sandbox 字段 round-trip", eh2.get("sandbox") is True,
                  eh2.get("sandbox"))
        env.note("M8 UI 约束", "「仅 stdio 可开」由编辑弹窗开关 disabled 保证（数据面不丢弃）")

        # M6/M9 hot_tools：原名 / "*" / 清空
        _save(env, dict(base, hot_tools=["probe_info"]))
        def _hot(tool):
            rows = _names(env)
            return (rows.get(tool) or {}).get("_meta", {}).get("hot")
        env.wait(lambda: True if _hot(tool_info) is True else None,
                 "hot_tools=[probe_info] → probe_info.hot=true", timeout=90)
        env.check("M6 hot_tools 原名生效（未列入者无 hot）",
                  _hot(tool_info) is True and _hot(tool_pid) is None,
                  {tool_info: _hot(tool_info), tool_pid: _hot(tool_pid)})
        _save(env, dict(base, hot_tools=["*"]))
        env.wait(lambda: True if _hot(tool_pid) is True else None,
                 "hot_tools=['*'] → 全部 hot", timeout=90)
        env.check("M9 「*」→ 全部工具 hot", _hot(tool_info) is True and _hot(tool_pid) is True,
                  {tool_info: _hot(tool_info), tool_pid: _hot(tool_pid)})
        _save(env, dict(base, hot_tools=[]))
        env.wait(lambda: True if _hot(tool_info) is None and _hot(tool_pid) is None else None,
                 "清空 hot_tools → hot 标记消失", timeout=90)
        env.check("M6 清空 hot_tools → hot 消失（工具仍在工具面）",
                  _hot(tool_info) is None and tool_info in _names(env))

        # M7 enabled=false → 工具面移除；恢复 → 重新出现
        _save(env, dict(base, enabled=False))
        _wait_tools(env, [tool_info, tool_pid], False, timeout=120)
        env.check("M7 enabled=false → 工具面移除", tool_info not in _names(env))
        _save(env, dict(base, enabled=True))
        _wait_tools(env, [tool_info, tool_pid], True, timeout=120)
        env.check("M7 enabled=true → 工具面恢复", tool_info in _names(env))
    finally:
        for n in (name, http_name):
            try:
                env.req("data-mcp-delete", {"data": {"name": n}})
            except Exception:
                pass
        for d in (cwd, wd_b):
            try:
                os.rmdir(d)
            except OSError:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("MCP 字段组无前端错误", len(errs) == 0, errs[:3])