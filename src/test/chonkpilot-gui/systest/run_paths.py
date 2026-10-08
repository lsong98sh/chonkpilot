# -*- coding: utf-8 -*-
"""P1 批次：usr 路径 / 工具链配置的 **A（落库确认）+ B（效果确认）** 端到端。

覆盖键 = `chonkpilot-data/persist/persist_userconfig.go:22-42 userConfigKeyKinds` 的**路径族全集**
（**7 键**，白名单内无第 8 个）：
    chromePath / javaPath / pythonPath / nodePath / goPath / rustPath / cCompilerPath

A（落库确认）：
  * 逐键写哨兵值 → `data-user-config-load` 回读 == 写入值（7/7，且 load 恒返回全部 7 键）；
  * **无串写**：单键改写（goPath）→ 其余 6 键不变；写回 → 7/7 恢复；
  * **重启后**再回读 → 7/7 仍一致（usr 主库持久，非内存态）。

B（效果确认；一律真实链路可观测证据，无「保存成功即算过」）：
  * `goPath` / `rustPath` / `cCompilerPath` → `{{toolchain.go|rust|c}}` 占位符（场景主 agent 提示词）
    → mock LLM `/last` 的 **system 原文**（`chonkpilot-llm/server/toolchain.go:35 toolchainVars`
    → `mcpms.ReplaceToolchain`）；
  * `pythonPath` / `nodePath` / `javaPath` → `CHONKPILOT_INTERPRETERS`（executor 子进程 env，
    `chonkpilot-mcp-server/server/config.go:218 executorEnv`）→ **`script_run` 真实执行**，
    以「被启动进程回显自身可执行路径（`process.execPath`）」为解释器选型证据
    （`chonkpilot-mcp-tools/internal/scriptrun/exec.go:191 resolveInterpreter`）；
  * `chromePath` → `CHONK_CHROME`（同链路子进程 env，经 cmd 探针回显）；
  * **java 真机（I-80 回归，套件末段追加一次）**：`javaPath` = 本机**真实** `java`
    （`shutil.which("java")`；无 → 打印原因并跳过）→ 再重启一次（解释器 env 需重启才注入）→
    `script_run(runtime=java, script=<打印哨兵 `CK-JAVA-OK` 的类>)` **真跑**，断言工具输出含哨兵。
    夹具（假解释器）只证明「选型 = 配置值」，真机 java 证明**java runtime 真的能跑**
    （临时脚本扩展名必须 `.java`：JDK 单文件源码模式 JEP 330；`.jsh` 会被当类名 →
    ClassNotFoundException，见 `chonkpilot-mcp-tools/internal/scriptrun/exec.go:47 scriptExt`）。

**需重启才生效（本套件真实重启：基线后一次；java 真机段再一次）**：
  解释器 / 浏览器 env 与 capability 原语取值在 **instance-register** 时注入
  （`chonkpilot-llm/server/server.go:564 loadExecConfig` → `mcpCfg.SetRuntime/SetToolchain`）；usr 配置
  刷新 `data-user-config-refresh` **不**重跑执行配置（`server.go:343 onUserConfigRefresh` 只做 usr mcps
  对账）→ 故本套件：实例 #1 写配置并采集「重启前」基线（断言 env **未**注入）→ `restart` → 实例 #2
  （同 HOME / work-dir / data-dir）采集 B 证据。
  反例（**无需重启**，同套件采集为证据）：场景主 agent 提示词里的 `{{toolchain.*}}` 为**每次调用**读 usr
  配置（`toolchain.go:51 replaceToolchain` → `toolchainVars` 逐次 `data-user-config-load`）→ 重启前即已生效。

隔离与还原（51-FP与测试映射 §6-8）：
  * **自起** GUI：动态端口 + 临时 work-dir / data-dir / HOME（`_h.tmp_home()` → 子实例
    `USERPROFILE` 指向临时目录）→ usr 主库全新，**不读不写机器 `~/.chonkpilot`**；
  * 被测值全部指向**哨兵路径**（`C:\\probe\\<tag>\\...`）或**可控副本**（`%TEMP%` 下
    `ck-paths-bin-*`，随 `harness.tmp_dir` 一并删除），**不碰本机真实安装**；
  * 假解释器口径（任务允许「可控的假解释器或真实 python 的副本」）：本机 Python 3.14 为 2.3GB 且
    `python.exe` 不可单独复制（缺 `python314.dll` / `Lib`，裸复制 exit 0xC0000135）→ `pythonPath`
    / `javaPath` 指向 **node.exe 副本重命名**（node 为自包含单文件 exe，可复制）；`nodePath` 用真
    node 副本（真解释器 + 真脚本）。判定依据 = 被启动进程回显的 `process.execPath`，与配置值一致；
  * 配置写入/还原 = `_h.snapshot_config` / `_h.restore_config`（套件级 usr+prj 全量，见 `finally`），
    末尾复核「7 键回落空串 + llms 清空」；
  * **零新增 MQ 主题**：只用 61-消息一览既有主题（`data-user-config-*` / `data-scenario-*` /
    `data-session-load-messages` / `llm-start` / `llm-complete` / `turn-start` / `tool-result` 兼容事件）
    + `mock_llm.py` 新增的**测试桩**路由 `call interp-probe`。

运行：python run_paths.py（自起自收，无需外部底座）
"""

