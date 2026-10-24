# -*- coding: utf-8 -*-
"""testplan.md 七.x 数据根归属测试（--data-dir 分离 + **数据根下沉（DATAROOT-1）**）。

前置：chonkpilot.exe 可用（GUI 模式）。
用例：
  一.1 缺省（**不传 --data-dir**）：prj 库恒留 `<workdir>/.chonkpilot/chonkpilot.db`；
      prjusr 库 + 日志 + 附件/截图 + 备份落
      `<workdir>/.chonkpilot/users/<OS 用户名>/`（个人运行态不再进 `~/.chonkpilot/data/<project-id>`，
      `project-id` 已整体删除）。对应 MW-T19/20/21。
  一.2 分离模式（绝对）：--data-dir=<独立目录> → 数据根独立，workdir 零污染。
  一.3 分离模式（相对）：--data-dir=data → <workdir>/data。
  一.5 ~ 展开：--data-dir=~/ck-dd-home → <home>/ck-dd-home。

显式 --data-dir 形态（一.2/一.3/一.5）数据根「内容」断言口径见 _assert_root_layout
（2026-09-15 迁移：db 合法性 + gui.upload 落盘）；缺省形态见 _assert_default_layout。
"""

import base64
import json
import os
import shutil
import sys
import tempfile
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case
import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）

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
    False（分离模式）：数据根必须在 workdir 外 → workdir 下不得出现数据根标志（chonkpilot.db）；
    `<workDir>/.chonkpilot` 本身允许存在（项目级 capability 根 + 单实例锁，见 60 §115 / 19 §8.11）。
    """
    db = os.path.join(root, PROJ_DB)
    if not os.path.isfile(db):
        raise TestError(f"数据根缺少项目主库 {PROJ_DB}: {root}")

    # ② 项目库可用性（真读一次 prj 层；比静态文件存在更强）
    res = cli.req("data-prj-config-load", {})
    if not isinstance(res, dict):
        raise TestError(f"data-prj-config-load 返回异常（数据根项目库不可用）: {res!r}")

    # ③ 运行时附件落盘到数据根 tmp/uploads（server 决定落盘位置，只回 name）
    payload = b"ck-data-dir-probe\n"
    res = cli.req("gui.upload", {
        "name": "dd_probe.txt",
        "data": base64.b64encode(payload).decode("ascii"),
        "kind": "file",
    })
    name = (res or {}).get("name") if isinstance(res, dict) else None
    if not name:
        raise TestError(f"gui.upload 未返回 name: {res!r}")
    up_dir_raw = os.path.join(root, "tmp", "uploads")
    path = os.path.join(up_dir_raw, str(name))
    up_dir = os.path.normcase(os.path.normpath(up_dir_raw))
    got = os.path.normcase(os.path.normpath(os.path.dirname(path)))
    if got != up_dir:
        raise TestError(f"附件应落在数据根 tmp/uploads：期望 {up_dir}，实际 {got}")
    if not os.path.isfile(path):
        raise TestError(f"附件未落盘: {path}")
    with open(path, "rb") as f:
        if f.read() != payload:
            raise TestError(f"附件内容不一致: {path}")

    if not expect_in_workdir:
        # 分离模式：**数据根**不得落在 workdir（--data-dir 必须生效）。
        # 注（2026-10-17 对齐实现）：`<workDir>/.chonkpilot` 可能被创建，属**既有规格**：
        #   ① 项目级 capability 根固定 `<workDir>/.chonkpilot/capability`（60-名词约定 §115，
        #      与 --data-dir 无关）；
        #   ② 项目单实例锁恒取 `<workDir>/.chonkpilot/chonkpilot.lock`（与 flash 同路径互斥，
        #      与 --data-dir 解耦，见 19 §8.11 / 62 §3A）。
        # → 断言改为：workdir 下不得出现**数据根标志**（项目主库 chonkpilot.db）。
        wd_ck = os.path.join(workdir, ".chonkpilot")
        if os.path.isfile(os.path.join(wd_ck, PROJ_DB)):
            raise TestError(f"数据根落回 workdir（{os.path.join(wd_ck, PROJ_DB)}），--data-dir 未生效")
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


def _assert_default_layout(cli, workdir, home):
    """缺省 --data-dir（DATAROOT-1）落盘断言：

      ① prj 库恒留项目内：`<workdir>/.chonkpilot/chonkpilot.db`，且 **config 不再有 `project-id`**；
      ② prjusr 库落 `<workdir>/.chonkpilot/users/<OS 用户名>/chonkpilot.db`（与 prj 不同文件）；
      ③ 日志随 prjusr 根：`<prjusr 根>/logs/gui.log` 存在，`gui.init-data` 的 logDir 指向该处；
      ④ 附件落 `<prjusr 根>/tmp/uploads`，且可经 `/show/` 取回（fileserver 白名单可达）；
      ⑤ `<workdir>/.chonkpilot` 下**不得**平铺会话/日志/附件/备份（个人运行态在 `users/<用户名>/`）；
      ⑥ 配置自动备份（`gui.file.save` mode=backup）落 `<prjusr 根>/backup/`。
    """
    # ① prj 库（项目内）+ project-id 已删除
    prj_db = os.path.join(workdir, ".chonkpilot", PROJ_DB)
    if not os.path.isfile(prj_db):
        raise TestError(f"prj 库应在项目内: {prj_db}")
    pid = (_h.prj_config_load(cli) or {}).get("project-id") or ""
    if pid:
        raise TestError(f"prj 库不应再有 project-id（DATAROOT-1 已删除）: {pid!r}")

    # ② prjusr 数据根（= <workdir>/.chonkpilot/users/<OS 用户名>）
    #    OS 用户名 = 应用进程 os.UserHomeDir() 的 basename；用例里 home 被隔离 → 取 basename(home)。
    user_name = os.path.basename(os.path.normpath(home))
    root = os.path.join(workdir, ".chonkpilot", "users", user_name)
    prjusr_db = os.path.join(root, PROJ_DB)
    if not os.path.isfile(prjusr_db):
        raise TestError(f"prjusr 库应在 {prjusr_db}")
    if os.path.normcase(os.path.normpath(prjusr_db)) == os.path.normcase(os.path.normpath(prj_db)):
        raise TestError(f"prjusr 与 prj 不应同库: {prjusr_db}")

    # ③ 日志跟随 prjusr 根
    log_dir = os.path.join(root, "logs")
    if not os.path.isfile(os.path.join(log_dir, "gui.log")):
        raise TestError(f"日志应在 {log_dir}/gui.log")
    init = cli.req("gui.init-data", {})
    got_log_dir = str((init or {}).get("logDir", "")) if isinstance(init, dict) else ""
    if os.path.normcase(os.path.normpath(got_log_dir)) != os.path.normcase(os.path.normpath(log_dir)):
        raise TestError(f"gui.init-data.logDir 应 = {log_dir}，实际 {got_log_dir!r}")

    # ④ 附件落 prjusr 根 tmp/uploads + /show/ 经**相对引用**可达（server 只回 name）
    payload = b"ck-default-layout-probe\n"
    res = cli.req("gui.upload", {
        "name": "dd_probe.txt",
        "data": base64.b64encode(payload).decode("ascii"),
        "kind": "file",
    })
    name = (res or {}).get("name") if isinstance(res, dict) else None
    if not name:
        raise TestError(f"gui.upload 未返回 name: {res!r}")
    up_dir_raw = os.path.join(root, "tmp", "uploads")
    path = os.path.join(up_dir_raw, str(name))
    up_dir = os.path.normcase(os.path.normpath(up_dir_raw))
    got_dir = os.path.normcase(os.path.normpath(os.path.dirname(path)))
    if got_dir != up_dir:
        raise TestError(f"附件应落 prjusr 根 tmp/uploads：期望 {up_dir}，实际 {got_dir}")
    # 预览走相对引用 `tmp/uploads/<name>`（fileserver 按 UploadRoot 解析，见 61 §1 gui.upload）
    status, body = _show_fetch(cli, "tmp/uploads/" + str(name))
    if status != 200 or body != payload.decode("ascii"):
        raise TestError(f"/show/ 应取回附件（白名单可达）：status={status} body={body!r}")

    # ⑤ 项目数据根不得平铺个人运行态落点（应在 users/<用户名>/ 之下）
    for sub in ("tmp", "logs", "backup"):
        p = os.path.join(workdir, ".chonkpilot", sub)
        if os.path.exists(p):
            raise TestError(f"项目数据根不应平铺 {sub}/（个人运行态应落 users/<用户名>/）: {p}")

    # ⑥ 配置自动备份随 prjusr 根（gui.file.save mode=backup；[24 §3.2] MW-8 · 41 G-43②(b)）
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
    """一.1 缺省（不传 --data-dir）：prj 留项目内；prjusr 库/日志/附件落 <workdir>/.chonkpilot/users/<用户名>/。"""
    base = os.path.join(tempfile.gettempdir(), "ck-dd-default")
    ws = os.path.join(base, "ws")
    home = os.path.join(base, "home")  # 隔离 ~（USERPROFILE/HOME）→ 用户名 = basename(home)
    os.makedirs(ws, exist_ok=True)
    os.makedirs(home, exist_ok=True)
    proc = None
    try:
        proc, cli = _spawn_ide(_h.free_port(), ws, home=home)
        time.sleep(1.0)
        _assert_default_layout(cli, ws, home)
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
        # 分离模式：数据根在 workdir 外 → 必须校验 workdir 零污染（2026-10-19 补强，
        # 此前漏传 expect_in_workdir=False，导致 index_queue 在 workdir 下建库未被发现）
        _assert_root_layout(cli, data, ws, expect_in_workdir=False)
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
    total += 1; ok += run_case("一.1 缺省：prj 留项目内 + prjusr 落 <workdir>/.chonkpilot/users/<用户名>/", case_default_mode)
    total += 1; ok += run_case("一.2 分离模式（绝对）：数据根平铺 + workdir 零污染", case_separate_absolute)
    total += 1; ok += run_case("一.3 分离模式（相对）：<workdir>/data 平铺", case_separate_relative)
    total += 1; ok += run_case("一.5 ~ 展开：--data-dir=~/ck-dd-home → <home>/ck-dd-home", case_home_expand)
    print(f"\n--data-dir 数据根：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
