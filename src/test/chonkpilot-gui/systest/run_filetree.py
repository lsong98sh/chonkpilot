# -*- coding: utf-8 -*-
"""testplan.md 一、目录变化监听（filetree）结合测试。

驱动：mq 事件（可用则 /eval window.mq.emit）或 DOM 点击（代码配合项未落地时退化，
见 testplan 附 4：/click 直接操作 DOM 箭头）。
断言：/exists /text 查 DOM + /console 查前端错误。
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

WS = r"E:\BizWorks\chonkpilot\src\test\chonkpilot-gui\systest\ws"
WS_POSIX = WS.replace("\\", "/")
IDE_EXE = r"e:\BizWorks\chonkpilot\dist\desktop\chonkpilot.exe"
# GUI 窗口标题 = work_dir 名（main.go: Title: filepath.Base(workDir)），非 "Chonk Pilot"。
WIN_TITLE = os.path.basename(WS)
import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345, work_dir=WS)  # 复用优先；无实例则自起并在结束时回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：filetree-*/opened-files/window.* 退出前回滚


def tree_sel(rel):
    """根节点 data-path 为绝对路径（正斜杠）。rel 统一转正斜杠（Windows os.path.join 产生反斜杠）。"""
    rel = rel.replace("\\", "/")
    return f'.tree-row[data-path="{WS_POSIX}/{rel}"]'


def p(rel):
    return os.path.join(WS, rel)


def wait_tree(rel, present=True, max_wait=15):
    """轮询等待树节点出现/消失。"""
    sel = tree_sel(rel)
    deadline = time.time() + max_wait
    while time.time() < deadline:
        info = c.exists(sel)
        if present and info.get("count", 0) > 0:
            return info
        if not present and info.get("count", 0) == 0:
            return info
        time.sleep(0.4)
    raise TestError(f"等待树节点 {'出现' if present else '消失'} 超时: {sel}")


def dir_expanded(rel_path):
    """目录是否已展开。"""
    return c.exists(tree_sel(rel_path) + " .arrow svg.expanded").get("count", 0) > 0


def expand_dir(rel_path):
    """确保目录展开（mq filesys.watch 驱动，幂等 + 重试；DOM 点击兜底）。"""
    abs_path = (WS_POSIX + "/" + rel_path.replace("\\", "/")).rstrip("/")
    for i in range(4):
        if dir_expanded(rel_path):
            return
        time.sleep(0.5)  # 静置，避免命中渲染中的旧 DOM
        c.mq_emit("filesys.watch", {"work_dir": WS, "path": abs_path})
        time.sleep(3.0)  # 长等待 loadDirChildren + 事件消化
        if not dir_expanded(rel_path) and i >= 1:
            # mq 事件未生效（事件未注册等）→ DOM 点击兜底
            c.click(tree_sel(rel_path) + " .arrow", 5000)
            time.sleep(2.0)
    raise TestError(f"展开目录失败: {rel_path}")


def collapse_dir(rel_path):
    """确保目录折叠（mq filesys.unwatch 驱动，幂等 + 重试；DOM 点击兜底）。"""
    abs_path = (WS_POSIX + "/" + rel_path.replace("\\", "/")).rstrip("/")
    for i in range(3):
        if not dir_expanded(rel_path):
            return
        c.mq_emit("filesys.unwatch", {"work_dir": WS, "path": abs_path})
        time.sleep(0.8)
        if dir_expanded(rel_path) and i >= 1:
            c.click(tree_sel(rel_path) + " .arrow", 5000)
            time.sleep(0.8)
    raise TestError(f"折叠目录失败: {rel_path}")


def reset_ws():
    """重建工作目录初始状态。"""
    import shutil
    shutil.rmtree(WS, ignore_errors=True)
    os.makedirs(WS)
    dirs = ["a", os.path.join("a", "b"), os.path.join("a", "b", "c"), os.path.join("a", "b", "c", "d")]
    for d in dirs:
        os.makedirs(p(d), exist_ok=True)
        for i in range(1, 4):
            with open(p(os.path.join(d, f"f{i}.txt")), "w", encoding="utf-8") as f:
                f.write(f"file {d} f{i} content\n")
    with open(p("a.txt"), "w", encoding="utf-8") as f:
        f.write("hello from a.txt\nline2\n")
    with open(p("b.txt"), "w", encoding="utf-8") as f:
        f.write("hello from b.txt\n")


def case_init():
    """一.0 初始化：根目录结构与隐藏目录过滤。"""
    wait_tree("a.txt")
    wait_tree("b.txt")
    wait_tree("a")
    # 隐藏目录 .chonkpilot 被过滤
    info = c.exists(tree_sel(".chonkpilot"))
    if info.get("count", 0) != 0:
        raise TestError("隐藏目录 .chonkpilot 未被过滤")


def case_root_add_rename_delete():
    """一.1 根目录添加 / 改名 / 删除文件。"""
    # 新增
    with open(p("new.txt"), "w", encoding="utf-8") as f:
        f.write("new\n")
    wait_tree("new.txt")
    # 改名
    os.rename(p("new.txt"), p("renamed.txt"))
    wait_tree("renamed.txt", present=True)
    wait_tree("new.txt", present=False)
    # 删除
    os.remove(p("renamed.txt"))
    wait_tree("renamed.txt", present=False)


def case_expanded_dir_ops():
    """一.2 展开子根目录后的文件操作（展开状态不变）。"""
    expand_dir("a")
    wait_tree("a/f1.txt")
    with open(p(os.path.join("a", "a_new.txt")), "w", encoding="utf-8") as f:
        f.write("new in a\n")
    wait_tree("a/a_new.txt")
    # 展开状态保持（a 的子节点仍可见）
    wait_tree("a/f1.txt")


def case_collapsed_no_change():
    """一.3 折叠目录下操作不触发 change 消息。"""
    collapse_dir("a")
    time.sleep(0.5)
    with open(p(os.path.join("a", "hidden_add.txt")), "w", encoding="utf-8") as f:
        f.write("x\n")
    time.sleep(1.5)
    # a 未展开：子节点不可见（树无变化）
    info = c.exists(tree_sel("a/hidden_add.txt"))
    if info.get("count", 0) != 0:
        raise TestError("折叠目录下操作不应触发子节点出现")


def case_reexpand_shows_latest():
    """一.4 重新展开折叠的目录（显示最新内容）。"""
    expand_dir("a")
    wait_tree("a/hidden_add.txt")
    # 清理
    os.remove(p(os.path.join("a", "hidden_add.txt")))


def case_subdir_expand_state_keep():
    """一.5 收起一级目录后，子级展开状态保持。"""
    time.sleep(3)  # 消化前序用例的文件事件，等待 DOM 静止
    # 确保 a 处于折叠状态（前序用例可能已展开）
    if c.exists(tree_sel("a/f1.txt")).get("count", 0) > 0:
        collapse_dir("a")
    expand_dir("a")
    time.sleep(1.2)  # 等待 a 的 children 完全渲染（避免时序竞态）
    wait_tree("a/b")
    expand_dir(os.path.join("a", "b"))
    wait_tree("a/b/c")
    expand_dir(os.path.join("a", "b", "c"))
    wait_tree("a/b/c/d")
    expand_dir(os.path.join("a", "b", "c", "d"))
    wait_tree("a/b/c/d/f1.txt")
    # 折叠一级 a
    collapse_dir("a")
    wait_tree("a/f1.txt", present=False)
    # 重新展开 a → b/c/d 保持展开（无需逐级再点）
    expand_dir("a")
    wait_tree("a/b/c/d/f1.txt")
    # 收起 a 恢复初始
    collapse_dir("a")


def case_collapsed_subdir_stops_watch():
    """一.6 收起一级目录后，子级文件操作不触发 change。

    折叠 a（file-collapse → 后端 UnwatchDir(p, true) 递归停止 a/b/c/d 监听）；
    期间在 a/b/c/d 下增改文件，不应收到 file-dir-contents。
    """
    # 确保四级展开
    expand_dir("a")
    expand_dir(os.path.join("a", "b"))
    expand_dir(os.path.join("a", "b", "c"))
    expand_dir(os.path.join("a", "b", "c", "d"))
    wait_tree(os.path.join("a", "b", "c", "d", "f1.txt"))
    # 折叠一级 a（递归停止子目录监听）
    collapse_dir("a")
    wait_tree("a/f1.txt", present=False)
    time.sleep(1.5)  # 等待 UnwatchDir 生效
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["file-dir-contents"])
    # 子级操作：新增 + 追加（折叠期间修改，供一.7 验证）
    with open(p(os.path.join("a", "b", "c", "d", "sub_add.txt")), "w", encoding="utf-8") as f:
        f.write("x\n")
    with open(p(os.path.join("a", "b", "c", "d", "f1.txt")), "a", encoding="utf-8") as f:
        f.write("extra\n")
    time.sleep(2.0)
    evs = c.events_of("file-dir-contents", clear=True)
    if len(evs) > 0:
        raise TestError(f"折叠 a 后子级操作仍触发 file-dir-contents {len(evs)} 次: {evs[:3]}")
    # 保留 sub_add.txt（折叠期间新建文件），供一.7 验证「子级为折叠前内存旧数据」


def case_expand_refreshes_own_children():
    """一.7 展开一级目录后，子级内容状态确认（缺陷修复后：递归刷新已展开子级）。

    折叠期间在 a 直接子级新建文件 → 展开 a 后该文件出现（a 自身 children 刷新）；
    b/c/d 保持折叠前内存展开状态（一.5），且**内容同步刷新**：折叠期间在
    a/b/c/d 下新建的 sub_add.txt 在展开 a 后出现（修复一.7 缺陷）。
    """
    # 前置：a 已折叠（一.6 末尾），折叠期间 a 直接子级新建 a_direct_new.txt、
    # 深层新建 sub_add.txt（保留）+ f1.txt 追加 extra
    with open(p(os.path.join("a", "a_direct_new.txt")), "w", encoding="utf-8") as f:
        f.write("direct new\n")
    # 展开 a：a 自身 children 刷新 + 已展开子孙递归刷新
    expand_dir("a")
    wait_tree("a/a_direct_new.txt")
    wait_tree(os.path.join("a", "b", "c", "d"))
    # 缺陷修复断言：折叠期间子级新建文件在展开一级后出现（递归刷新已展开子级）
    wait_tree(os.path.join("a", "b", "c", "d", "sub_add.txt"), max_wait=10)
    # 清理 + 还原深层 f1.txt
    os.remove(p(os.path.join("a", "a_direct_new.txt")))
    os.remove(p(os.path.join("a", "b", "c", "d", "sub_add.txt")))
    with open(p(os.path.join("a", "b", "c", "d", "f1.txt")), "w", encoding="utf-8") as f:
        f.write(f"file {os.path.join('a', 'b', 'c', 'd')} f1 content\n")
    collapse_dir("a")


def _gen_preview_files():
    """生成 8 种类型预览文件（a/ 下），返回相对路径列表。"""
    import base64
    import zipfile
    files = []
    # png（1x1）
    png = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
    )
    with open(p(os.path.join("a", "pic.png")), "wb") as f:
        f.write(png)
    files.append(os.path.join("a", "pic.png"))
    # pdf（最小单页）
    pdf = (b"%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"
           b"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n"
           b"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\n"
           b"xref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \n0000000115 00000 n \n"
           b"trailer<</Size 4/Root 1 0 R>>\nstartxref\n190\n%%EOF\n")
    with open(p(os.path.join("a", "test.pdf")), "wb") as f:
        f.write(pdf)
    files.append(os.path.join("a", "test.pdf"))

    def office(path, ctype, rels, part, body):
        with zipfile.ZipFile(path, "w") as z:
            z.writestr("[Content_Types].xml", ctype)
            z.writestr("_rels/.rels", rels)
            z.writestr(part, body)

    # docx / xlsx / pptx 最小结构（renderType 判定只看扩展名，无需内容有效）
    office(p(os.path.join("a", "test.docx")),
           '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
           '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
           '<Default Extension="xml" ContentType="application/xml"/>'
           '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>',
           '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
           '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>',
           "word/document.xml",
           '<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
           '<w:body><w:p><w:r><w:t>Hello</w:t></w:r></w:p></w:body></w:document>')
    files.append(os.path.join("a", "test.docx"))
    office(p(os.path.join("a", "test.xlsx")),
           '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
           '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
           '<Default Extension="xml" ContentType="application/xml"/>'
           '<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>',
           '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
           '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>',
           "xl/workbook.xml",
           '<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">'
           '<sheets><sheet name="S1" sheetId="1"/></sheets></workbook>')
    files.append(os.path.join("a", "test.xlsx"))
    office(p(os.path.join("a", "test.pptx")),
           '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
           '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
           '<Default Extension="xml" ContentType="application/xml"/>'
           '<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/></Types>',
           '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
           '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/></Relationships>',
           "ppt/presentation.xml",
           '<?xml version="1.0"?><p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst/></p:presentation>')
    files.append(os.path.join("a", "test.pptx"))
    # java / go / py
    for name, body in [("Test.java", "public class Test {}\n"),
                       ("test.go", "package main\n"),
                       ("test.py", "print('hi')\n")]:
        with open(p(os.path.join("a", name)), "w", encoding="utf-8") as f:
            f.write(body)
        files.append(os.path.join("a", name))
    return files


def _wait_render_type(expect, rel, max_wait=10):
    deadline = time.time() + max_wait
    tag = ""
    while time.time() < deadline:
        # 多 tab：取当前激活 tab（v-show 可见）的 file-type-tag
        tag = c.eval(
            "(function(){"
            "  var p = [...document.querySelectorAll('.tab-panel')]"
            "      .find(p => getComputedStyle(p).display !== 'none');"
            "  return p ? (p.querySelector('.file-type-tag')?.textContent || '') : '';"
            "})()"
        )
        if isinstance(tag, str) and expect in tag:
            return
        time.sleep(0.5)
    raise TestError(f"打开 {rel} 后 renderType 未达到 {expect!r}（实际 {tag!r}）")


def case_file_type_preview():
    """一.8 8 种文件类型预览（渲染落点断言：file-type-tag renderType）。"""
    files = _gen_preview_files()
    expect = {
        "a/pic.png": "image",
        "a/test.pdf": "pdf",
        "a/test.docx": "docx",
        "a/test.xlsx": "xlsx",
        "a/test.pptx": "pptx",
        "a/Test.java": "code",
        "a/test.go": "code",
        "a/test.py": "code",
    }
    for rel, rtype in expect.items():
        abs_path = (WS_POSIX + "/" + rel.replace("\\", "/")).rstrip("/")
        c.mq_emit("file-open", {"path": abs_path})
        _wait_render_type(rtype, rel)
        time.sleep(0.3)
    # 清理生成文件
    for rel in files:
        if os.path.exists(p(rel)):
            os.remove(p(rel))
    time.sleep(1.5)  # 等待 watcher 消化删除事件


def case_new_subdir_auto_watch():
    """C1 新创建的子目录自动被监听。"""
    time.sleep(2)  # 等待 DOM 静止
    if c.exists(tree_sel("a/f1.txt")).get("count", 0) > 0:
        collapse_dir("a")
    expand_dir("a")
    wait_tree("a/f1.txt")
    os.makedirs(p(os.path.join("a", "subnew")), exist_ok=True)
    wait_tree("a/subnew")
    time.sleep(1.0)  # 等待 watcher 对新目录完成 Add
    with open(p(os.path.join("a", "subnew", "inner.txt")), "w", encoding="utf-8") as f:
        f.write("inner\n")
    # inner.txt 在 subnew 内，需展开 subnew 才能断言显示
    expand_dir(os.path.join("a", "subnew"))
    wait_tree("a/subnew/inner.txt")
    # 清理
    import shutil
    shutil.rmtree(p(os.path.join("a", "subnew")))


def case_hidden_temp_filter():
    """C2 隐藏/临时文件过滤。"""
    expand_dir("a")
    for name in [".hidden", "~$tmp.docx", "x-wal", "y~"]:
        with open(p(os.path.join("a", name)), "w", encoding="utf-8") as f:
            f.write("tmp\n")
    time.sleep(1.5)
    for name in [".hidden", "~$tmp.docx", "x-wal", "y~"]:
        info = c.exists(tree_sel(f"a/{name}"))
        if info.get("count", 0) != 0:
            raise TestError(f"隐藏/临时文件 {name} 未被过滤")
    # 清理
    for name in [".hidden", "~$tmp.docx", "x-wal", "y~"]:
        os.remove(p(os.path.join("a", name)))


def case_write_refresh():
    """C3 Write 事件推送 filesys.changed：打开文件 → 外部修改 → CodeView 自动刷新。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["filesys.changed"])
    # 打开 a.txt（CodeView）；path 须用正斜杠（与 watcher file-changed 的 toSlash 一致）
    c.mq_emit("file-open", {"path": p("a.txt").replace("\\", "/")})
    time.sleep(1.5)
    txt = c.eval('document.body.innerText')
    if "hello from a.txt" not in txt:
        raise TestError("打开 a.txt 后 CodeView 未显示内容")
    # 外部修改文件
    with open(p("a.txt"), "a", encoding="utf-8") as f:
        f.write("\nline3 modified\n")
    ev = c.wait_events("filesys.changed", n=1, max_wait=15, clear=True)
    if not ev:
        raise TestError("外部修改后未收到 filesys.changed")
    # CodeView 内容刷新
    deadline = time.time() + 10
    while time.time() < deadline:
        txt = c.eval('document.body.innerText')
        if "line3 modified" in txt:
            # 还原 a.txt（删除追加行）
            with open(p("a.txt"), "w", encoding="utf-8") as f:
                f.write("hello from a.txt\nline2\n")
            return
        time.sleep(1)
    raise TestError("CodeView 未自动刷新（line3 modified 未见）")