import json
import os
import shutil
import sys
import time
import urllib.request

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

# ══════════════════════════════════════════════════════════
# 夹具：哨兵值 / 假解释器 / 场景
# ══════════════════════════════════════════════════════════

TAG = str(int(time.time()))[-6:]
SC_ID = "pathprobe-" + TAG
SC_NAME = "路径占位符探针-" + TAG

# 路径族全集（persist_userconfig.go userConfigKeyKinds；load 恒补齐这 7 键）
PATH_KEYS = ["chromePath", "javaPath", "pythonPath", "nodePath", "goPath", "rustPath", "cCompilerPath"]

# 场景主 agent 提示词：7 个已知 key 占位符 + 未知 key + {{arg}}（后两者应原样保留）
PROBE_PROMPT = "\n".join([
    "PATHS-PROBE-" + TAG,
    "GO={{toolchain.go}}",
    "RUST={{toolchain.rust}}",
    "C={{toolchain.c}}",
    "PY={{toolchain.python}}",
    "NODE={{toolchain.node}}",
    "JAVA={{toolchain.java}}",
    "CHROME={{toolchain.chrome}}",
    "UNKNOWN={{toolchain.unknown}}",
    "ARG={{arg}}",
])
# 占位符 → 配置键（断言 `LINE=<该键配置值>`）
PROMPT_LINES = [("GO", "goPath"), ("RUST", "rustPath"), ("C", "cCompilerPath"),
                ("PY", "pythonPath"), ("NODE", "nodePath"), ("JAVA", "javaPath"),
                ("CHROME", "chromePath")]

# 哨兵目录（临时，结束即删）：真 node 副本 + 两个「假解释器」（node 副本改名）
BIN = _h.tmp_dir("ck-paths-bin-")
NODE_SRC = shutil.which("node") or r"D:\DevTools\nvm4w\nodejs\node.exe"
if not (NODE_SRC and os.path.isfile(NODE_SRC)):
    raise RuntimeError("run_paths: 需要 node.exe 作为可控解释器夹具（真/假解释器副本），未找到: %r" % NODE_SRC)


def _copy_exe(sub, name):
    d = os.path.join(BIN, sub)
    os.makedirs(d, exist_ok=True)
    dst = os.path.join(d, name)
    shutil.copy2(NODE_SRC, dst)
    return dst


NODE_PATH = _copy_exe("node", "node.exe")        # nodePath：真 node 副本
PY_PATH = _copy_exe("pyfake", "python.exe")      # pythonPath：假解释器（node 副本）
JAVA_PATH = _copy_exe("javafake", "java.exe")    # javaPath：假解释器（node 副本）

# 真机 java（I-80 回归段用；None → 该段打印原因并跳过，不算失败）
JAVA_REAL = shutil.which("java")

