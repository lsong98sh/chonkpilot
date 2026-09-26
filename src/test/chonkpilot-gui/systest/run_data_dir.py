# -*- coding: utf-8 -*-
"""testplan.md 七.x 数据根归属测试（--data-dir 分离 + **数据层 B 方案**）。

前置：chonkpilot.exe 可用（GUI 模式，无需 LLM）；console 模式用例无需 LLM 配置。
用例：
  一.1 B 方案（**不传 --data-dir**，[24 §3] MW-7/MW-8）：prj 库恒留
       `<workdir>/.chonkpilot/chonkpilot.db`；prjusr 库 + 日志 + 附件/截图落
       `<home>/.chonkpilot/data/<project-id>/`（个人运行态不进项目目录）。对应 MW-T19/20/21。
  一.2 分离模式（绝对）：--data-dir=<独立目录> → 数据根独立，workdir 零污染。
  一.3 分离模式（相对）：--data-dir=data → <workdir>/data。
  一.4 临时模式：console 裸 --data-dir + --prompt → 临时数据根创建于系统 temp，进程退出自动删除。
  一.5 ~ 展开：--data-dir=~/ck-dd-home → <home>/ck-dd-home。

显式 --data-dir 形态（一.2/一.3/一.5）数据根「内容」断言口径见 _assert_root_layout
（2026-09-15 迁移：db 合法性 + gui.upload 落盘）；缺省（B 方案）形态见 _assert_b_layout。
"""

import base64
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case
import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）

EXE = r"e:\BizWorks\chonkpilot\dist\desktop\chonkpilot.exe"
PROJ_DB = "chonkpilot.db"


def _spawn_ide(port, workdir, extra_args=None, home=None):
    """经 harness 自起 GUI 实例（--test-port + --work-dir [+ 额外参数]），返回 (handle, ChonkClient)。

    自起实例已登记：finally 的 _kill_proc 与进程退出/信号兜底都会回收。
    home 非空 → 隔离 `~`（USERPROFILE/HOME）→ `~/.chonkpilot` 落在该目录（B 方案断言用）。
    """
    h = _h.start_gui(port=port, work_dir=workdir, home=home,
                     extra_args=extra_args or (), ready_timeout=60)
    return h, h.client


def _kill_proc(h):
    if h is not None:
        h.stop()


def _assert_root_layout(cli, root, workdir, expect_in_workdir=True):
    """断言数据根的实际应然内容（2026-09-15 迁移，依据 spec）。

    旧断言要求数据根平铺 db/logs/tmp 三项。**现状与规格均无 `logs/` 产出点**
    （全仓无该写入路径；spec 12-数据层 / 60-名词约定 亦未定义）→ 改为断言现行应然事实，
    强度不降反升：
      ① `chonkpilot.db` = prj 主库文件已在数据根生成；
      ② 项目库**可用**：经 `data-prj-config-load` 真读一次 prj 层（打开并读写该项目库）；
      ③ 数据根 = **运行时落盘根**：经消息面 `gui.upload`（61-消息一览 §gui.upload
         「落盘数据根 tmp/uploads」）真写一份附件 → 断言返回 path 落在 `<数据根>/tmp/uploads`
         且文件内容一致（把旧的「tmp 目录静态存在」升级为真实写链路断言）；
      ④ 布局口径：数据根不得再嵌套 `.chonkpilot` 子层；分离模式 workdir 零污染。
    （注：不做 SQLite 头部 magic 校验——prj 库为 WAL 模式、主库页可驻留 .db-wal，
      新库头部可能尚未落盘；改为经 `data-prj-config-load` 真读一次项目库证明其可用。
      项目级 capability 根按 spec 60-名词约定 §115 固定为 `<workDir>/.chonkpilot/capability`，
      与 `--data-dir` 无关，故不作为数据根归属断言。）

    expect_in_workdir=True（默认模式）：数据根本身就是 <workdir>/.chonkpilot，不检查零污染；
    False（分离模式）：数据根必须在 workdir 外，workdir 不得出现 .chonkpilot。
    """
    db = os.path.join(root, PROJ_DB)
    if not os.path.isfile(db):
        raise TestError(f"数据根缺少项目主库 {PROJ_DB}: {root}")

    # ② 项目库可用性（真读一次 prj 层；比静态文件存在更强）
    res = cli.req("data-prj-config-load", {})
    if not isinstance(res, dict):
        raise TestError(f"data-prj-config-load 返回异常（数据根项目库不可用）: {res!r}")

    # ③ 运行时附件落盘到数据根 tmp/uploads
    payload = b"ck-data-dir-probe\n"
    res = cli.req("gui.upload", {
        "name": "dd_probe.txt",
        "data": base64.b64encode(payload).decode("ascii"),
        "kind": "file",
    })
    path = (res or {}).get("path") if isinstance(res, dict) else None
    if not path:
        raise TestError(f"gui.upload 未返回落盘路径: {res!r}")
    up_dir = os.path.normcase(os.path.normpath(os.path.join(root, "tmp", "uploads")))
    got = os.path.normcase(os.path.normpath(os.path.dirname(str(path))))
    if got != up_dir:
        raise TestError(f"附件应落在数据根 tmp/uploads：期望 {up_dir}，实际 {got}")
    if not os.path.isfile(path):
        raise TestError(f"附件未落盘: {path}")
    with open(path, "rb") as f:
        if f.read() != payload:
            raise TestError(f"附件内容不一致: {path}")

    if not expect_in_workdir:
        # workdir 零污染：不得出现 .chonkpilot
        if os.path.exists(os.path.join(workdir, ".chonkpilot")):
            raise TestError(f"workdir 被污染：{os.path.join(workdir, '.chonkpilot')}")
    # 数据根下不得再嵌套 .chonkpilot
    if os.path.exists(os.path.join(root, ".chonkpilot")):
        raise TestError(f"数据根不应包含 .chonkpilot 子层: {os.path.join(root, '.chonkpilot')}")