def case_batch_merge():
    """C4 60ms 合并去重：200ms 内连续添加 10 个文件 → 合并刷新（file-dir-contents 推送远小于 10）。"""
    c.eval('if(!window.__chonkEvents) window.__chonkEvents={events:[],map:{}};')
    c.mq_on_capture(["file-dir-contents"])
    time.sleep(1.5)  # DOM / watcher 静止
    for i in range(10):
        with open(p(f"batch_{i}.txt"), "w", encoding="utf-8") as f:
            f.write(f"batch {i}\n")
    time.sleep(2.0)
    evs = c.events_of("file-dir-contents", clear=True)
    # 60ms 批量窗口合并去重：推送次数应显著小于添加文件数（10），不逐文件推送
    if len(evs) >= 10:
        raise TestError(f"批量添加 10 文件推送 file-dir-contents {len(evs)} 次，预期合并去重（<10）")
    if len(evs) > 4:
        print(f"    [dbg] 60ms 批量合并推送 {len(evs)} 次（10 文件，事件分批到达）")
    # 树中最终可见全部文件（合并结果正确）
    wait_tree("batch_9.txt")
    # 清理
    for i in range(10):
        if os.path.exists(p(f"batch_{i}.txt")):
            os.remove(p(f"batch_{i}.txt"))