SENT = {
    "chromePath": r"C:\probe\%s\chrome\chrome.exe" % TAG,
    "goPath": r"C:\probe\%s\go\go.exe" % TAG,
    "rustPath": r"C:\probe\%s\rust\rustc.exe" % TAG,
    "cCompilerPath": r"C:\probe\%s\c\gcc.exe" % TAG,
    "nodePath": NODE_PATH,
    "pythonPath": PY_PATH,
    "javaPath": JAVA_PATH,
}
ALT_GO = r"C:\probe\%s\go-alt\go.exe" % TAG

# 隔离实例（临时目录 / 动态端口；由 harness 结束即回收）
WS = _h.tmp_dir("ck-paths-ws-")
DD = _h.tmp_dir("ck-paths-dd-")
HOME = _h.tmp_home()
MOCK = _h.start_mock_llm(_h.free_port())
MOCK_ENTRY = {"name": "mock", "protocol": "openai", "apiKey": "", "model": "mock-model",
              "baseUrl": "http://127.0.0.1:%d/v1" % MOCK.port, "temperature": 0.7, "maxOutputToken": 4096}
GUI_ARGS = ("-llm-base=http://127.0.0.1:%d/v1" % MOCK.port, "-llm-model=mock")

CUR = [None]     # 当前活实例 client（重启后切换）
PORTS = []       # 自起端口（残留检查用）
_TURN = [0]


# ══════════════════════════════════════════════════════════
# 通用工具
# ══════════════════════════════════════════════════════════

def deep(v):
    """循环解包 JSON 字符串（test-port eval 结果可能被多重编码）。"""
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


def evi(case, **kw):
    print("[EVIDENCE] " + json.dumps({"case": case, **kw}, ensure_ascii=False), flush=True)


def user_cfg():
    r = deep(CUR[0].req("data-user-config-load", {})) or {}
    return (r.get("data") or {}) if isinstance(r, dict) else {}


def mock_last():
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/last" % MOCK.port, timeout=5) as r:
            return json.loads(r.read().decode("utf-8")) or {}
    except Exception:
        return {}


def mock_reset():
    try:
        urllib.request.urlopen("http://127.0.0.1:%d/reset" % MOCK.port, timeout=5).read()
    except Exception:
        pass
    time.sleep(0.2)


def _norm(s):
    """折叠多重转义（工具结果文本经 wrapSuccess 二次转义 → `\\\\`/`\\"`）→ 单层，便于子串断言。

    仅用于**文本匹配**；JSON 解析取解包一层后的 `output`（那里转义仍是合法 JSON）。
    """
    for _ in range(4):
        nxt = s.replace("\\\\", "\\").replace('\\"', '"')
        if nxt == s:
            break
        s = nxt
    return s


def _between(s, pre, post="]"):
    i = s.find(pre)
    if i < 0:
        return ""
    rest = s[i + len(pre):]
    j = rest.find(post)
    return rest[:j] if j >= 0 else rest


def _line_with(s, prefix):
    """取首个（strip 后）以 prefix 开头的行（无 → ""）。"""
    for ln in (s or "").splitlines():
        t = ln.strip()
        if t.startswith(prefix):
            return t
    return ""


# ── 轮次驱动（mq；主题全部取自 61-消息一览）───────────────

def run_turn(q, scenario=""):
    """发一轮 llm-start（mock 按关键词回 tool_call）→ 返回 (session, turn)。"""
    _TURN[0] += 1
    sid = "paths-%d-%03d" % (int(time.time() * 1000), _TURN[0])
    turn = "t-" + sid
    c = CUR[0]
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["turn-start", "tool-result", "llm-complete"])
    c.mq_emit("session-changed", {"session_id": sid})   # 新会话 → DOM/消息与上一轮隔离
    c.mq_emit("llm-start", {"session_id": sid, "turn": turn, "q": q, "llm": "mock",
                            "think": "", "effort": "", "scenario_id": scenario})
    return sid, turn


