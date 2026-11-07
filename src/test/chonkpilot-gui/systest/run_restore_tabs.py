# -*- coding: utf-8 -*-
"""FP L88 保存打开文件重启恢复：打开文件 → 重启 gui → preview tab 自动恢复。

流程：
  1. 以**独立临时 work-dir** 启动 GUI → emit file-open 打开 a.txt / b.txt
     （CodeView 保存 openedFiles 到 prj 库）
  2. 断言两个 tab 出现（`.tb-bar.tb-bottom .tb-tab .tb-name`）
  3. 强杀 GUI → 以同 work-dir 重启
  4. 断言 preview tab 自动恢复（同一文件名 tab 存在）

迁移（2026-09-15）：
  - 页签栏 = 公共 TabBar（`.tb-bar.tb-bottom .tb-tab` / `.tb-name`）；旧 `.preview-tab-name` 已移除。
  - 隔离：旧版直接用共用 `systest/ws`（与主测试实例**同 work-dir**）→ 双实例 DB 锁冲突。
    现改用 tempfile 独立 work-dir（用完清理），与任何在跑实例互不干扰。
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
# 边界用例（恢复时缺失文件跳过）独立一套 work-dir / 数据根：不与主流程共享状态，
# 保证「已落库 [a,b] → 删 b → 重启只恢复 a」的前置真实成立。
BASE_SKIP = os.path.join(tempfile.gettempdir(), "ck-restore-tabs-skip")
WORK_SKIP = os.path.join(BASE_SKIP, "ws")
DATA_SKIP = os.path.join(BASE_SKIP, "data")

TAB_NAME = ".tb-bar.tb-bottom .tb-tab .tb-name"

# 记录前端实际发出的 `gui.ui.save{opened_files}` 请求（frontend fetch 层）。
#
# 为什么需要：I-81 的产品语义是「打开文件即发**全量**写（不依赖 300ms 去抖）」，
# 这是**请求发出时刻**的性质。若只用「落库后 100ms 内回读」判定，会同时受 persist
# 往返/负载影响（满负载批跑实测：请求本身没延迟，但 DB 回读窗口内读不到 → 假红）。
# 页内时钟取样把「发出延迟」与「落库延迟」解耦；落库收敛另用宽松有界等待复核。
SAVE_SPY_JS = r"""(() => {
  if (!window.__ckSaveSpy) {
    window.__ckSaveSpy = [];
    const orig = window.fetch.bind(window);
    window.fetch = (...a) => {
      try {
        const b = a[1] && a[1].body ? JSON.parse(a[1].body) : null;
        if (b && b.type === 'gui.ui.save') {
          let p = b.payload;
          try { p = typeof p === 'string' ? JSON.parse(p) : p; } catch (e) {}
          window.__ckSaveSpy.push({ t: Date.now(), files: (p && p.opened_files) || null });
        }
      } catch (e) {}
      return orig(...a);
    };
  }
  window.__ckSaveSpy = [];
  return 'ok';
})()"""


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def prepare_ws(base=BASE, work_dir=WORK_DIR, data_dir=DATA_DIR):
    shutil.rmtree(base, ignore_errors=True)
    os.makedirs(work_dir, exist_ok=True)
    os.makedirs(data_dir, exist_ok=True)
    for name in ("a.txt", "b.txt"):
        with open(os.path.join(work_dir, name), "w", encoding="utf-8") as f:
            f.write("hello from %s\n" % name)


def start_gui(work_dir=WORK_DIR, data_dir=DATA_DIR):
    # 按需自起 + 登记回收（finally / 进程退出 / 信号三条路径都会清）
    return _h.start_gui(port=PORT, work_dir=work_dir, data_dir=data_dir, ready_timeout=40)


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
    `opened-files` 落 **prj 层** → 经 `data-prj-config-list` 回读（与 prjusr 是否同根无关）。
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


def fast_read_after_open(c, names, window=2.0):
    """落库收敛采样：打开文件后逐次回读 prj `opened-files`，直到同时含 names 或超时。

    返回 (是否命中, 逐次采样 [[t_ms, 读值], ...])，采样值即原始证据。
    「打开即全量写」的**即时性**改由 `wait_save_issued`（页内 `gui.ui.save` 请求 spy，
    与 persist 往返解耦）判定；本函数只负责「最终落库收敛」这一异步面（宽松有界，非断言
    放宽：旧实现即使去抖 300ms 最终也会写出全量，但它会在即时性判据上被抓住）。
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


