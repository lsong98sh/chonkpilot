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
  * `goPath` / `rustPath` / `cCompilerPath` / `chromePath` → `{{toolchain.*}}` 占位符（场景主 agent
    提示词）→ mock LLM `/last` 的 **system 原文**（`chonkpilot-llm/server/toolchain.go:35 toolchainVars`
    → `mcpms.ReplaceToolchain`）；未配置键 → 空串。
  * `pythonPath` / `nodePath` / `javaPath` → **B 不可覆盖（2026-10-23，41 D-67）**：原消费方
    `script_run`（读 `CHONKPILOT_INTERPRETERS`）已随 core 契约退役删除；`CHONK_CHROME` 的注入对象
    （executor 子进程）亦随之消失，现行 `task_run` 为普通 shell → 无稳定可观测面。本套件对这些
    子项**显式打印 `[SKIP]`**（A 面仍完整断言），缺口登记见 41 D-67。

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


# 〔2026-10-23〕原 probe()/java_real_probe()（`script_run` runtime 探针）随 core 契约退役删除，
# 见 41 D-67。


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
    """重启前基线：场景占位符**已**生效（每调用读 usr 配置，无需重启）。

    〔2026-10-23〕原「执行配置 env 未注入」的 cmd 探针随 `script_run` 退役移除（41 D-67）。
    """
    sys_txt = scenario_system()
    live_go = _line_with(sys_txt, "GO=")
    evi("重启前基线", scenario_GO_line=live_go,
        scenario_live_substituted=(SENT["goPath"] in sys_txt))
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
    """B：**不可覆盖（2026-10-23 口径）** —— 子进程 env 探针随 `script_run` 退役删除。

    `CHONK_CHROME` / `CHONKPILOT_INTERPRETERS` 的注入点在 mcp-server `executorEnv`（spawn executor
    子进程），唯一可观测消费方 = 已删除的 `script_run`；现行 `task_run`（task 插件）为普通 shell，
    不读这两项 → 无稳定可观测面。登记见 [41 D-67](../../../docs/spec/40-roadmap/41-未决项登记.md)。
    """
    print("    [SKIP] B-env注入：script_run 已删除，路径配置当前无执行侧消费面（41 D-67）", flush=True)


def case_b_interpreters_launch():
    """B：**不可覆盖（2026-10-23 口径）** —— 解释器选型探针随 `script_run` 退役删除（41 D-67）。"""
    print("    [SKIP] B-解释器真实启动：task_run 不读 CHONKPILOT_INTERPRETERS（41 D-67）", flush=True)


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

    〔2026-10-23〕原附加的「同轮复核 CHONK_CHROME 仍为旧值」随 `script_run` 退役移除（41 D-67）；
    占位符替换面仍完整断言。
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
    evi("B-未配置键空串", line=line, placeholder_left=False)


def case_b_java_real_interpreter():
    """B（I-80 回归）：**B 不可覆盖**（`script_run(runtime=java)` 已随 core 契约删除，41 D-67）。

    A（javaPath 回读）仍断言。
    """
    cfg = user_cfg()
    got = cfg.get("javaPath") or ""
    if got.lower() != JAVA_REAL.lower():
        raise TestError("A：javaPath 回读=%r，期望真机 java=%r" % (got, JAVA_REAL))
    print("    [SKIP] B-java真机端到端：script_run(runtime=java) 已删除（41 D-67）；A 回读通过", flush=True)


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
        total += 1; ok += run_case("（重启前基线）占位符已生效", case_baseline_before_restart)
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
        total += 1; ok += run_case("B 子进程 env 注入（不可覆盖：script_run 已删除，41 D-67）",
                                   case_b_env_injection)
        total += 1; ok += run_case("B 解释器真实启动（不可覆盖：script_run 已删除，41 D-67）",
                                   case_b_interpreters_launch)
        total += 1; ok += run_case("B {{toolchain.*}} 占位符替换（送 LLM 的 system 原文）",
                                   case_b_toolchain_placeholders)
        total += 1; ok += run_case("B 未配置键 → 空串", case_b_unconfigured_key_empty)
        for e in (CUR[0].console() or {}).get("entries", []):
            if e.get("level") == "error":
                print("  [CONSOLE-ERROR] %s" % e.get("text"), flush=True)

        # ── 阶段 3：javaPath 回读（原真机端到端 B 面随 script_run 删除 → 41 D-67）──
        if not JAVA_REAL:
            print("[skip] 本机无 java（shutil.which('java') 为空）→ 跳过 javaPath 段", flush=True)
        else:
            CUR[0].req("data-user-config-save", {"data": {"javaPath": JAVA_REAL}})
            print("[java-path] javaPath=%s → 重启实例 #2（验证 usr 持久 + 占位符面）" % JAVA_REAL,
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
            total += 1; ok += run_case("A javaPath 回读（B 不可覆盖：script_run 已删除，41 D-67）",
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