def _show_fetch(cli, abs_path, max_wait=8.0):
    """经页面内 fetch 走 `/show/` 拦截器取回文件 → 返回 (status, body)。

    证据口径：`/show/` 只经 WebView2 的 WebResourceRequested 拦截（虚拟源
    https://app.localhost，python 侧不可直连）→ 必须在页面上下文发请求。用一次性探针
    + 轮询（不依赖 eval 对 Promise 的等待语义）。
    """
    url = json.dumps("/show/" + str(abs_path).replace("\\", "/"))
    js = """(() => {
  if (!window.__ckShowProbe) {
    window.__ckShowProbe = { status: 0, body: '' };
    fetch(%s).then(r => r.text().then(t => { window.__ckShowProbe = { status: r.status, body: t }; }))
             .catch(e => { window.__ckShowProbe = { status: -1, body: String(e) }; });
  }
  return JSON.stringify(window.__ckShowProbe);
})()""" % url
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        raw = cli.eval(js, 5000)
        for _ in range(4):  # EvalWithResult 多重 JSON 编码 → 循环解包直到非字符串
            if not isinstance(raw, str):
                break
            try:
                parsed = json.loads(raw)
            except Exception:
                break
            raw = parsed
        last = raw if isinstance(raw, dict) else {"status": -2, "body": str(raw)}
        if last.get("status") not in (0, None):
            return int(last.get("status")), str(last.get("body", ""))
        time.sleep(0.3)
    raise TestError(f"/show/ 取回超时（{max_wait}s）: {last!r}")