def tabs_now(c):
    """当前 preview 底部页签名列表（DOM 顺序 = 页签打开顺序）。"""
    return _loads_deep(c.eval("""(() => {
      const els = [...document.querySelectorAll('%s')];
      return JSON.stringify(els.map(e => e.textContent.trim()));
    })()""" % TAB_NAME)) or []


def wait_tabs(c, names, max_wait=15):
    """等待 preview 底部页签栏出现 names 中的全部文件名；返回当前页签名列表。"""
    tabs = []
    deadline = time.time() + max_wait
    while time.time() < deadline:
        tabs = tabs_now(c)
        if all(any(n in t for t in tabs) for n in names):
            return tabs
        time.sleep(0.5)
    return tabs


def open_file_until(c, work_dir, name, max_wait=20):
    """发 `file-open` 直到该文件页签出现（**逐文件**幂等重发）。

    必要性（2026-11-07 实跑定位）：`/ping ready`/`.panel-inner` 只代表宿主与主聊天面板就绪，
    预览区 CodeView 是独立懒加载 chunk，负载下可晚数秒挂载；**挂载前发出的 `file-open` 没有
    订阅者，被 mq 直接丢弃**（无任何落库/页签副作用）。实测两个后果：
      · I-81 采样连续 `[]`（两事件都丢）；
      · 边界用例只开出后一个 → 落库 `['b.txt']`（前一个 a 丢）。
    逐文件重发既修复丢弃，又**保持打开顺序**（a 确认出现后才发 b）。
    """
    path = os.path.join(work_dir, name).replace("\\", "/")
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if any(name in t for t in tabs_now(c)):
            return
        c.mq_emit("file-open", {"path": path})
        time.sleep(0.15)
    raise TestError("CodeView 未就绪：%ss 内 file-open(%s) 未产生页签（订阅缺失/视图未挂载）"
                    % (max_wait, name))


def install_save_spy(c):
    """安装/清空 `gui.ui.save` 请求 spy（返回 'ok'）。"""
    c.eval(SAVE_SPY_JS)


def save_spy(c):
    """读取 spy 记录：list[{t(页面 Date.now), files}]。"""
    return _loads_deep(c.eval("JSON.stringify(window.__ckSaveSpy||[])")) or []


def wait_save_issued(c, names, max_wait=1.0):
    """有界等待 spy 中出现一条 `opened_files` **同时含** names 的请求；返回该记录或 None。

    轮询窗口放宽到 1s 只是容忍**测试通道 eval**在重载下变慢；即时性判据用的是页内时钟
    （`__ckEmitAt` → spy.t），与轮询耗时无关，故不会放宽 ≤100ms 的断言。
    """
    deadline = time.time() + max_wait
    while time.time() < deadline:
        for r in save_spy(c):
            files = [str(x) for x in (r.get("files") or [])]
            if all(any(n in f for f in files) for n in names):
                return r
        time.sleep(0.01)
    return None


def wait_prj_opened_stable(c, expected, max_wait=10, hold=1.0):
    """等待 prj `opened-files` **稳定等于** expected（连续 hold 秒一致才返回）。

    为什么要「稳定」而非「一旦命中即返回」：启动恢复期多个全量写并发落库时，
    瞬态可能先出现正确的 [a,b]，随后被更早发出、更晚落地的 [a] 覆盖。
    只读一次会把瞬态当收敛态（P6 断言失效），故要求期望值**持续**保持。
    """
    expected = list(expected)
    deadline = time.time() + max_wait
    since = None
    last = []
    while time.time() < deadline:
        last = prj_opened_files(c)
        if last == expected:
            since = since or time.time()
            if time.time() - since >= hold:
                return last
        else:
            since = None
        time.sleep(0.25)
    return last


