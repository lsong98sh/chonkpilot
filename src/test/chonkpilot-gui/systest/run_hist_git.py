# -*- coding: utf-8 -*-
"""L4 实机套件：文件历史**检查点链**（独立 ref，2026-09-27 批次③语义）端到端。

语义对照（2026-09-28 本套件改写：**删除**旧「轮边界提交到用户分支」全部断言）：
  ✗ 旧：轮边界 `git add -A` + `git commit` 到**用户分支**（提交信息 `chonk: snapshot`）——
        断言提交数增长 / HEAD 前进。产品语义已整体移除，旧断言**必然失败**。
  ✓ 新：**独立 ref 的检查点链** `refs/chonkpilot/<根会话>`（点 = `commit-tree` 产物，parent
        线性串联，**ref 只指向链头**）；**绝不碰 HEAD、绝不碰 `.git/index`**（打点走持久临时
        index `<workdir>/.chonkpilot/history/index`，`GIT_INDEX_FILE` 注入）。
        打点触发 = ① gateway **执行工具前**前置钩子（`tools/register.pre_hook_subject` →
        `history-pre-tool-hook`；脏才打点，**打点失败即拒绝该工具调用**）
        ② `session-complete`（主轮次）**轮末补点**；脏位来自 `filesys.changed`（不脏 = 零 git 调用）。
        门控 `history.enabled` **默认关闭**（仅显式 `"true"` 生效）；workdir 无 `.git` / git 不可用
        → 功能禁用且**工具摘除**（4 个工具 `history_status/diff/show/restore`，`category=self`）。

覆盖（每条断言**真实 git / 文件产物**，不以「保存成功」充数）：
  H3 默认关闭 + 工具摘除（先跑，兼作基线）：prj `history.enabled` 缺失 → 工具面**不含** `history_*`
     （基线工具面非空 → 排除"工具面空"造成的假绿）；经**真实工具**改文件一轮 → `refs/chonkpilot/*` 仍为空。
  H1 检查点链生成且不碰用户分支：显式开启 → 工具面出现 4 个 history 工具 → 真实工具改文件一轮 →
     `refs/chonkpilot/<本会话根>` 出现且链长 ≥1、**轮末补点**使链头内容 == 本轮最后写下的盘面；
     `git log --oneline HEAD` **逐行不变**、HEAD sha 不变、`.git/index` **字节不变**；
     **会话级**键 `history.status.<根会话>`（enabled/repo/mode/checkpointCount）与
     `history.timeline.<根会话>`（`timeline[0].files≥1`、`session==本根会话`、`timeline[0].id`
     == ref 指向的链头）可读（I-135：按会话、不再互相覆盖）。
  H2 单文件恢复 + 拒绝批量：链上把文件改成新内容后（轮末补点 → 链头 == 盘面），
     `history_restore{path,to=<上一状态 commit>}` 使文件**内容实测回到上一状态**；`to` 缺省（-1 = 链头）成功且**幂等**（盘面 == 链头）；
     `history_restore {}`（空 path）/ 目录 path → **工具报错拒绝**且盘面未动。
  H4 清空（**只清目标会话**）：写 prj `history.clear` = JSON `{ts, session}` → **目标会话**的链被清、
     `history.timeline.<目标>` = `[]`、`status.<目标>.checkpointCount` = 0；**其它会话的链与其回写保留**
     （I-136 —— 不再按仓库粒度清空全部 `refs/chonkpilot/*`）。
  H6 「涉及文件变动」（usr `tool_async.<工具>.touch_files`）：**同一轮内两次工具调用**
     （`history_restore` 执行时同步置脏 → `filesys_run` 的前置钩子据此判定）——
     缺省（涉及）链路 +2（前置点 + 轮末补点）；显式 `false`（不涉及）→ 前置钩子**放行不打点**、只余
     轮末补点 → +1（含 usr 键保存/回读）。
  H5 非 git 仓库：另起独立实例（workdir 无 `.git`，`gui.vcs.info.git=false`）→ 即使
     `history.enabled=true`，工具面仍**不含** `history_*`（工具摘除）。

为什么 H2 用「绝对 commit id」而非固定相对步（如 -2）——判据确定性（**非放宽断言**）：
  `history_restore` 的一致性校验以**链头**为基准（盘面 == 链头内容），而**恢复轮自身的前置打点**
  会先在当前盘面（C2）上补一个点 → `-1` / `-2` 都可能指向 C2 本身（同内容重复点），
  「上一状态」的相对号随重复点数漂移、不可作稳定判据。绝对 commit id 由测试**从真实链**
  （`git log` + `git show`）取出，且必然是本链祖先（`resolveTo` 以 `merge-base --is-ancestor` 校验）
  → 内容判据逐字确定。`to` 缺省（-1，= 链头）的语义单列一条（H2 ②，幂等：盘面 == 链头 → 写回同内容）。

「轮末补点」的确定性观测（H1 / H2 前置）：打点的脏位来自 `filesys.changed`（fsnotify **60ms 去抖**）。
  缺陷② 已修（轮末补点**不依赖脏位、强制走流程**）：`session-complete` 的补点不再等 `filesys.changed`
  到达（原实现「脏才打点」会与去抖赛跑而偶发漏点，实跑 2026-09-28 首轮即命中：H1 轮后链长=1、
  链头仍是改动前内容）。故本套件**无需任何续轮延迟**：工具改文件的盘面已落定，强制补点即捕获
  本轮最后产像 → 断言仍以「链头内容 == 本轮最后写下的盘面」为准（判据未放宽）。

观测面（**零新增 MQ 主题**，全部取自 61-消息一览既有面）：
  * 工具面 = 客户端能力面 `tools-list`（§4.5，桥按当前实例作用域过滤）→ `{tools:[{name}]}`；
  * 配置/状态 = prj/prjusr 键（§3）：写入走 `data-prj-config-save`（`history.enabled` /
    `history.clear`），读回走 `data-prj-config-list`（`history.status.<根会话>` /
    `history.timeline.<根会话>`，插件回写；I-135 按会话）；
  * 工具结果 = 旧协议兼容事件 `tool-result`（桥兼发；载荷 `{turn_id, tool, result, status}`）；
  * 真实产物 = 夹具仓库 `git log` / `git for-each-ref` / `git show` / `.git/index` 字节 / 文件内容。

隔离（51-FP与测试映射 §5/§6-8「天然隔离例外」）：本套件**自起**GUI（动态端口 + 临时 work-dir +
独立 `--data-dir` + 独立 `HOME` → usr 主库/prj 库全新，天然隔离，故**不接**套件级配置 guard）；
夹具 `git init` + 初始提交都在**临时目录**内，**不碰共享 `systest/ws`**；H5 另起第二个实例
（独立 work-dir/data-dir/HOME）。全部临时目录登记 `harness.tmp_dir` → 结束即回收。
夹具仓库**使用 git 默认配置**（不覆盖 `core.autocrlf`）：restore 的一致性校验已改为**交给 git 判定**
（`git diff --quiet <链头> -- <path>`，chain.go:547-570：退出码 0 = 工作区与链头一致），行尾归一
由 git 处理，故 Git for Windows 默认 `core.autocrlf=true` 下亦成立（原先按原始字节比较导致恒拒绝的
缺陷已修复，本套件不再做任何环境隔离）。

前置：mock LLM（`mock_llm.py`，本套件自起自收，含 4 条文件历史专用路由）；dist 下 chonkpilot.exe 为最新构建。
运行：python run_hist_git.py   （自起自收，无需外部底座）
"""

