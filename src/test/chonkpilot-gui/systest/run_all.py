"""全量回归运行器：依次执行 systest/gui 下所有 test_*.py + run_*.py，汇总结果。

用法 / 分组（2026-10-05 增分组开关；**默认仍为全量**，不改变既有习惯）：
  * `python run_all.py`                → `--group all`（等价默认）：core + sem 全量。
  * `python run_all.py --group core`   → 仅 `test_*.py` + 非 sem 的 `run_*.py`。
  * `python run_all.py --group sem`    → 仅语义/回环级 `run_sem_*.py`。
  * `python run_all.py --fast`         → 快速子集（精选少量确定性套件；**忽略 --group**）。
  * `-k SUBSTR`（可多次）              → 仅跑名字含 SUBSTR 的套件（与 --group/--fast 叠加过滤）。
  * `--list`                           → 只列出各分组将执行的脚本名后退出。
退出码：有 FAIL 才非零；SKIP 不算失败。

运行方式 / 隔离（2026-09-28 纳入 `run_*` 套件）：
  * **逐个子进程串行**（`cwd=HERE`）：任一时刻只有一个套件在跑，故各套件**不复用彼此**的
    实例/端口/临时目录 —— 天然无并发争用（这是本运行器唯一的隔离手段：串行 + 各套件自管资源）。
  * `test_*.py`：动态端口（`harness.free_port()`）+ 套件级配置快照-还原。
  * `run_*.py`：
      - 共享底座型（`run_config_ui` / `run_project_cfg` / `run_ui_regressions` / `run_fp_extra_ui` /
        `run_tool_async_config`）
        统一 `_h.acquire_gui(2345, ...)`：**复用优先**，无实例才自起并在退出时回收；串行执行
        → 固定端口 2345 在不同套件间「起-停」交替，不并存（`run_config_ui` 另起独立实例 2347/临时目录，
        并在清理时显式保护 2345，绝不波及底座）。
      - 自起型（`run_hist_git` / `run_index_gate` / `run_filetree_ignored` / `run_docs_gate`）：动态端口 +
        独立 `--data-dir`/临时 work-dir/独立 HOME（`run_hist_git` 另起自管 mock LLM；`run_docs_gate`
        另自拉/自停真转换器，产物缺失时 preflight SKIP）→ 与共享底座完全隔离，退出即回收。
      - **语义/回环级型**（`run_sem_*.py`，20 个）：自起 GUI（动态端口 + 独立临时 work-dir/data-dir/HOME），
        断言「语义内容 / 写-读回环」而非「元素存在」；夹具全落临时目录、结束即弃；产物缺失时自报 SKIP。

**SKIP ≠ PASS**：运行器按**原始字节**透传每个子进程输出，并据子进程自报的 `(SKIPPED` 标记（`run_*.py`
的 `RESULT: True (SKIPPED: ...)` 约定）把该套件记为 **SKIP**（既不计入 pass，也不判失败）。整轮退出码
= 有 FAIL 才非零；SKIP 不算失败。汇总显式区分 PASS / SKIP / FAIL 三态。
"""

import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))

# 第一批：`test_*.py`（动态端口 + 套件级快照-还原；`drive.GUIClient`）。
SCRIPTS = [
    "test_window.py",
    "test_layout.py",
    "test_toolbar.py",
    "test_filetree.py",
    "test_filetree_interact.py",
    "test_filetree_dnd.py",
    "test_filetree_ctxmenu.py",
    "test_chat.py",
    "test_session_bridge.py",
    "test_task.py",
    "test_session_drawer.py",
    "test_statusbar.py",
    "test_config.py",
    "test_turn_nav.py",
    "test_chat_flow.py",
    "test_queue.py",
    "test_filetree_watcher.py",
    "test_richtext.py",
    "test_screenshot.py",
    "test_chat_input_top.py",
    "test_preview.py",
    "test_tool_retry.py",
]