def wait_tool_result(turn, tool_suffix, max_wait=120):
    """等该轮某工具的 tool-result（兼容事件 `tool-result`，61 §6；网关侧 200 字摘要）。"""
    c = CUR[0]
    deadline = time.time() + max_wait
    payload = None
    while time.time() < deadline:
        for e in c.events_of("tool-result", clear=False):
            p = deep(e.get("payload")) or {}
            if isinstance(p, dict) and p.get("turn_id") == turn \
                    and (p.get("tool") or "").endswith(tool_suffix):
                payload = p
        if payload is not None:
            break
        time.sleep(0.3)
    if payload is None:
        raise TestError("未收到 %s 的 tool-result（turn=%s）" % (tool_suffix, turn))
    return payload


def wait_turn_done(sid, max_wait=120):
    """等本会话该轮终态 llm-complete。"""
    c = CUR[0]
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for e in c.events_of("llm-complete", clear=False):
            p = deep(e.get("payload")) or {}
            if isinstance(p, dict) and p.get("session") == sid:
                return p
        time.sleep(0.3)
    raise TestError("轮次未收尾（session=%s）" % sid)


def tool_output(turn):
    """该轮工具结果原文（`data-session-load-messages`，61 §3.2；全文，无 200 字摘要截断）。

    返回 (解包后的 output 文本, 归一化全文)。JS 探针正文与 CHROME/INTERPS 断言都取自前者
    （escapes 正确，可直接 json.loads）。
    """
    r = deep(CUR[0].req("data-session-load-messages", {"turn_id": turn})) or {}
    msgs = (r.get("messages") or []) if isinstance(r, dict) else []
    contents = []
    for m in msgs:
        m = deep(m) or {}
        if isinstance(m, dict) and m.get("role") == "tool" and m.get("content"):
            contents.append(m["content"])
    if not contents:
        raise TestError("未取到该轮 tool 消息（turn=%s，messages=%d）" % (turn, len(msgs)))
    txt = contents[-1]
    try:
        o = json.loads(txt)
        if isinstance(o, dict) and isinstance(o.get("output"), str):
            txt = o["output"]
    except Exception:
        pass
    return txt, _norm("\n".join(contents))


def probe(rt):
    """发一轮探针 → 返回 (output 文本, 归一化全文, tool-result 摘要)。"""
    sid, turn = run_turn("call interp-probe rt=%s" % rt)
    p = wait_tool_result(turn, "script_run")
    wait_turn_done(sid)
    txt, norm = tool_output(turn)
    print("[probe rt=%s] %s" % (rt, txt.replace("\n", " | ")[:300]), flush=True)
    return txt, norm, (p.get("result") or "")


def java_real_probe():
    """真机 java 段：发一轮 `call java-real`（mock 桩路由）→ 返回 (output 文本, 归一化全文, tool-result 摘要)。"""
    sid, turn = run_turn("call java-real")
    p = wait_tool_result(turn, "script_run")
    wait_turn_done(sid)
    txt, norm = tool_output(turn)
    print("[java-real] %s" % txt.replace("\n", " | ")[:300], flush=True)
    return txt, norm, (p.get("result") or "")


# ── 场景（占位符载体）────────────────────────────────────

def main_prompt(rec):
    """场景主 agent 提示词（新口径：落 `main.agent.md`；load 回读在 `agents[isMain].prompt`，
    场景层**无**派生 `systemPrompt` 字段）。"""
    ags = rec.get("agents") or []
    main = next((a for a in ags if a.get("isMain")), ags[0] if ags else {})
    return (main.get("prompt") or "")


def scenario_save():
    """建 user 级场景（`<usrDir>/capability/scenarios/<id>/`，落在临时 HOME 内）→ 返回主 agent 提示词。

    主 agent 提示词（`main.agent.md`）经 `data-scenario-load` 回读（`agents[isMain].prompt`；场景层
    无派生 `systemPrompt` 字段）。断言落盘文本仍含**未替换**的 `{{toolchain.go}}`
    （证明 B 观测的是运行时替换，而非夹具预替换）。
    """
    CUR[0].req("data-scenario-save", {"data": {
        "id": SC_ID, "name": SC_NAME, "level": "user",
        "agents": [{"name": "主", "roleTag": "主", "isMain": True, "prompt": PROBE_PROMPT}],
    }})
    rec = (deep(CUR[0].req("data-scenario-load", {"data": {"id": SC_ID, "level": "user"}})) or {}).get("data") or {}
    sp = main_prompt(rec)
    if "{{toolchain.go}}" not in sp or ("PATHS-PROBE-" + TAG) not in sp:
        raise TestError("场景未按预期落盘（主 agent 提示词缺未替换占位符）: %r" % sp[:200])
    print("[setup] 场景 %s 落盘（user 级，临时 HOME）主 agent 提示词含未替换占位符 %r"
          % (SC_ID, "{{toolchain.go}}"), flush=True)
    return sp


