#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""chonkpilot-mcp-tools 执行器 L4 系统测试（现行工具面，2026-09-11 按契约重写）。

被测执行体为 spawn-on-call 的 executor exe，进程级黑盒调用契约：

    <exe> <tool> --input=<参数 JSON 文件>   # 结果输出到 stdout（JSON 或文本）
    退出码：0 = 成功；1 = 失败（失败详情在 stdout）；2 = 用法错误（成功路径不出现）

现行工具面（契约源 src/initdata/capability/tools/**/*.tool.md）共 8 个：
    core    : file_read / file_find / file_diff / filesys_run / script_run / web_fetch
    browser : browser_run
    desktop : desktop_run
（历史脚本调用的 file_write/grep/diff/patch/replace/remove/rename/directory_*/fetch 及
 desktop 的 windows_list/window_find/... 等已随工具面收敛删除，本脚本不再引用。）

路径约束（决策 R-11）：工具与 DSL 内所有文件/目录参数须为 ① 绝对路径 ② `~/` 开头
③ `!/` 开头（临时目录）之一；相对路径 → 顶层失败（本脚本含回归用例）。
调用上下文经子进程环境变量注入：CHONKPILOT_INSTANCE 决定 `!/` 落地根，
CHONKPILOT_WORKDIR 供 `{{env.CHONKPILOT_WORKDIR}}` 拼项目内绝对路径。

用法：
    python run_executor_tests.py            # core 六工具（无外部依赖；web_fetch 走本机回环）
    python run_executor_tests.py --all      # 追加 browser_run / desktop_run 冒烟（需 headless Chrome / 交互桌面）
