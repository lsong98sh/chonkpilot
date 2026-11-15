# -*- coding: utf-8 -*-
"""`empty` 阶段测试点：**空工程**（项目里没有任何业务文件）。

顺序敏感：本阶段在预置工程之前执行，因此这里新建的夹具必须自清理或不影响后续阶段。
"""

import base64
import json
import os

from .framework import case


def _root_labels(env):
    """文件树根级直接子节点显示名（DOM 顺序）。"""
    parent = env.posix(env.workspace)
    js = ("JSON.stringify([...document.querySelectorAll('.filetree-panel .tree-row')].filter(r=>{"
          "const p=r.getAttribute('data-path')||'';const i=p.lastIndexOf('/');"
          "return i>=0&&p.slice(0,i)===%s}).map(r=>{"
          "const n=r.querySelector('.node-label');return n?n.textContent.trim():''}))" % json.dumps(parent))
    return env.jsj(js) or []


@case("E-FS-01", "空工程", "空工程启动初始化：工作区回显 + 文件树空态",
      stage="empty", order=10, fp="61 §1 gui.init-data · 32 文件树",
      desc="GUI 首启（未预置业务文件）→ 顶栏回显工作区路径、文件树无可业务条目、隐藏目录不泄漏。")
def e_fs_01_empty_project(env):
    title = env.wait(
        lambda: env.js("(document.querySelector('.header-path')?.getAttribute('title')||'').trim()"),
        "顶栏工作区路径回显", timeout=20)
    env.check("顶栏标题 == 工作区绝对路径",
              env.posix(title).rstrip("/") == env.posix(env.workspace).rstrip("/"),
              "title=%r ws=%r" % (title, env.workspace))
    env.check("文件树面板存在", bool(env.js("!!document.querySelector('.filetree-panel')")))

    labels = env.wait(
        lambda: True if _root_labels(env) == [] and env.js("!!document.querySelector('.filetree-panel')")
        else None,
        "空工程根级无业务条目", timeout=10)
    env.check("空工程根级无业务文件", labels is True and _root_labels(env) == [],
              "labels=%r" % (_root_labels(env),))
    all_paths = env.jsj("JSON.stringify([...document.querySelectorAll('.filetree-panel .tree-row')]"
                        ".map(r=>r.getAttribute('data-path')||''))") or []
    leaked = [p for p in all_paths if ".chonkpilot" in p or "/.ide" in p]
    env.check("隐藏目录（.chonkpilot/.ide）不出现在树中", not leaked, "leaked=%r" % leaked)
    env.note("空工程基线", "workDir=%s 树条目=%d" % (env.workspace, len(all_paths)))


@case("E-TOOL-01", "LLM 工具", "LLM 工具链路：file_write 建文件并自动创建父目录",
      stage="empty", order=20, fp="61 §5.1 tools/call · file_write 契约",
      desc="空工程中经 llm-start → mock LLM 决策 → file_write（createDirs=true）落盘 → "
           "磁盘字节 + filesys.content 读回一致。")
def e_tool_01_file_write_in_empty(env):
    import harness as _h
    _h.wait_plugins_running(env.client, ("file",), max_wait=180)
    _h.wait_tools(env.client, ("file_write", "file_read"), max_wait=120)

    mark = "E2E-EMPTY-TOOL-%s" % env.run_id
    rel = "e2e_probe/hello.txt"
    targets = [{"path": env.posix(env.path(rel)), "content": mark + "\n",
                "createDirs": True, "overwrite": False}]
    b64 = base64.b64encode(json.dumps(targets, ensure_ascii=False).encode("utf-8")).decode("ascii")
    env.run_turn("【T-E2E-EMPTYFILE】e2e-seed e2eplan=%s" % b64, timeout=240)

    p = env.path(rel)
    env.wait(lambda: os.path.isfile(p), "file_write 落盘（含自动建父目录）", timeout=20)
    env.eq("磁盘字节 == 写入内容", env.read_text(rel).strip(), mark)
    r = env.req("filesys.content", {"work_dir": env.workspace, "path": p}) or {}
    env.eq("filesys.content 读回一致", (r.get("content") or "").strip(), mark)

    lst = env.req("filesys.list", {"work_dir": env.workspace, "path": env.workspace}) or {}
    node = next((n for n in (lst.get("children") or []) if n.get("name") == "e2e_probe"), None)
    env.check("filesys.list 标出自动创建的目录", node and node.get("is_dir") is True, node)

    # 自清理：本阶段后续（及预置）不需要该探针
    env.emit("filesys.remove", {"work_dir": env.workspace, "path": env.path("e2e_probe")})
    env.wait(lambda: not os.path.exists(env.path("e2e_probe")), "探针目录已清理", timeout=15)


@case("E-FS-02", "文件面", "文件操作回环：mkdir/create/rename/copy/remove 与树/列表三处一致",
      stage="empty", order=30, fp="61 §2 filesys.*",
      desc="空工程内经 filesys 消息面建目录与文件 → 磁盘/树/list 三处一致；改名/复制/删除闭合。")