def main():
    prepare_ws()
    proc = start_gui()
    try:
        c = wait_port(PORT)
        install_save_spy(c)
        # 逐文件确认打开（CodeView 懒挂载期间发出的 file-open 会被丢弃；见 open_file_until）。
        # 先开 a 并等其首写落库，把「首写队列/往返」从 I-81 的被测窗口排除。
        open_file_until(c, WORK_DIR, "a.txt")
        of_a = wait_prj_opened_stable(c, ["a.txt"], max_wait=10, hold=0.5)
        if of_a != ["a.txt"]:
            raise TestError("I-81 前置：单独打开 a.txt 后落库应为 ['a.txt']，实际=%r" % of_a)

        # I-81 专项（2026-09-17；2026-11-07 加固）：打开 b 后**立即**发出含 a+b 的**全量**写。
        # 判据分两层（均按期望值，不对实现反推）：
        #   ① 即时性（≤100ms）：页内时钟测 `gui.ui.save{opened_files}` **请求发出**延迟——
        #      旧实现（legacy 单元素写 `[b]` 即时 + 300ms 去抖全量写）此窗口内只会发出 `[b]`
        #      或干脆没有全量请求 → 必失败；
        #   ② 收敛性：该全量写最终落库为 [a.txt, b.txt]（异步面，有界等待）。
        b_path = os.path.join(WORK_DIR, "b.txt").replace("\\", "/")
        hit = None
        lat_ms = None
        for attempt in (1, 2):  # 仅容忍极端重载下的事件丢失（重发不改变判据）
            install_save_spy(c)
            c.eval("window.__ckEmitAt=Date.now(); 'ok'")
            c.mq_emit("file-open", {"path": b_path})
            hit = wait_save_issued(c, ("a.txt", "b.txt"))
            emit_at = int(_loads_deep(c.eval("window.__ckEmitAt")) or 0)
            if hit and emit_at:
                lat_ms = int(hit["t"]) - emit_at
                break
            time.sleep(0.3)
        if not hit:
            raise TestError("I-81 回归：打开 b.txt 后未发出含 a.txt+b.txt 的 gui.ui.save 全量写"
                            "（旧去抖/单元素写；spy=%r）" % (save_spy(c),))
        print("I-81 全量写请求发出延迟：%dms · files=%r" % (lat_ms, hit.get("files")))
        if lat_ms > 100:
            raise TestError("I-81 回归：全量写请求发出延迟 %dms > 100ms（疑似退回 300ms 去抖）" % lat_ms)
        ok81, samples = fast_read_after_open(c, ("a.txt", "b.txt"), window=2.0)
        print("I-81 落库收敛采样(t_ms, prj opened-files): %s" % json.dumps(samples, ensure_ascii=False))
        if not ok81:
            raise TestError("I-81 回归：打开 a.txt/b.txt 后 prj opened-files 未收敛为两者（采样 %s）" % samples)
        print("I-81 断言通过：请求 %dms 内发出全量写，落库收敛为 a.txt+b.txt" % lat_ms)
        time.sleep(1.0)
        tabs1 = wait_tabs(c, ["a.txt", "b.txt"])
        # 期望值（不从实现反推）：打开顺序 a.txt → b.txt ⇒ 页签名与顺序 = ['a.txt', 'b.txt']。
        if tabs1 != ["a.txt", "b.txt"]:
            raise TestError(f"打开后 tab 名/顺序不符（期望 ['a.txt','b.txt']）：{tabs1}")
        print(f"打开文件 tab: {tabs1}")
        # A 落库回读（P6 补齐）：opened-files 键确实写入 prj 库（tab 名只是 DOM 现象）。
        # 期望值 = 打开顺序 + 全量列表（workdir 相对逻辑路径），逐项逐序比对。
        of1 = wait_prj_opened_stable(c, ["a.txt", "b.txt"])
        if of1 != ["a.txt", "b.txt"]:
            raise TestError(f"prj opened-files 未按 [a.txt,b.txt] 落库：{of1}")
        print(f"prj opened-files 落库回读: {of1}")
    finally:
        stop_gui(proc)

    # 重启（同 work-dir + 同 data-dir → 同 prj 库；独立于其它实例）
    proc2 = start_gui()
    try:
        c2 = wait_port(PORT)
        tabs2 = wait_tabs(c2, ["a.txt", "b.txt"])
        # 期望值：恢复的页签名与**顺序** == 重启前落库的打开顺序 ['a.txt', 'b.txt']。
        if tabs2 != ["a.txt", "b.txt"]:
            raise TestError(f"重启后 tab 名/顺序不符（期望 ['a.txt','b.txt']）：{tabs2}")
        print(f"重启后恢复 tab: {tabs2}")
        # A 落库回读（重启后）：同一数据根下 opened-files **稳定**为全量两文件（tab 恢复的持久化依据）。
        # 首读留证：启动恢复期逐文件全量写并发时，「先写 [a]」可能后落地覆盖 [a,b]（本次实跑缺陷），
        # 故这里要求期望值持续 hold 秒，而不是瞬态命中即放行。
        first2 = prj_opened_files(c2)
        of2 = wait_prj_opened_stable(c2, ["a.txt", "b.txt"])
        if of2 != ["a.txt", "b.txt"]:
            raise TestError(f"重启后 prj opened-files 丢失/乱序（期望 ['a.txt','b.txt']）：first={first2} final={of2}")
        print(f"重启后 prj opened-files 回读: first={first2} final={of2}")
    finally:
        stop_gui(proc2)
        shutil.rmtree(BASE, ignore_errors=True)

    # 边界：恢复时缺失文件跳过（独立前置：先正常落库 [a,b]，再删 b 重启）。
    missing_file_skip_case()
    print("\nL88 重启恢复：通过")
    return 0