返回：0 = 全部通过（或全部跳过）；1 = 存在失败。
"""
import argparse
import http.server
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile
import threading

# 运行实例 id：决定 `!/` 落地根 <系统 temp>/chonkpilot/<INSTANCE>/（与 CHONKPILOT_INSTANCE 同源）。
INSTANCE = "systest-l4"
RUN_TIMEOUT = 180

# 脚本位于 src/test/chonkpilot-mcp-tools/systest/，仓库根在 parents[4]。
REPO = pathlib.Path(__file__).resolve().parents[4]
# 部署布局候选（[41 D-28] 源/产物分区后）：`dist/other`（`build-mcp-server.ps1` 现行产物，与 mcp-gateway
# 同目录共用 capability）→ `dist/desktop`（桌面单体发行目录，亦含 `capability/executors/`）→
# `dist/mcp-server`（历史布局，保留回落）。
# 内置 executor 现统一落 `<capability>/executors/`（扁平命名 chonkpilot-<cat>-executor.exe）。
CAP_CANDS = [
    REPO / "dist" / "other" / "capability" / "executors",
    REPO / "dist" / "desktop" / "capability" / "executors",
    REPO / "dist" / "mcp-server" / "capability" / "executors",
]
CAP = next((c for c in CAP_CANDS if c.exists()), CAP_CANDS[-1])


def _first_existing(cands):
    for c in cands:
        if c.exists():
            return c
    return None


# 被测 exe：按候选布局逐个探测（executors 扁平目录），回落源码侧历史位置。
def _cap_exe(cat, name):
    return _first_existing([c / name for c in CAP_CANDS])


CORE_EXE = _first_existing([_cap_exe("core", "chonkpilot-core-executor.exe"),
                            REPO / "src" / "mcp-tools" / "chonkpilot-core-executor.exe"])
BROWSER_EXE = _cap_exe("browser", "chonkpilot-browser-executor.exe")
DESKTOP_EXE = _cap_exe("desktop", "chonkpilot-desktop-executor.exe")

passed = 0
failed = 0
skipped = 0
failures = []


# ─────────────────────────── 驱动与断言基础 ───────────────────────────

def base_env(extra=None):
    """构造 executor 子进程环境：剥离父进程 CHONKPILOT_*，注入本次调用上下文。

    TMP/TEMP 归一到 Python 的 tempfile.gettempdir()，保证 `!/` 落地根与脚本计算一致。
    extra 覆盖同名变量（如测试 `~/` 展开时的 USERPROFILE）。
    """
    env = {k: v for k, v in os.environ.items() if not k.startswith("CHONKPILOT_")}
    env["CHONKPILOT_INSTANCE"] = INSTANCE
    tmp = tempfile.gettempdir()
    env["TMP"] = tmp
    env["TEMP"] = tmp
    # 子进程 python 输出统一 UTF-8，避免 Windows 管道默认 locale 编码导致中文断言失败。
    env["PYTHONUTF8"] = "1"
    env["PYTHONIOENCODING"] = "utf-8"
    if extra:
        env.update(extra)
    return env


def run(exe, tool, args, env=None):
    """执行 executor，返回 (exit_code, stdout, stderr)。args 经临时 JSON 文件传入。"""
    if exe is None:
        raise AssertionError("被测 exe 未定位到（先执行 build-mcp-server.ps1）")
    fd, in_p = tempfile.mkstemp(prefix="ckexec-in-", suffix=".json")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(dict(args), f, ensure_ascii=False)
        p = subprocess.run(
            [str(exe), tool, f"--input={in_p}"],
            capture_output=True, text=True, encoding="utf-8", errors="replace",
            timeout=RUN_TIMEOUT, env=(env if env is not None else base_env()),
        )
        return p.returncode, p.stdout, p.stderr
    finally:
        os.remove(in_p)


def as_json(out):
    """stdout 为 JSON 时返回 dict，否则 None。"""
    s = (out or "").strip()
    if not s:
        return None
    try:
        return json.loads(s)
    except json.JSONDecodeError:
        return None


def check(name, fn):
    global passed, failed
    try:
        fn()
    except Exception as e:  # noqa: BLE001
        failed += 1
        failures.append(f"{name}: {e!r}")
        print(f"  FAIL  {name}: {e!r}")
        return
    passed += 1
    print(f"  PASS  {name}")


def skip(name, reason):
    global skipped
    skipped += 1
    print(f"  SKIP  {name}（{reason}）")


def assert_ok(rc, out, err, what):
    assert rc == 0, f"{what}: 期望退出码 0，实际 {rc}；stdout={out!r} stderr={err!r}"


def assert_reject(rc, out, needle):
    """R-11 等顶层失败断言：退出码非 0 且消息含原值。"""
    assert rc != 0, f"期望非 0 退出，实际 0；stdout={out!r}"
    assert needle in out, f"错误消息应含 {needle!r}；stdout={out!r}"


# ─────────────────────────── 文件读写辅助 ───────────────────────────

def write_text(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8", newline="") as f:  # newline="" 不做 \n → \r\n 转换
        f.write(text)


def write_utf16le(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(b"\xff\xfe" + text.encode("utf-16-le"))


def read_text(path):
    with open(path, "r", encoding="utf-8", newline="") as f:
        return f.read()


def fwd(p):
    """DSL 脚本内嵌路径：反斜杠 → 正斜杠（规避 DSL 引号串对 `\\n`/`\\t` 的转义歧义）。"""
    return p.replace("\\", "/")


def norm_path(p):
    """路径归一化（统一分隔符 + Clean），用于比较结果显示路径与本地绝对路径。"""
    return os.path.normpath(str(p).replace("/", os.sep))


def has_path(entries, target, key="path"):
    """entries 中是否存在 path（key）归一化后等于 target 的条目。"""
    t = norm_path(target)
    return any(norm_path(e.get(key, "")) == t for e in entries)


# ─────────────────────────── file_read ───────────────────────────

def suite_file_read(work):
    ok_file = os.path.join(work, "fr", "a.txt")
    write_text(ok_file, "hello\nworld\n中文内容\n")

    def t_content():
        rc, out, err = run(CORE_EXE, "file_read", {"files": [{"path": ok_file}]})
        res = as_json(out)
        assert res and res.get("status") == "success", f"rc={rc} out={out!r} err={err!r}"
        f0 = res["files"][0]
        assert "中文内容" in f0.get("content", ""), f0
        assert f0.get("md5"), f"缺少 md5: {f0}"
        assert f0.get("encoding") == "UTF-8", f0

    def t_info():
        rc, out, _ = run(CORE_EXE, "file_read", {"files": [{"path": ok_file, "info": True}]})
        res = as_json(out)
        assert rc == 0 and res and res.get("status") == "success", out
        f0 = res["files"][0]
        assert "content" not in f0, f"info 模式不应返回正文: {f0}"
        assert f0.get("lines") == 3 and f0.get("size", 0) > 0, f0

    def t_range():
        rc, out, _ = run(CORE_EXE, "file_read",
                         {"files": [{"path": ok_file, "start": 1, "limit": 2, "line_numbers": True}]})
        res = as_json(out)
        assert rc == 0 and res and res.get("status") == "success", out
        content = res["files"][0].get("content", "")
        assert "hello" in content and "world" in content, content
        assert "中文内容" not in content, f"limit 未生效: {content!r}"

    def t_utf16():
        p = os.path.join(work, "fr", "u16.txt")
        write_utf16le(p, "第一行\nhello\n")
        rc, out, _ = run(CORE_EXE, "file_read", {"files": [{"path": p}]})
        res = as_json(out)
        assert rc == 0 and res and res.get("status") == "success", out
        f0 = res["files"][0]
        assert "第一行" in f0.get("content", "") and "hello" in f0.get("content", ""), f0
        assert f0.get("encoding") == "UTF-16LE", f0

    def t_tilde():
        write_text(os.path.join(work, "tilde.txt"), "tilde-ok\n")
        rc, out, err = run(CORE_EXE, "file_read", {"files": [{"path": "~/tilde.txt"}]},
                           env=base_env({"USERPROFILE": work}))
        res = as_json(out)
        assert rc == 0 and res and res.get("status") == "success", f"rc={rc} out={out!r} err={err!r}"
        assert "tilde-ok" in res["files"][0].get("content", ""), res

    def t_temp_prefix():
        root = os.path.join(tempfile.gettempdir(), "chonkpilot", INSTANCE)
        write_text(os.path.join(root, "tmp-ok.txt"), "temp-prefix-ok\n")
        rc, out, _ = run(CORE_EXE, "file_read", {"files": [{"path": "!/tmp-ok.txt"}]})
        res = as_json(out)
        assert rc == 0 and res and res.get("status") == "success", out
        assert "temp-prefix-ok" in res["files"][0].get("content", ""), res

    def t_relative_rejected():
        rc, out, _ = run(CORE_EXE, "file_read", {"files": [{"path": "a.txt"}]})
        assert_reject(rc, out, "绝对路径")
        assert "a.txt" in out, out

    def t_missing_files():
        rc, out, _ = run(CORE_EXE, "file_read", {})
        assert_reject(rc, out, "array")

    def t_nonexistent():
        rc, out, _ = run(CORE_EXE, "file_read",
                         {"files": [{"path": os.path.join(work, "fr", "__nope__.txt")}]})
        res = as_json(out)
        assert rc != 0 and res and res.get("status") == "fail", f"rc={rc} out={out!r}"

    for fn in [t_content, t_info, t_range, t_utf16, t_tilde, t_temp_prefix,
               t_relative_rejected, t_missing_files, t_nonexistent]:
        check(f"[file_read] {fn.__name__[2:]}", fn)


# ─────────────────────────── file_find ───────────────────────────

def suite_file_find(work):
    root = os.path.join(work, "findroot")
    write_text(os.path.join(root, "a.txt"), "alpha\n")
    write_text(os.path.join(root, "b.go"), "beta\n")
    write_text(os.path.join(root, "sub", "c.txt"), "TODO in c\n")
    write_text(os.path.join(root, "sub", "d.log"), "delta\n")

    def t_default_file():
        rc, out, err = run(CORE_EXE, "file_find", {"path": root})
        assert_ok(rc, out, err, "file_find 默认 file 模式")
        assert "a.txt" in out and "c.txt" in out, out

    def t_glob():
        rc, out, _ = run(CORE_EXE, "file_find", {"path": root, "glob": "*.txt"})
        assert rc == 0, out
        assert "a.txt" in out and "c.txt" in out, out
        assert "b.go" not in out, f"glob 未过滤: {out!r}"

    def t_depth1():
        rc, out, _ = run(CORE_EXE, "file_find", {"path": root, "depth": 1})
        assert rc == 0, out
        assert "a.txt" in out and "b.go" in out, out
        assert "c.txt" not in out, f"depth=1 应仅当前目录: {out!r}"

    def t_grep_summary():
        rc, out, _ = run(CORE_EXE, "file_find", {"path": root, "grep": "TODO", "output": "summary"})
        assert rc == 0, out
        assert "c.txt" in out and "TODO in c" in out, out
        assert "a.txt" not in out, f"grep 不应命中无匹配文件: {out!r}"

    def t_tree():
        rc, out, _ = run(CORE_EXE, "file_find", {"path": root, "output": "tree"})
        assert rc == 0, out
        for want in (".", "sub/", "a.txt"):
            assert want in out, f"tree 缺 {want!r}: {out!r}"

    def t_no_match():
        rc, out, _ = run(CORE_EXE, "file_find", {"path": root, "glob": "*.nomatch"})
        assert rc == 0 and "no matches" in out, out

    def t_relative_rejected():
        rc, out, _ = run(CORE_EXE, "file_find", {"path": "src"})
        assert_reject(rc, out, "绝对路径")
        assert "src" in out, out

    for fn in [t_default_file, t_glob, t_depth1, t_grep_summary, t_tree,
               t_no_match, t_relative_rejected]:
        check(f"[file_find] {fn.__name__[2:]}", fn)


# ─────────────────────────── file_diff ───────────────────────────

def suite_file_diff(work):
    x = os.path.join(work, "diff", "x.txt")
    y = os.path.join(work, "diff", "y.txt")
    write_text(x, "a\nb\nc\n")
    write_text(y, "a\nb\nC\n")

    def t_file_pair():
        rc, out, err = run(CORE_EXE, "file_diff", {"file1": x, "file2": y})
        assert_ok(rc, out, err, "file_diff file1+file2")
        assert "--- " in out and "+++ " in out, out
        assert "-c" in out and "+C" in out, out

    def t_files_array():
        rc, out, _ = run(CORE_EXE, "file_diff", {"files": [{"path": x, "path2": y}]})
        assert rc == 0 and "-c" in out and "+C" in out, out

    def t_path_array():
        rc, out, _ = run(CORE_EXE, "file_diff", {"path": [x, y]})
        assert rc == 0 and "+C" in out, out

    def t_identical():
        rc, out, _ = run(CORE_EXE, "file_diff", {"file1": x, "file2": x})
        assert rc == 0, out

    def t_no_args():
        rc, out, _ = run(CORE_EXE, "file_diff", {})
        assert_reject(rc, out, "provide")

    def t_relative_rejected():
        rc, out, _ = run(CORE_EXE, "file_diff", {"file1": "a.txt", "file2": "b.txt"})
        assert_reject(rc, out, "绝对路径")
        assert "a.txt" in out, out

    for fn in [t_file_pair, t_files_array, t_path_array, t_identical,
               t_no_args, t_relative_rejected]:
        check(f"[file_diff] {fn.__name__[2:]}", fn)


# ─────────────────────────── filesys_run ───────────────────────────

def suite_filesys_run(work):
    base = os.path.join(work, "fsr")

    def _script(*lines):
        return "\n".join(lines)

    def t_ins_create():
        p = os.path.join(base, "ins.txt")
        rc, out, err = run(CORE_EXE, "filesys_run",
                           {"script": f'INS #"{fwd(p)}" "hello\\n世界\\n"'})
        assert_ok(rc, out, err, "filesys_run INS")
        res = as_json(out)
        assert res and res.get("status") == "success", out
        assert has_path(res.get("created", []), p), f"created 缺条目: {res}"
        assert read_text(p) == "hello\n世界\n", read_text(p)

    def t_rpl():
        p = os.path.join(base, "rpl.txt")
        write_text(p, "foo bar foo\n")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'RPL #"{fwd(p)}" "foo" "qux"'})
        assert rc == 0, out
        res = as_json(out)
        assert has_path(res.get("modified", []), p), res
        assert read_text(p) == "qux bar qux\n", read_text(p)

    def t_apd():
        p = os.path.join(base, "apd.txt")
        write_text(p, "line1\n")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'APD #"{fwd(p)}" "line2"'})
        assert rc == 0, out
        assert read_text(p) == "line1\nline2", read_text(p)

    def t_del_line():
        p = os.path.join(base, "delline.txt")
        write_text(p, "keep\nremove-me\nalso\n")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'DEL #"{fwd(p)}" "remove-me"'})
        assert rc == 0, out
        content = read_text(p)
        assert "remove-me" not in content and "keep" in content, content

    def t_mov():
        src = os.path.join(base, "old.txt")
        dst = os.path.join(base, "new.txt")
        write_text(src, "x")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'MOV #"{fwd(src)}" #"{fwd(dst)}"'})
        assert rc == 0, out
        res = as_json(out)
        assert has_path(res.get("created", []), dst), res
        assert any(norm_path(d) == norm_path(src) for d in res.get("deleted", [])), res
        assert os.path.exists(dst) and not os.path.exists(src), res

    def t_cpy():
        src = os.path.join(base, "src.txt")
        dst = os.path.join(base, "dst.txt")
        write_text(src, "copy-me\n")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'CPY #"{fwd(src)}" #"{fwd(dst)}"'})
        assert rc == 0, out
        assert os.path.exists(src) and os.path.exists(dst), out
        assert read_text(dst) == "copy-me\n", read_text(dst)

    def t_del_file():
        p = os.path.join(base, "del.txt")
        write_text(p, "x")
        rc, out, _ = run(CORE_EXE, "filesys_run", {"script": f'DEL #"{fwd(p)}"'})
        assert rc == 0, out
        assert any(norm_path(d) == norm_path(p) for d in as_json(out).get("deleted", [])), out
        assert not os.path.exists(p), out

    def t_ins_existing_fails():
        p = os.path.join(base, "exists.txt")
        write_text(p, "already\n")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'INS #"{fwd(p)}" "new"'})
        assert rc == 0, out
        res = as_json(out)
        assert res.get("fails"), f"已存在应记 fails: {res}"
        assert "已存在" in json.dumps(res["fails"], ensure_ascii=False), res

    def t_md5_mismatch_stays_fails():
        p = os.path.join(base, "md5.txt")
        write_text(p, "original")
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": f'RPL #"{fwd(p)}" "original" "changed"',
                          "md5": {p: "deadbeef"}})
        assert rc == 0, f"md5 不一致应整体成功: {out!r}"
        assert "MD5 不一致" in out, out
        assert read_text(p) == "original", f"不应写盘: {read_text(p)!r}"

    def t_relative_dsl_rejected():
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": 'RPL #"src/main.py" "a" "b"'})
        assert_reject(rc, out, "绝对路径")
        assert "src/main.py" in out, out

    def t_relative_dataref_rejected():
        rc, out, _ = run(CORE_EXE, "filesys_run",
                         {"script": 'LOOP row=#"rows.csv".lines\n   SET "s" => last\nEND\n'})
        assert_reject(rc, out, "绝对路径")
        assert "rows.csv" in out, out

    def t_missing_script():
        rc, out, _ = run(CORE_EXE, "filesys_run", {})
        assert_reject(rc, out, "required")

    def t_env_workdir():
        csv = os.path.join(base, "rows.csv")
        write_text(csv, "a\nb\n")
        script = _script('LOOP row=#"{{env.CHONKPILOT_WORKDIR}}/rows.csv".lines',
                         '   SET "s" => last',
                         'END')
        rc, out, err = run(CORE_EXE, "filesys_run", {"script": script},
                           env=base_env({"CHONKPILOT_WORKDIR": base}))
        assert_ok(rc, out, err, "filesys_run env 拼绝对路径")

    for fn in [t_ins_create, t_rpl, t_apd, t_del_line, t_mov, t_cpy, t_del_file,
               t_ins_existing_fails, t_md5_mismatch_stays_fails,
               t_relative_dsl_rejected, t_relative_dataref_rejected,
               t_missing_script, t_env_workdir]:
        check(f"[filesys_run] {fn.__name__[2:]}", fn)


# ─────────────────────────── script_run ───────────────────────────

def suite_script_run(work):
    interp = sys.executable  # 绝对路径（R-11）

    def t_python_print():
        rc, out, err = run(CORE_EXE, "script_run",
                           {"runtime": "python", "script": "print('hello 中文')",
                            "interpreter": interp})
        assert_ok(rc, out, err, "script_run python")
        assert "hello 中文" in out, out

    def t_shell_echo():
        rc, out, err = run(CORE_EXE, "script_run",
                           {"runtime": "shell", "script": "echo hello-shell"})
        assert_ok(rc, out, err, "script_run shell")
        assert "hello-shell" in out, out

    def t_both_script_and_file():
        f = os.path.join(work, "sr", "s.py")
        write_text(f, "print(1)\n")
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "print(1)", "file": f,
                          "interpreter": interp})
        assert rc != 0 and "仅有一个" in out, out

    def t_neither():
        rc, out, _ = run(CORE_EXE, "script_run", {"runtime": "python", "interpreter": interp})
        assert rc != 0 and "仅有一个" in out, out

    def t_unconfigured_runtime():
        rc, out, _ = run(CORE_EXE, "script_run", {"runtime": "java", "script": "x"})
        assert rc != 0 and "未配置" in out, out

    def t_invalid_runtime():
        rc, out, _ = run(CORE_EXE, "script_run", {"runtime": "haskell", "script": "x"})
        assert rc != 0 and "不支持" in out, out

    def t_env():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python",
                          "script": "import os; print(os.environ['CK_TEST_ENV'])",
                          "env": {"CK_TEST_ENV": "env-ok"}, "interpreter": interp})
        assert rc == 0 and "env-ok" in out, out

    def t_args():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "import sys; print(sys.argv[1])",
                          "args": ["arg-ok"], "interpreter": interp})
        assert rc == 0 and "arg-ok" in out, out

    def t_workdir():
        sub = os.path.join(work, "sr", "sub")
        os.makedirs(sub, exist_ok=True)
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "import os; print(os.getcwd())",
                          "workdir": sub, "interpreter": interp})
        assert rc == 0 and "sub" in out, out

    def t_file_mode():
        f = os.path.join(work, "sr", "f.py")
        write_text(f, "print('file-mode')\n")
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "file": f, "interpreter": interp})
        assert rc == 0 and "file-mode" in out, out

    def t_exit_code():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "import sys; sys.exit(3)",
                          "interpreter": interp})
        assert rc != 0 and "exit 3" in out, out

    def t_filter():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python",
                          "script": "print('alpha')\nprint('beta-1')\nprint('gamma')\n",
                          "filter": "beta", "interpreter": interp})
        assert rc == 0 and "beta-1" in out, out
        assert "alpha" not in out and "gamma" not in out, out

    def t_filter_invalid():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "print(1)", "filter": "([",
                          "interpreter": interp})
        assert rc != 0 and "regex" in out, out

    def t_overflow_redirects_to_temp():
        # 超限纯文本：executor 统一层写系统 temp 临时文件并给出提示（exit 0）
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "print('y' * 250000)\n",
                          "interpreter": interp})
        assert rc == 0, f"超限不应失败: {out!r}"
        assert "已保存至" in out, out
        m = re.search(r"已保存至 (.+?)（", out)
        assert m and os.path.exists(m.group(1).strip()), f"临时文件不存在: {out!r}"

    def t_relative_workdir_rejected():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "print(1)", "workdir": "sub",
                          "interpreter": interp})
        assert_reject(rc, out, "绝对路径")

    def t_relative_interpreter_rejected():
        rc, out, _ = run(CORE_EXE, "script_run",
                         {"runtime": "python", "script": "print(1)", "interpreter": "python"})
        assert_reject(rc, out, "绝对路径")

    for fn in [t_python_print, t_shell_echo, t_both_script_and_file, t_neither,
               t_unconfigured_runtime, t_invalid_runtime, t_env, t_args, t_workdir,
               t_file_mode, t_exit_code, t_filter, t_filter_invalid,
               t_overflow_redirects_to_temp, t_relative_workdir_rejected,
               t_relative_interpreter_rejected]:
        check(f"[script_run] {fn.__name__[2:]}", fn)


# ─────────────────────────── web_fetch（本机回环，无外网依赖） ───────────────────────────

_PAGE_HTML = "<html><head><title>CK-TITLE</title></head><body><h1 id='h'>hi</h1></body></html>"


class _EchoHandler(http.server.BaseHTTPRequestHandler):
    def _send(self, body, ctype="text/plain; charset=utf-8"):
        data = body.encode("utf-8") if isinstance(body, str) else body
        self.send_response(200)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/gbk":
            self._send("中文内容".encode("gbk"), "text/plain; charset=gbk")
        elif self.path == "/page":
            self._send(_PAGE_HTML, "text/html; charset=utf-8")
        else:
            self._send("GET-OK " + self.path)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length).decode("utf-8", "replace")
        self._send(f"POST-OK {body} UA={self.headers.get('User-Agent', '')}")

    def log_message(self, *a):
        pass


def _start_echo_server():
    srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _EchoHandler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{srv.server_port}"


def suite_web_fetch(work):
    srv, base = _start_echo_server()
    try:
        def t_get():
            rc, out, err = run(CORE_EXE, "web_fetch", {"url": base + "/hello"})
            assert_ok(rc, out, err, "web_fetch GET")
            assert "HTTP 200" in out and "GET-OK /hello" in out, out

        def t_post_user_agent():
            rc, out, _ = run(CORE_EXE, "web_fetch",
                             {"url": base + "/p", "method": "POST", "body": "BODY"})
            assert rc == 0 and "POST-OK BODY" in out, out
            assert "ChonkPilot" in out, out

        def t_save_as():
            dst = os.path.join(work, "wf", "down.txt")
            rc, out, err = run(CORE_EXE, "web_fetch", {"url": base + "/dl", "save_as": dst})
            assert_ok(rc, out, err, "web_fetch save_as")
            assert "已下载到" in out, out
            assert read_text(dst) == "GET-OK /dl", read_text(dst)

        def t_encoding_gbk():
            rc, out, _ = run(CORE_EXE, "web_fetch", {"url": base + "/gbk", "encoding": "gbk"})
            assert rc == 0 and "中文内容" in out, out

        def t_missing_url():
            rc, out, _ = run(CORE_EXE, "web_fetch", {})
            assert_reject(rc, out, "url")

        def t_relative_save_as_rejected():
            rc, out, _ = run(CORE_EXE, "web_fetch", {"url": base + "/dl", "save_as": "down.txt"})
            assert_reject(rc, out, "绝对路径")
            assert "down.txt" in out, out

        for fn in [t_get, t_post_user_agent, t_save_as, t_encoding_gbk,
                   t_missing_url, t_relative_save_as_rejected]:
            check(f"[web_fetch] {fn.__name__[2:]}", fn)
    finally:
        srv.shutdown()


# ─────────────────────────── browser_run / desktop_run（可选） ───────────────────────────

def suite_browser(include):
    if not include:
        skip("[browser_run] 冒烟", "需 headless Chrome/Edge，默认跳过；加 --all 启用")
        return
    if BROWSER_EXE is None:
        skip("[browser_run] 冒烟", "未定位到 chonkpilot-browser-executor.exe")
        return
    srv, base = _start_echo_server()
    try:
        def t_smoke():
            script = f'OPN "{base}/page"\nEXP page title "CK-TITLE"\nEXP css="#h" text "hi"'
            rc, out, err = run(BROWSER_EXE, "browser_run", {"script": script})
            assert_ok(rc, out, err, "browser_run 冒烟")
        check("[browser_run] 冒烟（本地页面断言）", t_smoke)
    finally:
        srv.shutdown()


def suite_desktop(include):
    if not include:
        skip("[desktop_run] 冒烟", "需交互桌面会话，默认跳过；加 --all 启用")
        return
    if DESKTOP_EXE is None:
        skip("[desktop_run] 冒烟", "未定位到 chonkpilot-desktop-executor.exe")
        return

    def t_smoke():
        rc, out, err = run(DESKTOP_EXE, "desktop_run", {"script": "WIN list"})
        assert_ok(rc, out, err, "desktop_run 冒烟")
        assert out.strip(), "WIN list 输出为空"
    check("[desktop_run] 冒烟（WIN list）", t_smoke)


# ─────────────────────────── 入口 ───────────────────────────

def main():
    parser = argparse.ArgumentParser(description="chonkpilot-mcp-tools 执行器 L4 系统测试")
    parser.add_argument("--all", action="store_true",
                        help="追加 browser_run / desktop_run 冒烟（需 headless Chrome / 交互桌面）")
    args = parser.parse_args()

    if CORE_EXE is None:
        print(f"缺少可执行文件：{CAP / 'chonkpilot-core-executor.exe'}"
              f"（先执行 build-mcp-server.ps1）")
        return 2
    print(f"被测 core executor: {CORE_EXE}")
    print(f"运行实例: CHONKPILOT_INSTANCE={INSTANCE}，临时根={tempfile.gettempdir()}")

    print("== core executor（无头/无外部依赖） ==")
    with tempfile.TemporaryDirectory(prefix="cksys-") as work:
        suite_file_read(work)
        suite_file_find(work)
        suite_file_diff(work)
        suite_filesys_run(work)
        suite_script_run(work)
        suite_web_fetch(work)

    print("== browser / desktop（可选） ==")
    suite_browser(args.all)
    suite_desktop(args.all)

    print(f"\n== 汇总: {passed} 通过, {failed} 失败, {skipped} 跳过 ==")
    if failures:
        print("失败明细:")
        for f in failures:
            print(f"  - {f}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