import json
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51 §6 测试资源规范）  # noqa: E402

HIST_TOOLS = ("history_status", "history_diff", "history_show", "history_restore")
CHAIN_PREFIX = "refs/chonkpilot/"
HIST_FILE = "hist.md"   # 被改 / 被恢复的单文件（workdir 相对）
SUB_DIR = "sub"         # 目录（H2「禁止批量」拒绝用例）

# 内容常量：被改文件在各阶段写入的字面内容（逐字比对链头 / 盘面）。
C1 = "v1"      # H1 写入 / H2 恢复目标内容
C2 = "v2"      # H2「改坏」后内容

# ── 夹具目录（临时；GUI 启动前 git init + 初始提交 → 插件 ensureWork 需见 .git）──
WS = _h.tmp_dir("ck-hist-ws-")
DD = _h.tmp_dir("ck-hist-dd-")
HOME = _h.tmp_home()

MOCK = None   # mock LLM 资源（main 内自起）
c = None      # 主实例客户端
SID = ""      # 根会话（= 链 slug → refs/chonkpilot/<SID>）


# ══════════════════════════════════════════════════════════
# 通用工具（git / 文件 / prj / 工具面）
# ══════════════════════════════════════════════════════════

def git(wd, *args):
    return subprocess.run(["git", "-C", wd] + list(args),
                          capture_output=True, text=True, timeout=20)


def fwd(p):
    """绝对路径 → 正斜杠（filesys_run DSL 句柄内 `\\` 是转义符，反斜杠路径会被误解析）。"""
    return p.replace("\\", "/")


def read(path):
    try:
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            return f.read()
    except OSError:
        return None


def read_bytes(path):
    try:
        with open(path, "rb") as f:
            return f.read()
    except OSError:
        return None


def prj_map():
    r = c.req("data-prj-config-list", {}) or {}
    return r.get("list") or {}


def prj_save(k, v):
    return c.req("data-prj-config-save", {"data": {"key": k, "value": v}})


def prj_json(key):
    """prj 单键 → JSON 解包（缺失/空/非 JSON → None；已是对象则原样返回）。"""
    raw = prj_map().get(key)
    if raw in (None, ""):
        return None
    if isinstance(raw, (dict, list)):
        return raw
    try:
        return json.loads(raw)
    except (TypeError, ValueError):
        return None


# ── 会话级键（I-135/I-136）：键名 = 前缀 + 根会话 slug（与后端 chainSlug 同口径）──

def status_key(sid):
    return "history.status." + sid


def timeline_key(sid):
    return "history.timeline." + sid


def chain_count(ref):
    """链上检查点个数（链不存在 → 0）。"""
    out = git(WS, "rev-list", "--count", ref).stdout.strip()
    try:
        return int(out or 0)
    except ValueError:
        return 0


def write_disk(content):
    """**直写**盘面（不经工具）：供「同轮内两次工具调用」用例构造前置状态。"""
    with open(os.path.join(WS, HIST_FILE), "w", encoding="utf-8") as f:
        f.write(content)


def head_blob(ref, rel):
    """该链**链头**中该文件的内容（链为空 → None）。"""
    shas = chain_shas(ref)
    return blob_at(shas[0], rel) if shas else None


def tools():
    r = c.req("tools-list", {}) or {}
    return sorted(t.get("name") or "" for t in (r.get("tools") or []))