def e_fs_02_filesys_loop(env):
    d = "e2e_fs_dir_%s" % env.run_id
    f = "e2e_fs_file_%s.txt" % env.run_id
    renamed = "e2e_fs_renamed_%s.txt" % env.run_id
    copied = "e2e_fs_copy_%s.txt" % env.run_id
    mark = "E2E-FS2-MARK-%s" % env.run_id

    # 1) 建目录
    env.emit("filesys.mkdir", {"work_dir": env.workspace, "dir": env.workspace, "name": d})
    env.wait(lambda: os.path.isdir(env.path(d)), "mkdir 落盘", timeout=15)
    env.wait(lambda: env.tree_count(d), "树节点出现（目录）", timeout=20)
    lst = env.req("filesys.list", {"work_dir": env.workspace, "path": env.workspace}) or {}
    node = next((n for n in (lst.get("children") or []) if n.get("name") == d), None)
    env.check("filesys.list 目录 is_dir=true", node and node.get("is_dir") is True, node)

    # 2) 建文件（父目录外，根级）
    env.emit("filesys.create", {"work_dir": env.workspace, "dir": env.workspace,
                                "name": f, "content": mark})
    env.wait(lambda: os.path.isfile(env.path(f)), "create 落盘", timeout=15)
    env.eq("磁盘字节 == 写入内容", env.read_text(f), mark)
    env.wait(lambda: env.tree_count(f), "树节点出现（文件）", timeout=20)
    r = env.req("filesys.content", {"work_dir": env.workspace, "path": env.path(f)}) or {}
    env.eq("filesys.content 读回一致", r.get("content"), mark)

    # 3) 改名
    env.emit("filesys.rename", {"work_dir": env.workspace, "path": env.path(f),
                                "new_name": renamed})
    env.wait(lambda: os.path.isfile(env.path(renamed)) and not os.path.exists(env.path(f)),
             "rename 磁盘态（新在/旧无）", timeout=15)
    env.wait(lambda: env.tree_count(renamed) and env.tree_count(f, present=False),
             "树随改名更新（新在/旧无）", timeout=20)
    env.eq("改名后内容保持", env.read_text(renamed), mark)

    # 4) 复制
    env.emit("filesys.copy", {"work_dir": env.workspace, "path": env.path(renamed),
                              "new_name": copied})
    env.wait(lambda: os.path.isfile(env.path(copied)), "copy 落盘", timeout=15)
    env.eq("复制内容保持", env.read_text(copied), mark)
    env.wait(lambda: env.tree_count(copied), "树节点出现（副本）", timeout=20)

    # 5) 删除闭合
    for name in (d, renamed, copied):
        env.emit("filesys.remove", {"work_dir": env.workspace, "path": env.path(name)})
    env.wait(lambda: not any(os.path.exists(env.path(n)) for n in (d, renamed, copied)),
             "remove 磁盘消失", timeout=20)
    for name in (d, renamed, copied):
        env.wait(lambda n=name: env.tree_count(n, present=False), "树节点消失 %s" % name,
                 timeout=15)
    names = [n.get("name") for n in
             ((env.req("filesys.list", {"work_dir": env.workspace, "path": env.workspace}) or {})
              .get("children") or [])]
    env.check("filesys.list 不含已删条目",
              not any(n in names for n in (d, renamed, copied)), names)


@case("E-CFG-01", "配置", "用户配置写→读→删回环（独立 HOME）",
      stage="empty", order=40, fp="61 §3.1 data-user-config-*",
      desc="写 usr `responseTimeout` 哨兵 → 回读一致 → 删除 → 回落系统默认（非哨兵）。")
def e_cfg_01_user_config_loop(env):
    old = env.usr().get("responseTimeout")
    env.check("写前旧值 != 哨兵（避免默认值碰巧通过）", old != 37, "old=%r" % old)
    env.req("data-user-config-save", {"data": {"responseTimeout": 37}})
    env.wait(lambda: env.usr().get("responseTimeout") == 37, "usr 配置回读 == 37", timeout=15)
    env.req("data-user-config-delete", {"id": "responseTimeout"})
    env.wait(lambda: env.usr().get("responseTimeout") != 37, "删除后回落（!= 37）", timeout=15)
    env.note("配置回落值", env.usr().get("responseTimeout"))


@case("E-SESS-01", "会话", "会话生命周期：建/列表/改名广播/活动位/删除闭合",
      stage="empty", order=50, fp="61 §3.2 data-session-*",
      desc="空工程内建会话 → list/get 一致 → 改名广播 title-changed → active-set/get → 删除后 list 不含。")
def e_sess_01_session_lifecycle(env):
    sid = "e2e-sess-%s" % env.run_id
    title = "E2E 会话 %s" % env.run_id
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})

    def in_list():
        lst = (env.req("data-session-list", {}) or {}).get("list") or []
        return next((s for s in lst if s.get("session_id") == sid), None)

    env.wait(in_list, "会话出现在 list", timeout=20)
    got = env.req("data-session-get", {"id": sid}) or {}
    env.check("data-session-get 回读该会话", (got.get("data") or {}).get("session_id") == sid, got)

    env.capture(["data-session-title-changed"])
    env.req("data-session-title", {"data": {"id": sid, "title": title}})
    ev = env.wait_events("data-session-title-changed",
                         lambda p: p.get("session_id") == sid and p.get("title") == title,
                         "改名广播 data-session-title-changed", timeout=15)
    env.check("改名广播携带新标题", (ev or {}).get("title") == title, ev)
    env.wait(lambda: (in_list() or {}).get("title") == title, "list 标题 == 新标题", timeout=15)

    env.req("data-session-active-set", {"session_id": sid})
    env.wait(lambda: (env.req("data-session-active-get", {}) or {}).get("session_id") == sid,
             "活动会话 == 本会话", timeout=15)

    env.req("data-session-delete", {"id": sid})
    env.wait(lambda: in_list() is None, "删除后 list 不含该会话", timeout=20)
    g2 = None
    try:
        g2 = env.req("data-session-get", {"id": sid}) or {}
        gone = (g2.get("data") is None)
    except Exception:
        gone = True
    env.check("删除后 get 返回空/报错（不再自举重建）", gone, "get=%r" % (g2,))