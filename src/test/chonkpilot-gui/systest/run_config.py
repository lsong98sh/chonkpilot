# -*- coding: utf-8 -*-
"""testplan.md 二/三/四、配置与场景 结合测试（mq 驱动 + DOM 断言）。

新架构（2026-09 重构）：
  - window.go RPC 已移除 → 一律走消息面（data-user-config / data-prj-config / data-scenario）。
  - 配置弹窗改为 preview 区 tab（CodeView kind=settings-*）：config-open → preview-tab-open
    {kind:'settings-llm'}；保存即时落库（edit-llm-save / edit-mcp-save / 参数 change），
    旧 `config-save` / `config-refresh` 事件已废弃，变更广播为 `data-<domain>-refresh`。
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345, work_dir=os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws"))
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：usr+prj 全量，退出前自动回滚

CFG_EVENTS = ["data-user-config-refresh", "data-prj-config-refresh", "data-scenario-refresh"]


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def wait_el(selector, max_wait=10, visible=True):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        info = c.exists(selector)
        if visible and info.get("count", 0) > 0:
            return info
        if not visible and info.get("count", 0) == 0:
            return info
        time.sleep(0.4)
    raise TestError(f"等待元素超时: {selector} (visible={visible})")


def _user_cfg():
    res = c.req("data-user-config-load", {})
    return (res.get("data") or {}) if isinstance(res, dict) else {}


def _llms():
    return list(_user_cfg().get("llms") or [])


def _save_llms(llms):
    c.req("data-user-config-save", {"data": {"llms": llms}})


def _remove_llm(name):
    _save_llms([l for l in _llms() if l.get("name") != name])


def _mcps():
    res = c.req("data-mcp-list", {})
    return list((res or {}).get("list") or [])


def _remove_mcp(name):
    # 2026-10-01 起 MCP 四级文件化（<级别>/capability/mcps/<名>.json）→ 按名删文件。
    c.req("data-mcp-delete", {"name": name})


def _open(kind, wait_sel=".settings-page", max_wait=12):
    """打开 preview 设置页（替代旧 config-open 弹窗）。

    先 close-all 预览页，确保以最新配置重新挂载（避免 keep-alive 旧实例导致列表陈旧）。
    """
    c.mq_emit("preview-tab-close-all")
    time.sleep(0.6)
    c.mq_emit("preview-tab-open", {"kind": kind})
    time.sleep(1.2)
    wait_el(wait_sel, max_wait=max_wait)
    time.sleep(0.8)


def _close_dialogs():
    """关闭遗留的编辑弹窗（多实例同时订阅同一取消事件 → 一次 emit 全关）。"""
    c.mq_emit("edit-llm-cancel")
    c.mq_emit("edit-mcp-cancel")
    time.sleep(0.6)


def _fill_field(label_cands, value):
    """在配置编辑弹窗（.form-layout）中按 label 文本（中文/英文候选）定位 input 并填值。"""
    r = _loads_deep(c.eval("""(() => {
      const cands = %s;
      const items = [...document.querySelectorAll('.form-layout .form-item')];
      const it = items.find(x => {
        const lb = x.querySelector('.form-label');
        return lb && cands.some(c => lb.textContent.includes(c));
      });
      const inp = it ? it.querySelector('input, textarea') : null;
      if (!inp) return 'not-found';
      const proto = inp instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(proto, 'value').set.call(inp, %s);
      inp.dispatchEvent(new Event('input', { bubbles: true }));
      inp.dispatchEvent(new Event('change', { bubbles: true }));
      return 'ok';
    })()""" % (json.dumps(label_cands, ensure_ascii=False), json.dumps(value))))
    if r != "ok":
        raise TestError(f"填表失败（label≈{label_cands}）: {r}")


# ── 三.6 / 三.1 显示层 ─────────────────────────────────────

def case_config_open_close():
    """三.6：打开 LLM 配置页（preview tab）→ 出现 → 含工具栏。"""
    _open("settings-llm")
    wait_el(".config-toolbar-actions")


def case_llm_list_display():
    """三.1（显示层）：LLM 列表显示条目（name/model）。mock 条目由 main 预置。

    配置页仅在挂载时 loadConfig（无 refresh 订阅），且 preview 页 keep-alive 不重挂；
    为使预置条目生效，先 reload 前端再打开（列表从 data-user-config-load 读取）。
    """
    _close_dialogs()
    try:
        c.eval("location.reload()", 3000)
    except Exception:
        pass
    time.sleep(6)
    c.wait_ready(30)
    _open("settings-llm")
    deadline = time.time() + 8
    body = ""
    while time.time() < deadline:
        body = c.eval('document.body.innerText')
        if "mock" in body and "mock-model" in body:
            return
        time.sleep(0.4)
    raise TestError(f"LLM 列表未显示 mock/mock-model：{body[:300]}")


def case_config_save_refresh():
    """三.6 变更传播：编辑 LLM 保存 → 落库 → 广播 data-user-config-refresh。"""
    _close_dialogs()
    c.mq_on_capture(CFG_EVENTS)
    _open("settings-llm")
    c.mq_emit("config-add-llm")
    wait_el(".form-layout")
    time.sleep(0.4)
    _fill_field(["Name", "名称"], "refresh-llm")
    _fill_field(["Model", "模型"], "refresh-model")
    c.mq_emit("edit-llm-save")
    ev = c.wait_events("data-user-config-refresh", n=1, max_wait=15, clear=True)
    if not ev:
        raise TestError("保存后未收到 data-user-config-refresh")
    names = [l.get("name") for l in _llms()]
    if "refresh-llm" not in names:
        raise TestError(f"保存未落库: {names}")
    _remove_llm("refresh-llm")


# ── 三.1 LLM 增删改查 ───────────────────────────────────────

def case_llm_crud():
    """三.1 LLM 增删改查：add → 填表 → 保存落库 → 展示 → 清理还原。"""
    _close_dialogs()
    _remove_llm("test-llm")  # 幂等清理
    try:
        _open("settings-llm")
        c.mq_emit("config-add-llm")
        wait_el(".form-layout")
        time.sleep(0.4)
        _fill_field(["Name", "名称"], "test-llm")
        _fill_field(["API Key", "API 密钥"], "test-key")
        _fill_field(["Model", "模型"], "test-model-x")
        _fill_field(["Base URL", "接口地址"], "http://127.0.0.1:9999/v1")
        _fill_field(["Max Output Tokens", "最大输出 Token"], "2048")
        c.mq_emit("edit-llm-save")
        time.sleep(1.0)
        t = [l for l in _llms() if l.get("name") == "test-llm"]
        if not t:
            raise TestError("test-llm 未落库")
        if t[0].get("model") != "test-model-x":
            raise TestError(f"test-llm.model={t[0].get('model')}，期望 test-model-x")
        if t[0].get("apiKey") != "test-key":
            raise TestError("test-llm.apiKey 未落库")
        # 保存后列表本地即时更新（onSave push → llms.value）
        body = c.eval('document.body.innerText')
        if "test-llm" not in body or "test-model-x" not in body:
            raise TestError("列表未见 test-llm/test-model-x")
    finally:
        _remove_llm("test-llm")


# ── 三.3 MCP 增删 ──────────────────────────────────────────

def case_mcp_crud():
    """三.3 MCP 增删：add → 填表 → 保存落库 → 清理。"""
    _close_dialogs()
    _remove_mcp("test_mcp")
    try:
        _open("settings-mcp")
        c.mq_emit("config-add-mcp")
        wait_el(".form-layout")
        time.sleep(0.4)
        _fill_field(["Name", "名称"], "test_mcp")
        _fill_field(["Server URL", "URL", "地址"], "http://127.0.0.1:9999/mcp")
        c.mq_emit("edit-mcp-save")
        time.sleep(1.0)
        names = [m.get("name") for m in _mcps()]
        if "test_mcp" not in names:
            raise TestError(f"MCP 未落库: {names}")
    finally:
        _remove_mcp("test_mcp")


# ── 三.5 通用参数 ──────────────────────────────────────────

def case_general_params():
    """三.5 通用参数：改用户级 responseTimeout（blur 即落库）。

    注：maxToolIterations 已从"通用参数"移至每个 LLM 条目（EditLLMDialog），
    本页用户级参数 = 超时重试四项；以 responseTimeout 验证即时落库链路。
    还原口径（2026-09-16）：走 harness 统一「快照-还原」（51 §6-8）——原值 == 系统默认
    （120，`persist_userconfig.go:191`）→ 删键回落兜底；原值非默认 → 写回原值。
    原实现用 `data-user-config-delete {"key": ...}` 属**错误载荷**：persist `reqKey` 只认
    `id`/`data.key`，缺 key → 触发「清空整份 usr 配置」（theme/locale/llms 一并被清）。
    """
    snap = _h.snapshot_user_config(c, ["responseTimeout"])
    try:
        _open("settings-params")
        r = _loads_deep(c.eval("""(() => {
          const rows = [...document.querySelectorAll('.param-row')];
          const row = rows.find(x => (x.querySelector('.param-label')||{}).textContent
                            && /response\\s*timeout|响应超时/i.test(x.querySelector('.param-label').textContent))
                      || rows[0];
          const inp = row ? row.querySelector('input') : null;
          if (!inp) return 'not-found';
          const proto = HTMLInputElement.prototype;
          Object.getOwnPropertyDescriptor(proto, 'value').set.call(inp, '123');
          inp.dispatchEvent(new Event('input', { bubbles: true }));
          inp.dispatchEvent(new Event('blur', { bubbles: true }));
          return 'ok';
        })()"""))
        if r != "ok":
            raise TestError(f"未找到 responseTimeout 输入框: {r}")
        time.sleep(1.2)
        v = _user_cfg().get("responseTimeout")
        if v != 123:
            raise TestError(f"responseTimeout={v}，期望 123")
    finally:
        _h.restore_user_config(c, snap)


# ── 四.x 项目配置页 ────────────────────────────────────────

def case_project_config_open():
    """四.x（入口层）：打开项目配置页（preview tab settings-project）。"""
    c.mq_emit("preview-tab-open", {"kind": "settings-project"})
    deadline = time.time() + 10
    found = None
    while time.time() < deadline:
        body = c.eval('document.body.innerText')
        if any(k in body for k in ("笔记", "安全", "上下文", "Notes", "Security", "Context")):
            found = body
            break
        time.sleep(0.4)
    if not found:
        raise TestError("项目配置页未出现（无 笔记/安全/上下文 Tab）")


# ── 六章 6.2 配置补充（C8-C10）───────────────────────────

def _spawn_ide(port, workdir, data_dir=None, home=None):
    """经 harness 自起子进程 IDE（独立 test-port/work-dir/data-dir[/home]），返回 (handle, ChonkClient)。

    home 非空 → 子进程以独立用户主目录启动（**USERPROFILE** 指临时目录）：usr 主库路径 =
    `os.UserHomeDir()/.chonkpilot/chonkpilot.db`（`chonkpilot-data/db.go:104 UserPath`，无 CLI 开关；
    `--data-dir` 仅重定位 prj/prjusr 层，见 `chonkpilot-data/db.go:136 ProjectPath`）→ 该实例
    usr 库/偏好**全新**，既读不到也不写入机器 `~/.chonkpilot`。
    自起实例已登记 harness：本用例 finally 的 _kill_proc 与进程退出/信号兜底都会回收。
    """
    h = _h.start_gui(port=port, work_dir=workdir, data_dir=data_dir, home=home, ready_timeout=60)
    return h, h.client


def _kill_proc(h):
    """回收 harness 句柄（taskkill /F /T 整棵进程树；复用的外部实例 owned=False → 不动）。"""
    if h is not None:
        h.stop()


def case_c8_missing_fields_defaults():
    """C8 配置缺字段兜底：**独立 usr 库**（全新用户主目录）→ load 回落**系统默认**。

    隔离口径（2026-09-16）：usr 主库 = `os.UserHomeDir()/.chonkpilot/chonkpilot.db`
    （`chonkpilot-data/db.go:104 UserPath`；无 CLI 开关，`--data-dir` 只重定位 prj/prjusr 层），
    故以 **USERPROFILE=临时目录** 起子实例（见 `_spawn_ide`）→ 其 usr 库全新（`seedMap` 三层
    均不预写，`chonkpilot-data/seed.go:39`）→ 断言针对**系统默认**而非机器偏好：
      `chonkpilot-data/internal/config/userconfig.go` userConfigSystemDefaults
      = theme`light`（2026-09-16 由 `system` 订正，原值无实现）/ responseTimeout`120` /
      streamTimeout`60` / retryCount`2`（退避间隔不自持 —— 经 router.RetryWait，无 retryDelay 键）。
    机器 `~/.chonkpilot`（本机 theme=dark）全用例零读写：用例结束后现场比对底座实例配置快照。
    """
    # 临时目录走 harness 唯一入口（`tmp_dir` 登记 → 退出时带重试回收，见 51 §6）：
    # 子实例 WebView2 profile / bbolt 句柄释放是**异步**的，本地 `rmtree(ignore_errors=True)`
    # 会静默失败 → `%TEMP%\ck-c8-*` 残留（实测 2026-09-17）。
    tmp_home = _h.tmp_dir("ck-c8-home-")
    tmp_ws = _h.tmp_dir("ck-c8-ws-")
    tmp_dd = _h.tmp_dir("ck-c8-data-")
    before = dict(_user_cfg())  # 机器 usr 配置快照（底座实例 = ~/.chonkpilot）
    proc = None
    try:
        proc, c2 = _spawn_ide(_h.free_port(), tmp_ws, tmp_dd, home=tmp_home)
        res = c2.req("data-user-config-load", {})
        cfg = (res.get("data") or {}) if isinstance(res, dict) else {}
        # ① 独立 usr 库证据：全新库无任何 llms 集合（未读到机器偏好）
        if cfg.get("llms"):
            raise TestError(f"usr 库非全新（读到 llms）: {cfg.get('llms')}")
        # ② 系统默认兜底（usr 层缺 key → userConfigSystemDefaults）
        if cfg.get("responseTimeout") != 120:
            raise TestError(f"responseTimeout={cfg.get('responseTimeout')}，期望系统默认 120")
        if cfg.get("streamTimeout") != 60:
            raise TestError(f"streamTimeout={cfg.get('streamTimeout')}，期望系统默认 60")
        if cfg.get("retryCount") is None:
            raise TestError(f"retryCount 缺兜底默认: {cfg}")
        if cfg.get("theme") != "light":
            raise TestError(f"theme={cfg.get('theme')!r}，期望系统默认 'light'")
    finally:
        if proc:
            _kill_proc(proc)
    # ③ 机器 usr 偏好零改动（本用例不得写 ~/.chonkpilot）
    after = dict(_user_cfg())
    for k in ("theme", "responseTimeout", "streamTimeout", "retryCount"):
        if after.get(k) != before.get(k):
            raise TestError(f"机器 usr 配置被本用例改动：{k} {before.get(k)!r} → {after.get(k)!r}")


def case_c10_scenario_lock():
    """C10 场景并发访问：主 IDE 与子进程并发查询场景列表均成功（数据未损坏）。"""
    main_list = c.req("data-scenario-list", {})
    tmp_ws = _h.tmp_dir("ck-c10-ws-")
    tmp_dd = _h.tmp_dir("ck-c10-data-")
    proc = None
    try:
        proc, c2 = _spawn_ide(_h.free_port(), tmp_ws, tmp_dd)
        res2 = c2.req("data-scenario-list", {})
        if not isinstance(res2, dict) or "list" not in res2:
            raise TestError(f"子进程 data-scenario-list 返回异常: {res2!r}")
    finally:
        if proc:
            _kill_proc(proc)
    res3 = c.req("data-scenario-list", {})
    if not isinstance(res3, dict) or "list" not in res3:
        raise TestError(f"并发访问后主 IDE 场景列表异常: {res3!r}")


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    # 预置展示样本 mock 条目（DB 用户配置默认空；供三.1 显示层断言），结束后按快照还原
    snap = _h.snapshot_user_config(c, ["llms"])
    orig_llms = list(snap["llms"] or [])
    seeded = not any(l.get("name") == "mock" for l in orig_llms)
    if seeded:
        _save_llms(orig_llms + [{"name": "mock", "protocol": "openai", "apiKey": "mock-key",
                                 "model": "mock-model", "baseUrl": "http://127.0.0.1:8901/v1",
                                 "temperature": 0.7, "maxOutputToken": 4096}])
    try:
        with _h.prj_config_guard(c, None):  # 套件级 prj 快照-还原（51 §6-8）：`_open` 关页签会隐式落 opened-files
            total += 1; ok += run_case("三.6 配置页 打开（preview tab）", case_config_open_close)
            total += 1; ok += run_case("三.1 LLM 列表显示", case_llm_list_display)
            total += 1; ok += run_case("三.6 保存 → data-user-config-refresh 传播", case_config_save_refresh)
            total += 1; ok += run_case("三.1 LLM 增删改查", case_llm_crud)
            total += 1; ok += run_case("三.3 MCP 增删", case_mcp_crud)
            total += 1; ok += run_case("三.5 通用参数（responseTimeout 落库）", case_general_params)
            total += 1; ok += run_case("四.x 项目配置页入口", case_project_config_open)
            total += 1; ok += run_case("C8 配置缺字段兜底（默认值）", case_c8_missing_fields_defaults)
            total += 1; ok += run_case("C10 场景并发访问（多进程）", case_c10_scenario_lock)
    finally:
        if seeded:
            _h.restore_user_config(c, snap)  # 还原 usr llms（原本为空/缺省 → 清空集合）
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\n配置结合测试：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
