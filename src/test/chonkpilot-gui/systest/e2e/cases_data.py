# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ④：配置 / 知识库 / 记忆库 / 插件面 / 主题 / 重启前置。

全部经既有消息面驱动（61-消息一览），不新增主题；写配置的用例都读回确认（不依赖「写成功」）。
"""

import json
import os
import time

import harness as _h

from .framework import case

LAYOUT_SIZES = [0.7, 0.3]


def _expand_tree(env, rel):
    """展开文件树目录（幂等，判据 = `.arrow svg.expanded`）。

    前序测试点可能把资源面板留在「记忆/扩展」模式 → 先切回项目树并等行挂载，避免 no-arrow。
    """
    env.js("window.mq.emit('filetree-mode-select', {mode:'project'}); 'ok'")
    sel = '.tree-row[data-path="%s"]' % env.posix(env.path(rel))
    js = ("(()=>{const r=document.querySelector(%s);"
          "return !!(r&&r.querySelector('.arrow svg.expanded'));})()" % json.dumps(sel))
    env.wait(lambda: True if env.js("!!document.querySelector(%s)" % json.dumps(sel)) is True
             else None, "项目树节点 %s 就绪" % rel, timeout=20)
    if env.js(js) is True:
        return
    r = "no-arrow"
    for _ in range(4):
        r = env.js("(()=>{const r=document.querySelector(%s);const a=r&&r.querySelector('.arrow');"
                   "if(!a)return 'no-arrow';a.click();return 'ok';})()" % json.dumps(sel))
        if r == "ok":
            break
        time.sleep(0.5)
    env.check("展开 %s" % rel, r == "ok", r)
    env.wait(lambda: True if env.js(js) is True else None, "目录已展开 %s" % rel, timeout=20)

PERSIST_KEY = "e2e.persist"


def _canon(tool):
    t = tool or ""
    return t[5:] if t.startswith("self_") else t


def _tools(env):
    raw = env.req("tools-list", {}) or {}
    tools = raw.get("tools") if isinstance(raw, dict) else None
    if isinstance(tools, str):
        try:
            tools = json.loads(tools)
        except Exception:
            tools = []
    return [t.get("name") for t in (tools or []) if isinstance(t, dict)]


def _prj_opened(env):
    raw = env.prj().get("opened-files")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except Exception:
            raw = []
    return [str(x) for x in (raw or [])]


def _open_file(env, rel, timeout=25):
    end = time.time() + timeout
    name = os.path.basename(rel)
    while time.time() < end:
        env.emit("file-open", {"path": env.posix(env.path(rel))})
        tabs = env.jsj("JSON.stringify([...document.querySelectorAll("
                       "'.code-view .tb-bar.tb-bottom .tb-tab .tb-name')]"
                       ".map(e=>e.textContent.trim()))") or []
        if name in tabs:
            return name
        time.sleep(0.5)
    raise AssertionError("打开 %s 未产生页签" % rel)


@case("S-CFG-02", "配置", "项目配置批量写：一次广播 ids 全组键 → 精确回读 → 单键删除",
      stage="seeded", order=100, fp="61 §3.1 data-prj-config-*（批量 entries）",
      desc="批量 entries 写两键 → 只收到 1 条 refresh（ids 含两组键）→ prj list 精确回读 → "
           "删一键后仅该键消失、另一键保留。")
def s_cfg_02_prj_batch(env):
    a, b = "e2e.probe.a", "e2e.probe.b"
    va, vb = "A-%s" % env.run_id, "B-%s" % env.run_id
    env.capture(["data-prj-config-refresh"])
    env.req("data-prj-config-save", {"data": {"entries": {a: va, b: vb}}})
    lst = env.wait(lambda: env.prj() if env.prj().get(a) == va and env.prj().get(b) == vb
                   else None, "批量写回读两键", timeout=15)
    env.eq("prj 键 a", lst.get(a), va)
    env.eq("prj 键 b", lst.get(b), vb)

    refs = env.events("data-prj-config-refresh")
    env.check("批量写只广播 1 条 refresh", len(refs) == 1, "n=%d" % len(refs))
    if refs:
        p = refs[0].get("payload") or {}
        ids = p.get("ids") or []
        env.check("refresh 带 ids 全组键", set(ids) == {a, b}, ids)

    env.req("data-prj-config-delete", {"id": a})
    env.wait(lambda: a not in env.prj(), "键 a 已删除", timeout=15)
    env.eq("键 b 保留", env.prj().get(b), vb)
    env.req("data-prj-config-delete", {"id": b})
    env.wait(lambda: b not in env.prj(), "键 b 已删除", timeout=15)


@case("S-CFG-03", "配置", "用户配置：refresh 后独立回读 + 删除回落系统默认",
      stage="seeded", order=110, fp="61 §3.1 data-user-config-* · 64 配置项",
      desc="usr `retryCount` 写哨兵 4 → config-refresh → 独立 load 仍 4 → 删除 → 回落默认 2。")
def s_cfg_03_user_fallback(env):
    base = env.usr().get("retryCount")
    env.check("写前基线 != 哨兵（避免默认值碰巧通过）", base != 4, "base=%r" % base)
    env.req("data-user-config-save", {"data": {"retryCount": 4}})
    env.wait(lambda: env.usr().get("retryCount") == 4, "usr retryCount == 4", timeout=15)
    env.emit("config-refresh")
    time.sleep(0.4)
    env.eq("refresh 后独立回读仍为 4", env.usr().get("retryCount"), 4)
    env.req("data-user-config-delete", {"id": "retryCount"})
    # 回落判据 = 回到**写前基线**（系统默认可能随版本变化，硬编码默认值会误判）
    got = env.wait(lambda: env.usr().get("retryCount") if env.usr().get("retryCount") == base
                   else None, "删除后回落写前基线 %r" % (base,), timeout=15)
    env.eq("回落值 == 写前基线", got, base)


@case("S-KB-01", "知识库", "原语管理回环：create → read → save → 磁盘/列表 → delete 闭合",
      stage="seeded", order=120, fp="61 §3.3 data-knowledge-*",
      desc="project 级根下 create tool 契约 → 磁盘含 `# 基本信息/# 摘要`；read 的 source == 磁盘；"
           "save 改 description 后读回/落盘一致；delete 后磁盘与知识库列表同时消失。"
           "（能力面暴露另记证据：当前构建契约型项目工具未进 tools-list，见 run_sem_kb_loop K3）")
def s_kb_01_primitive_loop(env):
    name = "e2e_kb_%s" % env.run_id
    root = (env.req("data-knowledge-root", {"kind": "project"}) or {}).get("root") or ""
    env.check("知识库 project 根非空", bool(root), root)
    tools_dir = os.path.join(root, "tools")
    tool_file = os.path.join(tools_dir, name + ".tool.md")
    os.makedirs(tools_dir, exist_ok=True)

    env.req("data-knowledge-create", {"dir": env.posix(tools_dir), "type": "tool", "name": name})
    env.wait(lambda: os.path.isfile(tool_file), "create 落盘", timeout=20)
    content = open(tool_file, encoding="utf-8").read()
    env.check("落盘含契约分区 # 基本信息", "# 基本信息" in content, content[:100])
    env.check("落盘含契约分区 # 摘要", "# 摘要" in content, content[:100])

    r = env.req("data-knowledge-read", {"path": env.posix(tool_file)}) or {}
    disk = open(tool_file, encoding="utf-8").read()
    env.eq("read.source == 磁盘", (r.get("source") or ""), disk)

    doc = dict(r.get("doc") or {})
    mark = "E2E-KB-DESC-%s" % env.run_id
    doc["description"] = mark
    doc["content"] = (doc.get("content") or "") + "\n" + mark
    env.req("data-knowledge-save", {"path": env.posix(tool_file), "doc": doc})
    r2 = env.req("data-knowledge-read", {"path": env.posix(tool_file)}) or {}
    env.eq("save 后 doc.description 读回", (r2.get("doc") or {}).get("description"), mark)
    env.check("save 后落盘含新描述", mark in open(tool_file, encoding="utf-8").read(), mark)
    env.note("能力面暴露（当前构建未出现，已知缺口：契约型项目工具未进 tools-list）",
             [n for n in _tools(env) if name in (n or "")] or "未出现")

    env.req("data-knowledge-delete", {"path": env.posix(tool_file)})
    env.wait(lambda: not os.path.exists(tool_file), "delete 后磁盘消失", timeout=15)
    lst = env.req("data-knowledge-list", {"dir": env.posix(tools_dir)}) or {}
    names = [f.get("name") for f in (lst.get("files") or [])]
    env.check("delete 后知识库列表不含该工具", name + ".tool.md" not in names, names)


@case("S-MEM-01", "记忆库", "项目记忆：save → read → 磁盘 → list → 编辑 → delete 闭合",
      stage="seeded", order=130, fp="61 §3.1 data-memory-*",
      desc="写自定义类别记忆 → read 内容一致、read.path 落盘字节一致、list 归属 project；"
           "再写新哨兵后 read/磁盘均更新；delete 后磁盘消失且 read 为空。")
def s_mem_01_memory_loop(env):
    cat = "e2e_mem_%s" % env.run_id
    m1 = "E2E-MEM-ONE-%s" % env.run_id
    m2 = "E2E-MEM-TWO-%s" % env.run_id
    env.capture(["data-memory-refresh"])
    env.req("data-memory-save", {"data": {"category": cat, "content": m1}})

    def mem_read():
        try:
            return (env.req("data-memory-read", {"data": {"category": cat}}) or {}).get("data") or {}
        except Exception:
            # 删除后读已删类别 → 数据面报 `unknown memory category`（视为「已为空」）
            return {}

    d = env.wait(lambda: mem_read() if mem_read().get("content") == m1 else None,
                 "记忆 read 回读 == 哨兵", timeout=15)
    env.eq("read.content", d.get("content"), m1)
    env.wait_events("data-memory-refresh", lambda p: True, "data-memory-refresh 广播", timeout=10)

    path = d.get("path") or ""
    if path and not os.path.isabs(path):
        path = env.path(path)
    env.check("read.path 指向真实文件", bool(path) and os.path.isfile(path), path)
    if path and os.path.isfile(path):
        with open(path, "rb") as f:
            env.eq("记忆落盘字节 == 哨兵", f.read().decode("utf-8").strip(), m1)

    lst = (env.req("data-memory-list", {}) or {}).get("list") or []
    row = next((x for x in lst if x.get("category") == cat), None)
    env.check("list 含该类别", row is not None, [x.get("category") for x in lst][:10])
    env.eq("记忆级别 project", (row or {}).get("level"), "project")

    env.req("data-memory-save", {"data": {"category": cat, "content": m2}})
    env.wait(lambda: mem_read().get("content") == m2, "编辑后 read == 新哨兵", timeout=15)
    if path and os.path.isfile(path):
        with open(path, "rb") as f:
            env.eq("编辑后落盘字节 == 新哨兵", f.read().decode("utf-8").strip(), m2)

    env.req("data-memory-delete", {"data": {"category": cat}})
    env.wait(lambda: not (mem_read().get("content") or "").strip(), "delete 后 read 为空", timeout=15)
    env.check("delete 后磁盘消失", not (path and os.path.isfile(path)), path)


@case("S-PLUG-01", "插件平台", "插件台账 running + 工具面注册齐全",
      stage="seeded", order=140, fp="61 §7.2 plugin-list · §4.5 tools-list",
      desc="plugin-list 中 file/task 均 status=running 且无 failed；"
           "tools-list 含 file_write/file_read/task_run/mcp_find。")
def s_plug_01_plugin_surface(env):
    statuses = _h.plugin_statuses(env.client)
    env.check("插件台账非空", bool(statuses), statuses)
    for pid in ("file", "task", "history"):
        env.eq("插件 %s running" % pid, statuses.get(pid), "running")
    bad = {k: v for k, v in statuses.items() if v in ("failed", "error", "failed-start")}
    env.check("无失败插件", not bad, bad)

    required = ("file_write", "file_read", "task_run", "mcp_find")
    names = _h.wait_tools(env.client, required, max_wait=120)
    canon = {_canon(n) for n in names}
    env.check("工具面含必需工具", all(r in canon for r in required), sorted(canon)[:20])


@case("S-THEME-01", "主题", "主题切换：落库 + 广播 + DOM data-theme 生效，并可还原",
      stage="seeded", order=150, fp="61 §1 gui.ui.save{ui} · 31 主题",
      desc="gui.ui.save{ui:{theme:dark}} → usr 回读 dark、广播 data-user-config-changed 携带 dark、"
           "documentElement[data-theme]=dark；随后还原 light 并确认生效。")
def s_theme_01_theme_roundtrip(env):
    env.capture(["data-user-config-changed"])
    env.req("gui.ui.save", {"ui": {"theme": "dark"}})
    env.wait(lambda: env.usr().get("theme") == "dark", "usr theme == dark", timeout=15)
    dom = env.wait(lambda: True if env.js(
        "document.documentElement.getAttribute('data-theme')") == "dark" else None,
        "DOM data-theme == dark", timeout=20)
    env.check("DOM 主题已生效", dom is True)

    evs = [e.get("payload") for e in env.events("data-user-config-changed")]
    env.check("配置变更广播到达", len(evs) >= 1, "n=%d" % len(evs))
    if evs:
        data = (evs[-1] or {}).get("data") or {}
        env.eq("广播携带 theme=dark", data.get("theme"), "dark")

    # 还原 light：usr 落库 + 广播 → 前端应用。广播是逐窗口异步投递，极端情况下
    # dark 的迟到投递会覆盖 light（既有 run_mw/useUserConfigSync 口径：事件驱动应用），
    # 因此按「落库已 light，DOM 仍未 light → 重发一次」收敛，最多 3 次。
    restored = False
    tries = 0
    for tries in range(1, 4):
        env.req("gui.ui.save", {"ui": {"theme": "light"}})
        env.wait(lambda: env.usr().get("theme") == "light", "usr theme 落库 light", timeout=10)
        try:
            env.wait(lambda: env.js("document.documentElement.getAttribute('data-theme')") == "light",
                     "主题还原 light（第 %d 次）" % tries, timeout=8)
            restored = True
            break
        except AssertionError:
            env.note("主题还原重试 #%d：DOM=%r"
                     % (tries, env.js("document.documentElement.getAttribute('data-theme')")))
    env.check("DOM 主题已还原 light", restored,
              "tries=%d dom=%r" % (tries, env.js(
                  "document.documentElement.getAttribute('data-theme')")))
    env.eq("usr theme 还原", env.usr().get("theme"), "light")


@case("S-PREP-01", "持久化前置", "重启前写入持久状态：布局占比 / 打开文件 / 项目配置 / 会话 / 主题",
      stage="seeded", order=900, fp="61 §1 gui.ui.save · §3.1 · §3.2",
      desc="为 restart 阶段准备确定性状态：layout.free 占比 0.7/0.3、打开 README.md + main.py、"
           "prj `e2e.persist` 哨兵、会话标题 + 活动位、主题 dark。")
def s_prep_01_persist_state(env):
    # 1) 布局占比（等价拖分割条 → saveLiveLayout）
    doc = env.jsj("JSON.stringify((window.__ckLayout&&window.__ckLayout.engine)?"
                  "window.__ckLayout.engine.state.config.live:null)")
    env.check("取到 layout.free 文档", isinstance(doc, dict) and isinstance(doc.get("layout"), dict), doc)
    body = doc["layout"]
    kids = body.get("children") or []
    if len(kids) == 2:
        body["sizes"] = list(LAYOUT_SIZES)
    else:
        for nd in kids:
            if (isinstance(nd, dict) and nd.get("type") == "split"
                    and nd.get("direction") == "row" and len(nd.get("children") or []) == 2):
                nd["sizes"] = list(LAYOUT_SIZES)
                break
    env.req("gui.ui.save", {"layout": {"free": json.dumps(doc, ensure_ascii=False)}})
    env.wait(lambda: isinstance(env.prj().get("layout.free"), str)
             and "[0.7, 0.3]" in env.prj().get("layout.free", "").replace("0.70", "0.7"),
             "layout.free 落库含 [0.7,0.3]", timeout=15)

    # 2) 打开文件集（供 restart 恢复页签）
    a = _open_file(env, "README.md")
    b = _open_file(env, "src/main.py")
    env.wait(lambda: all(any(n in p for p in _prj_opened(env)) for n in (a, b)),
             "opened-files 收敛为两者", timeout=25)
    env.check("opened-files 已落库", all(any(n in p for p in _prj_opened(env)) for n in (a, b)),
              _prj_opened(env))

    # 3) 项目配置哨兵（restart 回读）
    mark = "E2E-PERSIST-%s" % env.run_id
    env.req("data-prj-config-save", {"data": {"key": PERSIST_KEY, "value": mark}})
    env.wait(lambda: env.prj().get(PERSIST_KEY) == mark, "prj e2e.persist 落库", timeout=15)

    # 4) 会话（restart 回读列表 + 活动位）
    sid = "e2e-restart-sess-%s" % env.run_id
    title = "E2E 重启会话 %s" % env.run_id
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    env.req("data-session-title", {"data": {"id": sid, "title": title}})
    env.req("data-session-active-set", {"session_id": sid})
    env.wait(lambda: (env.req("data-session-active-get", {}) or {}).get("session_id") == sid,
             "活动会话已切换", timeout=15)
    env.shared.update(sid=sid, title=title, persist_mark=mark)

    # 5) 主题 dark（restart 回读 DOM/usr）
    env.req("gui.ui.save", {"ui": {"theme": "dark"}})
    env.wait(lambda: env.usr().get("theme") == "dark", "主题落库 dark", timeout=15)

    # 6) 窗口几何（宿主权威：gui.ui.save 只触发一次落库，值 = 真实普通窗口矩形）
    geom = env.jsj("JSON.stringify({w: Math.round(outerWidth*devicePixelRatio),"
                   " h: Math.round(outerHeight*devicePixelRatio)})") or {}
    env.req("gui.ui.save", {"window": {"width": int(geom.get("w") or 0),
                                       "height": int(geom.get("h") or 0),
                                       "maximized": False}})
    win = env.wait(lambda: env.prj() if env.prj().get("window.width") else None,
                   "window.* 几何落库", timeout=15)
    env.shared.update(win_w=int(float(win.get("window.width"))),
                      win_h=int(float(win.get("window.height"))))
    env.note("窗口几何前置", "w=%s h=%s" % (env.shared["win_w"], env.shared["win_h"]))

    # 7) 文件树展开状态（restart 恢复；快照为防抖写，留出落库窗口）
    _expand_tree(env, "docs")
    _expand_tree(env, "docs/design")
    time.sleep(2.0)
    env.shared.update(tree_open=["docs", "docs/design"])
    env.note("持久化前置完成", "sid=%s title=%s" % (sid, title))