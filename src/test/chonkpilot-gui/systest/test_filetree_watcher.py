"""回归测试：filetree watcher（51-FP与测试映射「filetree 内容」文件变化自动刷新）。

覆盖：
- 根目录（自动监视）顶层新建/删除文件 → 树自动刷新
- 展开目录（watch 声明）下新建文件 → 树子节点自动出现
- 修改文件 → 无前端错误（write 事件不崩）

驱动：Python 直接在 workdir 增删文件，等待后端轮询（2s）+ 缓冲。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原

PORT = free_port()
HERE = os.path.dirname(os.path.abspath(__file__))
WORK = os.path.join(HERE, "_watcher_ws")
DATA_DIR = os.path.join(HERE, "_watcher_data")

W = WORK.replace("\\", "/")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def node_exists(gui, relpath):
    """树中是否存在指定相对路径节点（data-path 精确匹配，正斜杠）。"""
    return J(gui, "Array.from(document.querySelectorAll('.tree-row')).some(r => r.getAttribute('data-path').replace(/\\\\/g,'/') === %s)" % json.dumps(W + "/" + relpath))


def expand_dir(gui, relpath):
    """点击目录的展开箭头（触发 filemon.file.watch）。"""
    J(gui, """(()=>{const rows=Array.from(document.querySelectorAll('.tree-row'));const r=rows.find(x=>x.getAttribute('data-path').replace(/\\\\/g,'/')===%s);const a=r&&r.querySelector('.arrow');if(a){a.click();return true}return false})()""" % json.dumps(W + "/" + relpath))
    time.sleep(1.0)


def main():
    shutil.rmtree(WORK, ignore_errors=True)
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(WORK, exist_ok=True)
    os.makedirs(os.path.join(WORK, "sub"), exist_ok=True)
    os.makedirs(os.path.join(WORK, ".git"), exist_ok=True)  # VCS 图标验证（GetVCSInfo 桥）
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, work_dir=WORK, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        for _ in range(30):
            if J(gui, "document.querySelectorAll('.tree-row').length") > 0:
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # V0 git 图标：workdir 含 .git → 标题栏显示 git 图标（GetVCSInfo 桥 + 前端 call）
        c.check("V0 .git 目录 → git 图标显示", bool(J(gui, "!!document.querySelector('.vcs-git')")))

        # W1 根目录顶层新建文件 → 树自动出现（根目录自动 watch）
        with open(os.path.join(WORK, "new_root.txt"), "w") as f:
            f.write("hello watcher")
        time.sleep(3.5)
        c.check("W1 顶层新建文件自动出现", bool(node_exists(gui, "new_root.txt")))

        # W2 顶层删除文件 → 树自动消失
        os.remove(os.path.join(WORK, "new_root.txt"))
        time.sleep(3.5)
        c.check("W2 顶层删除文件自动消失", not node_exists(gui, "new_root.txt"))

        # W3 展开 sub 目录（watch 声明）→ sub 内新建文件 → 子节点自动出现
        expand_dir(gui, "sub")
        time.sleep(0.5)
        with open(os.path.join(WORK, "sub", "sub_file.txt"), "w") as f:
            f.write("nested")
        time.sleep(3.5)
        c.check("W3 展开目录内新建文件自动出现", bool(node_exists(gui, "sub/sub_file.txt")))

        # W4 修改 sub 内文件 → write 事件不崩（无前端错误）
        gui.console(clear=True)
        with open(os.path.join(WORK, "sub", "sub_file.txt"), "w") as f:
            f.write("updated content")
        time.sleep(3.5)
        errs = [e.get('text') for e in (gui.console() or {}).get('entries', []) if e.get('level') == 'error']
        c.check("W4 文件修改（write）不崩", len(errs) == 0, repr(errs[:2]))

        # W5 删除 sub 内文件 → 子节点消失
        os.remove(os.path.join(WORK, "sub", "sub_file.txt"))
        time.sleep(3.5)
        c.check("W5 展开目录内删除文件自动消失", not node_exists(gui, "sub/sub_file.txt"))

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（watcher 组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()