def hist_tool_names(names=None):
    """工具面里的文件历史工具（按**后缀**匹配：注册名经 self 节点前缀 → `self_history_*`）。"""
    names = tools() if names is None else names
    return [n for n in names if any(n == t or n.endswith("_" + t) for t in HIST_TOOLS)]


def refs():
    """本夹具仓库的全部检查点 ref（`refs/chonkpilot/*`）。"""
    out = git(WS, "for-each-ref", "--format=%(refname)", CHAIN_PREFIX).stdout
    return [l.strip() for l in out.splitlines() if l.strip()]


def chain_shas(ref):
    """链上检查点 commit（新→旧）。"""
    out = git(WS, "log", "--format=%H", ref).stdout
    return [l.strip() for l in out.splitlines() if l.strip()]


def blob_at(sha, rel):
    """该 commit 中该文件的内容（不存在 → None）。"""
    r = subprocess.run(["git", "-C", WS, "show", "%s:%s" % (sha, rel)],
                       capture_output=True, text=True, timeout=20)
    return r.stdout if r.returncode == 0 else None


def newest_ckpt_with(ref, rel, content):
    """链上**最新**一个该文件内容 == content 的检查点 commit（无 → None）。"""
    for sha in chain_shas(ref):
        if blob_at(sha, rel) == content:
            return sha
    return None


def wait_until(fn, desc, max_wait=60):
    """有界轮询直至 fn() 返回真值（返回该值）；超时抛 TestError（打印末次观测）。"""
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        last = fn()
        if last:
            return last
        time.sleep(0.4)
    raise TestError("%s 超时（%ss）；末次观测=%r" % (desc, max_wait, last))


# ══════════════════════════════════════════════════════════
# 轮次驱动（mock LLM：既有驱动方式 = mq llm-start）
# ══════════════════════════════════════════════════════════

_TURNS = [0]


def run_turn(text, expect_tool=True, timeout=150, sess=None):
    """发一轮 llm-start（mock 按关键词回工具调用）→ 等该轮 `llm-complete`；
    返回该轮 `tool-result` 载荷列表（list[dict]）。`sess` 缺省 = 本套件根会话 SID。"""
    _TURNS[0] += 1
    turn = "t-hist-%02d" % _TURNS[0]
    sid = sess or SID
    c.eval("if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};")
    c.mq_on_capture(["turn-start", "tool-result", "llm-complete"])
    c.mq_emit("session-changed", {"session_id": sid})
    c.mq_emit("llm-start", {"session": sid, "turn": turn, "llm": "mock", "think": "",
                            "effort": "", "scenario_id": "", "q": text})
    done = None
    deadline = time.time() + timeout
    while time.time() < deadline and done is None:
        for e in c.events_of("llm-complete", clear=False):
            p = e.get("payload") or {}
            if isinstance(p, dict) and p.get("turn") == turn:
                done = p
        if done is None:
            time.sleep(0.3)
    if done is None:
        raise TestError("轮次未在 %ss 内终态（turn=%s, q=%r）" % (timeout, turn, text))
    if done.get("status") != "complete":
        raise TestError("轮次终态异常（turn=%s）：%r" % (turn, done))
    out = []
    deadline = time.time() + (30 if expect_tool else 0)
    while time.time() < deadline:
        out = [e.get("payload") for e in c.events_of("tool-result", clear=False)
               if isinstance(e.get("payload"), dict) and e["payload"].get("turn_id") == turn]
        if out:
            break
        time.sleep(0.3)
    if expect_tool and not out:
        raise TestError("轮次未产生 tool-result（turn=%s, q=%r）" % (turn, text))
    return out


def tool_text(tr):
    """本轮的**唯一** tool-result 文本（工具结果经任务终态摘要回传；拒绝 = 错误文本）。"""
    if len(tr) != 1:
        raise TestError("本轮应恰有 1 条 tool-result，实际 %d 条：%r" % (len(tr), tr))
    return str(tr[0].get("result") or "")


def write_file(frm, to):
    """驱动一轮**真实改文件**（filesys_run RPL：frm → to，盘面内容实测已变）。"""
    return run_turn("call hist-write histabs=%s histfrom=%s histval=%s"
                    % (fwd(os.path.join(WS, HIST_FILE)), frm, to))


def disk():
    return read(os.path.join(WS, HIST_FILE))


# ══════════════════════════════════════════════════════════
# H3 默认关闭 + 工具摘除（兼作基线）
# ══════════════════════════════════════════════════════════

def case_h3_default_off_no_tools_no_chain():
    """`history.enabled` 缺失（默认关闭）→ 工具面无 history_*；真实改文件一轮**不产生**检查点。

    判据（真实产物）：① 基线工具面**非空**且不含 history_*（排除"工具面空"假绿）；
    ② prj `history.enabled` 缺失；③ mock 驱动 filesys_run 真实改文件（v0→w0，盘面实测已变）
    → `refs/chonkpilot/*` 仍为空（前置钩子与轮末补点都不打点 = 零 git 调用）。
    """
    names = tools()
    if not names:
        raise TestError("工具面为空（实例/网关未就绪），无法判定 history 工具是否摘除")
    bad = hist_tool_names(names)
    if bad:
        raise TestError("默认（history.enabled 缺失）不应注册 history 工具，实际=%r" % bad)
    if prj_map().get("history.enabled") not in (None, ""):
        raise TestError("基线 history.enabled 应为缺失（独立 work-dir），实际=%r"
                        % prj_map().get("history.enabled"))

    write_file("v0", "w0")
    got = disk()
    if got != "w0\n":
        raise TestError("真实工具未改到文件（应 w0\\n）：%r" % got)
    time.sleep(3.0)   # 给「轮末补点」充分窗口（若错误地打了点，此窗口内必然出现）
    if refs():
        raise TestError("默认关闭不应打点，实际 refs=%r；log=%r"
                        % (refs(), git(WS, "log", "--oneline", CHAIN_PREFIX + SID).stdout))
    print("[H3] 工具面 %d 项且无 history_*（%r）· history.enabled 缺失 · 真实改文件后 refs=[]"
          % (len(names), sorted(HIST_TOOLS)), flush=True)


