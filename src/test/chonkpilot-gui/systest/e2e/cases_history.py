# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㊸：**文件历史检查点链（独立 ref）端到端**
（迁移自 run_hist_git H1–H6）。

该测试点**需要独立实例**：文件历史要求 workdir 是 git 仓库，且链按根会话隔离；在主预置工程
（非 git）上不可测，故在点内自起隔离实例（临时 git 仓库 + 独立 HOME）。

* H3 默认关闭 → 工具面无 `history_*`，真实改文件不产生 `refs/chonkpilot/*`；
* H1 开启 → 4 个 history 工具热注册；真实工具改文件 → 检查点链 refs/chonkpilot/<根会话> 出现，
  链头 == 本轮最后盘面；HEAD 提交历史 / HEAD sha / `.git/index` 字节逐字不变；
  `history.status.<会话>` / `history.timeline.<会话>` 回写正确；
* H2 `history_restore{path,to}` 回到上一状态；缺省 to=-1 幂等；空 path / 目录 path 拒绝且盘面不动；
* H4 `history.clear{session}` 只清目标会话链与其回写，其它会话链保留；
* H6 `tool_async.<tool>.touch_files=false` → 前置钩子放行，链只 +1（缺省 +2）；
* H5 非 git 仓库即使 enabled=true 也摘除 history 工具。
"""

import json
import os
import re
import shutil
import subprocess
import time

import harness as _h
from .framework import CaseSkip, case

HIST_TOOLS = ("history_status", "history_diff", "history_show", "history_restore")
CHAIN_PREFIX = "refs/chonkpilot/"
HIST_FILE = "hist.md"
SUB_DIR = "sub"
C1, C2 = "v1", "v2"


def _git(ws, *args):
    return subprocess.run(["git", "-C", ws] + list(args),
                          capture_output=True, text=True, timeout=20)


def _fwd(p):
    return p.replace("\\", "/")


def _read(p):
    try:
        with open(p, encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return None


def _bytes(p):
    try:
        with open(p, "rb") as f:
            return f.read()
    except OSError:
        return None


def _tools(c):
    return sorted(t.get("name") or "" for t in
                  ((c.req("tools-list", {}) or {}).get("tools") or []) if isinstance(t, dict))


def _hist_tools(c):
    return [n for n in _tools(c) if any(n == t or n.endswith("_" + t) for t in HIST_TOOLS)]


def _refs(ws):
    out = _git(ws, "for-each-ref", "--format=%(refname)", CHAIN_PREFIX).stdout
    return [x.strip() for x in out.splitlines() if x.strip()]


def _chain(ws, ref):
    return [x.strip() for x in _git(ws, "log", "--format=%H", ref).stdout.splitlines()
            if x.strip()]


def _blob(ws, sha, rel):
    r = subprocess.run(["git", "-C", ws, "show", "%s:%s" % (sha, rel)],
                       capture_output=True, text=True, timeout=20)
    return r.stdout if r.returncode == 0 else None


def _head_blob(ws, ref, rel):
    shas = _chain(ws, ref)
    return _blob(ws, shas[0], rel) if shas else None


def _newest(ws, ref, rel, content):
    for sha in _chain(ws, ref):
        if _blob(ws, sha, rel) == content:
            return sha
    return None


def _prj(c):
    return (c.req("data-prj-config-list", {}) or {}).get("list") or {}


def _prj_save(c, k, v):
    return c.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def _pjson(c, key):
    raw = _prj(c).get(key)
    if raw in (None, ""):
        return None
    if isinstance(raw, (dict, list)):
        return raw
    try:
        return json.loads(raw)
    except (TypeError, ValueError):
        return None


def _wait(fn, desc, timeout=60):
    end = time.time() + timeout
    last = None
    while time.time() < end:
        last = fn()
        if last:
            return last
        time.sleep(0.4)
    raise AssertionError("%s 超时（%ss）；末次=%r" % (desc, timeout, last))


class _Ctx:
    def __init__(self, client, ws, root):
        self.c, self.ws, self.root = client, ws, root
        self.n = 0

    def turn(self, q, expect_tool=True, timeout=150, sess="", seed=None):
        self.n += 1
        turn = "t-hist-%02d" % self.n
        sid = sess or self.root
        self.c.mq_on_capture(["turn-start", "tool-result", "llm-complete"])
        self.c.mq_emit("session-changed", {"session_id": sid})
        try:
            self.c.req("data-session-ensure-session", {"data": {"session_id": sid}})
        except Exception:
            pass
        self.c.mq_emit("llm-start", {"session_id": sid, "turn": turn, "llm": "mock",
                                     "think": "", "effort": "", "scenario_id": "", "q": q})
        done = None
        end = time.time() + timeout
        while time.time() < end and done is None:
            for e in self.c.events_of("llm-complete", clear=False):
                p = e.get("payload") or {}
                if isinstance(p, dict) and p.get("turn") == turn:
                    done = p
            if done is None:
                time.sleep(0.3)
        if done is None:
            raise AssertionError("轮次未终态（turn=%s）" % turn)
        if done.get("status") != "complete":
            raise AssertionError("轮次终态异常：%r" % done)
        out = []
        end = time.time() + (30 if expect_tool else 0)
        while time.time() < end:
            out = [e.get("payload") for e in self.c.events_of("tool-result", clear=False)
                   if isinstance(e.get("payload"), dict) and e["payload"].get("turn_id") == turn]
            if out:
                break
            time.sleep(0.3)
        if expect_tool and not out:
            raise AssertionError("轮次未产生 tool-result（turn=%s）" % turn)
        return out

    def write_file(self, frm, to):
        abs_path = _fwd(os.path.join(self.ws, HIST_FILE))
        tr = self.turn("call hist-write histabs=%s histfrom=%s histval=%s"
                       % (abs_path, frm, to))
        return str((tr[0] or {}).get("result") or "")

    def disk(self):
        return _read(os.path.join(self.ws, HIST_FILE))


def _wait_plugins(c, targets):
    end = time.time() + 180
    while time.time() < end:
        rows = (c.req("plugin-list", {}) or {}).get("plugins") or []
        by = {p.get("id"): p for p in rows if isinstance(p, dict)}
        if all(by.get(x, {}).get("status") == "running" for x in targets):
            return True
        time.sleep(1.0)
    return False


@case("S-HIST-01", "文件历史", "检查点链：默认关闭/开启热注册/链不碰 HEAD/恢复/按会话清空/touch_files/非 git 摘除",
      stage="seeded", order=396, fp="文件历史 · run_hist_git H1–H6",
      desc="隔离 git 仓库实例：默认无 history_* 且不产生链；开启后热注册 4 工具、真实改文件产生会话链"
           "且 HEAD/HEAD sha/.git/index 不变，status/timeline 回写；restore 回上一状态、缺省幂等、"
           "空/目录拒绝；clear 只清目标会话、其它会话链保留；touch_files=false 链 +1（缺省 +2）；"
           "非 git 实例 enabled=true 仍摘除工具。")
def s_hist_01(env):
    if shutil.which("git") is None:
        raise CaseSkip("git 不可用 → 文件历史功能禁用")
    ws = _h.tmp_dir("ck-e2e-hist-ws-")
    home = _h.tmp_home()
    if _git(ws, "init", "-q").returncode != 0:
        raise CaseSkip("git init 失败")
    _git(ws, "config", "user.email", "e2e@chonkpilot.local")
    _git(ws, "config", "user.name", "e2e-hist")
    with open(os.path.join(ws, ".gitignore"), "w", encoding="utf-8") as f:
        f.write(".chonkpilot/\n")
    with open(os.path.join(ws, HIST_FILE), "w", encoding="utf-8") as f:
        f.write("v0\n")
    with open(os.path.join(ws, "README.md"), "w", encoding="utf-8") as f:
        f.write("history fixture\n")
    os.makedirs(os.path.join(ws, SUB_DIR), exist_ok=True)
    with open(os.path.join(ws, SUB_DIR, "inner.txt"), "w", encoding="utf-8") as f:
        f.write("inner\n")
    _git(ws, "add", "-A")
    if _git(ws, "commit", "-q", "-m", "init").returncode != 0:
        raise CaseSkip("初始提交失败")
    sid = "hist-%s" % re.sub(r"[^0-9]", "", env.run_id)[-8:]
    h = None
    h2 = None
    try:
        h = _h.start_gui(port=_h.free_port(), work_dir=ws, home=home, ensure_spec=True,
                         extra_args=("-llm-base=http://127.0.0.1:%d/v1" % env.mock_port,
                                     "-llm-model=mock-model"))
        c = h.client
        c.wait_ready(120)
        c.req("data-user-config-save", {"data": {
            "llms": [{"name": "mock", "protocol": "openai", "model": "mock-model",
                      "baseUrl": "http://127.0.0.1:%d/v1" % env.mock_port,
                      "apiKey": "sk-test"}], "defaultLLM": 0}})
        c.mq_emit("config-refresh")
        env.check("history/file/task 插件就绪", _wait_plugins(c, ("history", "file", "task")))
        _wait(lambda: True if {"file_replace", "task_run"} <= set(_tools(c)) else None,
              "文件/任务工具就绪", timeout=90)
        ctx = _Ctx(c, ws, sid)
        ref = CHAIN_PREFIX + sid

        # ── H3 默认关闭 ──
        env.check("H3 默认关闭 → 工具面无 history_*",
                  not _hist_tools(c) and len(_tools(c)) > 0, len(_tools(c)))
        ctx.write_file("v0", "w0")
        time.sleep(2.0)
        env.check("H3 默认关闭真实改文件 → 不产生检查点链", not _refs(ws), _refs(ws))

        # ── H1 开启 → 链 ──
        _prj_save(c, "history.enabled", "true")
        env.eq("H1 prj history.enabled 回读", _prj(c).get("history.enabled"), "true")
        _wait(lambda: True if len(_hist_tools(c)) == 4 else None, "history 工具热注册 4 个", 45)
        env.check("H1 4 个 history 工具热注册", len(_hist_tools(c)) == 4, _hist_tools(c))
        head_log0 = _git(ws, "log", "--oneline", "HEAD").stdout
        head_sha0 = _git(ws, "rev-parse", "HEAD").stdout.strip()
        idx0 = _bytes(os.path.join(ws, ".git", "index"))
        ctx.write_file("w0", C1)
        env.check("H1 真实工具改到盘面", ctx.disk() == C1 + "\n", ctx.disk())
        _wait(lambda: True if _refs(ws) else None, "refs/chonkpilot/* 出现", 40)
        env.check("H1 检查点 ref == [refs/chonkpilot/<根会话>]", _refs(ws) == [ref], _refs(ws))
        env.check("H1 链长 ≥1", len(_chain(ws, ref)) >= 1, len(_chain(ws, ref)))
        env.check("H1 不碰用户分支（log/HEAD/index 逐字不变）",
                  _git(ws, "log", "--oneline", "HEAD").stdout == head_log0
                  and _git(ws, "rev-parse", "HEAD").stdout.strip() == head_sha0
                  and _bytes(os.path.join(ws, ".git", "index")) == idx0)
        _wait(lambda: _head_blob(ws, ref, HIST_FILE) == C1 + "\n" or None,
              "链头收敛到本轮最后盘面", 40)
        env.check("H1 链头 == 本轮最后写下的盘面", _head_blob(ws, ref, HIST_FILE) == C1 + "\n",
                  _head_blob(ws, ref, HIST_FILE))
        st = _wait(lambda: (lambda o: o if o and int(o.get("checkpointCount") or 0) >= 1 else None)(
            _pjson(c, "history.status." + sid)), "history.status.<会话> 回写", 40)
        env.check("H1 status enabled/repo/mode/checkpointCount",
                  st.get("enabled") is True and st.get("repo") is True
                  and st.get("mode") == "active", st)
        tl = _wait(lambda: (lambda a: a if isinstance(a, list) and a
                            and int(a[0].get("files") or 0) >= 1 else None)(
            _pjson(c, "history.timeline." + sid)), "history.timeline.<会话> 回写", 40)
        env.check("H1 timeline[0] files≥1 / session==根会话 / n=-1",
                  int(tl[0].get("files") or 0) >= 1 and tl[0].get("session") == sid
                  and tl[0].get("n") == -1, tl[:1])
        head_ref = _git(ws, "rev-parse", ref).stdout.strip()

        def _tl_synced():
            a = _pjson(c, "history.timeline." + sid) or []
            return a if a and tl[0].get("id") == head_ref else None
        try:
            _wait(_tl_synced, "timeline 链头同步", 20)
        except Exception:
            env.note("timeline 链头同步", "回写异步，20s 内未对齐（不阻断 H1 主判据）")

        # ── H2 恢复 ──
        ctx.write_file(C1, C2)
        env.check("H2 前置改动落到盘面", ctx.disk() == C2 + "\n", ctx.disk())
        _wait(lambda: _head_blob(ws, ref, HIST_FILE) == C2 + "\n" or None,
              "链头收敛到 C2", 40)
        ckpt = _wait(lambda: _newest(ws, ref, HIST_FILE, C1 + "\n"), "链上存在 C1 检查点", 30)
        tr = ctx.turn("call hist-restore histrel=%s histto=%s" % (HIST_FILE, ckpt))
        txt = str((tr[0] or {}).get("result") or "")
        env.check("H2 restore 到上一状态（restored + 盘面 v1）",
                  "restored" in txt and ctx.disk() == C1 + "\n", {"txt": txt[:80], "disk": ctx.disk()})
        tr = ctx.turn("call hist-restore histrel=%s" % HIST_FILE)
        txt = str((tr[0] or {}).get("result") or "")
        env.check("H2 缺省 to=-1 成功且幂等",
                  "restored" in txt and ctx.disk() == C1 + "\n", {"txt": txt[:80]})
        before = ctx.disk()
        tr = ctx.turn("call hist-restore-nopath")
        env.check("H2 空 path 拒绝且盘面不动",
                  "path 必填" in str((tr[0] or {}).get("result") or "") and ctx.disk() == before,
                  str((tr[0] or {}).get("result"))[:80])
        tr = ctx.turn("call hist-restore-dir histrel=%s" % SUB_DIR)
        env.check("H2 目录 path 拒绝且盘面不动",
                  "是目录" in str((tr[0] or {}).get("result") or "") and ctx.disk() == before,
                  str((tr[0] or {}).get("result"))[:80])

        # ── H4 按会话清空 ──
        sid2 = sid + "-b"
        ctx.turn("call hist-write histabs=%s histfrom=%s histval=%s"
                 % (_fwd(os.path.join(ws, HIST_FILE)), C1, "s2v"), sess=sid2)
        ref2 = CHAIN_PREFIX + sid2
        _wait(lambda: True if ref2 in _refs(ws) else None, "第二会话链建立", 40)
        n_self = len(_chain(ws, ref))
        _prj_save(c, "history.clear", json.dumps(
            {"ts": time.strftime("%Y-%m-%dT%H:%M:%S"), "session": sid}))
        _wait(lambda: True if ref not in _refs(ws) else None, "目标会话链清空", 30)
        env.check("H4 clear 只清目标会话（第二会话链保留）",
                  ref not in _refs(ws) and ref2 in _refs(ws), _refs(ws))
        _wait(lambda: True if _pjson(c, "history.timeline." + sid) == [] else None,
              "目标 timeline=[]", 25)
        st2 = _pjson(c, "history.status." + sid) or {}
        tl2 = _pjson(c, "history.timeline." + sid2) or []
        env.check("H4 目标 status=0/timeline=[]，第二会话回写保留",
                  int(st2.get("checkpointCount") or 0) == 0
                  and _pjson(c, "history.timeline." + sid) == []
                  and isinstance(tl2, list) and bool(tl2),
                  {"count": st2.get("checkpointCount"), "tl2": len(tl2)})

        # ── H6 touch_files=false → 前置钩子放行（+1 而非 +2） ──
        with open(os.path.join(ws, HIST_FILE), "w", encoding="utf-8") as f:
            f.write("h6z\n")
        ctx.write_file("h6z", "h6a")
        _wait(lambda: _head_blob(ws, ref, HIST_FILE) == "h6a\n" or None,
              "H6 前置链头 == 盘面", 40)
        tool = next((n for n in _tools(c) if n.endswith("task_run")), "task_run")
        cur = ctx.disk().strip()
        n0 = len(_chain(ws, ref))
        ctx.turn("call hist-dirty-write histrel=%s histto=-1 histabs=%s histfrom=%s histval=h6b"
                 % (HIST_FILE, _fwd(os.path.join(ws, HIST_FILE)), cur))
        env.check("H6 同轮两次工具调用均执行（盘面 h6b）", ctx.disk() == "h6b\n", ctx.disk())
        _wait(lambda: (lambda n: n if n - n0 >= 2 else None)(len(_chain(ws, ref))),
              "H6 缺省 +2", 25)
        env.check("H6 缺省（涉及）链 +2", len(_chain(ws, ref)) - n0 == 2,
                  len(_chain(ws, ref)) - n0)
        c.req("data-user-config-save", {"data": {"tool_async": json.dumps(
            {tool: {"touch_files": False}})}})
        c.mq_emit("config-refresh")
        raw = (c.req("data-user-config-load", {}) or {}).get("data", {}).get("tool_async")
        if isinstance(raw, str):
            try:
                raw = json.loads(raw)
            except Exception:
                raw = {}
        env.check("H6 usr touch_files=false 落库回读",
                  isinstance(raw, dict) and (raw.get(tool) or {}).get("touch_files") is False,
                  raw)
        cur2 = ctx.disk().strip()
        n2 = len(_chain(ws, ref))
        ctx.turn("call hist-dirty-write histrel=%s histto=-1 histabs=%s histfrom=%s histval=h6c"
                 % (HIST_FILE, _fwd(os.path.join(ws, HIST_FILE)), cur2))
        env.check("H6 false 轮次改到盘面", ctx.disk() == "h6c\n", ctx.disk())
        _wait(lambda: (lambda n: n if n - n2 >= 1 else None)(len(_chain(ws, ref))),
              "H6 false +1", 25)
        env.check("H6 touch_files=false 只余轮末补点 +1",
                  len(_chain(ws, ref)) - n2 == 1, len(_chain(ws, ref)) - n2)

        # ── H5 非 git 仓库 → 摘除工具（独立实例） ──
        ws2 = _h.tmp_dir("ck-e2e-hist-nogit-")
        h2 = _h.start_gui(port=_h.free_port(), work_dir=ws2, home=_h.tmp_home(),
                          ensure_spec=True,
                          extra_args=("-llm-base=http://127.0.0.1:%d/v1" % env.mock_port,
                                      "-llm-model=mock-model"))
        c2 = h2.client
        c2.wait_ready(120)
        _wait_plugins(c2, ("history", "file", "task"))
        _wait(lambda: True if {"file_replace", "task_run"} <= set(_tools(c2)) else None,
              "非 git 实例工具面就绪", 90)
        env.check("H5 非 git 实例基线无 history_*", not _hist_tools(c2), _tools(c2)[:6])
        c2.req("data-prj-config-save", {"data": {"key": "history.enabled", "value": "true"}})
        time.sleep(8.0)
        env.check("H5 非 git + enabled=true 仍摘除 history 工具", not _hist_tools(c2),
                  _hist_tools(c2))
        vcs = c2.req("gui.vcs.info", {}) or {}
        env.check("H5 gui.vcs.info.git=false", vcs.get("git") in (False, None), vcs)
    finally:
        for hx in (h2, h):
            if hx is not None:
                try:
                    hx.stop()
                except Exception:
                    pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("文件历史组（主实例）无前端错误", len(errs) == 0, errs[:3])