def _assert_b_layout(cli, workdir, home):
    """B 方案（缺省 --data-dir，[24 §3.2] MW-7/MW-8）落盘断言：

      ① prj 库恒留项目内：`<workdir>/.chonkpilot/chonkpilot.db`（含 project-id）；
      ② prjusr 库落 `<home>/.chonkpilot/data/<project-id>/chonkpilot.db`；
      ③ 日志随数据根：`<prjusr 根>/logs/gui.log` 存在，且 `gui.init-data` 的 logDir 指向该处；
      ④ 附件落 `<prjusr 根>/tmp/uploads`，且可经 `/show/` 取回（fileserver 白名单可达）；
      ⑤ 项目数据根**不得**再出现会话/日志/附件/备份（个人运行态已迁走）；
      ⑥ 配置自动备份（`gui.file.save` mode=backup）落 `<prjusr 根>/backup/`。
    """
    # ① prj 库（项目内）+ project-id
    prj_db = os.path.join(workdir, ".chonkpilot", PROJ_DB)
    if not os.path.isfile(prj_db):
        raise TestError(f"prj 库应在项目内: {prj_db}")
    pid = (_h.prj_config_load(cli) or {}).get("project-id") or ""
    if not pid:
        raise TestError("prj 库 config 缺 project-id（prjusr 数据根无法绑定）")

    # ② prjusr 数据根（= <home>/.chonkpilot/data/<project-id>）
    root = os.path.join(home, ".chonkpilot", "data", pid)
    if not os.path.isfile(os.path.join(root, PROJ_DB)):
        raise TestError(f"prjusr 库应在 {root}/{PROJ_DB}")

    # ③ 日志跟随数据根（U-1 已决 2026-09-24）
    log_dir = os.path.join(root, "logs")
    if not os.path.isfile(os.path.join(log_dir, "gui.log")):
        raise TestError(f"日志应在 {log_dir}/gui.log")
    init = cli.req("gui.init-data", {})
    got_log_dir = str((init or {}).get("logDir", "")) if isinstance(init, dict) else ""
    if os.path.normcase(os.path.normpath(got_log_dir)) != os.path.normcase(os.path.normpath(log_dir)):
        raise TestError(f"gui.init-data.logDir 应 = {log_dir}，实际 {got_log_dir!r}")

    # ④ 附件落 prjusr 根 tmp/uploads + /show/ 可达
    payload = b"ck-b-layout-probe\n"
    res = cli.req("gui.upload", {
        "name": "b_probe.txt",
        "data": base64.b64encode(payload).decode("ascii"),
        "kind": "file",
    })
    path = (res or {}).get("path") if isinstance(res, dict) else None
    if not path:
        raise TestError(f"gui.upload 未返回落盘路径: {res!r}")
    up_dir = os.path.normcase(os.path.normpath(os.path.join(root, "tmp", "uploads")))
    got_dir = os.path.normcase(os.path.normpath(os.path.dirname(str(path))))
    if got_dir != up_dir:
        raise TestError(f"附件应落 prjusr 根 tmp/uploads：期望 {up_dir}，实际 {got_dir}")
    status, body = _show_fetch(cli, path)
    if status != 200 or body != payload.decode("ascii"):
        raise TestError(f"/show/ 应取回附件（白名单可达）：status={status} body={body!r}")

    # ⑤ 项目数据根不得出现个人运行态落点
    for sub in ("tmp", "logs", "backup"):
        p = os.path.join(workdir, ".chonkpilot", sub)
        if os.path.exists(p):
            raise TestError(f"项目数据根不应出现 {sub}/（B 方案后个人运行态落 prjusr 根）: {p}")

    # ⑥ 配置自动备份随 prjusr 根（gui.file.save mode=backup；[24 §3.2] MW-8 · 41 G-43②(b)）：
    # 导入 / 恢复出厂前的自动备份落 <prjusr 根>/backup/，不进项目数据根。
    res = cli.req("gui.file.save", {
        "name": "dd-backup-probe.json", "content": "{}", "mode": "backup",
    })
    bpath = (res or {}).get("path") if isinstance(res, dict) else None
    if not bpath:
        raise TestError(f"gui.file.save(backup) 未返回落盘路径: {res!r}")
    bk_dir = os.path.normcase(os.path.normpath(os.path.join(root, "backup")))
    got_bk = os.path.normcase(os.path.normpath(os.path.dirname(str(bpath))))
    if got_bk != bk_dir:
        raise TestError(f"配置备份应落 prjusr 根 backup/：期望 {bk_dir}，实际 {got_bk}")
    if not os.path.isfile(bpath):
        raise TestError(f"配置备份未落盘: {bpath}")


def case_default_mode():
    """一.1 B 方案（不传 --data-dir）：prj 留项目内；prjusr 库/日志/附件落 <home>/.chonkpilot/data/<prj-id>/。"""
    base = os.path.join(tempfile.gettempdir(), "ck-dd-default")
    ws = os.path.join(base, "ws")
    home = os.path.join(base, "home")  # 隔离 ~（USERPROFILE/HOME）→ prjusr 数据根可断言
    os.makedirs(ws, exist_ok=True)
    os.makedirs(home, exist_ok=True)
    proc = None
    try:
        proc, cli = _spawn_ide(_h.free_port(), ws, home=home)
        time.sleep(1.0)
        _assert_b_layout(cli, ws, home)
    finally:
        if proc:
            _kill_proc(proc)
        shutil.rmtree(base, ignore_errors=True)