# ══════════════════════════════════════════════════════════
# H1 检查点链生成 + 绝不碰用户分支
# ══════════════════════════════════════════════════════════

def case_h1_chain_and_user_branch_untouched():
    """显式开启 → 真实改文件一轮 → 检查点链出现；HEAD / `.git/index` / 提交历史**逐字不变**。

    链路：prj `history.enabled=true` → persist 广播 `data-prj-config-refresh` →
    `plugin-history onPrjConfigRefresh`（`history.go:357-404`）→ `syncTools()` 注册 4 个 gateway
    工具（载荷带 `pre_hook_subject`，`callgate.go:81-109`）→ **同一实例内** `tools-list` 即时可见。
    打点：`filesys.changed`（真实文件写）置脏 → 前置钩子 / `session-complete` 轮末补点
    （`chain.go:136-246`）→ `commit-tree` + `update-ref refs/chonkpilot/<根会话>`。
    """
    prj_save("history.enabled", "true")
    if prj_map().get("history.enabled") != "true":
        raise TestError("A：prj history.enabled 回读=%r" % prj_map().get("history.enabled"))

    # 工具面：4 个 history 工具（保存即生效，无需重启）
    deadline = time.time() + 40
    have = []
    while time.time() < deadline:
        have = hist_tool_names()
        if len(have) >= len(HIST_TOOLS):
            break
        time.sleep(0.4)
    if len(have) != len(HIST_TOOLS):
        raise TestError("启用后应注册 %d 个 history 工具，实际=%r" % (len(HIST_TOOLS), have))
    print("[H1] 启用后工具面增量=%r" % sorted(have), flush=True)

    # ── 用户分支 / 用户索引 前置快照（**驱动工具调用之前**）──
    head_log0 = git(WS, "log", "--oneline", "HEAD").stdout
    head_sha0 = git(WS, "rev-parse", "HEAD").stdout.strip()
    idx_path = os.path.join(WS, ".git", "index")
    idx0 = read_bytes(idx_path)
    if not head_sha0:
        raise TestError("夹具应有初始提交（HEAD 未就绪）")

    write_file("w0", C1)
    got = disk()
    if got != C1 + "\n":
        raise TestError("真实工具未改到文件（应 %s\\n）：%r" % (C1, got))

    want_ref = CHAIN_PREFIX + SID
    got_refs = wait_until(lambda: refs() or None, "改文件后出现 refs/chonkpilot/*")
    if got_refs != [want_ref]:
        raise TestError("检查点 ref 应恰为 [%s]（= 根会话 slug），实际=%r" % (want_ref, got_refs))
    n = int(git(WS, "rev-list", "--count", want_ref).stdout.strip() or 0)
    if n < 1:
        raise TestError("链长应 ≥1，实际=%d" % n)

    # ── 绝不碰用户分支 / 用户索引 ──
    head_log1 = git(WS, "log", "--oneline", "HEAD").stdout
    head_sha1 = git(WS, "rev-parse", "HEAD").stdout.strip()
    if head_log1 != head_log0:
        raise TestError("打点改动了用户分支提交历史：\n前=%r\n后=%r" % (head_log0, head_log1))
    if head_sha1 != head_sha0:
        raise TestError("打点移动了用户分支 HEAD：%s → %s" % (head_sha0, head_sha1))
    idx1 = read_bytes(idx_path)
    if idx0 is not None and idx1 != idx0:
        raise TestError(".git/index 被改动（%d → %d 字节）—— 打点必须走 GIT_INDEX_FILE 临时索引"
                        % (len(idx0), len(idx1 or b"")))

    # ── 轮末补点（真实产物）：链头 == 该轮最后写下的盘面 ──
    wait_until(lambda: head_blob(want_ref, HIST_FILE) == C1 + "\n" or None,
               "链头收敛到 %s\\n（轮末补点：最后一步的产像入链）" % C1, max_wait=40)
    if head_blob(want_ref, HIST_FILE) != C1 + "\n":
        raise TestError("链头内容应为最后一步的产像 %s\\n，实际=%r"
                        % (C1, head_blob(want_ref, HIST_FILE)))

    # ── prj 回写：**会话级**键 history.status.<SID> / history.timeline.<SID>（I-135）──
    st = wait_until(
        lambda: (lambda o: o if o and int(o.get("checkpointCount") or 0) >= 1 else None)(
            prj_json(status_key(SID))),
        "prj history.status.<SID>.checkpointCount ≥ 1")
    if not st.get("enabled") or not st.get("repo"):
        raise TestError("history.status 应 enabled=true / repo=true，实际=%r" % st)
    if st.get("mode") != "active":
        raise TestError("history.status.mode 应 active，实际=%r" % st.get("mode"))
    tl = wait_until(
        lambda: (lambda a: a if isinstance(a, list)
                 and any(int(e.get("files") or 0) >= 1 for e in a) else None)(prj_json(timeline_key(SID))),
        "prj history.timeline.<SID> 含 files≥1 的步")
    if not tl or int(tl[0].get("files") or 0) < 1:
        raise TestError("timeline[0]（= 链头 = 最后一步产像）应 files≥1，实际=%r" % (tl[:2],))
    if not any(e.get("session") == SID for e in tl if int(e.get("files") or 0) >= 1):
        raise TestError("timeline 步的 session 应 = 根会话 %s，实际=%r"
                        % (SID, [(e.get("session"), e.get("files")) for e in tl]))
    if tl[0].get("n") != -1:
        raise TestError("timeline 最新在前、相对编号应从 -1 起，实际 n=%r" % tl[0].get("n"))
    head_ref = git(WS, "rev-parse", want_ref).stdout.strip()
    if tl[0].get("id") != head_ref:
        raise TestError("ref 应只指向链头：ref=%s timeline[0].id=%s" % (head_ref, tl[0].get("id")))
    print("[H1] refs=%r 链长=%d · HEAD %s 未动（log 逐行不变）· .git/index %d 字节不变\n"
          "     history.status: enabled=%r repo=%r mode=%r checkpointCount=%r\n"
          "     history.timeline[0]: n=%r id=%s tool=%r session=%r files=%r(+%r/-%r)"
          % (got_refs, n, head_sha1[:8], len(idx1 or b""), st.get("enabled"), st.get("repo"),
             st.get("mode"), st.get("checkpointCount"), tl[0].get("n"),
             str(tl[0].get("id"))[:8], tl[0].get("tool"), tl[0].get("session"),
             tl[0].get("files"), tl[0].get("added"), tl[0].get("removed")), flush=True)