def case_root_dir_add_rebuild():
    """C5 根目录变更整树重建：根目录新增目录 → 整树重建且展开状态恢复。"""
    time.sleep(1.5)
    expand_dir("a")
    wait_tree("a/f1.txt")
    # 根目录新增目录
    os.makedirs(p("rootnew"), exist_ok=True)
    wait_tree("rootnew")
    # 展开状态恢复（a 仍展开）
    wait_tree("a/f1.txt")
    # 清理
    os.rmdir(p("rootnew"))
    wait_tree("rootnew", present=False)


def _llm_create_session():
    """生成 LLM 会话 id（mq-only；新架构无 window.go.CreateSession，llm-start 幂等落库）。"""
    sid = "ft-sess-%d" % int(time.time() * 1000)
    c.mq_emit("session-changed", {"session_id": sid})
    time.sleep(0.3)
    return sid


def _wait_ide_down(max_wait=60):
    """等待 IDE 退出（test server 连接失败）。"""
    deadline = time.time() + max_wait
    while time.time() < deadline:
        try:
            c.ping()
        except Exception:
            return
        time.sleep(1)
    raise TestError("IDE 未在预期时间内退出（Alt+F4 未生效）")


def _close_ide_winapi():
    """WM_CLOSE 兜底：FindWindowW + PostMessage（等价 Alt+F4 的关闭路径，不受前台聚焦限制）。

    窗口标题 = work_dir 名（main.go: Title: filepath.Base(workDir)），非 "Chonk Pilot"。
    """
    import ctypes
    u = ctypes.windll.user32
    hwnd = u.FindWindowW(None, WIN_TITLE)
    if hwnd:
        u.PostMessageW(hwnd, 0x0010, 0, 0)  # WM_CLOSE
        return True
    return False


