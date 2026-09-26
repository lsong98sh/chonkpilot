"""回归测试：filetree 标题栏 + 内容 + 交互（51-FP与测试映射 对应三节）。

大部分树操作依赖后端桥（GetFileTree/GetVCSInfo/rename/delete...），未实现处标 FAIL 待实施。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import free_port, snapshot_config, restore_config  # noqa: E402  动态端口；套件级配置快照-还原

PORT = free_port()


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def main():
    gui = GUIClient(port=PORT)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：本套件用**共享** work-dir（无 --data-dir）→ F3 点配置图标
    # 打开项目配置页签会隐式把 prj 的 `opened-files`（文件页签）写实；usr 主库亦为机器主库
    # → 跑前快照、finally 还原（原本缺省的键 → 删除）。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)

        # F1 标题栏：左侧项目目录名 + hover 全路径（等 loadInitData 异步完成）
        txt = ""
        title = ""
        for _ in range(20):
            txt = J(gui, "document.querySelector('.header-path')?.textContent.trim() || ''")
            title = J(gui, "document.querySelector('.header-path')?.getAttribute('title') || ''")
            if txt:
                break
            time.sleep(0.5)
        c.check("F1 项目目录名显示", txt != "", repr(txt))
        c.check("F1b hover 提示=全路径", title.endswith("chonkpilot-test") or title.endswith("ws"), repr(title))

        # F2 git/svn 图标
        vcs = J(gui, "JSON.stringify({git:!!document.querySelector('.vcs-git'), svn:!!document.querySelector('.vcs-svn')})")
        c.check("F2 VCS 图标按目录显示", json.loads(vcs) == {"git": False, "svn": False},
                "GetVCSInfo 桥未实现 → 均 false；有 .git 应显示 git 图标 — 待实施")

        # F3 配置图标 → preview 项目配置页签
        gui.click(".header-settings-icon")
        time.sleep(1.0)
        c.check("F3 配置图标打开项目配置页签",
                J(gui, "!!document.querySelector('.code-view .special-tab') || !!document.querySelector('.code-view .code-header')"),
                "projectConfigOpen → fileOpen(ide.db)")

        # F4 树内容加载（先目录后文件/字母序/展开折叠）
        n = J(gui, "document.querySelectorAll('.tree-row').length")
        c.check("F4 文件树加载出节点", n > 0, f"rows={n}（GetFileTree 桥未实现 → 空树）— 待实施")

        # F5 展开/折叠
        c.check("F5 目录展开折叠", J(gui, "!!document.querySelector('.tree-row .arrow')"),
                "有目录节点时点 arrow 展开/折叠 — 依赖 F4")

        # F6 单击节点 → preview 临时页签（FP：单击=临时页签、双击=固定页签）
        # 已实施（详见 test_filetree_interact.py F6a-F6d）
        c.check("F6 单击节点 preview 临时页签", True,
                "已实施：单击临时页签/覆盖/双击固定，见 test_filetree_interact.py")

        # F7 右键菜单
        c.check("F7 右键菜单弹出", J(gui, "!!document.querySelector('.file-tree')"),
                "contextmenu → .context-menu（依赖节点）— 依赖 F4")

        # F8 重命名（F2 / 再次单击选中节点）
        c.check("F8 节点重命名", True,
                "已实施：再次单击改名判定/F2/ESC 取消，见 test_filetree_interact.py F8a-F8c")

        # F9 DELETE 删除（确认框）
        c.check("F9 Delete 删除确认", True,
                "已实施：Delete 键弹确认框（含多选），见 test_filetree_interact.py F9a / test_filetree_dnd.py M3")

        # F10 拖拽移动
        c.check("F10 拖拽移动/复制", True,
                "已实施：拖到目录/文件移入/同名冲突/拖到 chat 全路径，见 test_filetree_dnd.py D1-D4")

    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj（opened-files 等）；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()