# ══════════════════════════════════════════════════════════
# H2 单文件恢复 + 拒绝批量
# ══════════════════════════════════════════════════════════

def case_h2_restore_single_and_reject_batch():
    """单文件回滚（内容实测回上一状态）+ 空 path / 目录 **报错拒绝**（盘面未动）。

    一致性前提：`history_restore` 要求「盘面当前内容 == 链头内容」或「该文件已被删除」
    （`chain.go:529-547 checkConsistency`）。故前置 = 先由真实工具把文件改成 C2，并由
    **轮末补点**使链头 == 盘面（C2），再以链上 C1 点作恢复源。
    """
    ref = CHAIN_PREFIX + SID
    if ref not in refs():
        raise TestError("前置：检查点链不存在（H1 未通过？）；refs=%r" % refs())

    # 前置：真实工具把文件改成 C2（"改坏"）+ 轮末补点 → 断言 盘面 == 链头（一致性前提成立）
    write_file(C1, C2)
    if disk() != C2 + "\n":
        raise TestError("前置改文件失败（应 %s\\n）：%r" % (C2, disk()))
    wait_until(lambda: head_blob(ref, HIST_FILE) == C2 + "\n" or None,
               "链头收敛到 %s（轮末补点）" % C2, max_wait=40)
    if head_blob(ref, HIST_FILE) != C2 + "\n":
        raise TestError("链头内容应 = 盘面（%s\\n），实际=%r" % (C2, head_blob(ref, HIST_FILE)))

    # ① 单文件恢复：to = 链上「上一状态（C1）」的检查点 commit（绝对 id，确定性判据）
    ckpt_v1 = wait_until(lambda: newest_ckpt_with(ref, HIST_FILE, C1 + "\n"),
                         "链上存在内容为 %s\\n 的检查点" % C1)
    pre = [(s[:8], blob_at(s, HIST_FILE)) for s in chain_shas(ref)]
    tr = run_turn("call hist-restore histrel=%s histto=%s" % (HIST_FILE, ckpt_v1))
    txt = tool_text(tr)
    if "restored" not in txt:
        post = [(s[:8], blob_at(s, HIST_FILE)) for s in chain_shas(ref)]
        raise TestError("history_restore 应成功（回执含 restored），实际=%r\n"
                        "  盘面=%r 目标点=%s refs=%r\n  调用前链（新→旧）=%r\n  调用后链（新→旧）=%r"
                        % (txt, disk(), ckpt_v1[:8], refs(), pre, post))
    if disk() != C1 + "\n":
        raise TestError("history_restore 后文件内容应回到上一状态 %s\\n，实际=%r" % (C1, disk()))
    print("[H2] ① restore{path=%s,to=%s} → 盘面 %r（回上一状态）；回执=%r"
          % (HIST_FILE, ckpt_v1[:8], disk(), txt.strip()[:120]), flush=True)

    # ② to 缺省（-1 = 链头）：成功且幂等（盘面 == 链头 → 写回同内容）
    tr = run_turn("call hist-restore histrel=%s" % HIST_FILE)
    txt = tool_text(tr)
    if "restored" not in txt:
        raise TestError("缺省 to（-1）应成功，实际=%r" % txt)
    if disk() != C1 + "\n":
        raise TestError("缺省 to（-1 = 链头）应幂等（仍 %s\\n），实际=%r" % (C1, disk()))
    print("[H2] ② restore{path=%s}（缺省 to=-1，链头）→ 成功且幂等，盘面 %r"
          % (HIST_FILE, disk()), flush=True)

    # ③ 空 path → 拒绝（path 必填、单文件、禁止批量）
    before = disk()
    tr = run_turn("call hist-restore-nopath")
    txt = tool_text(tr)
    if "path 必填" not in txt:
        raise TestError("空 path 应被拒绝（提示 path 必填），实际=%r" % txt)
    if disk() != before:
        raise TestError("拒绝时应不改动文件：%r → %r" % (before, disk()))
    print("[H2] ③ restore{}（空 path）→ 拒绝；回执=%r" % txt.strip()[:120], flush=True)

    # ④ path 为目录 → 拒绝（单文件、禁止批量）
    tr = run_turn("call hist-restore-dir histrel=%s" % SUB_DIR)
    txt = tool_text(tr)
    if "是目录" not in txt:
        raise TestError("目录 path 应被拒绝（只支持单文件），实际=%r" % txt)
    if disk() != before:
        raise TestError("拒绝时应不改动文件：%r → %r" % (before, disk()))
    print("[H2] ④ restore{path=%s}（目录）→ 拒绝；回执=%r" % (SUB_DIR, txt.strip()[:120]), flush=True)


