# -*- coding: utf-8 -*-
"""FP L88 保存打开文件重启恢复：打开文件 → 重启 gui → preview tab 自动恢复。

流程：
  1. 以**独立临时 work-dir + 独立 --data-dir** 启动 GUI → emit file-open 打开 a.txt / b.txt
     （CodeView 保存 openedFiles 到 prj 库）
  2. 断言两个 tab 出现（`.tb-bar.tb-bottom .tb-tab .tb-name`）
  3. 强杀 GUI → 以同 work-dir / 同 data-dir 重启
  4. 断言 preview tab 自动恢复（同一文件名 tab 存在）

迁移（2026-09-15）：
  - 页签栏 = 公共 TabBar（`.tb-bar.tb-bottom .tb-tab` / `.tb-name`）；旧 `.preview-tab-name` 已移除。
  - 隔离：旧版直接用共用 `systest/ws`（与主测试实例**同 work-dir**）→ 双实例 DB 锁冲突。
    现改用 tempfile 独立 work-dir + 独立 `--data-dir`（用完清理），与任何在跑实例互不干扰。
  - file-open 路径改用**绝对路径**（与 FileTree/run_filetree 同口径）。

前置：mock_llm（8901）非必需（本用例不涉及 LLM 回合）。
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）

GUI_EXE = _h.resolve_gui_exe()
PORT = _h.free_port()  # 动态端口：不与他套件/机器上的固定端口实例争用
BASE = os.path.join(tempfile.gettempdir(), "ck-restore-tabs")
WORK_DIR = os.path.join(BASE, "ws")
DATA_DIR = os.path.join(BASE, "data")

TAB_NAME = ".tb-bar.tb-bottom .tb-tab .tb-name"


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def prepare_ws():
    shutil.rmtree(BASE, ignore_errors=True)
    os.makedirs(WORK_DIR, exist_ok=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    for name in ("a.txt", "b.txt"):
        with open(os.path.join(WORK_DIR, name), "w", encoding="utf-8") as f:
            f.write("hello from %s\n" % name)


def start_gui():
    # 按需自起 + 登记回收（finally / 进程退出 / 信号三条路径都会清）
    return _h.start_gui(port=PORT, work_dir=WORK_DIR, data_dir=DATA_DIR, ready_timeout=40)


def stop_gui(h):
    if h is not None:
        h.stop()


def wait_port(port, max_wait=40):
    c = ChonkClient(base=f"http://127.0.0.1:{port}")
    deadline = time.time() + max_wait
    while time.time() < deadline:
        try:
            r = c.eval("1+1", 3000)
            if r:
                return c
        except Exception:
            pass
        time.sleep(1)
    raise TestError(f"端口 {port} 未就绪")


def prj_opened_files(c):
    """prj 库 `opened-files` 落库回读（P6 复核补齐：原用例只比 tab 名，未比对持久化键）。

    A 口径：`codeview/SaveOpenedFiles` → 桥 `gui.ui.save{opened_files}` →
    `bridge/local.go:245-256 callSaveOpenedFiles` → `prjConfigSave("opened-files", <JSON 数组>)`；
    `--data-dir` 非空时 prjusr == prj（persist.go:400-408）→ 同一库可经 `data-prj-config-list` 回读。
    """
    lst = (c.req("data-prj-config-list", {}) or {}).get("list") or {}
    raw = lst.get("opened-files")
    if isinstance(raw, str):
        try:
            raw = json.loads(raw)
        except Exception:
            raw = []
    return [str(x) for x in (raw or [])]


def wait_prj_opened(c, names, max_wait=8):
    """轮询 prj `opened-files` 直到含全部 names（返回最后读到的列表）。

    为什么要轮询：`CodeView.openFileInto` 一次打开同时触发**两个写者**——
    ① `saveOpenedFile(path)`（legacy 单元素写，`opened_files: [path]`，见 file.js:182-184）即时发；
    ② `persistOpenedFiles()`（全量列表写）去抖 300ms 后才发（CodeView.vue:473-481）。
    启动恢复逐文件重放 fileOpen 时，① 可能后于 ② 落库 → 读到的**瞬态**值只有最后一个文件；
    两写者最终收敛为全量列表（本函数即以此收敛态为准，瞬态值由调用方打印留证）。
    """
    deadline = time.time() + max_wait
    last = []
    while time.time() < deadline:
        last = prj_opened_files(c)
        if all(any(n in p for p in last) for n in names):
            return last
        time.sleep(0.4)
    return last


def fast_read_after_open(c, names, window=0.1):
    """I-81 专项：打开 A → 打开 B 后**不等去抖窗口**（≤100ms）回读 prj `opened-files`。

    断言目标 = 该键在窗口内**已同时包含 A 与 B**（= 打开即「立即全量写」）。
    反例（旧实现，I-81 原文）：`openFileInto` 同时发 ① legacy 单元素写
    `saveOpenedFile(path)` → `{opened_files:[path]}` 即时落库；② 去抖 300ms 的全量写
    → 本窗口内每次读到的都只有**最后打开的那一个**（`[B]`）→ 断言必失败。
    返回 (是否命中, 逐次采样 [[t_ms, 读值], ...])，采样值即原始证据。
    """
    t0 = time.time()
    samples = []
    while True:
        cur = prj_opened_files(c)
        samples.append([round((time.time() - t0) * 1000, 1), cur])
        if all(any(n in p for p in cur) for n in names):
            return True, samples
        if time.time() - t0 >= window:
            return False, samples
        time.sleep(0.02)


def wait_tabs(c, names, max_wait=15):
    """等待 preview 底部页签栏出现 names 中的全部文件名；返回当前页签名列表。"""
    tabs = []
    deadline = time.time() + max_wait
    while time.time() < deadline:
        tabs = _loads_deep(c.eval("""(() => {
          const els = [...document.querySelectorAll('%s')];
          return JSON.stringify(els.map(e => e.textContent.trim()));
        })()""" % TAB_NAME)) or []
        if all(any(n in t for t in tabs) for n in names):
            return tabs
        time.sleep(0.5)
    return tabs


def main():
    prepare_ws()
    proc = start_gui()
    try:
        c = wait_port(PORT)
        # 打开两个文件（file-open → CodeView tab + SaveOpenedFiles 持久化）
        for name in ("a.txt", "b.txt"):
            c.mq_emit("file-open", {"path": os.path.join(WORK_DIR, name).replace("\\", "/")})
        # I-81 专项（2026-09-17）：连开两文件后**不等待去抖窗口**（≤100ms）即回读 prj `opened-files`，
        # 断言窗口内**已同时**含 a.txt 与 b.txt（= 打开即全量写）。旧实现（单元素写 + 300ms 去抖全量写）
        # 在此窗口内只会读到 [b.txt] → 本断言必失败，故它正是 I-81 的回归判据。
        ok81, samples = fast_read_after_open(c, ("a.txt", "b.txt"), window=0.1)
        print("I-81 立即全量写回读采样(t_ms, prj opened-files): %s" % json.dumps(samples, ensure_ascii=False))
        if not ok81:
            raise TestError("I-81 回归：打开 a.txt/b.txt 后 ≤100ms 内 prj opened-files 未同时含两者（采样 %s）" % samples)
        print("I-81 断言通过：%.1fms 内已同时落库 a.txt/b.txt（旧单元素写会退化为 [b.txt]）" % samples[-1][0])
        time.sleep(2.5)
        tabs1 = wait_tabs(c, ["a.txt", "b.txt"])
        if not (any("a.txt" in t for t in tabs1) and any("b.txt" in t for t in tabs1)):
            raise TestError(f"打开后 tab 未出现: {tabs1}")
        print(f"打开文件 tab: {tabs1}")
        # A 落库回读（P6 补齐）：opened-files 键确实写入 prj 库（tab 名只是 DOM 现象）
        of1 = wait_prj_opened(c, ["a.txt", "b.txt"])
        if not all(any(n in p for p in of1) for n in ("a.txt", "b.txt")):
            raise TestError(f"prj opened-files 未落库 a.txt/b.txt: {of1}")
        print(f"prj opened-files 落库回读: {of1}")
    finally:
        stop_gui(proc)

    # 重启（同 work-dir + 同 data-dir → 同 prj 库；独立于其它实例）
    proc2 = start_gui()
    try:
        c2 = wait_port(PORT)
        tabs2 = wait_tabs(c2, ["a.txt", "b.txt"])
        if not (any("a.txt" in t for t in tabs2) and any("b.txt" in t for t in tabs2)):
            raise TestError(f"重启后 tab 未恢复: {tabs2}")
        print(f"重启后恢复 tab: {tabs2}")
        # A 落库回读（重启后）：同一数据根下 opened-files 最终仍含两文件（tab 恢复的持久化依据）。
        # 瞬态留证：启动恢复期 legacy 单元素写（saveOpenedFile）可能与去抖全量写交错 → first 可能只 1 项。
        first2 = prj_opened_files(c2)
        of2 = wait_prj_opened(c2, ["a.txt", "b.txt"])
        if not all(any(n in p for p in of2) for n in ("a.txt", "b.txt")):
            raise TestError(f"重启后 prj opened-files 丢失 a.txt/b.txt: first={first2} final={of2}")
        print(f"重启后 prj opened-files 回读: first={first2} final={of2}")
        print("\nL88 重启恢复：通过")
        return 0
    finally:
        stop_gui(proc2)
        shutil.rmtree(BASE, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
