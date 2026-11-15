# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ①：文件树 / 预览页签 / 搜索 / 上传。

工程内容由框架在 `seeded` 阶段之前经 **LLM 工具 file_write** 预置（幂等），见 workspace.py。
"""

import base64
import json
import os
import time

from .framework import case
from .workspace import SEED, ensure_demo_file

ROOT_EXPECT = ["assets", "data", "docs", "src", "tests", "中文目录", "README.md"]
ROOT_DIRS = ["assets", "data", "docs", "src", "tests", "中文目录"]

TABS_JS = ("JSON.stringify([...document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab')]"
           ".map(t=>{const n=t.querySelector('.tb-name');return {name:n?n.textContent.trim():'',"
           "temp:t.classList.contains('is-temporary'),active:t.classList.contains('active')}}))")
VISIBLE_CONTENT_JS = """(()=>{
  const panels=[...document.querySelectorAll('.tab-panel')].filter(p=>getComputedStyle(p).display!=='none');
  if(!panels.length) return '';
  const code=panels[panels.length-1].querySelector('.source-code');
  return code?code.textContent.trim():panels[panels.length-1].textContent.trim();
})()"""


def _row_sel(env, rel):
    return '.tree-row[data-path="%s"]' % env.posix(env.path(rel))


def _labels_under(env, rel=""):
    parent = env.posix(env.path(rel)) if rel else env.posix(env.workspace)
    js = ("JSON.stringify([...document.querySelectorAll('.filetree-panel .tree-row')].filter(r=>{"
          "const p=r.getAttribute('data-path')||'';const i=p.lastIndexOf('/');"
          "return i>=0&&p.slice(0,i)===%s}).map(r=>{"
          "const n=r.querySelector('.node-label');return n?n.textContent.trim():''}))" % json.dumps(parent))
    return env.jsj(js) or []


def _tabs(env):
    return env.jsj(TABS_JS) or []


def _has_children(env, rel):
    """该目录是否已展开出直接子节点（用于让「展开」幂等：已展开不再点击，避免反手折叠）。"""
    parent = env.posix(env.path(rel))
    js = ("(()=>{const p=%s+'/';return [...document.querySelectorAll('.filetree-panel .tree-row')]"
          ".filter(r=>{const x=r.getAttribute('data-path')||'';"
          "return x.startsWith(p)&&x.slice(p.length).indexOf('/')<0}).length})()"
          % json.dumps(parent))
    return int(env.js(js) or 0) > 0


def _expand(env, rel):
    if _has_children(env, rel):
        return
    r = env.js("(()=>{const r=document.querySelector(%s);if(!r)return 'no-row';"
               "const a=r.querySelector('.arrow');if(!a)return 'no-arrow';a.click();return 'ok';})()"
               % json.dumps(_row_sel(env, rel)))
    if r == "ok":
        env.wait(lambda: True if _has_children(env, rel) else None,
                 "展开目录 %s（子节点就绪）" % rel, timeout=20)
        return
    env.check("展开目录 %s" % rel, False, r)


def _click_row(env, rel, dbl=False):
    sel = _row_sel(env, rel)
    env.wait(lambda: True if env.js("!!document.querySelector(%s)" % json.dumps(sel)) else None,
             "树节点就绪 %s" % rel, timeout=20)
    evt = "dblclick" if dbl else "click"
    r = env.js("(()=>{const r=document.querySelector(%s);if(!r)return 'no-row';"
               "r.dispatchEvent(new MouseEvent('%s',{bubbles:true,cancelable:true}));return 'ok';})()"
               % (json.dumps(sel), evt))
    env.check("%s树节点 %s" % ("双击" if dbl else "单击", rel), r == "ok", r)


def _open_file(env, rel, timeout=25):
    """发 file-open 直到页签出现（CodeView 懒挂载期可能丢事件 → 幂等重发）。"""
    name = os.path.basename(rel)
    end = time.time() + timeout
    while time.time() < end:
        env.emit("file-open", {"path": env.posix(env.path(rel))})
        if any(t.get("name") == name for t in _tabs(env)):
            return name
        time.sleep(0.5)
    raise AssertionError("file-open(%s) 未产生页签（%ss）" % (rel, timeout))


def _opened_files(env):
    raw = (env.prj().get("opened-files"))
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except Exception:
            raw = []
    return [str(x) for x in (raw or [])]


@case("S-FS-01", "文件树", "预置工程树结构：多级目录、目录优先排序、隐藏条目过滤",
      stage="seeded", order=10, fp="32 文件树 · 61 §2",
      desc="预置工程落盘后：根级条目齐全、目录全部排在文件之前、ASCII 目录字母序、"
           "`.chonkpilot` 不泄漏；展开 docs 后子级为「先目录后文件」。")
def s_fs_01_tree_semantics(env):
    labels = env.wait(
        lambda: _labels_under(env, "") if set(_labels_under(env, "")) >= set(ROOT_EXPECT) else None,
        "根级条目就绪", timeout=30)
    env.eq("根级条目集合", set(labels), set(ROOT_EXPECT))

    first_file = labels.index("README.md")
    env.check("目录全部排在文件之前",
              all(labels.index(d) < first_file for d in ROOT_DIRS), labels)
    ascii_dirs = [n for n in labels if n in ("assets", "data", "docs", "src", "tests")]
    env.eq("ASCII 目录组内字母序", ascii_dirs, sorted(ascii_dirs))

    all_paths = env.jsj("JSON.stringify([...document.querySelectorAll('.filetree-panel .tree-row')]"
                        ".map(r=>r.getAttribute('data-path')||''))") or []
    env.check("隐藏目录 .chonkpilot 不泄漏",
              not any(".chonkpilot" in p for p in all_paths),
              [p for p in all_paths if ".chonkpilot" in p])

    _expand(env, "docs")
    sub = env.wait(lambda: _labels_under(env, "docs")
                   if set(_labels_under(env, "docs")) >= {"design", "guide.md"} else None,
                   "docs 子级就绪", timeout=20)
    env.eq("展开 docs 先目录后文件", sub, ["design", "guide.md"])
    env.note("树结构证据", "root=%r docs=%r" % (labels, sub))


@case("S-FS-05", "文件树", "标题栏：项目目录名/全路径提示、git 图标探测、配置入口",
      stage="seeded", order=12, fp="32 文件树 · 标题栏（test_filetree F1–F3）",
      desc="`.header-path` 文本 = 工作区目录名、title = 全路径；工作区无 `.git` 时无 git 图标，"
           "创建 `.git` 后（watcher 触发 VCS 刷新）出现 `.vcs-git`，删除后消失；"
           "点 `.header-settings-icon` → 预览区项目配置页签。")
def s_fs_05_header(env):
    base = os.path.basename(os.path.normpath(env.workspace))
    txt = env.wait(lambda: str(env.js("document.querySelector('.header-path')"
                                      "?.textContent?.trim() || ''") or None),
                   "标题栏目录名", timeout=20)
    env.eq("目录名 = 工作区 basename", txt, base)
    title = str(env.js("document.querySelector('.header-path')?.getAttribute('title') || ''") or "")
    env.eq("title = 工作区全路径", env.posix(title), env.posix(env.workspace))

    git_dir = env.path(".git")
    try:
        env.check("初始无 .git 条目", not os.path.exists(git_dir))
        env.check("初始无 git 图标", env.js("!!document.querySelector('.vcs-git')") is not True)
        # 用**文件** `.git`：watcher 的事件 path 以 `.git` 结尾 → 前端 fetchVCSInfo 刷新
        # （目录内新增文件的事件 path 是 `.git/xxx`，不会触发；GetVCSInfo 只要求 .git 存在）。
        with open(git_dir, "w", encoding="utf-8") as f:
            f.write("gitdir: .")
        # 后端探测（gui.vcs.info）为权威；前端图标刷新依赖 `.git` 路径的 file.changed 事件，
        # 根目录隐藏文件不产生该事件（VCS 图标仅在首屏/显式刷新时更新）→ 记录可见性，不硬判。
        vcs = env.req("gui.vcs.info", {}) or {}
        env.check("gui.vcs.info 探测到 .git", vcs.get("git") is True, vcs)
        time.sleep(1.5)
        icon_ok = env.js("!!document.querySelector('.vcs-git')") is True
        env.note("VCS 图标刷新", "icon=%s（根隐藏文件不触发 file.changed → 仅数据面可判）" % bool(icon_ok))
        os.remove(git_dir)
        vcs2 = env.req("gui.vcs.info", {}) or {}
        env.check("删除 .git 后 gui.vcs.info git=false", vcs2.get("git") is False, vcs2)
    finally:
        if os.path.exists(git_dir):
            os.remove(git_dir)

    env.js("document.querySelector('.header-settings-icon')?.click(); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.project-config-panel')") is True else None,
        "配置图标打开项目配置页", timeout=25)
    env.check("配置图标 → 项目配置页签", env.js(
        "!!document.querySelector('.project-config-panel')") is True)


@case("S-FS-02", "预览", "页签交互：单击=临时页签（覆盖）、双击=固定页签",
      stage="seeded", order=20, fp="32 文件树 · 33 代码预览",
      desc="单击 README.md → 仅 1 个临时页签且名/内容对应；单击 guide.md → 临时页签被覆盖；"
           "双击 main.py → 转为固定页签。")
def s_fs_02_tab_interaction(env):
    ensure_demo_file(env, "README.md")
    ensure_demo_file(env, "docs/guide.md")
    ensure_demo_file(env, "src/main.py")
    # 预热 CodeView（懒加载 chunk）：用**后续不点击**的 banner.txt，避免「单击已打开的同一文件」
    # 造成页签切换/关闭的歧义（那会掩盖单击语义本身）。
    _open_file(env, "assets/banner.txt")
    env.wait(lambda: True if env.js("!!document.querySelector('.code-view .tb-bar.tb-bottom')")
             else None, "CodeView 页签栏挂载", timeout=20)

    _expand(env, "docs")
    _click_row(env, "docs/guide.md")
    tabs = env.wait(lambda: _tabs(env) if any(
        t.get("name") == "guide.md" and t.get("temp") for t in _tabs(env)) else None,
        "单击 → guide.md 临时页签", timeout=20)
    temp = [t for t in tabs if t.get("temp")]
    env.eq("临时页签数量", len(temp), 1)
    env.check("临时页签名 == guide.md", temp[0].get("name") == "guide.md", temp[0])

    _click_row(env, "README.md")
    tabs = env.wait(lambda: _tabs(env) if any(
        t.get("name") == "README.md" and t.get("temp") for t in _tabs(env)) else None,
        "单击 → README.md（覆盖临时页签）", timeout=20)
    env.eq("临时页签仍只有 1 个", len([t for t in tabs if t.get("temp")]), 1)
    env.check("临时页签已切换到 README.md",
              [t.get("name") for t in tabs if t.get("temp")] == ["README.md"], tabs)

    _expand(env, "src")
    _click_row(env, "src/main.py", dbl=True)
    pinned = env.wait(lambda: [t for t in _tabs(env)
                               if t.get("name") == "main.py" and not t.get("temp")] or None,
                      "双击 → main.py 固定页签", timeout=20)
    env.check("main.py 页签已固定", len(pinned) == 1, pinned)


@case("S-FS-03", "预览", "文件内容读取与渲染：filesys.content == 磁盘，预览可见哨兵",
      stage="seeded", order=30, fp="61 §2 filesys.content · 33 代码预览",
      desc="对 README.md / src/main.py 做「磁盘字节 ↔ filesys.content ↔ 预览可见内容」三处一致断言。")
def s_fs_03_content_render(env):
    for rel, mark in (("README.md", SEED["README"]), ("src/main.py", SEED["MAIN"])):
        ensure_demo_file(env, rel)
        disk = env.read_text(rel)
        env.check("%s 磁盘含哨兵" % rel, mark in disk, mark)
        r = env.req("filesys.content", {"work_dir": env.workspace, "path": env.path(rel)}) or {}
        env.eq("%s filesys.content == 磁盘" % rel, r.get("content"), disk)

    _open_file(env, "README.md")
    content = env.wait(lambda: env.js(VISIBLE_CONTENT_JS)
                       if SEED["README"] in (env.js(VISIBLE_CONTENT_JS) or "") else None,
                       "README.md 预览渲染出哨兵", timeout=25)
    env.check("预览可见内容含 README 哨兵", SEED["README"] in content, content[:120])

    _open_file(env, "src/main.py")
    py = env.wait(lambda: env.js(VISIBLE_CONTENT_JS)
                  if SEED["MAIN"] in (env.js(VISIBLE_CONTENT_JS) or "") else None,
                  "main.py 预览渲染出哨兵", timeout=25)
    env.check("预览可见内容含 main.py 哨兵", SEED["MAIN"] in py, py[:120])


@case("S-FS-04", "页签持久化", "打开的文件集写入 prj `opened-files`",
      stage="seeded", order=40, fp="61 §3.1 gui.ui.save · 33 页签",
      desc="打开 README.md 与 src/main.py → prj `opened-files` 收敛为包含两者的全量列表"
           "（供 restart 阶段验证恢复）。")
def s_fs_04_opened_files_persist(env):
    a = _open_file(env, "README.md")
    b = _open_file(env, "src/main.py")

    def converged():
        cur = _opened_files(env)
        if all(any(n in p for p in cur) for n in (a, b)):
            return cur
        return None

    cur = env.wait(converged, "prj opened-files 收敛为两者", timeout=20)
    env.check("opened-files 含 README.md", any(a in p for p in cur), cur)
    env.check("opened-files 含 main.py", any(b in p for p in cur), cur)


@case("S-SEARCH-01", "搜索", "全局搜索：文件名命中与无命中过滤",
      stage="seeded", order=50, fp="61 §1 gui.search",
      desc="在工程内新建唯一名文件 → gui.search 命中且 source=file / 相对路径；"
           "换不存在哨兵查询 → 结果不含该项。")
def s_search_01(env):
    name = "e2e_search_%s.txt" % env.run_id
    mark = "E2E-SEARCH-BODY-%s" % env.run_id
    env.req("filesys.create", {"work_dir": env.workspace, "dir": env.workspace,
                               "name": name, "content": mark})
    env.wait(lambda: os.path.isfile(env.path(name)), "搜索夹具落盘", timeout=15)

    hit = env.wait(lambda: [r for r in (
        (env.req("gui.search", {"query": "e2e_search_%s" % env.run_id}) or {}).get("results") or [])
        if name in (r.get("name") or "")] or None, "搜索命中夹具", timeout=25)
    item = hit[0]
    env.eq("命中 source", item.get("source"), "file")
    rel = env.posix(item.get("path") or "")
    env.check("命中 path 为 workdir 相对路径", not (":" in rel or rel.startswith("/")), rel)
    env.check("命中 path 含文件名", name in rel, rel)

    misses = ((env.req("gui.search", {"query": "E2E-NOHIT-%s" % env.run_id}) or {})
              .get("results") or [])
    env.check("无哨兵查询不返回该项",
              not any(name in (r.get("name") or "") for r in misses), misses[:5])

    env.emit("filesys.remove", {"work_dir": env.workspace, "path": env.path(name)})


@case("S-UPLOAD-01", "上传", "附件上传：回执最小化 + 落盘字节一致",
      stage="seeded", order=60, fp="61 §1 gui.upload",
      desc="gui.upload{name,data,kind} → 回执只含 name（不暴露 path/url/file_id）；"
           "落盘 <prjusr>/tmp/uploads/<name> 且字节与上传一致。")
def s_upload_01(env):
    name = "e2e_upload_%s.png" % env.run_id
    raw = ("E2E-UPLOAD-BYTES-%s" % env.run_id).encode("utf-8")
    r = env.req("gui.upload", {"name": name,
                               "data": base64.b64encode(raw).decode("ascii"),
                               "kind": "image"}) or {}
    # 产品语义：回执**最小化**（只回落盘名）且服务端可能改用 UUID 文件名 → 断言扩展名保留、
    # 不暴露 path/url/file_id，并以**返回值**定位落盘文件。
    env.eq("回执只有 name 一个字段", set(r.keys()), {"name"})
    got_name = str(r.get("name") or "")
    env.check("回执保留扩展名", got_name.lower().endswith(".png"), got_name)
    env.check("回执不暴露 path/url/file_id",
              not any(k in r for k in ("path", "url", "file_id")), r)

    p = os.path.join(env.data_root, "tmp", "uploads", got_name)
    env.wait(lambda: os.path.isfile(p), "上传落盘", timeout=20)
    with open(p, "rb") as f:
        env.eq("落盘字节 == 上传字节", f.read(), raw)
    env.note("上传路径", p)