# ══════════════════════════════════════════════════════════
# H4 清空（history.clear）
# ══════════════════════════════════════════════════════════

def case_h4_clear():
    """写 prj `history.clear` = JSON `{ts, session}` → **只清目标会话**的链 + 其状态回写（I-136）。

    链路：`data-prj-config-save` → `data-prj-config-refresh` → `onPrjConfigRefresh`（case clearKey）→
    `clearChain(slug)`（`update-ref -d refs/chonkpilot/<slug>` + `writeback(ws, slug)`，`history.go`）。

    判据（I-136 闭环，**两个根会话各有链**）：clear 目标会话后
      · 目标会话 ref 消失、`history.timeline.<目标>` = []、`status.<目标>.checkpointCount` = 0；
      · **其它会话的链与其回写全部保留**（不再按仓库粒度清空全部 `refs/chonkpilot/*`）。
    """
    if not refs():
        raise TestError("前置：清空前应存在检查点链，实际 refs=%r" % refs())
    ref_self = CHAIN_PREFIX + SID
    if ref_self not in refs():
        raise TestError("前置：本套件根会话链不存在（H1 未通过？）；refs=%r" % refs())

    # 前置：**另起一个根会话**（SID-b）也建一条链 → 两条链并存（I-136 的判据前提）
    sid2 = SID + "-b"
    if CHAIN_PREFIX + sid2 in refs():
        raise TestError("前置：第二会话链不应已存在")
    run_turn("call hist-write histabs=%s histfrom=%s histval=%s"
             % (fwd(os.path.join(WS, HIST_FILE)), C1, "s2v"), sess=sid2)
    want_ref2 = CHAIN_PREFIX + sid2
    wait_until(lambda: want_ref2 in refs() or None, "第二会话链 refs/chonkpilot/<SID-b> 建立")
    if chain_count(want_ref2) < 1:
        raise TestError("第二会话链应为非空，实际=%d" % chain_count(want_ref2))
    n_self = chain_count(ref_self)

    # clear 目标 = 本套件根会话（**不是**第二会话）
    prj_save("history.clear", json.dumps({"ts": time.strftime("%Y-%m-%dT%H:%M:%S"), "session": SID}))

    # ① 目标会话 ref 消失、第二会话链保留
    deadline = time.time() + 30
    left = refs()
    while time.time() < deadline and ref_self in left:
        time.sleep(0.4)
        left = refs()
    if ref_self in left:
        raise TestError("history.clear 应清空**目标会话**的链，实际仍在：%r" % left)
    if want_ref2 not in left:
        raise TestError("其它会话的链不得被清（I-136）：refs=%r" % left)

    # ② 目标会话回写：timeline=[] / checkpointCount=0；第二会话回写不受影响
    tl_key, tl2_key = timeline_key(SID), timeline_key(sid2)
    deadline = time.time() + 20
    tl = prj_json(tl_key)
    while time.time() < deadline and tl != []:
        time.sleep(0.4)
        tl = prj_json(tl_key)
    if tl != []:
        raise TestError("清空后 history.timeline.<SID> 应为空数组，实际=%r" % tl)
    st = prj_json(status_key(SID)) or {}
    if int(st.get("checkpointCount") or 0) != 0:
        raise TestError("清空后 status.<SID>.checkpointCount 应=0，实际=%r" % st.get("checkpointCount"))
    tl2 = prj_json(tl2_key)
    if not isinstance(tl2, list) or not tl2:
        raise TestError("其它会话的 history.timeline.<SID-b> 应保留非空，实际=%r" % tl2)
    print("[H4] history.clear{session=%s} → 目标链清空（清前链长=%d）· timeline.<SID>=[] · "
          "status.<SID>.checkpointCount=0 · **第二会话链与其回写保留**（refs=%r，tl2=%d 步）"
          % (SID, n_self, left, len(tl2)), flush=True)


# ══════════════════════════════════════════════════════════
# H6 「涉及文件变动」（usr tool_async.touch_files）→ 前置钩子打点 / 放行
# ══════════════════════════════════════════════════════════

def ucfg():
    """usr 配置视图（data-user-config-load）。"""
    r = c.req("data-user-config-load", {}) or {}
    return (r.get("data") or {}) if isinstance(r, dict) else {}