def scenario_system():
    """发一轮带该场景的 llm-start → 返回 mock `/last` 的 system 原文（送 LLM 的 system 文本）。"""
    mock_reset()
    sid, _ = run_turn("hello paths probe", scenario=SC_ID)
    wait_turn_done(sid)
    last = mock_last()
    return last.get("system") or ""


# ══════════════════════════════════════════════════════════
# 用例
# ══════════════════════════════════════════════════════════

def case_a_paths_readback():
    """A：7 键逐键哨兵回读一致 + **无串写**（写 goPath 不影响其余 6 键）。"""
    c = CUR[0]
    c.req("data-user-config-save", {"data": dict(SENT, llms=[MOCK_ENTRY])})
    cfg = user_cfg()
    missing = [k for k in PATH_KEYS if k not in cfg]
    if missing:
        raise TestError("data-user-config-load 未返回路径键: %r" % missing)
    bad = {k: cfg.get(k) for k in PATH_KEYS if cfg.get(k) != SENT[k]}
    if bad:
        raise TestError("A 逐键回读不一致: %r" % bad)
    # 无串写：只改 goPath → 其余键不得变化
    c.req("data-user-config-save", {"data": {"goPath": ALT_GO}})
    cfg2 = user_cfg()
    if cfg2.get("goPath") != ALT_GO:
        raise TestError("goPath 改写未落库: %r" % cfg2.get("goPath"))
    crossed = {k: cfg2.get(k) for k in PATH_KEYS if k != "goPath" and cfg2.get(k) != SENT[k]}
    if crossed:
        raise TestError("写 goPath 串改了其它键: %r" % crossed)
    c.req("data-user-config-save", {"data": {"goPath": SENT["goPath"]}})
    cfg3 = user_cfg()
    back = {k: cfg3.get(k) for k in PATH_KEYS if cfg3.get(k) != SENT[k]}
    if back:
        raise TestError("写回 goPath 后不一致: %r" % back)
    evi("A-7键回读", keys=PATH_KEYS, readback={k: cfg3[k] for k in PATH_KEYS},
        no_cross_write="写 goPath 后其余 6 键不变")


def case_baseline_before_restart():
    """重启前基线：执行配置 env **未**注入（instance-register 已过）；场景占位符**已**生效（每调用读）。"""
    txt, norm, _ = probe("cmd")
    chrome = _between(txt, "CHROME=[", "]")
    interps = _between(txt, "INTERPS=[", "]")
    sys_txt = scenario_system()
    live_go = _line_with(sys_txt, "GO=")
    evi("重启前基线", CHONK_CHROME=chrome, CHONKPILOT_INTERPRETERS=interps,
        scenario_GO_line=live_go, scenario_live_substituted=(SENT["goPath"] in sys_txt))
    # 未注入的直接证据 = cmd 原样回显变量名（变量不存在）或空值；环境自带非哨兵值时只提示不误判
    for name, got in (("CHONK_CHROME", chrome), ("CHONKPILOT_INTERPRETERS", interps)):
        if got not in ("", "%" + name + "%"):
            print("  [note] 环境已有 %s=%r（非本次写入的哨兵值）" % (name, got[:60]), flush=True)
    leaked = [k for k in ("chromePath", "pythonPath", "nodePath", "javaPath") if SENT[k] in norm]
    if leaked:
        raise TestError("重启前执行配置已注入（与 server.go:564 instance-register 注入不符）: %r" % leaked)
    if SENT["goPath"] not in sys_txt:
        raise TestError("场景 {{toolchain.go}} 未替换（应为每调用读 usr 配置，无需重启）: %r" % live_go)


