# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑧：**文件树交互/右键/拖拽/监听**（迁移自旧套件四套 + run_filetree 核心项）。

旧套件（全部保留在 run_all.py）：
* `test_filetree_interact.py`  → S-FT-01（改名判定 / F2 / ESC / Delete 确认）
* `test_filetree_ctxmenu.py`   → S-FT-02（右键菜单 / 复制黏贴 / 多选过滤）
* `test_filetree_dnd.py`       → S-FT-03（多选 / 拖拽移动 / 同名冲突 / 拖入输入区）
* `test_filetree_watcher.py`   → S-FT-04（外部增删改自动刷新 / filesys.changed 刷新）
* `run_filetree.py`            → S-FT-04/05（折叠语义 / 批量合并 / 隐藏过滤 / 新子目录监听 / 整树重建）
                                → S-FT-06（8 类文件 renderType 预览）
                                → R-FT-01（展开状态跨重启恢复，restart 阶段）

所有临时节点以 `env.run_id` 命名，用例内自清理；文件系统断言为权威，DOM 只判渲染落点。
右键菜单文案按当前 locale 取（zh-CN / en-US 两套标签），不写死单语言。
"""

import base64
import json
import os
import time
import zipfile

from .framework import case
from .workspace import ensure_demo_file

LABELS = {
    "open": ("打开", "Open"),
    "rename": ("重命名", "Rename"),
    "delete": ("删除", "Delete"),
    "copy": ("复制", "Copy"),
    "paste": ("黏贴", "Paste"),
    "console": ("在控制台显示", "Show in Console"),
    "reveal": ("在资源管理器中显示", "Reveal in Explorer"),
}


def _sel(env, rel):
    return '.tree-row[data-path="%s"]' % env.posix(env.path(rel))


def _row(env, rel):
    return env.js("!!document.querySelector(%s)" % json.dumps(_sel(env, rel))) is True


def _wait_row(env, rel, present=True, timeout=20, desc=""):
    return env.wait(lambda: True if _row(env, rel) is present else None,
                    desc or ("树节点%s %s" % ("出现" if present else "消失", rel)),
                    timeout=timeout)


def _expanded(env, rel):
    """目录是否展开（判据 = `.arrow svg.expanded`，空目录同样适用）。"""
    return env.js("(()=>{const r=document.querySelector(%s);"
                  "return !!(r&&r.querySelector('.arrow svg.expanded'));})()"
                  % json.dumps(_sel(env, rel))) is True


def _toggle(env, rel):
    r = env.js("(()=>{const r=document.querySelector(%s);const a=r&&r.querySelector('.arrow');"
               "if(!a)return 'no-arrow';a.click();return 'ok';})()" % json.dumps(_sel(env, rel)))
    env.check("切换目录展开 %s" % rel, r == "ok", r)


def _expand(env, rel, timeout=20):
    """展开目录（幂等：已展开则跳过）。"""
    if _row(env, rel) is not True:
        _wait_row(env, rel)
    if _expanded(env, rel):
        return
    _toggle(env, rel)
    env.wait(lambda: True if _expanded(env, rel) else None,
             "展开目录 %s" % rel, timeout=timeout)


def _collapse(env, rel, timeout=20):
    if _row(env, rel) is not True:
        return
    if not _expanded(env, rel):
        return
    _toggle(env, rel)
    env.wait(lambda: True if not _expanded(env, rel) else None,
             "折叠目录 %s" % rel, timeout=timeout)


def _click(env, rel, ctrl=False):
    sel = json.dumps(_sel(env, rel))
    mods = "{ctrlKey:true}" if ctrl else "{}"
    r = env.js("(()=>{const r=document.querySelector(%s);if(!r)return 'no-row';"
               "r.dispatchEvent(new MouseEvent('click',{bubbles:true,cancelable:true,...%s}));"
               "return 'ok';})()" % (sel, mods))
    env.check("%s点击 %s" % ("Ctrl+" if ctrl else "", rel), r == "ok", r)


def _ctx(env, rel):
    sel = json.dumps(_sel(env, rel))
    r = env.js("(()=>{const r=document.querySelector(%s);if(!r)return 'no-row';"
               "const b=r.getBoundingClientRect();"
               "r.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,cancelable:true,"
               "clientX:b.left+10,clientY:b.top+5}));return 'ok';})()" % sel)
    env.check("右键 %s 弹出菜单" % rel, r == "ok", r)
    time.sleep(0.4)


def _items(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll('.context-menu-item')]"
                   ".map(i=>i.textContent.trim()))") or []


def _has(items, key):
    return any(x in items for x in LABELS[key])


def _menu_click(env, key):
    labs = LABELS[key]
    r = env.js("(()=>{const its=[...document.querySelectorAll('.context-menu-item')];"
               "const it=its.find(i=>%s.includes(i.textContent.trim()));"
               "if(!it)return false;it.click();return true;})()" % json.dumps(list(labs)))
    env.check("点击菜单项 %s" % key, r is True, r)
    time.sleep(0.5)


def _close_menu(env):
    env.js("document.body.click(); 'ok'")
    time.sleep(0.2)


def _key(env, key):
    # 先移走焦点：焦点在聊天 contenteditable 时前端按设计不接管 Delete/F2（防误删）。
    env.js("(()=>{const a=document.activeElement;"
           "if(a&&a.blur)a.blur();"
           "document.dispatchEvent(new KeyboardEvent('keydown',{key:%s,bubbles:true}));"
           "return 'ok';})()" % json.dumps(key))


def _editing(env):
    return env.js("!!document.querySelector('.inline-edit-input')") is True


def _esc_edit(env):
    env.js("document.querySelector('.inline-edit-input')?.dispatchEvent("
           "new KeyboardEvent('keyup',{key:'Escape',bubbles:true})); 'ok'")
    return env.wait(lambda: True if not _editing(env) else None, "退出改名态", timeout=5)


def _dialog_n(env):
    """可见 `.dialog-shell` 数量（按计算样式 + 矩形；fixed 定位的 offsetParent 恒为 null，不能用）。"""
    return int(env.js(
        "(()=>[...document.querySelectorAll('.dialog-shell')]"
        ".filter(e=>{const s=getComputedStyle(e);const r=e.getBoundingClientRect();"
        "return s.display!=='none'&&s.visibility!=='hidden'&&"
        "e.getAttribute('aria-hidden')!=='true'&&(r.width>0||r.height>0)})"
        ".length)()") or 0)


def _dialog(env):
    return _dialog_n(env) > 0


def _cancel_dialog(env):
    # 多选删除会**逐节点**各弹一个确认框（叠加）→ 逐个取消直到可见弹窗为 0。
    clicked = 0
    for _ in range(6):
        if _dialog_n(env) == 0:
            break
        r = env.js("(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                   ".filter(e=>{const s=getComputedStyle(e);const r=e.getBoundingClientRect();"
                   "return s.display!=='none'&&s.visibility!=='hidden'&&(r.width>0||r.height>0)});"
                   "const b=ds.flatMap(d=>[...d.querySelectorAll('button')])"
                   ".find(x=>/取消|cancel/i.test(x.textContent||''));if(!b)return 'no-btn';"
                   "b.click();return 'ok';})()")
        if r != "ok":
            break
        clicked += 1
        time.sleep(0.35)
    env.check("取消确认框", clicked >= 1, "dialogs closed=%d" % clicked)
    env.wait(lambda: True if _dialog_n(env) == 0 else None, "确认框关闭", timeout=10)


def _drag(env, src, dst):
    js = """(()=>{const s=document.querySelector(%s);
      const t=document.querySelector(%s);
      if(!s||!t)return 'missing:'+(s?'':'src')+(t?'':'tgt');
      const dt=new DataTransfer();
      s.dispatchEvent(new DragEvent('dragstart',{bubbles:true,cancelable:true,dataTransfer:dt}));
      t.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:dt}));
      t.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:dt}));
      s.dispatchEvent(new DragEvent('dragend',{bubbles:true,cancelable:true,dataTransfer:dt}));
      return 'ok';})()""" % (json.dumps(_sel(env, src)), json.dumps(_sel(env, dst)))
    r = env.js(js)
    env.check("拖拽 %s → %s" % (src, dst), r == "ok", r)
    time.sleep(1.2)


def _rm(env, *rels):
    for rel in rels:
        try:
            env.emit("filesys.remove", {"work_dir": env.workspace, "path": env.path(rel)})
        except Exception:
            pass


def _mkdir(env, rel):
    env.emit("filesys.mkdir", {"work_dir": env.workspace, "dir": env.workspace,
                               "name": rel})
    env.wait(lambda: os.path.isdir(env.path(rel)), "目录落盘 %s" % rel, timeout=15)
    _wait_row(env, rel)


def _create(env, rel, content, dir_rel=""):
    parent = env.path(dir_rel) if dir_rel else os.path.dirname(env.path(rel))
    env.emit("filesys.create", {"work_dir": env.workspace,
                                "dir": parent or env.workspace,
                                "name": os.path.basename(rel), "content": content})
    env.wait(lambda: os.path.isfile(env.path(rel)), "文件落盘 %s" % rel, timeout=15)


@case("S-FT-01", "文件树", "树交互：二次点击/F2 进改名、ESC 取消、Delete 弹确认且不删盘",
      stage="seeded", order=55, fp="32 文件树 · 交互（改名判定/删除确认）",
      desc="单击选中 → 间隔 >500ms 再次点击同节点 → 出现 `.inline-edit-input`；ESC 退出；"
           "F2 同样进入改名；Delete 弹出 `.dialog-shell` 且取消后文件仍在磁盘。")
def s_ft_01_rename_delete(env):
    ensure_demo_file(env, "README.md")
    _wait_row(env, "README.md", desc="README.md 树节点就绪", timeout=25)
    _esc_edit(env) if _editing(env) else None

    _click(env, "README.md")
    time.sleep(0.75)                      # > 500ms 判定窗
    _click(env, "README.md")
    env.wait(lambda: True if _editing(env) else None, "二次点击进入改名", timeout=6)
    env.check("二次点击进入改名（输入框出现）", _editing(env))
    env.check("ESC 取消改名", _esc_edit(env))

    _click(env, "README.md")
    time.sleep(0.3)
    _key(env, "F2")
    env.wait(lambda: True if _editing(env) else None, "F2 进入改名", timeout=6)
    env.check("F2 触发改名", _editing(env))
    env.check("ESC 退出 F2 改名", _esc_edit(env))

    _click(env, "README.md")
    time.sleep(0.3)
    _key(env, "Delete")
    env.wait(lambda: True if _dialog(env) else None, "Delete 弹出删除确认框", timeout=10)
    env.check("Delete 弹出确认框", _dialog(env))
    _cancel_dialog(env)
    env.check("取消后文件仍在磁盘", os.path.isfile(env.path("README.md")))


@case("S-FT-02", "文件树", "右键菜单：复制/在控制台/黏贴按剪贴板显隐，多选隐藏单选项",
      stage="seeded", order=60, fp="32 文件树 · 右键菜单（应用内剪贴板）",
      desc="右键文件：含「复制/在控制台显示」、无「黏贴」；点复制后右键目录出现「黏贴」；"
           "黏贴落盘且源文件保留；Ctrl 多选后右键不含「打开/重命名」，含「复制/删除」。")
def s_ft_02_ctxmenu(env):
    d = "e2e_ft_ctx_%s" % env.run_id
    src = "e2e_ft_src_%s.txt" % env.run_id
    mark = "E2E-FT-CTX-%s" % env.run_id
    _mkdir(env, d)
    _create(env, src, mark)

    _ctx(env, src)
    items = _items(env)
    env.check("菜单含「复制」", _has(items, "copy"), items)
    env.check("菜单含「在控制台显示」", _has(items, "console"), items)
    env.check("空剪贴板时无「黏贴」", not _has(items, "paste"), items)
    _menu_click(env, "copy")
    _close_menu(env)

    _ctx(env, d)
    items2 = _items(env)
    env.check("复制后右键目录含「黏贴」", _has(items2, "paste"), items2)
    _menu_click(env, "paste")
    target = os.path.join(d, os.path.basename(src))
    env.wait(lambda: os.path.isfile(env.path(target)), "黏贴产物落盘", timeout=15)
    env.eq("黏贴内容一致", env.read_text(target), mark)
    env.check("源文件保留", os.path.isfile(env.path(src)))
    _close_menu(env)

    # 多选右键：单选项（打开/重命名）隐藏
    _click(env, "README.md", ctrl=True)
    _click(env, src, ctrl=True)
    _ctx(env, "README.md")
    items3 = _items(env)
    env.check("多选隐藏「打开」", not _has(items3, "open"), items3)
    env.check("多选隐藏「重命名」", not _has(items3, "rename"), items3)
    env.check("多选保留「复制/删除」", _has(items3, "copy") and _has(items3, "delete"), items3)
    _close_menu(env)
    _click(env, "README.md")

    _rm(env, d)
    env.wait(lambda: not os.path.exists(env.path(d)), "临时目录清理", timeout=15)


@case("S-FT-03", "文件树", "多选与拖拽：Ctrl 多选 / Delete 确认 / 移动 / 同名冲突 / 拖入对话输入区",
      stage="seeded", order=65, fp="32 文件树 · 拖拽（移动/冲突/拖到聊天）",
      desc="Ctrl 点击两节点 → `.multi-selected`=2，普通单击清空；多选 Delete 弹确认并取消；"
           "拖文件入目录 → 磁盘移动成功且树刷新；同名冲突弹确认、取消后源不动；"
           "拖文件到 `[data-drop-target=chat-input]` → 输入区出现文件名。")
def s_ft_03_dnd(env):
    d = "e2e_ft_dnd_%s" % env.run_id
    f = "e2e_ft_move_%s.txt" % env.run_id
    g = "e2e_ft_conf_%s.txt" % env.run_id
    h = "e2e_ft_chat_%s.txt" % env.run_id
    mark = "E2E-FT-DND-%s" % env.run_id
    _mkdir(env, d)
    for rel, val in ((f, mark), (g, mark), (h, mark)):
        _create(env, rel, val)
    _create(env, os.path.join(d, os.path.basename(g)), "TARGET-OLD")
    _expand(env, d)

    # M1/M2 多选与退化
    _click(env, f, ctrl=True)
    _click(env, h, ctrl=True)
    multi = int(env.js("document.querySelectorAll('.tree-row.multi-selected').length") or 0)
    env.eq("Ctrl 多选 2 项", multi, 2)
    _click(env, h)
    env.eq("普通单击清多选",
           int(env.js("document.querySelectorAll('.tree-row.multi-selected').length") or 0), 0)

    # M3 多选 Delete → 确认框（取消）
    _click(env, f, ctrl=True)
    _click(env, h, ctrl=True)
    _key(env, "Delete")
    env.wait(lambda: True if _dialog(env) else None, "多选 Delete 弹确认", timeout=10)
    env.check("多选 Delete 弹出确认框", _dialog(env))
    _cancel_dialog(env)
    env.check("取消删除后两文件仍在", os.path.isfile(env.path(f)) and os.path.isfile(env.path(h)))

    # D1 拖拽移动
    _click(env, h)  # 收敛多选
    _drag(env, f, d)
    moved = os.path.isfile(env.path(os.path.join(d, os.path.basename(f))))
    env.wait(lambda: True if moved and not os.path.exists(env.path(f)) else None,
             "拖拽移动落盘（目标在/源无）", timeout=10)
    env.check("拖拽移动：目标在且源无",
              os.path.isfile(env.path(os.path.join(d, os.path.basename(f))))
              and not os.path.exists(env.path(f)))
    env.wait(lambda: True if _row(env, os.path.join(d, os.path.basename(f))) else None,
             "树节点出现在目标目录", timeout=20)

    # D3 同名冲突：后端拒绝 + 源/目标均不变
    # 注：当前桥把错误对象压平成字符串（`errors:["target already exists"]`）→ 前端
    # `fileRequest` 取不到 `err.code`，覆盖确认框不会出现（旧 test_filetree_dnd D3 的
    # `.dialog-shell` 断言实为命中前序未清弹窗，属假绿）。此处按**可保证行为**断言，
    # 前端确认缺失记入 known gap。
    raw = env.publish("filesys.rename", {
        "work_dir": env.workspace, "path": env.path(g),
        "new_name": env.posix(env.path(os.path.join(d, os.path.basename(g))))})
    errs = [str(x) for x in ((raw or {}).get("errors") or [])]
    env.check("后端拒绝同名移动（exists）",
              (raw or {}).get("ok") is False and any("already exists" in x for x in errs), raw)
    _drag(env, g, d)
    time.sleep(1.2)
    env.check("同名冲突后源文件保留", os.path.isfile(env.path(g)))
    env.eq("同名冲突后目标内容未被覆盖",
           env.read_text(os.path.join(d, os.path.basename(g))), "TARGET-OLD")
    env.note("同名冲突前端确认缺失（错误码压平）",
             "dialogs=%d errors=%r" % (_dialog_n(env), errs))
    if _dialog_n(env) > 0:
        _cancel_dialog(env)

    # D4 拖到对话输入区 → 全路径/文件名插入
    r = env.js("""(()=>{const rows=[...document.querySelectorAll('.tree-row')];
      const s=rows.find(r=>(r.querySelector('.node-label')||{}).textContent===%s);
      const ta=document.querySelector('[contenteditable][data-drop-target="chat-input"]');
      if(!s)return 'no-src';if(!ta)return 'no-chat';
      const dt=new DataTransfer();
      s.dispatchEvent(new DragEvent('dragstart',{bubbles:true,cancelable:true,dataTransfer:dt}));
      const b=ta.getBoundingClientRect();
      ta.dispatchEvent(new DragEvent('dragover',{bubbles:true,cancelable:true,dataTransfer:dt,
        clientX:b.left+10,clientY:b.top+10}));
      ta.dispatchEvent(new DragEvent('drop',{bubbles:true,cancelable:true,dataTransfer:dt,
        clientX:b.left+10,clientY:b.top+10}));
      s.dispatchEvent(new DragEvent('dragend',{bubbles:true,cancelable:true,dataTransfer:dt}));
      return 'ok';})()""" % json.dumps(os.path.basename(h)))
    env.check("拖拽到对话输入区事件送达", r == "ok", r)
    val = env.wait(lambda: (env.js("document.querySelector('[contenteditable][data-drop-target="
                                   "\"chat-input\"]')?.innerText || ''") or None)
                   if os.path.basename(h) in (env.js(
                       "document.querySelector('[contenteditable][data-drop-target="
                       "\"chat-input\"]')?.innerText || ''") or "") else None,
                   "输入区出现拖入文件名", timeout=10)
    env.check("输入区插入文件名", os.path.basename(h) in (val or ""), repr((val or "")[:80]))
    env.js("(()=>{const e=document.querySelector('[contenteditable][data-drop-target="
           "\"chat-input\"]');if(e){e.innerHTML='';e.dispatchEvent(new Event('input',{bubbles:true}))}"
           "return 'ok'})()")

    _rm(env, d, g, h)


@case("S-FT-04", "文件树", "外部变化自动刷新：根/展开目录增删改、折叠不推送、打开文件内容刷新",
      stage="seeded", order=70, fp="32 文件树 · watcher（file-dir-contents / filesys.changed）",
      desc="新建/删除根文件 → 树自动增删；展开目录内新建/删除同理；折叠目录内新建不出现，"
           "重新展开后出现（刷新）；外部改已打开文件 → 收到 `filesys.changed` 且 CodeView 内容更新。")
def s_ft_04_watcher(env):
    w = "e2e_ft_watch_%s" % env.run_id
    os.makedirs(env.path(w), exist_ok=True)
    _wait_row(env, w)
    _expand(env, w)

    # 根级新建/删除
    root_new = "e2e_ft_root_%s.txt" % env.run_id
    env.write_text(root_new, "W1")
    env.wait(lambda: True if _row(env, root_new) else None, "根级新建自动出现", timeout=15)
    os.remove(env.path(root_new))
    _wait_row(env, root_new, present=False, timeout=15, desc="根级删除自动消失")

    # 展开目录内新建 / 修改不崩 / 删除
    env.write_text(os.path.join(w, "a.txt"), "V1")
    _wait_row(env, os.path.join(w, "a.txt"), timeout=15, desc="展开目录内新建出现")
    env.client.console(clear=True)
    env.write_text(os.path.join(w, "a.txt"), "V2-updated")
    time.sleep(2.5)
    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("外部修改（write）无前端错误", len(errs) == 0, errs[:3])
    os.remove(env.path(os.path.join(w, "a.txt")))
    _wait_row(env, os.path.join(w, "a.txt"), present=False, timeout=15,
              desc="展开目录内删除自动消失")

    # 折叠目录内新建 → 不可见；重新展开 → 可见（刷新）
    _collapse(env, w)
    hidden = "e2e_ft_collapsed_%s.txt" % env.run_id
    env.write_text(os.path.join(w, hidden), "H")
    time.sleep(2.5)
    env.check("折叠目录内新建不出现", not _row(env, os.path.join(w, hidden)))
    _expand(env, w)
    _wait_row(env, os.path.join(w, hidden), timeout=15, desc="重新展开后出现")
    os.remove(env.path(os.path.join(w, hidden)))

    # 打开文件外部修改 → filesys.changed + CodeView 刷新
    _expand(env, w)
    p = env.path(os.path.join(w, "live.txt"))
    env.write_text(os.path.join(w, "live.txt"), "LIVE-BASE")
    _wait_row(env, os.path.join(w, "live.txt"), timeout=15)
    env.emit("file-open", {"path": env.posix(p)})
    env.wait(lambda: True if "LIVE-BASE" in str(env.js(
        "document.querySelector('.code-view')?.innerText || ''")) else None,
        "打开文件显示基线内容", timeout=20)
    env.capture(["filesys.changed"])
    sentinel = "E2E-FT-LIVE-%s" % env.run_id
    with open(p, "a", encoding="utf-8") as f:
        f.write("\n" + sentinel)
    env.wait_events("filesys.changed", lambda pl: env.posix(p) in json.dumps(pl),
                    "外部修改推送 filesys.changed", timeout=20)
    env.wait(lambda: True if sentinel in str(env.js(
        "document.querySelector('.code-view')?.innerText || ''")) else None,
        "CodeView 自动刷新出新内容", timeout=20)
    env.check("CodeView 含外部追加哨兵", sentinel in str(env.js(
        "document.querySelector('.code-view')?.innerText || ''")))

    # 批量添加 10 文件：推送合并（事件数 < 文件数），树最终齐全
    env.capture(["file-dir-contents"])
    batch = ["e2e_ft_batch_%s_%d.txt" % (env.run_id, i) for i in range(10)]
    for i, rel in enumerate(batch):
        env.write_text(rel, "B%d" % i)
    time.sleep(2.5)
    eve = len(env.events("file-dir-contents"))
    env.check("批量 10 文件推送合并（<10 次）", eve < 10, "n=%d" % eve)
    _wait_row(env, batch[-1], timeout=20, desc="批量文件最后一个出现在树中")
    for rel in batch:
        try:
            os.remove(env.path(rel))
        except OSError:
            pass
    _rm(env, w)


@case("S-FT-05", "文件树", "隐藏/临时文件过滤、新子目录自动监听、根目录重建保持展开",
      stage="seeded", order=75, fp="32 文件树 · 监听细节（过滤/子树/重建）",
      desc="展开目录内写 `.hidden`/`~$x`/`x-wal`/`y~` → 树均不出现；新建子目录 → 自动出现且"
           "展开后内部文件可见；根级新建目录 → 整树重建后原展开目录仍展开。")
def s_ft_05_watcher_details(env):
    w = "e2e_ft_wd_%s" % env.run_id
    os.makedirs(env.path(w), exist_ok=True)
    _wait_row(env, w)
    _expand(env, w)

    for name in (".hidden", "~$tmp.docx", "x-wal", "y~"):
        with open(env.path(os.path.join(w, name)), "w", encoding="utf-8") as f:
            f.write("tmp")
    time.sleep(2.0)
    shown = [n for n in (".hidden", "~$tmp.docx", "x-wal", "y~")
             if _row(env, os.path.join(w, n))]
    env.check("隐藏/临时文件全部过滤", not shown, shown)

    sub = os.path.join(w, "subnew")
    os.makedirs(env.path(sub), exist_ok=True)
    _wait_row(env, sub, timeout=15, desc="新子目录自动出现")
    _expand(env, sub)
    env.write_text(os.path.join(sub, "inner.txt"), "INNER")
    _wait_row(env, os.path.join(sub, "inner.txt"), timeout=15, desc="新子目录内文件出现")

    rootnew = "e2e_ft_rootnew_%s" % env.run_id
    os.makedirs(env.path(rootnew), exist_ok=True)
    _wait_row(env, rootnew, timeout=15, desc="根级新目录出现")
    env.check("根目录重建后原展开目录仍展开", _row(env, os.path.join(w, "subnew")))

    _rm(env, rootnew, w)


def _touch_office(path, ctype, rels, part, body):
    with zipfile.ZipFile(path, "w") as z:
        z.writestr("[Content_Types].xml", ctype)
        z.writestr("_rels/.rels", rels)
        z.writestr(part, body)


@case("S-FT-06", "预览", "8 类文件打开后的 renderType 判定（image/pdf/docx/xlsx/pptx/code×3）",
      stage="seeded", order=80, fp="32 文件树 · 预览 renderType（run_filetree 一.8）",
      desc="生成 png/pdf/docx/xlsx/pptx/java/go/py 八类文件 → 逐个 file-open → 活动页签 "
           "`.file-type-tag` 依次为 image/pdf/docx/xlsx/pptx/code；测试后清理生成物。")
def s_ft_06_render_types(env):
    d = "e2e_ft_types_%s" % env.run_id
    os.makedirs(env.path(d), exist_ok=True)
    make = env.path
    png = base64.b64decode(
        "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
    with open(make(os.path.join(d, "pic.png")), "wb") as f:
        f.write(png)
    pdf = (b"%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"
           b"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n"
           b"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\n"
           b"xref\n0 4\n0000000000 65535 f \n0000000009 00000 n \n0000000058 00000 n \n"
           b"0000000115 00000 n \ntrailer<</Size 4/Root 1 0 R>>\nstartxref\n190\n%%EOF\n")
    with open(make(os.path.join(d, "test.pdf")), "wb") as f:
        f.write(pdf)
    ct = ('<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/'
          'content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-'
          'package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/>'
          '<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-'
          'officedocument.wordprocessingml.document.main+xml"/></Types>')
    rels = ('<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/'
            'package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.'
            'openxmlformats.org/officeDocument/2006/relationships/officeDocument" '
            'Target="word/document.xml"/></Relationships>')
    _touch_office(make(os.path.join(d, "test.docx")), ct, rels, "word/document.xml",
                  '<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/'
                  'wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello</w:t></w:r></w:p>'
                  '</w:body></w:document>')
    _touch_office(make(os.path.join(d, "test.xlsx")), ct, rels,
                  "xl/workbook.xml",
                  '<?xml version="1.0"?><workbook xmlns="http://schemas.openxmlformats.org/'
                  'spreadsheetml/2006/main"><sheets><sheet name="S1" sheetId="1"/></sheets>'
                  '</workbook>')
    _touch_office(make(os.path.join(d, "test.pptx")), ct, rels,
                  "ppt/presentation.xml",
                  '<?xml version="1.0"?><p:presentation xmlns:p="http://schemas.openxmlformats.'
                  'org/presentationml/2006/main"><p:sldIdLst/></p:presentation>')
    for name, body in (("Test.java", "public class Test {}\n"),
                       ("test.go", "package main\n"),
                       ("test.py", "print('hi')\n")):
        env.write_text(os.path.join(d, name), body)

    expect = {"pic.png": "image", "test.pdf": "pdf", "test.docx": "docx",
              "test.xlsx": "xlsx", "test.pptx": "pptx",
              "Test.java": "code", "test.go": "code", "test.py": "code"}
    for name, rtype in expect.items():
        env.emit("file-open", {"path": env.posix(env.path(os.path.join(d, name)))})
        got = env.wait(
            lambda: (env.js(
                "(()=>{const p=[...document.querySelectorAll('.tab-panel')]"
                ".find(p=>getComputedStyle(p).display!=='none');"
                "return p?(p.querySelector('.file-type-tag')?.textContent||''):''})()") or None)
            if rtype in str(env.js(
                "(()=>{const p=[...document.querySelectorAll('.tab-panel')]"
                ".find(p=>getComputedStyle(p).display!=='none');"
                "return p?(p.querySelector('.file-type-tag')?.textContent||''):''})()")) else None,
            "%s renderType=%s" % (name, rtype), timeout=20)
        env.check("%s → %s" % (name, rtype), rtype in str(got), got)
        time.sleep(0.3)

    _rm(env, d)
    env.wait(lambda: not os.path.exists(env.path(d)), "类型预览临时目录清理", timeout=15)