def case_persistence_restart():
    """C6 filetree 状态持久化：展开 a/b/c/d → 模拟 Alt+F4 关闭 → 脚本重启 → 展开状态恢复。"""
    import subprocess
    # 1. 展开目录链（触发 filetree-data 快照写入 config 桶）
    expand_dir("a")
    expand_dir(os.path.join("a", "b"))
    expand_dir(os.path.join("a", "b", "c"))
    expand_dir(os.path.join("a", "b", "c", "d"))
    wait_tree(os.path.join("a", "b", "c", "d", "f1.txt"))
    time.sleep(3)  # 快照落库
    # 2. mock 驱动 Alt+F4 关闭（key_press 带 window 参数自动聚焦；window = work_dir 名）
    sid = _llm_create_session()
    c.mq_emit("llm-start", {"session_id": sid, "q": "please call close",
                            "llm": "mock", "think": "", "effort": "", "scenario_id": 0})
    # 3. 等待 IDE 退出；Alt+F4 受 Windows 前台聚焦限制可能未生效 → WM_CLOSE 兜底
    try:
        _wait_ide_down(45)
    except TestError:
        _close_ide_winapi()
        _wait_ide_down(30)
    # 3. 脚本重启 IDE（同一 workdir + test-port）；经 harness 自起 → 结束即回收
    # 夹具：预置工程规格文件 → 抑制场景向导启动自动弹出（本实例走 popen_own，不经 harness.start_gui）
    _h.ensure_project_spec(WS)
    _h.popen_own([IDE_EXE, "--test-port=2345", "--work-dir=" + WS],
                 name="gui:filetree-restart", cwd=os.path.dirname(IDE_EXE))
    c.wait_ready(60)
    # 4. 断言展开状态恢复（快照递归保存展开节点）
    wait_tree(os.path.join("a", "b", "c", "d", "f1.txt"), max_wait=30)
    for rel in ["a", os.path.join("a", "b"), os.path.join("a", "b", "c"), os.path.join("a", "b", "c", "d")]:
        if not dir_expanded(rel):
            raise TestError(f"重启后目录未恢复展开: {rel}")
    # 收尾：折叠回初始态
    collapse_dir(os.path.join("a", "b", "c", "d"))
    collapse_dir(os.path.join("a", "b", "c"))
    collapse_dir(os.path.join("a", "b"))
    collapse_dir("a")