def case_a_after_restart():
    """A：真实重启后 7 键仍 == 哨兵（usr 主库持久）。"""
    cfg = user_cfg()
    bad = {k: cfg.get(k) for k in PATH_KEYS if cfg.get(k) != SENT[k]}
    if bad:
        raise TestError("重启后 A 回读不一致: %r" % bad)
    evi("A-重启后回读", readback={k: cfg[k] for k in PATH_KEYS})


def case_b_env_injection():
    """B：chromePath → 子进程 env `CHONK_CHROME`；pythonPath/nodePath/javaPath → `CHONKPILOT_INTERPRETERS`。"""
    txt, norm, summary = probe("cmd")
    chrome = _between(txt, "CHROME=[", "]")
    raw = _between(txt, "INTERPS=[", "]")
    m = None
    try:
        m = json.loads(raw)
    except Exception:
        m = None
    if chrome != SENT["chromePath"]:
        raise TestError("CHONK_CHROME=%r，期望 chromePath=%r" % (chrome, SENT["chromePath"]))
    want = {"python": SENT["pythonPath"], "js": SENT["nodePath"], "java": SENT["javaPath"]}
    if not isinstance(m, dict):
        raise TestError("CHONKPILOT_INTERPRETERS 非法 JSON: %r" % raw)
    diff = {k: m.get(k) for k, v in want.items() if (m.get(k) or "").lower() != v.lower()}
    if diff:
        raise TestError("CHONKPILOT_INTERPRETERS 映射不符: %r" % diff)
    evi("B-env注入", CHONK_CHROME=chrome, CHONKPILOT_INTERPRETERS=m,
        raw_interps_head=raw[:120], tool_result_summary=summary)


def case_b_interpreters_launch():
    """B：三个解释器键 → `script_run` 真实启动的解释器 == 配置值（被启动进程回显 process.execPath）。"""
    got = {}
    for rt, key in (("js", "nodePath"), ("python", "pythonPath"), ("java", "javaPath")):
        txt, _, _ = probe(rt)
        line = _line_with(txt, "INTERP=")
        path = line.split("INTERP=", 1)[1].strip() if line else ""
        got[key] = path
        if path.lower() != SENT[key].lower():
            raise TestError("runtime=%s 实际启动 %r，期望 %s=%r" % (rt, path, key, SENT[key]))
    evi("B-解释器真实启动", observed=got,
        note="js=真 node 副本；python/java=假解释器（node.exe 副本改名，真机 python 3.14 无法裸复制）")


def case_b_toolchain_placeholders():
    """B：`{{toolchain.*}}`（场景主 agent 提示词）→ 送 LLM 的 system 原文 == 配置值。"""
    sys_txt = scenario_system()
    if ("PATHS-PROBE-" + TAG) not in sys_txt:
        raise TestError("场景主 agent 提示词未进入送 LLM 的 system: %r" % sys_txt[:200])
    lines = {}
    for tag, key in PROMPT_LINES:
        line = _line_with(sys_txt, tag + "=")
        lines[key] = line
        if line != "%s=%s" % (tag, SENT[key]):
            raise TestError("{{toolchain.%s}} 未替换为配置值：line=%r，期望 %r"
                            % (key, line, "%s=%s" % (tag, SENT[key])))
    unk = _line_with(sys_txt, "UNKNOWN=")
    arg = _line_with(sys_txt, "ARG=")
    if unk != "UNKNOWN={{toolchain.unknown}}":
        raise TestError("未知 key 应原样保留: %r" % unk)
    if arg != "ARG={{arg}}":
        raise TestError("{{arg}} 不应被本机制替换: %r" % arg)
    evi("B-占位符替换", lines=lines, unknown=unk, arg=arg)


