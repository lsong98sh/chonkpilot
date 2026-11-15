# -*- coding: utf-8 -*-
"""场景化 E2E 框架：**一次启动，多阶段测试**。

设计目标（用户需求，2026-11-11）：

1. **只启一次 GUI**：预置一个空工程 → 用 LLM 工具把它建成「有数据工程」→ 在同一实例内
   跑完所有可测点；确实需要跨重启的测试点单独归入 `restart` 阶段（第二次启动，同工作区）。
2. **可全量 / 可指定**：`--list` / `--only` / `--area` / `-k`；结果报告逐点写明**功能点**。
3. **两种初始化**：`empty`（空工程）与 `seeded`（有数据工程）；预置**幂等**（已有文件不再创建）。
4. **问题记录但不中断**：单个测试点失败只记问题，继续下一个；`--max-issues N` 累计超过 N 个
   问题后停止后续测试点（仍写报告、仍清理）。

阶段顺序（`stage`）：

    empty    : GUI 首启后、预置工程之前 → 只能测「空工程」行为
    (seed)   : 框架用 LLM 工具（file_write 批量）把空工程建成演示工程（幂等）
    seeded   : 工程已就绪 → 测文件/会话/任务/配置/知识库/记忆/插件/主题等
    restart  : 同工作区第二次启动后 → 测持久化（布局/页签/会话/配置/主题）

断言口径沿用仓库既有约定（51-FP与测试映射）：**能由消息/磁盘判定的不依赖 DOM**；DOM 只验证
渲染落点；每条断言写入 evidence，报告与 JSON 一并落盘。
"""

import json
import os
import shutil
import subprocess
import sys
import time
import traceback
from dataclasses import dataclass, field

_HERE = os.path.dirname(os.path.abspath(__file__))
_SYSTEST = os.path.dirname(_HERE)
if _SYSTEST not in sys.path:
    sys.path.insert(0, _SYSTEST)

import harness as _h  # noqa: E402

STAGES = ("empty", "seeded", "restart", "destructive")
_STAGE_ORDER = {"empty": 0, "seeded": 1, "restart": 2, "destructive": 3}

# 预置工程（seeded）的演示文件清单见 workspace.py；阶段名打印用中文。
STAGE_LABEL = {"empty": "空工程", "seeded": "有数据工程", "restart": "重启后",
               "destructive": "破坏性（默认不跑）"}


class CaseFailure(AssertionError):
    """单个测试点内断言失败（由 Env.check/eq 抛出）。"""


class CaseSkip(Exception):
    """测试点前置不满足（非产品缺陷）：记为 SKIP，不计问题、不中断后续。"""


@dataclass
class TestPoint:
    """一个测试点 = 一个可独立选中、独立判定、独立报错的用例。"""

    id: str
    area: str
    feature: str            # **功能点**（必填，报告/列表逐点显示）
    fn: object
    stage: str = "seeded"
    desc: str = ""          # 测试点说明（方法/判据）
    fp: str = ""            # 功能点文档引用（如 31-WIN-026 / 61 §2）
    order: int = 100
    tags: tuple = ()

    @property
    def title(self):
        return "%s · %s" % (self.area, self.feature)


_REGISTRY = []


def case(id, area, feature, stage="seeded", desc="", fp="", order=100, tags=()):
    """注册测试点装饰器。

        @case("S-FS-01", "文件树", "预置工程树结构与排序", stage="seeded", fp="61 §2")
        def s_fs_01(env): ...

        # 破坏性用例：stage="destructive" → 默认全量**跳过**；仅 --with-destructive /
        # run_e2e_destructive.py 运行，且永远排在最后（可清空配置，不再影响其它点）。
        @case("D-XXX-01", "破坏性", "恢复出厂", stage="destructive")
        def d_xxx_01(env): ...
    """
    if stage not in STAGES:
        raise ValueError("未知阶段 %r（允许 %r）" % (stage, STAGES))

    def deco(fn):
        if any(p.id == id for p in _REGISTRY):
            raise ValueError("测试点 id 重复：%s" % id)
        _REGISTRY.append(TestPoint(id=id, area=area, feature=feature, fn=fn,
                                   stage=stage, desc=desc or (fn.__doc__ or "").strip(),
                                   fp=fp, order=order, tags=tuple(tags)))
        return fn

    return deco