def case_h6_touch_files_flag():
    """「涉及文件变动」= false → 前置打点钩子**直接放行、不打点**（只余轮末补点）；缺省 = 打点。

    机制（**同一轮内两次工具调用**，确定性、不依赖 fsnotify 去抖）：
      ① `history_restore` 执行时**同步置脏**（`restoreFile` → `ws.dirty=true`）；其自身前置钩子
         时脏位尚未置（= 不打点），且 self 非白名单工具缺省即「不涉及」；
      ② 紧随其后的 `filesys_run` 前置钩子读到脏位 → 该工具的 `touch_files` 决定是否打点：
         · 缺省（= 涉及）→ ②前置点（前置状态）+ 轮末补点（最终产像）→ 链 **+2**；
         · usr `tool_async.<工具>.touch_files=false` → 放行不打点 → 只余轮末补点 → 链 **+1**。

    并断言 usr 键**保存 → 回读**（新选项落库）。
    """
    fs = [n for n in tools() if n.endswith("filesys_run")]
    if not fs:
        raise TestError("工具面无 filesys_run（无法验证 touch_files 生效面）")
    tool = fs[0]
    ref = CHAIN_PREFIX + SID
    abs_hist = fwd(os.path.join(WS, HIST_FILE))

    # 前置：H4 已清空本会话链 → 直写盘面 + 驱动一轮（filesys_run RPL）重建链，
    # 并把盘面收敛到链头（`history_restore{to:-1}` 的一致性前提）
    if ref not in refs():
        write_disk("h6z\n")
        write_file("h6z", "h6a")
        wait_until(lambda: ref in refs() and head_blob(ref, HIST_FILE) == "h6a\n" or None,
                   "H6 前置：重建本会话链且链头 == 盘面")
    if head_blob(ref, HIST_FILE) != disk():
        raise TestError("H6 前置：链头内容应 == 盘面（%r vs %r）" % (head_blob(ref, HIST_FILE), disk()))

    # ── A 缺省（= 涉及）：前置点 + 轮末补点 → +2 ──
    cur = disk().strip()          # 当前盘面内容（不含换行）
    nxt = "h6b"
    n0 = chain_count(ref)
    run_turn("call hist-dirty-write histrel=%s histto=-1 histabs=%s histfrom=%s histval=%s"
             % (HIST_FILE, abs_hist, cur, nxt))
    if disk() != nxt + "\n":
        raise TestError("同轮两次工具调用未全部执行（盘面应 %s\\n，实际 %r）" % (nxt, disk()))
    n1 = chain_count(ref)
    if n1 - n0 != 2:
        raise TestError("缺省（涉及）应 +2（前置点 + 轮末补点），实际 +%d（n0=%d n1=%d）"
                        % (n1 - n0, n0, n1))
    print("[H6] 缺省（涉及）：history_restore 置脏 → filesys_run 前置点 + 轮末补点 → 链 +%d" % (n1 - n0),
          flush=True)

    # ── 保存 usr 新选项：tool_async.<工具>.touch_files=false → 回读 ──
    c.req("data-user-config-save",
          {"data": {"tool_async": json.dumps({tool: {"touch_files": False}})}})
    got = None
    deadline = time.time() + 8
    while time.time() < deadline:
        raw = ucfg().get("tool_async")
        if isinstance(raw, str):
            try:
                raw = json.loads(raw)
            except (TypeError, ValueError):
                raw = None
        if isinstance(raw, dict) and (raw.get(tool) or {}).get("touch_files") is False:
            got = raw
            break
        time.sleep(0.4)
    if got is None:
        raise TestError("usr tool_async.%s.touch_files=false 未落库/未回读：%r" % (tool, ucfg().get("tool_async")))

    # ── B 显式 false（= 不涉及）：前置钩子放行 → 只余轮末补点 → +1 ──
    cur2 = disk().strip()
    nxt2 = "h6c"
    n2 = chain_count(ref)
    run_turn("call hist-dirty-write histrel=%s histto=-1 histabs=%s histfrom=%s histval=%s"
             % (HIST_FILE, abs_hist, cur2, nxt2))
    if disk() != nxt2 + "\n":
        raise TestError("同轮两次工具调用未全部执行（盘面应 %s\\n，实际 %r）" % (nxt2, disk()))
    n3 = chain_count(ref)
    if n3 - n2 != 1:
        raise TestError("touch_files=false（不涉及）应只余轮末补点 +1，实际 +%d（n2=%d n3=%d）"
                        % (n3 - n2, n2, n3))
    if head_blob(ref, HIST_FILE) != nxt2 + "\n":
        raise TestError("轮末补点应保证链头 == 最终产像 %s\\n，实际 %r" % (nxt2, head_blob(ref, HIST_FILE)))
    print("[H6] touch_files=false（不涉及）：前置钩子放行不打点 → 只余轮末补点 → 链 +%d（链头 == 最终产像）"
          % (n3 - n2), flush=True)

    # 清理：删 usr `tool_async`（恢复缺省；本套件 usr 库隔离，此处仍显式回收）
    c.req("data-user-config-delete", {"id": "tool_async"})


# ══════════════════════════════════════════════════════════
# H5 非 git 仓库 → 工具摘除
# ══════════════════════════════════════════════════════════