def case_b_unconfigured_key_empty():
    """B：未配置键 → 替换为**空串**（删 chromePath → system 行变 `CHROME=`，且不留占位符）。

    附：同轮复核「删除后 CHONK_CHROME 仍为旧值」——env 注入在 instance-register 定格（未重启），
    与占位符的「每调用读」形成对照（证据打印，不作硬断言）。
    """
    CUR[0].req("data-user-config-delete", {"id": "chromePath"})
    cfg = user_cfg()
    if cfg.get("chromePath") != "":
        raise TestError("A：删 chromePath 后回读=%r，期望空串" % cfg.get("chromePath"))
    sys_txt = scenario_system()
    line = _line_with(sys_txt, "CHROME=")
    if "{{toolchain.chrome}}" in sys_txt:
        raise TestError("未配置 key 占位符原样残留（应替换为空串）")
    if line != "CHROME=":
        raise TestError("未配置 key 应替换为空串：line=%r" % line)
    env_txt, _, _ = probe("cmd")
    stale = _between(env_txt, "CHROME=[", "]")
    evi("B-未配置键空串", line=line, placeholder_left=False,
        env_after_delete=stale, env_note="删键后未重启 → CHONK_CHROME 仍为实例级定格值")


def case_b_java_real_interpreter():
    """B（I-80 回归）：`javaPath` = 本机**真实** java → 重启后 `script_run(runtime=java)` 真跑。

    A：回读 javaPath == 真机 java 路径；
    B：工具输出含哨兵 `CK-JAVA-OK` —— 临时脚本扩展名 `.java` 时 JDK 单文件源码模式可执行
    （`.jsh` 会被当类名 → ClassNotFoundException）。
    """
    cfg = user_cfg()
    got = cfg.get("javaPath") or ""
    if got.lower() != JAVA_REAL.lower():
        raise TestError("A：javaPath 回读=%r，期望真机 java=%r" % (got, JAVA_REAL))
    txt, norm, summary = java_real_probe()
    if "CK-JAVA-OK" not in txt:
        raise TestError("java 真机脚本未输出哨兵 CK-JAVA-OK（临时脚本扩展名应为 .java）: %r" % txt[:400])
    evi("B-java真机端到端", javaPath=JAVA_REAL, sentinel_found="CK-JAVA-OK",
        output=txt[:200], tool_result_summary=summary)


# ══════════════════════════════════════════════════════════
# 执行
# ══════════════════════════════════════════════════════════