def missing_file_skip_case():
    """边界：已落库的页签中某个文件在重启前被删除 → 恢复时**跳过该文件**且落库收敛为现存项。

    期望值（产品口径 `callLoadInitData`：`os.Stat` 不存在的路径不恢复）：
      · 页签只恢复 a.txt（不得因持久化里仍有 b.txt 而出现空/错误页签）；
      · prj `opened-files` 最终 = ['a.txt']（恢复时全量写把已删项收敛掉，不留死路径）。
    """
    prepare_ws(BASE_SKIP, WORK_SKIP, DATA_SKIP)
    h1 = start_gui(WORK_SKIP, DATA_SKIP)
    try:
        c = wait_port(PORT)
        # 逐文件确认打开（懒挂载期间的 file-open 会丢；实测丢前一个 → 落库退化 ['b.txt']）
        open_file_until(c, WORK_SKIP, "a.txt")
        open_file_until(c, WORK_SKIP, "b.txt")
        of = wait_prj_opened_stable(c, ["a.txt", "b.txt"])
        if of != ["a.txt", "b.txt"]:
            raise TestError(f"边界前置：落库应为 ['a.txt','b.txt']，实际={of}")
    finally:
        stop_gui(h1)

    b_path = os.path.join(WORK_SKIP, "b.txt")
    if not os.path.exists(b_path):
        raise TestError("边界前置：b.txt 应存在")
    os.remove(b_path)

    h2 = start_gui(WORK_SKIP, DATA_SKIP)
    try:
        c2 = wait_port(PORT)
        tabs = wait_tabs(c2, ["a.txt"])
        time.sleep(1.5)  # 覆盖可能的迟发恢复（若实现错误地恢复了 b，此窗口内必然出现）
        tabs = tabs_now(c2)
        if tabs != ["a.txt"]:
            raise TestError(f"缺失文件应跳过（期望仅 ['a.txt']）：{tabs}")
        of2 = wait_prj_opened_stable(c2, ["a.txt"])
        if of2 != ["a.txt"]:
            raise TestError(f"缺失文件应从 opened-files 收敛掉（期望 ['a.txt']）：{of2}")
        print(f"边界（缺失文件跳过）：删除 b.txt 后重启 → tab={tabs} · opened-files={of2}")
    finally:
        stop_gui(h2)
        shutil.rmtree(BASE_SKIP, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