def registry():
    return list(_REGISTRY)


def select(only=None, areas=None, stages=None, keywords=None):
    """按条件选测试点（保序：stage → order → id）。"""
    pts = list(_REGISTRY)
    if only:
        wanted = {s.strip() for s in only if s.strip()}
        pts = [p for p in pts if p.id in wanted]
        missing = wanted - {p.id for p in pts}
        if missing:
            raise SystemExit("未知测试点：%s" % ", ".join(sorted(missing)))
    if areas:
        wanted = {s.strip() for s in areas if s.strip()}
        pts = [p for p in pts if p.area in wanted]
    if stages:
        wanted = set(stages)
        pts = [p for p in pts if p.stage in wanted]
    if keywords:
        pts = [p for p in pts
               if any(k.lower() in (p.id + " " + p.title).lower() for k in keywords)]
    return sorted(pts, key=lambda p: (_STAGE_ORDER[p.stage], p.order, p.id))


# ══════════════════════════════════════════════════════════
# 运行环境（一个 GUI 实例 + 当前测试点上下文）
# ══════════════════════════════════════════════════════════

def _plain(v):
    return _h._plain(v)


def _deep_loads(v):
    """循环解包 JSON 字符串（test-port eval 结果可能被双重编码）。"""
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            nv = json.loads(v)
        except Exception:
            return v
        if nv == v:
            return v
        v = nv
    return v