def case_separate_absolute():
    """一.2 --data-dir=<独立绝对目录> → 数据根独立，workdir 零污染。"""
    base = os.path.join(tempfile.gettempdir(), "ck-dd-abs")
    ws = os.path.join(base, "ws")
    data = os.path.join(base, "data")
    os.makedirs(ws, exist_ok=True)
    os.makedirs(data, exist_ok=True)
    proc = None
    try:
        proc, cli = _spawn_ide(_h.free_port(), ws, [f"--data-dir={data}"])
        time.sleep(1.0)
        _assert_root_layout(cli, data, ws)
    finally:
        if proc:
            _kill_proc(proc)
        shutil.rmtree(base, ignore_errors=True)


def case_separate_relative():
    """一.3 --data-dir=data（相对）→ <workdir>/data。"""
    ws = os.path.join(tempfile.gettempdir(), "ck-dd-rel", "ws")
    os.makedirs(ws, exist_ok=True)
    proc = None
    try:
        proc, cli = _spawn_ide(_h.free_port(), ws, ["--data-dir=data"])
        time.sleep(1.0)
        # 相对形态下数据根在 workdir 内（data/），故不检查 workdir 零污染
        _assert_root_layout(cli, os.path.join(ws, "data"), ws, expect_in_workdir=True)
    finally:
        if proc:
            _kill_proc(proc)
        shutil.rmtree(os.path.dirname(ws), ignore_errors=True)


def case_temp_mode():
    """一.4 裸 --data-dir（console）→ 临时数据根创建于系统 temp，进程退出自动删除。

    LLM 指向本机不可达端点（127.0.0.1:1）强制快速失败：连接拒绝 → 报错退出，
    cleanupTempDB 在 defer 中执行，临时目录必须被删除。
    """
    ws = os.path.join(tempfile.gettempdir(), "ck-dd-temp", "ws")
    os.makedirs(ws, exist_ok=True)
    temp_root = os.path.join(tempfile.gettempdir(), "chonkpilot")
    try:
        os.makedirs(temp_root, exist_ok=True)
        before = set(os.listdir(temp_root))
        r = subprocess.run(
            [EXE, f"--work-dir={ws}", "--data-dir", "--prompt=hi",
             "--llm-protocol=openai", "--llm-model=no-such-model",
             "--llm-api-url=http://127.0.0.1:1", "--llm-api-key=x", "--output=json"],
            cwd=os.path.dirname(EXE), capture_output=True, text=True, timeout=180,
        )
        # LLM 连接失败 → 预期非零退出
        if r.returncode == 0:
            raise TestError("临时模式 LLM 连接失败应报错退出，却成功退出")
        after = set(os.listdir(temp_root))
        added = after - before
        if added:
            raise TestError(f"临时数据根未被清理，残留: {added}")
    finally:
        shutil.rmtree(os.path.dirname(ws), ignore_errors=True)


def case_home_expand():
    """一.5 ~ 展开：--data-dir=~/ck-dd-home → <home>/ck-dd-home。"""
    home = os.path.expanduser("~")
    ws = os.path.join(tempfile.gettempdir(), "ck-dd-home", "ws")
    os.makedirs(ws, exist_ok=True)
    data = os.path.join(home, "ck-dd-home")
    shutil.rmtree(data, ignore_errors=True)
    proc = None
    try:
        proc, cli = _spawn_ide(_h.free_port(), ws, ["--data-dir=~/ck-dd-home"])
        time.sleep(1.0)
        _assert_root_layout(cli, data, ws, expect_in_workdir=False)
    finally:
        if proc:
            _kill_proc(proc)
        shutil.rmtree(os.path.dirname(ws), ignore_errors=True)
        shutil.rmtree(data, ignore_errors=True)


def main():
    ok = 0
    total = 0
    total += 1; ok += run_case("一.1 B 方案：prj 留项目内 + prjusr 库/日志/附件落 ~/.chonkpilot/data/<id>/", case_default_mode)
    total += 1; ok += run_case("一.2 分离模式（绝对）：数据根平铺 + workdir 零污染", case_separate_absolute)
    total += 1; ok += run_case("一.3 分离模式（相对）：<workdir>/data 平铺", case_separate_relative)
    total += 1; ok += run_case("一.4 临时模式：临时数据根退出自动删除", case_temp_mode)
    total += 1; ok += run_case("一.5 ~ 展开：--data-dir=~/ck-dd-home → <home>/ck-dd-home", case_home_expand)
    print(f"\n--data-dir 数据根：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