# 第二批：`run_*.py`（端到端/UI 断言套件；隔离与端口分配见模块 docstring）。
RUN_SCRIPTS = [
    "run_config_ui.py",           # 共享底座 2345（复用优先）+ 独立实例 2347
    "run_project_cfg.py",         # 共享底座 2345
    "run_ui_regressions.py",      # 共享底座 2345
    "run_fp_extra_ui.py",         # 共享底座 2345
    "run_tool_async_config.py",   # 共享底座 2345（工具配置页：usr `tool_async` 读/写/效果）
    "run_index_gate.py",          # 自起（动态端口 + 临时 work-dir/data-dir/HOME）
    "run_docs_gate.py",           # 自起（动态端口 + 临时 work-dir/data-dir/HOME；真转换器自拉/自停；产物缺失则 SKIP）
    "run_filetree_ignored.py",    # 自起（动态端口 + 临时 work-dir/data-dir/HOME）
    "run_wizard_auto.py",         # 自起（动态端口 + 临时 work-dir/data-dir/HOME；**刻意不预置** project_spec → 验向导自动弹出）
    "run_explore_kb.py",          # 自起（临时 work-dir/data-dir；扩展页四级 5 子 tab + C14 项目私有端到端）
    "run_hist_git.py",            # 自起（动态端口 + 临时目录 + 自管 mock LLM；无 git 则 SKIP）
]

# 第三批：语义/回环级套件（`run_sem_*.py`）：自起 GUI（动态端口 + 临时 work-dir/data-dir/HOME），
# 断言「语义/回环」而非「元素存在」；夹具全落临时目录，结束即弃；产物缺失则 SKIP。
SEM_SCRIPTS = [
    "run_sem_messages.py",        # 会话/消息：内容片段 + markdown 渲染 + 历史回环（§3.2/§4.3）
    "run_sem_tool_result.py",     # 工具执行结果：真实执行 stdout → 渲染 → 历史回读（§4.3/§3.2）
    "run_sem_retry_state.py",     # 会话工具中断/重试态：徽标语义 + 重试事件契约 + 状态回环（§4.2）
    "run_sem_tabs_loop.py",       # 预览页签切换：激活态↔内容对应 + 切回保持（页签集持久回读）
    "run_sem_filetree_loop.py",   # 文件树/目录操作回环：消息写 → 磁盘 → 树 → 读回 → 删除闭合（§2）
    "run_sem_kb_loop.py",         # 知识库/原语读写回环：写→落盘→读回 + 能力面可见（§3.3/§4.5）
    "run_sem_config_loop.py",     # 设置页配置持久化回环：写→落库→UI 重开→跨重启回读（§3.1/§6.1）
    "run_sem_wizard.py",          # 场景向导：走完一遍 → 产物落盘 + 各步选项非空（刻意不预置 project_spec）
    "run_sem_scenario_ref.py",    # 场景编辑页 ref 型 agent：非空→编辑→保存→重开值一致 + ref 不变（§3.1/§3.3）
    "run_sem_prompt_optimise.py", # 提示词优化流式回环：增量拼接==optimize-done.prompt + 内容级 + 真链路（§1）
    "run_sem_memory_edit.py",     # 记忆库类别编辑回环：写→读回→磁盘落盘→列表→删除闭合（§3.1）
    "run_sem_task_tree.py",       # 任务树/委派结果回读：树结构 → 委派子任务 → 结果回写父节点（§4.4）
    "run_sem_kb_skill_loop.py",   # 知识库原语 skill/prompt 读写回环：写→落盘→读回 + 能力面移除（§3.3）
    "run_sem_search_upload.py",   # 搜索/上传结果语义：上传落盘 + filelist 入索引 + 搜索命中（§2/§3.3）
    "run_sem_window_layout_loop.py",  # 窗口与布局持久化回环：尺寸写→落库→重启 DOM 对应 + 显隐切换 + 主题/语言落库广播（31）
    "run_sem_session_loop.py",    # 会话生命周期回环：建/重命名广播/活动切换/删除闭合/跨重启（33 对话）
    "run_sem_filetree_tree.py",   # 项目树展示语义 + 预览页签回环：隐藏过滤/排序 + 临时·固定页签（32 文件树）
    "run_sem_task_status_loop.py",  # 任务节点状态语义 + 逻辑删除只读回环：图标文案迁移 + 「已关闭」（34 任务）
    "run_sem_error_recovery.py",  # 错误终态恢复入口语义：空回复/不可重试/可重试（手动↔自动）分类（35 错误处理）
    "run_sem_config_fallback.py", # 配置项写→回读→删→回落回环：usr 精确值落库 + 重置继承（36 配置）
]

# 分组定义（`--group`）：core = 交互/端到端（test_* + 非 sem 的 run_*）；sem = 语义/回环级；all = 全量。
GROUPS = {
    "all": SCRIPTS + RUN_SCRIPTS + SEM_SCRIPTS,
    "core": SCRIPTS + RUN_SCRIPTS,
    "sem": list(SEM_SCRIPTS),
}

