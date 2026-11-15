# -*- coding: utf-8 -*-
"""`restart` 阶段测试点：**同一工作区第二次启动**，验证持久化（布局 / 页签 / 会话 / 配置 / 主题）。

前置状态由 `S-PREP-01` 在第一次启动内写入（见 cases_data.py）。
"""

import json

from .framework import case

PERSIST_KEY = "e2e.persist"


def _width(env, sel):
    return env.js("(document.querySelector(%s)?.getBoundingClientRect().width)||0" % json.dumps(sel)) or 0


def _tabs(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll("
                   "'.code-view .tb-bar.tb-bottom .tb-tab .tb-name')]"
                   ".map(e=>e.textContent.trim()))") or []


@case("R-LAYOUT-01", "布局持久化", "布局占比跨重启恢复：layout.free 0.7/0.3 → chat 面板宽度对应",
      stage="restart", order=10, fp="61 §1 gui.ui.save{layout} · 31 WIN-026",
      desc="重启后 prj `layout.free` 仍含 [0.7,0.3]；`.chat-panel` 实际渲染宽度落在 0.30 占比区间"
           "（200–460px，默认 0.4483 约 573px）。")
def r_layout_01_layout_restore(env):
    free = env.wait(lambda: env.prj().get("layout.free")
                    if isinstance(env.prj().get("layout.free"), str)
                    and "[0.7, 0.3]" in env.prj().get("layout.free", "").replace("0.70", "0.7")
                    else None, "重启后 layout.free 仍为 [0.7,0.3]", timeout=30)
    env.check("layout.free 回读一致", "[0.7, 0.3]" in free.replace("0.70", "0.7"), free[:160])

    w = env.wait(lambda: _width(env, ".chat-panel") if 200 <= _width(env, ".chat-panel") <= 460
                 else None, "chat 面板宽度对应 0.30 占比", timeout=30)
    env.check("chat 宽度在 0.30 占比区间", 200 <= w <= 460, "width=%.1f" % w)
    env.note("布局宽度", "chat=%.1f filetree=%.1f"
             % (w, _width(env, ".filetree-panel")))


@case("R-WIN-01", "窗口持久化", "窗口尺寸跨重启保持（宿主权威几何）",
      stage="restart", order=15, fp="61 §1 gui.ui.save{window} · 31 WIN-026",
      desc="重启后窗口非最大化；prj window.width/height 与首次启动落库值一致，"
           "实际 outer 几何（物理像素）与落库值一致（±30px）。")
def r_win_01_window_geometry(env):
    st = env.req("gui.window.status", {}) or {}
    env.check("重启后窗口非最大化（S-PREP 保存的是普通几何）",
              st.get("maximized") is False, st)
    w = int((env.shared or {}).get("win_w") or 0)
    h = int((env.shared or {}).get("win_h") or 0)
    env.check("前置窗口尺寸已记录", w > 0 and h > 0, (w, h))
    prj_w = int(float(env.prj().get("window.width") or 0))
    prj_h = int(float(env.prj().get("window.height") or 0))
    env.eq("prj window.width 跨重启一致", prj_w, w)
    env.eq("prj window.height 跨重启一致", prj_h, h)
    g = env.jsj("JSON.stringify({w: Math.round(outerWidth*devicePixelRatio),"
                " h: Math.round(outerHeight*devicePixelRatio)})") or {}
    env.check("实际窗口宽度 ≈ 落库值（±30）", abs(int(g.get("w") or 0) - w) <= 30,
              "real=%s prj=%s" % (g.get("w"), w))
    env.check("实际窗口高度 ≈ 落库值（±30）", abs(int(g.get("h") or 0) - h) <= 30,
              "real=%s prj=%s" % (g.get("h"), h))


@case("R-TABS-01", "页签持久化", "打开的文件页签跨重启恢复",
      stage="restart", order=20, fp="61 §3.1 opened-files · 33 页签",
      desc="重启后 `opened-files` 中的 README.md 与 main.py 重新出现在预览页签栏。")
def r_tabs_01_opened_files_restore(env):
    a, b = "README.md", "main.py"
    tabs = env.wait(lambda: _tabs(env) if a in _tabs(env) and b in _tabs(env) else None,
                    "重启后页签恢复为 README.md + main.py", timeout=45)
    env.check("README.md 页签已恢复", a in tabs, tabs)
    env.check("main.py 页签已恢复", b in tabs, tabs)

    raw = (env.prj().get("opened-files"))
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except Exception:
            raw = []
    entries = [str(x) for x in (raw or [])]
    env.check("persist 键 opened-files 仍含两文件",
              all(any(n in p for p in entries) for n in (a, b)), entries)


@case("R-FT-01", "文件树持久化", "文件树展开状态跨重启恢复（docs / docs/design）",
      stage="restart", order=25, fp="32 文件树 · filetree-expanded-key（run_filetree C6）",
      desc="首次启动内展开 docs 与 docs/design（S-PREP-01）→ 重启后两目录仍为展开态、"
           "docs/guide.md 与 docs/design/arch.md 节点可见。")
def r_ft_01_expanded_restore(env):
    def expanded(rel):
        return env.js("(()=>{const r=document.querySelector(%s);"
                      "return !!(r&&r.querySelector('.arrow svg.expanded'));})()"
                      % json.dumps('.tree-row[data-path="%s"]' % env.posix(env.path(rel)))) is True

    env.wait(lambda: True if expanded("docs") else None, "重启后 docs 仍展开", timeout=40)
    env.check("docs 展开状态恢复", expanded("docs"))
    env.wait(lambda: True if expanded("docs/design") else None,
             "重启后 docs/design 仍展开", timeout=20)
    env.check("docs/design 展开状态恢复", expanded("docs/design"))
    env.check("docs/guide.md 可见", env.js(
        "!!document.querySelector(%s)" % json.dumps(
            '.tree-row[data-path="%s"]' % env.posix(env.path("docs/guide.md")))) is True)
    env.check("docs/design/arch.md 可见", env.js(
        "!!document.querySelector(%s)" % json.dumps(
            '.tree-row[data-path="%s"]' % env.posix(env.path("docs/design/arch.md")))) is True)

@case("R-SESS-01", "会话持久化", "会话标题与活动会话跨重启存活",
      stage="restart", order=30, fp="61 §3.2 data-session-*",
      desc="重启后会话仍在 list、标题不变、data-session-active-get 仍指向该会话。")
def r_sess_01_session_survive(env):
    sid = (env.shared or {}).get("sid") or ""
    title = (env.shared or {}).get("title") or ""
    env.check("前置会话 id 存在（跨阶段共享）", bool(sid), env.shared)

    row = env.wait(lambda: next(
        (s for s in ((env.req("data-session-list", {}) or {}).get("list") or [])
         if s.get("session_id") == sid), None), "重启后会话仍在 list", timeout=30)
    env.check("会话标题保持不变", (row or {}).get("title") == title,
              "got=%r want=%r" % ((row or {}).get("title"), title))

    got = env.req("data-session-get", {"id": sid}) or {}
    env.eq("data-session-get 回读该会话", (got.get("data") or {}).get("session_id"), sid)
    active = env.wait(lambda: (env.req("data-session-active-get", {}) or {}).get("session_id"),
                      "活动会话回读", timeout=20)
    env.eq("活动会话仍是该会话", active, sid)


@case("R-CFG-01", "配置持久化", "项目配置跨重启回读",
      stage="restart", order=40, fp="61 §3.1 data-prj-config-list",
      desc="重启后 prj `e2e.persist` 仍等于重启前写入的哨兵值。")
def r_cfg_01_prj_persist(env):
    mark = (env.shared or {}).get("persist_mark") or ""
    env.check("前置哨兵存在", bool(mark), mark)
    val = env.wait(lambda: env.prj().get(PERSIST_KEY) if env.prj().get(PERSIST_KEY) == mark
                   else None, "重启后 prj 配置 == 哨兵", timeout=20)
    env.eq("prj 配置跨重启一致", val, mark)


@case("R-THEME-01", "主题持久化", "主题跨重启生效并可还原",
      stage="restart", order=50, fp="61 §1 gui.ui.save{ui} · 31 主题",
      desc="重启后 usr theme=dark 且 documentElement[data-theme]=dark；测试末尾还原 light。")
def r_theme_01_theme_persist(env):
    env.eq("usr theme 跨重启 == dark", env.usr().get("theme"), "dark")
    dom = env.wait(lambda: True if env.js(
        "document.documentElement.getAttribute('data-theme')") == "dark" else None,
        "重启后 DOM 主题 dark", timeout=25)
    env.check("DOM 主题跨重启生效", dom is True)
    env.req("gui.ui.save", {"ui": {"theme": "light"}})
    env.wait(lambda: env.js("document.documentElement.getAttribute('data-theme')") == "light",
             "主题还原 light", timeout=20)

@case("R-MQ-SCOPE-01", "消息持久化", "workdir 级事件按 workdir 收口在重启后仍成立（策略无内存态）",
      stage="restart", order=60, fp="49 §2.22.7 S2 · run_mq_scope ④",
      desc="重启后开两个对话窗：work_dir=本 ws 的 filesys.changed 到达主窗与对话窗；work_dir=别处"
           "的同类事件均收不到（同轮正控制），证明分发策略跨重启一致。")
def r_mq_scope_01(env):
    import os
    import tempfile
    import time
    from .cases_mq_scope import _client, _capture, _close_all, _open_two, _probes, _pub

    other = tempfile.mkdtemp(prefix="e2e-mqscope-r-")
    try:
        w1, w2 = _open_two(env)
        cm, c1 = env.client, _client(env, w1)
        _capture(cm, ["filesys.changed"])
        _capture(c1, ["filesys.changed"])
        tag = "r-%d" % int(time.time() * 1000)
        ctrl, neg = tag + "-ctrl", tag + "-neg"
        _pub(_client(env, w2), "filesys.changed",
             {"instance_id": "", "work_dir": env.workspace, "probe": ctrl,
              "path": env.posix(env.path("r-scope.txt")), "operation": "write"})
        _pub(_client(env, w2), "filesys.changed",
             {"instance_id": "", "work_dir": other, "probe": neg,
              "path": os.path.join(other, "x.txt").replace("\\", "/"), "operation": "write"})
        env.check("重启后同 workdir 事件到达主窗",
                  ctrl in env.wait(lambda: (lambda v: v if ctrl in v else None)(
                      _probes(cm, "filesys.changed")), "重启后主窗 probe", timeout=20), ctrl)
        env.check("重启后同 workdir 事件到达对话窗",
                  ctrl in env.wait(lambda: (lambda v: v if ctrl in v else None)(
                      _probes(c1, "filesys.changed")), "重启后对话窗 probe", timeout=20), ctrl)
        time.sleep(1.5)
        env.check("重启后 work_dir 指向别处仍不投递",
                  neg not in _probes(cm, "filesys.changed")
                  and neg not in _probes(c1, "filesys.changed"))
    finally:
        try:
            _close_all(env)
        except Exception:
            pass
        try:
            os.rmdir(other)
        except OSError:
            pass