def clean_residue():
    """清理测试残留文件（保留初始结构），并等待 watcher 事件消化。"""
    import shutil
    for name in ["a_new.txt", os.path.join("a", "a_new.txt"), "hidden_add.txt",
                 "new.txt", "renamed.txt", "tmp_add.txt", "capture_add.txt", "capture_del.txt"]:
        f = p(name)
        if os.path.exists(f):
            os.remove(f)
    for i in range(10):
        f = p(f"batch_{i}.txt")
        if os.path.exists(f):
            os.remove(f)
    rd = p("rootnew")
    if os.path.isdir(rd):
        os.rmdir(rd)
    sub = p(os.path.join("a", "subnew"))
    if os.path.isdir(sub):
        shutil.rmtree(sub)
    # 还原 a.txt（C3 可能追加过）
    a = p("a.txt")
    if os.path.exists(a):
        with open(a, "r", encoding="utf-8") as f:
            if "line3 modified" in f.read():
                with open(a, "w", encoding="utf-8") as f:
                    f.write("hello from a.txt\nline2\n")
    time.sleep(3)  # 让残留 file-dir-contents 事件被前端消化


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    clean_residue()
    # 不 reset 工作目录：reset 会删除重建目录，破坏 fsnotify 根目录监听句柄。
    # 初始状态由 IDE 启动前的 ws 准备脚本保证。
    wait_tree("a.txt", present=True)
    wait_tree("new.txt", present=False)
    total += 1; ok += run_case("一.0 初始化", case_init)
    total += 1; ok += run_case("一.1 根目录 添加/改名/删除", case_root_add_rename_delete)
    total += 1; ok += run_case("一.2 展开目录后文件操作", case_expanded_dir_ops)
    total += 1; ok += run_case("一.3 折叠目录下操作不触发 change", case_collapsed_no_change)
    total += 1; ok += run_case("一.4 重新展开显示最新", case_reexpand_shows_latest)
    total += 1; ok += run_case("一.5 子级展开状态保持", case_subdir_expand_state_keep)
    total += 1; ok += run_case("一.6 收起一级目录后子级操作不触发 change", case_collapsed_subdir_stops_watch)
    total += 1; ok += run_case("一.7 展开一级目录子级内容状态确认", case_expand_refreshes_own_children)
    total += 1; ok += run_case("一.8 8 种文件类型预览", case_file_type_preview)
    total += 1; ok += run_case("C1 新子目录自动监听", case_new_subdir_auto_watch)
    total += 1; ok += run_case("C2 隐藏/临时文件过滤", case_hidden_temp_filter)
    total += 1; ok += run_case("C3 Write 事件刷新（filesys.changed）", case_write_refresh)
    total += 1; ok += run_case("C4 批量添加 60ms 合并去重", case_batch_merge)
    total += 1; ok += run_case("C5 根目录变更整树重建", case_root_dir_add_rebuild)
    total += 1; ok += run_case("C6 展开状态持久化（Alt+F4 关闭 + 重启恢复）", case_persistence_restart)
    errs = c.console()
    if errs.get("entries"):
        for e in errs["entries"]:
            if e.get("level") in ("error",):
                print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\nfiletree 结合测试：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