# `--fast` 快速子集：精选**确定性高、体量小**的套件（跨 UI/消息/文件树/配置/预览语义面），
# 用于日常改动后的快速自证；**非全量替代**（完整门禁仍跑 `--group all`）。
FAST = [
    "test_window.py",
    "test_statusbar.py",
    "run_sem_messages.py",
    "run_sem_filetree_loop.py",
    "run_sem_config_loop.py",
]


def _run_script(path):
    """运行单个子套件：实时透传输出（原始字节）并探测 SKIP 标记。

    返回 (returncode, skipped)。SKIP 判据 = 子进程自报的 `RESULT: True (SKIPPED: ...)`
    约定（纯 ASCII 标记，按原始字节匹配 → 不受子进程 stdout 编码影响）。透传用原始字节
    保证与「子进程直连终端」（capture_output=False）逐字节一致，不引入父进程编码干扰。
    """
    env = dict(os.environ)
    env["PYTHONUNBUFFERED"] = "1"  # PIPE 下子进程默认块缓冲 → 逐行刷新以保实时性
    r = subprocess.Popen([sys.executable, path], cwd=HERE, env=env,
                         stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    buf = getattr(sys.stdout, "buffer", None)
    skipped = False
    for raw in r.stdout:
        if buf is not None:
            sys.stdout.flush()
            buf.write(raw)
            buf.flush()
        else:  # 非常规 stdout（无 buffer）→ 退回文本模式
            sys.stdout.write(raw.decode("utf-8", "replace"))
        if b"(SKIPPED" in raw:
            skipped = True
    r.wait()
    return r.returncode, skipped


def _select_scripts(args):
    """按 --fast / --group / -k 选出本次要跑的脚本名（保序、去重）。"""
    base = list(FAST) if args.fast else list(GROUPS[args.group])
    if args.k:
        base = [n for n in base if any(s in n for s in args.k)]
    seen, out = set(), []
    for n in base:
        if n not in seen:
            seen.add(n)
            out.append(n)
    return out


def main(argv=None):
    import argparse
    ap = argparse.ArgumentParser(description="回归运行器：串行执行 systest 套件；SKIP≠PASS")
    ap.add_argument("--group", choices=sorted(GROUPS), default="all",
                    help="分组：all（默认·全量）/ core（交互+端到端）/ sem（语义/回环级）")
    ap.add_argument("--fast", action="store_true",
                    help="快速子集（精选少量确定性套件；忽略 --group）")
    ap.add_argument("-k", action="append", default=[], metavar="SUBSTR",
                    help="仅跑名字含 SUBSTR 的套件（可多次，取并集）")
    ap.add_argument("--list", action="store_true", help="只列出将执行的脚本名后退出")
    args = ap.parse_args(argv)

    scripts = _select_scripts(args)
    tag = "fast" if args.fast else args.group
    if args.list:
        print("分组[%s] 共 %d 个套件：" % (tag, len(scripts)))
        for n in scripts:
            print("  " + n)
        return 0
    print("分组[%s] 共 %d 个套件" % (tag, len(scripts)))

    results = []
    for name in scripts:
        path = os.path.join(HERE, name)
        if not os.path.exists(path):
            print(f"[SKIP] {name} 不存在")
            results.append((name, "skip"))
            continue
        print(f"\n===== {name} =====")
        t0 = time.time()
        rc, skipped = _run_script(path)
        dt = time.time() - t0
        if rc == 0 and skipped:
            state = "skip"
        else:
            state = "pass" if rc == 0 else "fail"
        print(f"----- {name} -> {'PASS' if state == 'pass' else ('SKIP' if state == 'skip' else 'FAIL')} ({dt:.0f}s) -----")
        results.append((name, state))

    print("\n\n===== 汇总 =====")
    label = {"pass": "PASS", "fail": "FAIL", "skip": "SKIP"}
    passed = sum(1 for _, s in results if s == "pass")
    skipped = sum(1 for _, s in results if s == "skip")
    failed = sum(1 for _, s in results if s == "fail")
    for name, s in results:
        print(f"[{label[s]:>4}] {name}")
    print(f"\n== {passed} passed / {skipped} skipped / {failed} failed (共 {len(results)}) ==")
    if skipped:
        print(f"   注：{skipped} 项 SKIP（前置不满足，非失败；SKIP 不计入 passed）")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())