@dataclass
class Env:
    """测试点运行环境：封装 client / 工作区路径 / 证据 / 断言。"""

    client: object
    workspace: str
    data_root: str
    mock_port: int
    run_id: str
    gui_pid: int = 0
    stage: str = ""
    point: object = None
    evidence: list = field(default_factory=list)
    console_errors: list = field(default_factory=list)
    # 跨阶段/跨重启共享的少量状态（如 restart 阶段要复用的会话 id）——由 Runner 复用同一 dict
    shared: dict = field(default_factory=dict)

    # ── 路径 ──
    def path(self, *rel):
        return os.path.join(self.workspace, *rel)

    @staticmethod
    def posix(p):
        return str(p).replace("\\", "/")

    # ── 驱动 ──
    def js(self, js, timeout=10000):
        return _plain(self.client.eval(js, timeout))

    def jsj(self, js, timeout=10000):
        """eval 并解析 JSON（自动解双重编码）。"""
        return _deep_loads(self.js(js, timeout))

    def emit(self, topic, payload=None, timeout=10000):
        return self.client.mq_emit(topic, payload, timeout)

    def req(self, typ, payload=None, timeout=30000):
        return self.client.req(typ, payload, timeout)

    def publish(self, typ, payload=None, timeout=30000):
        return self.client.publish(typ, payload, timeout)

    def capture(self, topics):
        """装事件捕获（每次调用清空上一轮）。"""
        return self.client.mq_on_capture(list(topics))

    def events(self, topic):
        """当前捕获的某主题事件（不清空；兼容 test-port 批量 deferral）。"""
        return self.client.events_of(topic, clear=False)

    # ── 等待 ──
    def wait(self, fn, desc, timeout=20.0, interval=0.4):
        """有界轮询：fn() 真值即返回；超时抛 CaseFailure（带最后异常）。"""
        end = time.time() + timeout
        last_exc = None
        while time.time() < end:
            try:
                v = fn()
                if v:
                    return v
            except Exception as e:  # 轮询期瞬时错误不作为最终判据
                last_exc = e
            time.sleep(interval)
        raise CaseFailure("等待超时（%ss）：%s%s"
                          % (timeout, desc, ("（末次异常：%s）" % last_exc) if last_exc else ""))

    def wait_events(self, topic, pred=None, desc="", timeout=60.0):
        """等待某主题至少一条满足 pred 的事件（不清空捕获）。"""
        def _f():
            for e in self.events(topic):
                p = e.get("payload")
                if pred is None or pred(p):
                    return p
            return None
        return self.wait(_f, desc or ("事件 %s" % topic), timeout=timeout, interval=0.4)

    # ── 断言（写入 evidence；失败抛 CaseFailure）──
    def check(self, name, cond, detail=""):
        ok = bool(cond)
        self.evidence.append({"check": name, "ok": ok, "detail": detail})
        if ok:
            print("    [check] OK   %s%s" % (name, (" — " + str(detail)) if detail else ""),
                  flush=True)
            return True
        print("    [check] FAIL %s%s" % (name, (" — " + str(detail)) if detail else ""),
              flush=True)
        raise CaseFailure("%s%s" % (name, ("：%s" % detail) if detail else ""))

    def eq(self, name, actual, expected):
        return self.check(name, actual == expected, "期望 %r，实际 %r" % (expected, actual))

    def note(self, name, detail=""):
        """只记录证据，不做判定。"""
        self.evidence.append({"check": name, "ok": True, "detail": detail, "note": True})
        print("    [info] %s%s" % (name, (" — " + str(detail)) if detail else ""), flush=True)

    # ── 常用读面 ──
    def usr(self):
        res = self.req("data-user-config-load", {})
        return (res or {}).get("data") or {}

    def prj(self):
        res = self.req("data-prj-config-list", {})
        return (res or {}).get("list") or {}

    def tree_count(self, rel, present=True):
        sel = '.tree-row[data-path="%s/%s"]' % (self.posix(self.workspace), self.posix(rel))
        n = int(self.js("document.querySelectorAll(%s).length" % json.dumps(sel)) or 0)
        return n > 0 if present else n == 0

    def read_text(self, *rel):
        with open(self.path(*rel), "r", encoding="utf-8") as f:
            return f.read()

    def write_text(self, rel, content, only_if_missing=False):
        """直接落盘夹具（用于测试前置；测试点自身应尽量走 mq 主题驱动）。"""
        p = self.path(rel)
        if only_if_missing and os.path.exists(p):
            return False
        d = os.path.dirname(p)
        if d:
            os.makedirs(d, exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write(content)
        return True

    def delete_path(self, *rel):
        p = self.path(*rel)
        if os.path.isdir(p):
            shutil.rmtree(p, ignore_errors=True)
        elif os.path.isfile(p):
            try:
                os.remove(p)
            except OSError:
                pass


# ══════════════════════════════════════════════════════════
# 问题记录（记录但不中断；可配置累计阈值）
# ══════════════════════════════════════════════════════════

@dataclass
class Issue:
    point_id: str
    feature: str
    stage: str
    error: str
    traceback: str = ""
    evidence: list = field(default_factory=list)
    ts: float = field(default_factory=time.time)


@dataclass
class Result:
    point: TestPoint
    state: str                       # pass / fail / skip
    seconds: float = 0.0
    evidence: list = field(default_factory=list)
    error: str = ""
    skip_reason: str = ""


class IssueRecorder:
    """问题累计器：达到阈值后置 aborted（由 Runner 停止后续测试点）。"""

    def __init__(self, max_issues=0):
        self.max_issues = int(max_issues or 0)
        self.issues = []
        self.aborted = False

    def add(self, point, error, tb="", evidence=None):
        self.issues.append(Issue(point_id=point.id, feature=point.feature, stage=point.stage,
                                 error=str(error), traceback=tb or "", evidence=evidence or []))
        n = len(self.issues)
        print("    [ISSUE] #%d %s：%s" % (n, point.id, error), flush=True)
        if self.max_issues and n > self.max_issues:
            self.aborted = True
            print("    [ABORT] 累计问题超过阈值 %d（当前 %d）→ 停止后续测试点（已跑结果与问题已记录）"
                  % (self.max_issues, n), flush=True)


# ══════════════════════════════════════════════════════════
# 报告
# ══════════════════════════════════════════════════════════

def write_report(run_dir, cfg, results, issues, started_at, finished_at):
    """落盘 report.json + report.md；返回 (json_path, md_path)。"""
    os.makedirs(run_dir, exist_ok=True)
    payload = {
        "run_id": cfg.get("run_id"),
        "started_at": started_at,
        "finished_at": finished_at,
        "duration_s": round(finished_at - started_at, 1),
        "init": cfg.get("init"),
        "workspace": cfg.get("workspace"),
        "workspace_kept": cfg.get("workspace_kept"),
        "cleanup": cfg.get("cleanup"),
        "max_issues": cfg.get("max_issues"),
        "aborted": cfg.get("aborted"),
        "gui_starts": cfg.get("gui_starts"),
        "counts": {
            "total": len(results),
            "pass": sum(1 for r in results if r.state == "pass"),
            "fail": sum(1 for r in results if r.state == "fail"),
            "skip": sum(1 for r in results if r.state == "skip"),
            "issues": len(issues),
        },
        "results": [{
            "id": r.point.id, "area": r.point.area, "feature": r.point.feature,
            "stage": r.point.stage, "fp": r.point.fp, "desc": r.point.desc,
            "state": r.state, "seconds": round(r.seconds, 2),
            "error": r.error, "skip_reason": r.skip_reason, "evidence": r.evidence,
        } for r in results],
        "issues": [{
            "point_id": i.point_id, "feature": i.feature, "stage": i.stage,
            "error": i.error, "traceback": i.traceback, "evidence": i.evidence,
        } for i in issues],
    }
    jp = os.path.join(run_dir, "report.json")
    with open(jp, "w", encoding="utf-8") as f:
        json.dump(_jsonable(payload), f, ensure_ascii=False, indent=2)

    c = payload["counts"]
    lines = [
        "# 场景化 E2E 测试报告",
        "",
        "- 运行：`%s`（%.1fs，GUI 启动 %d 次）" % (cfg.get("run_id"), payload["duration_s"],
                                              cfg.get("gui_starts") or 0),
        "- 初始化：`%s` · 工作区：`%s`（%s）" % (cfg.get("init"), cfg.get("workspace"),
                                          "保留" if cfg.get("workspace_kept") else "已清理"),
        "- 结果：**%d passed / %d failed / %d skipped**（测试点 %d，问题 %d 条）"
        % (c["pass"], c["fail"], c["skip"], c["total"], c["issues"]),
        "",
        "## 测试点明细",
        "",
        "| 状态 | ID | 阶段 | 功能点 | 说明 | 耗时 |",
        "|---|---|---|---|---|---|",
    ]
    icon = {"pass": "✅ PASS", "fail": "❌ FAIL", "skip": "⏭ SKIP"}
    for r in results:
        lines.append("| %s | `%s` | %s | %s | %s | %.1fs |"
                     % (icon.get(r.state, r.state), r.point.id,
                        STAGE_LABEL.get(r.point.stage, r.point.stage),
                        r.point.feature.replace("|", "/"),
                        (r.error if r.state == "fail" else r.skip_reason or r.point.desc)
                        .replace("|", "/").replace("\n", " ")[:160],
                        r.seconds))
    if issues:
        lines += ["", "## 问题清单（记录但不中断）", ""]
        for i, it in enumerate(issues, 1):
            lines.append("%d. `%s` %s — %s" % (i, it.point_id, it.feature, it.error))
    lines += ["", "## 如何复跑", "",
              "```bash",
              "python src/test/chonkpilot-gui/systest/run_e2e.py --list",
              "python src/test/chonkpilot-gui/systest/run_e2e.py --init seeded --max-issues 5",
              "python src/test/chonkpilot-gui/systest/run_e2e.py --only S-FS-01,S-CHAT-01",
              "```", ""]
    mp = os.path.join(run_dir, "report.md")
    with open(mp, "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    return jp, mp


def print_list(points):
    """打印测试点清单（id / 阶段 / 功能点 / 说明）。"""
    if not points:
        print("（无匹配测试点）")
        return
    w = max(len(p.id) for p in points)
    print("%-*s  %-6s  %-12s  %s" % (w, "ID", "阶段", "领域", "功能点（说明）"))
    print("-" * (w + 60))
    for p in points:
        print("%-*s  %-6s  %-12s  %s" % (w, p.id, p.stage, p.area,
                                     p.feature + ("  [破坏性]" if p.stage == "destructive" else "")))
        if p.desc:
            print("%-*s  %-6s  %-12s    ↳ %s" % (w, "", "", "", p.desc))
        if p.fp:
            print("%-*s  %-6s  %-12s    引用：%s" % (w, "", "", "", p.fp))
    print("\n共 %d 个测试点（阶段：empty=空工程 / seeded=有数据工程 / restart=重启后 / "
          "destructive=破坏性（默认不跑，需 --with-destructive））" % len(points))


# ══════════════════════════════════════════════════════════
# 执行器
# ══════════════════════════════════════════════════════════

def _jsonable(o):
    """把 evidence/issue 里的任意对象转成可 JSON 序列化的形状（防报告写一半截断）。"""
    if isinstance(o, dict):
        return {str(k): _jsonable(v) for k, v in o.items()}
    if isinstance(o, (list, tuple, set)):
        return [_jsonable(v) for v in o]
    if isinstance(o, float):
        return o if o == o and o not in (float("inf"), float("-inf")) else str(o)
    if isinstance(o, (str, int, bool)) or o is None:
        return o
    return repr(o)


def _supports_test_port(exe):
    """预检被测产物是否编入 `--test-port`（仅 `build-desktop.ps1 -Debug` 带 debugport）。

    Release 构建带该参数会立即退出（`flag provided but not defined: -test-port`）→ 启动等待 90s
    才报「未就绪」，且错误信息不指向根因。预检把它变成一条**明确 SKIP**（SKIP 不计失败）。
    """
    try:
        p = subprocess.run([exe, "--help"], capture_output=True, timeout=20,
                           creationflags=getattr(subprocess, "CREATE_NO_WINDOW", 0))
        out = (p.stdout or b"") + (p.stderr or b"")
        return b"test-port" in out
    except Exception:
        return True  # 预检失败不阻断（交给真实启动判定）


class Runner:
    """单实例多阶段执行器。

    生命周期：
        start_mock → start_gui(#1) → empty 测试点 → 预置工程（LLM 工具）
        → seeded 测试点 → [可选] stop_gui → start_gui(#2) → restart 测试点
        → 写报告 → 按策略清理
    """

    def __init__(self, points, options):
        self.points = points
        self.opt = options
        self.recorder = IssueRecorder(options.get("max_issues", 0))
        self.results = []
        self.handle = None
        self.mock = None
        self.env = None
        self.gui_starts = 0
        self.started_at = time.time()
        self.run_id = options["run_id"]
        self.run_dir = options["run_dir"]
        self.workspace = options["workspace"]
        self.home = options["home"]
        self.report_dir = options["report_dir"]

    # ── 资源 ──
    def _start_mock(self):
        self.mock = _h.acquire_mock_llm(_h.DEFAULT_MOCK_LLM_PORT)
        print("[e2e] mock LLM → 127.0.0.1:%d" % self.mock.port, flush=True)

    def _start_gui(self, tag):
        # `-llm-base`：插件侧 llm-simple（记忆沉淀 / 摘要压缩）走该端点；与 usr `llms` 指向同一
        # 本机 mock，保证 B 面效果可观测（run_memory_ctx 同法）。
        self.handle = _h.start_gui(port=self.opt.get("port") or _h.free_port(),
                                   work_dir=self.workspace, home=self.home,
                                   extra_args=("-llm-base=http://127.0.0.1:%d/v1"
                                               % _h.DEFAULT_MOCK_LLM_PORT,
                                               "-llm-model=mock-model"))
        self.gui_starts += 1
        self.env = Env(client=self.handle.client, workspace=self.workspace,
                       data_root=self.handle.data_dir, mock_port=_h.DEFAULT_MOCK_LLM_PORT,
                       run_id=self.run_id, gui_pid=self.handle.pid() or 0,
                       shared=getattr(self, "_shared", None) or {})
        self._shared = self.env.shared
        # 测试点可直接发一轮 LLM（llm-start → turn-start → llm-complete）
        self.env.run_turn = self.run_turn
        print("[e2e] GUI #%d 就绪 port=%d ws=%s" % (self.gui_starts, self.handle.port,
                                                    self.workspace), flush=True)

    def _stop_gui(self):
        if self.handle is not None:
            print("[e2e] 停止 GUI（pid=%s）" % self.handle.pid(), flush=True)
            self.handle.stop()
            self.handle = None

    # ── LLM 配置 / 轮次 ──
    def _configure_llm(self):
        """把 usr `llms` 指向本机 mock（独立 HOME → 每轮确定）。"""
        c = self.env.client
        entry = {"name": "mock", "protocol": "openai", "model": "mock-model",
                 "baseUrl": "http://127.0.0.1:%d/v1" % _h.DEFAULT_MOCK_LLM_PORT,
                 "apiKey": "sk-test-key", "temperature": 0.7, "maxOutputToken": 4096}
        cur = (self.env.usr().get("llms") or [])
        keep = [e for e in cur if isinstance(e, dict) and e.get("name") != "mock"]
        llms = keep + [entry]
        c.req("data-user-config-save", {"data": {"llms": llms, "defaultLLM": len(llms) - 1}})
        c.mq_emit("config-refresh")
        time.sleep(0.6)

    def run_turn(self, q, session_id=None, timeout=240):
        """发一轮 llm-start → 等主会话 turn-start → 等 llm-complete；返回事件快照。"""
        env = self.env
        sid = session_id or ("e2e-turn-%d-%d" % (int(time.time() * 1000), self.gui_starts))
        env.req("data-session-ensure-session", {"data": {"session_id": sid}})
        env.capture(["turn-start", "llm-token", "llm-tool-call", "tool-pair",
                     "tool-result", "llm-receive", "llm-complete", "error",
                     "tasks.started", "tasks.updated", "tasks.done"])
        env.emit("llm-start", {"session_id": sid, "turn": "t-" + sid, "q": q,
                               "llm": "mock", "think": "", "effort": "",
                               "scenario_id": ""})
        env.wait_events("turn-start", lambda p: p.get("session") == sid and not p.get("parents"),
                        "主轮次 turn-start（%s）" % sid, timeout=min(90, timeout))
        done = env.wait_events(
            "llm-complete",
            lambda p: p.get("session") == sid,
            "llm-complete（%s）" % sid, timeout=timeout)
        status = (done or {}).get("status")
        if status not in (None, "", "ok", "success", "completed", "complete"):
            raise CaseFailure("轮次终态异常：status=%r code=%r message=%r"
                              % (status, (done or {}).get("code"), (done or {}).get("message")))
        return {"session_id": sid, "complete": done}

    # ── 测试点执行 ──
    def _run_point(self, point):
        env = self.env
        env.stage = point.stage
        env.point = point
        env.evidence = []
        print("\n  ▶ %s [%s] %s" % (point.id, STAGE_LABEL.get(point.stage, point.stage),
                                    point.feature), flush=True)
        if point.desc:
            print("      说明：%s" % point.desc.replace("\n", " ")[:200], flush=True)
        t0 = time.time()
        try:
            point.fn(env)
        except CaseSkip as e:
            dt = time.time() - t0
            print("  ⊘ %s SKIP（%.1fs）：%s" % (point.id, dt, e), flush=True)
            return Result(point=point, state="skip", seconds=dt,
                          evidence=list(env.evidence), skip_reason=str(e))
        except (CaseFailure, Exception) as e:
            dt = time.time() - t0
            tb = traceback.format_exc(limit=6)
            self.recorder.add(point, e, tb=tb, evidence=list(env.evidence))
            self._collect_console_errors(point)
            print("  ✗ %s FAIL（%.1fs）" % (point.id, dt), flush=True)
            return Result(point=point, state="fail", seconds=dt,
                          evidence=list(env.evidence), error=str(e))
        dt = time.time() - t0
        print("  ✓ %s PASS（%.1fs）" % (point.id, dt), flush=True)
        return Result(point=point, state="pass", seconds=dt, evidence=list(env.evidence))

    def _collect_console_errors(self, point):
        """失败时记录前端 console error（不参与判定，只作证据）。"""
        try:
            entries = (self.env.client.console() or {}).get("entries") or []
        except Exception:
            return
        for e in entries:
            txt = e.get("text") or ""
            if e.get("level") == "error" and "SetActiveSessionID" not in txt:
                print("    [console] %s" % txt[:300], flush=True)

    def _stage_points(self, stage):
        return [p for p in self.points if p.stage == stage]

    # ── 主流程 ──
    def run(self):
        exe = _h.resolve_gui_exe()
        if not os.path.isfile(exe):
            print("RESULT: True (SKIPPED: 未找到 GUI 产物 %s → 先构建 dist)" % exe, flush=True)
            return 0
        if not _supports_test_port(exe):
            print("RESULT: True (SKIPPED: %s 未编入 --test-port → 需 `build-desktop.ps1 -Debug`；"
                  "当前疑为 release 构建)" % exe, flush=True)
            return 0
        if not self.points:
            print("（无匹配测试点）")
            return 0

        print("=" * 78, flush=True)
        print("场景化 E2E：%d 个测试点 · 初始化=%s · 工作区=%s"
              % (len(self.points), self.opt.get("init"), self.workspace), flush=True)
        print("=" * 78, flush=True)

        stages = [p.stage for p in self.points]
        try:
            self._start_mock()
            try:
                self._start_gui("main")
            except Exception as e:
                print("[e2e] GUI 启动失败：%s" % e, flush=True)
                print("RESULT: True (SKIPPED: GUI 未就绪 → %s)" % e, flush=True)
                for p in self.points:
                    self.results.append(self._skip(p, "GUI 未就绪：%s" % e))
                return self._finish()
            self._configure_llm()

            # 1) 空工程阶段（工程仍为空，只测空工程行为）
            for p in self._stage_points("empty"):
                if self.recorder.aborted:
                    self.results.append(self._skip(p, "累计问题达到阈值 → 中断"))
                    continue
                self.results.append(self._run_point(p))

            # 2) 预置工程（幂等；已有文件不重建）
            need_seed = any(p.stage in ("seeded", "restart", "destructive")
                            for p in self.points)
            if need_seed and not self.recorder.aborted:
                self._seed_stage()
            elif need_seed:
                print("\n  [seed] 已达问题阈值 → 跳过预置，seeded/restart 测试点记为 SKIP",
                      flush=True)

            # 3) 有数据工程阶段
            for p in self._stage_points("seeded"):
                if self.recorder.aborted or not need_seed:
                    self.results.append(self._skip(p, "累计问题达到阈值 → 中断"
                                                    if self.recorder.aborted else "未执行预置"))
                    continue
                self.results.append(self._run_point(p))

            # 4) 重启阶段（同工作区二次启动；仅当选中且未中断）
            restart_pts = self._stage_points("restart")
            if restart_pts:
                if self.recorder.aborted:
                    for p in restart_pts:
                        self.results.append(self._skip(p, "累计问题达到阈值 → 中断"))
                else:
                    print("\n[e2e] === 第二阶段：同工作区重启（验证持久化）===", flush=True)
                    self._stop_gui()
                    time.sleep(1.0)
                    self._start_gui("restart")
                    self._configure_llm()
                    for p in restart_pts:
                        if self.recorder.aborted:
                            self.results.append(self._skip(p, "累计问题达到阈值 → 中断"))
                            continue
                        self.results.append(self._run_point(p))

            # 5) 破坏性阶段（**最后**执行；可清空配置，不再影响其它测试点）
            destr_pts = self._stage_points("destructive")
            if destr_pts:
                print("\n[e2e] === 破坏性阶段（最后执行；默认不纳入全量）===", flush=True)
                for p in destr_pts:
                    if self.recorder.aborted or not need_seed:
                        self.results.append(self._skip(
                            p, "累计问题达到阈值 → 中断" if self.recorder.aborted else "未执行预置"))
                        continue
                    self.results.append(self._run_point(p))
        finally:
            self._stop_gui()
            if self.mock is not None and getattr(self.mock, "owned", False):
                self.mock.stop()
            self.mock = None

        return self._finish()

    def _seed_stage(self):
        """用 LLM 工具把空工程建成演示工程（幂等）。记录为一次预置步骤（不计测试点）。"""
        print("\n[e2e] === 预置工程（LLM 工具 file_write，幂等）===", flush=True)
        t0 = time.time()
        try:
            from . import workspace as ws_mod
            plan = ws_mod.pending_plan(self.workspace)
            if not plan:
                print("[e2e] 预置文件已全部存在 → 跳过（不重复创建）", flush=True)
                return
            # 插件 / 工具面就绪栅栏（冷启动竞争）
            try:
                _h.wait_plugins_running(self.env.client, ("file",), max_wait=180)
                _h.wait_tools(self.env.client, ("file_write", "file_read", "task_run"),
                              max_wait=120)
            except Exception as e:
                print("[e2e] 工具面未就绪（%s）→ 回退直接落盘" % e, flush=True)
                for rel, content in plan:
                    self.env.write_text(rel, content)
                return
            ok = ws_mod.seed_via_llm(self.env, self.run_turn, plan)
            if not ok:
                print("[e2e] LLM 工具预置未完全成功 → 缺失项回退直接落盘", flush=True)
                for rel, content in plan:
                    if not os.path.exists(self.env.path(rel)):
                        self.env.write_text(rel, content)
        except Exception as e:
            print("[e2e][WARN] 预置异常：%s → 缺失项回退直接落盘" % e, flush=True)
            from . import workspace as ws_mod
            for rel, content in ws_mod.DEMO_FILES.items():
                if not os.path.exists(os.path.join(self.workspace, rel)):
                    self.env.write_text(rel, content)
        finally:
            print("[e2e] 预置完成（%.1fs）" % (time.time() - t0), flush=True)

    def _skip(self, point, reason):
        print("\n  ⊘ %s SKIP：%s" % (point.id, reason), flush=True)
        return Result(point=point, state="skip", skip_reason=reason)

    def _finish(self):
        finished = time.time()
        self.opt["aborted"] = self.recorder.aborted
        self.opt["gui_starts"] = self.gui_starts

        counts = {"pass": 0, "fail": 0, "skip": 0}
        for r in self.results:
            counts[r.state] = counts.get(r.state, 0) + 1

        print("\n" + "=" * 78, flush=True)
        print("测试点结果（阶段 / 功能点）", flush=True)
        print("-" * 78, flush=True)
        label = {"pass": "PASS", "fail": "FAIL", "skip": "SKIP"}
        for r in self.results:
            print("[%s] %-12s %-14s %s"
                  % (label.get(r.state, "?"), r.point.id, r.point.stage, r.point.feature),
                  flush=True)
            if r.state == "fail":
                print("       ↳ %s" % r.error.replace("\n", " ")[:300], flush=True)
            elif r.state == "skip":
                print("       ↳ %s" % r.skip_reason, flush=True)
        print("-" * 78, flush=True)
        print("汇总：%d PASS / %d FAIL / %d SKIP（测试点 %d，问题 %d 条，GUI 启动 %d 次）"
              % (counts["pass"], counts["fail"], counts["skip"], len(self.results),
                 len(self.recorder.issues), self.gui_starts), flush=True)
        if self.recorder.issues:
            print("问题清单：", flush=True)
            for i, it in enumerate(self.recorder.issues, 1):
                print("  %d) %s %s — %s" % (i, it.point_id, it.feature,
                                            it.error.replace("\n", " ")[:200]), flush=True)

        # 报告落盘
        jp, mp = write_report(self.report_dir, {
            "run_id": self.run_id, "init": self.opt.get("init"),
            "workspace": self.workspace, "workspace_kept": self.opt.get("workspace_kept"),
            "cleanup": self.opt.get("cleanup"), "max_issues": self.opt.get("max_issues"),
            "aborted": self.recorder.aborted, "gui_starts": self.gui_starts,
        }, self.results, self.recorder.issues, self.started_at, finished)
        print("\n报告：%s\n      %s" % (mp, jp), flush=True)

        # 用户确认块（明确告知清理/保留结果）
        print("\n结果确认：", flush=True)
        print("  · 测试点 %d（PASS %d / FAIL %d / SKIP %d）"
              % (len(self.results), counts["pass"], counts["fail"], counts["skip"]), flush=True)
        print("  · 问题 %d 条%s" % (len(self.recorder.issues),
                                   "（已达阈值中断）" if self.recorder.aborted else "（未中断）"),
              flush=True)
        print("  · 工程与用户数据：%s（%s）"
              % (self.workspace, "保留供排查" if self.opt.get("workspace_kept") else "已清理"),
              flush=True)
        print("  · 清理策略：%s" % self.opt.get("cleanup"), flush=True)
        print("  · 报告（始终保留）：%s" % self.report_dir, flush=True)
        print("RESULT: %s" % (counts["fail"] == 0), flush=True)
        return 0 if counts["fail"] == 0 else 1


def cleanup_workspace(workspace, home, explicit_workspace):
    """删除自建工作区与独立 HOME（显式给定的 workspace 永不删除）。"""
    removed = []
    if not explicit_workspace and workspace and os.path.isdir(workspace):
        shutil.rmtree(workspace, ignore_errors=True)
        removed.append(workspace)
    if home and os.path.isdir(home):
        shutil.rmtree(home, ignore_errors=True)
        removed.append(home)
    return removed