def main():
    ok = 0
    total = 0
    g1 = g2 = g3 = None
    snap = None
    try:
        print("[env] tag=%s mock=%d bin=%s\n      ws=%s dd=%s home=%s（均为临时目录，结束即删）"
              % (TAG, MOCK.port, BIN, WS, DD, HOME), flush=True)
        # ── 实例 #1：写配置 + 重启前基线 ──
        port1 = _h.free_port()
        g1 = _h.start_gui(port=port1, work_dir=WS, data_dir=DD, home=HOME, extra_args=GUI_ARGS)
        CUR[0] = g1.client
        PORTS.extend([port1, g1.port])
        snap = _h.snapshot_config(CUR[0])     # 套件级 usr+prj 全量快照（finally 还原）
        CUR[0].console(clear=True)
        print("[env] 实例 #1 port=%d pid=%s work_dir=%s" % (g1.port, g1.pid(), g1.work_dir), flush=True)
        scenario_save()

        total += 1; ok += run_case("A 7 键哨兵回读 + 无串写", case_a_paths_readback)
        total += 1; ok += run_case("（重启前基线）env 未注入 / 占位符已生效", case_baseline_before_restart)
        for e in (CUR[0].console() or {}).get("entries", []):
            if e.get("level") == "error":
                print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)

        # ── 真实重启（同 HOME / work-dir / data-dir，仅换进程与端口）──
        print("[restart] 关闭实例 #1（port=%d pid=%s）" % (g1.port, g1.pid()), flush=True)
        g1.stop(); g1 = None
        if not _h.wait_port_free(port1, timeout=20):
            print("  [WARN] 端口 %d 未在 20s 内释放" % port1, flush=True)
        time.sleep(1.5)
        g2 = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME, extra_args=GUI_ARGS)
        CUR[0] = g2.client
        PORTS.append(g2.port)
        CUR[0].console(clear=True)
        print("[restart] 实例 #2 port=%d pid=%s（instance-register 重跑 → 执行配置重新注入）"
              % (g2.port, g2.pid()), flush=True)

        total += 1; ok += run_case("A 重启后 7 键仍一致（usr 主库持久）", case_a_after_restart)
        total += 1; ok += run_case("B 子进程 env 注入（CHONK_CHROME / CHONKPILOT_INTERPRETERS）",
                                   case_b_env_injection)
        total += 1; ok += run_case("B 解释器真实启动（js / python / java == 配置值）",
                                   case_b_interpreters_launch)
        total += 1; ok += run_case("B {{toolchain.*}} 占位符替换（送 LLM 的 system 原文）",
                                   case_b_toolchain_placeholders)
        total += 1; ok += run_case("B 未配置键 → 空串（+ env 定格对照）", case_b_unconfigured_key_empty)
        for e in (CUR[0].console() or {}).get("entries", []):
            if e.get("level") == "error":
                print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)

        # ── 阶段 3：真机 java 端到端（I-80 回归）：javaPath=真机 java → 再重启一次 ──
        if not JAVA_REAL:
            print("[skip] 本机无 java（shutil.which('java') 为空）→ 跳过 java 真机端到端用例", flush=True)
        else:
            CUR[0].req("data-user-config-save", {"data": {"javaPath": JAVA_REAL}})
            print("[java-real] javaPath=%s → 重启实例 #2（重注入 CHONKPILOT_INTERPRETERS）" % JAVA_REAL,
                  flush=True)
            port2 = g2.port
            g2.stop(); g2 = None
            if not _h.wait_port_free(port2, timeout=20):
                print("  [WARN] 端口 %d 未在 20s 内释放" % port2, flush=True)
            time.sleep(1.5)
            g3 = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME, extra_args=GUI_ARGS)
            CUR[0] = g3.client
            PORTS.append(g3.port)
            CUR[0].console(clear=True)
            print("[java-real] 实例 #3 port=%d pid=%s" % (g3.port, g3.pid()), flush=True)
            total += 1; ok += run_case("B java 真机端到端（script_run runtime=java → CK-JAVA-OK）",
                                       case_b_java_real_interpreter)
            for e in (CUR[0].console() or {}).get("entries", []):
                if e.get("level") == "error":
                    print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)

        # 场景清理（含 HOME 临时目录整体回收）
        try:
            CUR[0].req("data-scenario-delete", {"data": {"id": SC_ID, "level": "user"}})
        except Exception as e:
            print("[cleanup] 场景删除失败: %s" % e, flush=True)
    finally:
        # ① 配置还原（51 §6-8；client 尚在 → 还原真实生效）
        if CUR[0] is not None and snap is not None:
            try:
                _h.restore_config(CUR[0], snap)
            except Exception as e:
                print("[cleanup] 配置还原失败: %s" % e, flush=True)
        # ② 还原复核：7 键回落空串 + llms 清空
        left = None
        if CUR[0] is not None:
            try:
                cfg = user_cfg()
                left = {k: cfg.get(k) for k in PATH_KEYS if cfg.get(k)}
                print("[cleanup] 还原复核：路径键非空项=%r，llms=%r"
                      % (left, cfg.get("llms")), flush=True)
            except Exception as e:
                print("[cleanup] 还原复核失败: %s" % e, flush=True)
        # ③ 资源回收（实例 + 临时目录，含哨兵目录）
        for g in (g1, g2, g3):
            if g is not None:
                g.stop()
        _h.cleanup_all()
        # ④ 残留检查
        busy = [p for p in PORTS if _h.port_open(p)]
        busy += [MOCK.port] if _h.port_open(MOCK.port) else []
        dirs = [d for d in (BIN, WS, DD, HOME) if os.path.isdir(d)]
        print("[残留] 端口占用=%r 临时目录残留=%r 配置还原后非空路径键=%r"
              % (busy, dirs, left), flush=True)
        print("\n路径配置端到端：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
        print("RESULT: %s" % (ok == total), flush=True)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