def case_h5_non_git_repo_tools_removed():
    """workdir 不是 git 仓库（不存在 `.git`）→ 即使 `history.enabled=true`，工具仍被**摘除**。

    另起**独立实例**（独立 work-dir / data-dir / HOME），不复用主实例（`hasGit` 在 workState 创建时
    探测一次，事后删 `.git` 不会改判）——判据才真实。
    链路：`ensureWork` 探测 `.git` → `hasGit=false` → `anyActive()=false` → `syncTools()` 不注册
    （`history.go:442-458 / 584-618`）。
    """
    if MOCK is None:
        raise TestError("mock LLM 未就绪")
    ws2 = _h.tmp_dir("ck-hist-nogit-")
    dd2 = _h.tmp_dir("ck-hist-dd2-")
    home2 = _h.tmp_home()
    g2 = _h.start_gui(port=_h.free_port(), work_dir=ws2, data_dir=dd2, home=home2,
                      extra_args=("--llm-base=http://127.0.0.1:%d/v1" % MOCK.port,))
    try:
        c2 = g2.client
        if os.path.exists(os.path.join(ws2, ".git")):
            raise TestError("夹具不应是 git 仓库：%s" % os.path.join(ws2, ".git"))
        vcs = c2.req("gui.vcs.info", {}) or {}
        if vcs.get("git"):
            raise TestError("gui.vcs.info.git 应为 false（非 git 仓库），实际=%r" % vcs)

        def names2():
            r = c2.req("tools-list", {}) or {}
            return [t.get("name") or "" for t in (r.get("tools") or [])]

        base = names2()
        if not base:
            raise TestError("第二实例工具面为空（实例未就绪），无法判定工具摘除")
        if hist_tool_names(base):
            raise TestError("非 git 仓库不应注册 history 工具（基线），实际=%r"
                            % hist_tool_names(base))
        c2.req("data-prj-config-save", {"data": {"key": "history.enabled", "value": "true"}})
        time.sleep(4.0)   # 给「回读 → 收敛工具面」窗口
        now = names2()
        bad = hist_tool_names(now)
        if bad:
            raise TestError("非 git 仓库即使 history.enabled=true 也应摘除工具，实际=%r" % bad)
        print("[H5] 非 git 仓库实例：git=%r gitInstalled=%r · 工具面 %d 项无 history_*"
              "（enabled=true 后仍摘除）"
              % (vcs.get("git"), vcs.get("gitInstalled"), len(now)), flush=True)
    finally:
        g2.stop()


# ══════════════════════════════════════════════════════════
# main
# ══════════════════════════════════════════════════════════

def main():
    global MOCK, c, SID
    if shutil.which("git") is None:
        print("[SKIP] git 不可用（文件历史功能禁用，无判据可测）")
        return 0

    # ── 夹具：临时 work-dir（git init + 初始提交，均在临时目录内）──
    r = git(WS, "init", "-q")
    if r.returncode != 0:
        print("[SKIP] git init 失败：%s" % r.stderr.strip())
        return 0
    git(WS, "config", "user.email", "hist@chonkpilot.local")
    git(WS, "config", "user.name", "chonkpilot-hist")
    # 夹具仓库**不覆盖 core.autocrlf**（使用 git 默认配置，Git for Windows 默认 true）；
    # restore 一致性由 git 判定（`git diff --quiet <链头> -- <path>`，chain.go:547-570），
    # 行尾归一交给 git，不再自行比较原始字节。
    with open(os.path.join(WS, ".gitignore"), "w", encoding="utf-8") as f:
        f.write(".chonkpilot/\n")            # 插件临时 index 目录不入库（ensureGitignore 幂等前提）
    with open(os.path.join(WS, HIST_FILE), "w", encoding="utf-8") as f:
        f.write("v0\n")
    with open(os.path.join(WS, "README.md"), "w", encoding="utf-8") as f:
        f.write("history checkpoint fixture\n")
    os.makedirs(os.path.join(WS, SUB_DIR), exist_ok=True)
    with open(os.path.join(WS, SUB_DIR, "inner.txt"), "w", encoding="utf-8") as f:
        f.write("inner\n")
    git(WS, "add", "-A")
    r = git(WS, "commit", "-q", "-m", "init")
    if r.returncode != 0:
        print("[SKIP] 夹具初始提交失败：%s" % r.stderr.strip())
        return 0

    # ── 自起 mock LLM + GUI（动态端口 / 独立 data-dir / 独立 HOME）──
    MOCK = _h.start_mock_llm(_h.free_port())
    g = _h.start_gui(port=_h.free_port(), work_dir=WS, data_dir=DD, home=HOME,
                     extra_args=("--llm-base=http://127.0.0.1:%d/v1" % MOCK.port,))
    c = g.client
    c.wait_ready(120)
    c.console(clear=True)
    SID = "hist-%d" % int(time.time() * 1000)
    print("[env] ws=%s data=%s mock=%d gui=%d 根会话=%s（临时目录，结束即回收）"
          % (WS, DD, MOCK.port, g.port, SID), flush=True)

    cases = [
        ("H3 默认关闭 → 工具面不含 history_* 且真实改文件不产生检查点",
         case_h3_default_off_no_tools_no_chain),
        ("H1 检查点链生成 + 绝不碰用户分支（HEAD/日志/.git/index 逐字不变）+ status/timeline 回写",
         case_h1_chain_and_user_branch_untouched),
        ("H2 单文件恢复（内容回上一状态）+ 空 path/目录 拒绝批量",
         case_h2_restore_single_and_reject_batch),
        ("H4 history.clear{session} → 只清目标会话链（第二会话链保留）+ 会话级回写",
         case_h4_clear),
        ("H6 touch_files=false（不涉及）→ 前置钩子放行不打点（只余轮末补点）；缺省=打点",
         case_h6_touch_files_flag),
        ("H5 非 git 仓库 → 工具摘除（enabled=true 仍不注册）",
         case_h5_non_git_repo_tools_removed),
    ]
    ok = total = 0
    for name, fn in cases:
        total += 1
        ok += run_case(name, fn)
    print("\n文件历史检查点链断言：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
