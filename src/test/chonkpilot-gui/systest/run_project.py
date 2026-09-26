# -*- coding: utf-8 -*-
"""testplan.md 四、工作目录级（项目级配置）结合测试（消息面驱动）。

新架构：原 window.go RPC（SetConfig/GetAllConfig/GetPrompt/
GetProjectSecurity/SaveProjectSecurity/
GetRecentDirs/CreateSession/GetSession/DeleteSession）一律移除，改走：
  - data-prj-config-{list,load,save,delete}（项目配置 key-value）
  - data-prompt-{load,save}（prompt 键）
  - data-prj-security-{list,save,delete}（安全目录条目键）
  - data-session-{ensure-session,get,delete}（会话）
  - gui.recent.list（最近目录）

前置：IDE --test-port=2345 --work-dir=<ws>。
"""

import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

WS = r"E:\BizWorks\chonkpilot\src\test\chonkpilot-gui\systest\ws"
import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345, work_dir=WS)  # 复用优先；无实例则自起并在结束时回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：项目配置/prompt/安全目录 → 退出前回滚


def case_context_compression():
    """四.1 上下文压缩：data-prj-config save 落库 + data-prj-config-refresh 传播 + load 断言。

    2026-09-24（D1）：三项键 = `keep_full_max_turns` / `keep_full_max_tokens`（新）/ `compress_token_threshold`。

    快照-还原（51 §6-8）：三个 prj 键跑前快照、finally 还原（原本无该键 → 删除，回落默认）。
    """
    c.mq_on_capture(["data-prj-config-refresh"])
    snap = _h.snapshot_prj_config(c, ["keep_full_max_turns", "keep_full_max_tokens", "compress_token_threshold"])
    try:
        c.req("data-prj-config-save", {"data": {"key": "keep_full_max_turns", "value": "2"}})
        c.req("data-prj-config-save", {"data": {"key": "keep_full_max_tokens", "value": "24000"}})
        c.req("data-prj-config-save", {"data": {"key": "compress_token_threshold", "value": "20000"}})
        ev = c.wait_events("data-prj-config-refresh", n=1, max_wait=10, clear=True)
        if not ev:
            raise TestError("保存后未收到 data-prj-config-refresh")
        v1 = (c.req("data-prj-config-load", {"id": "keep_full_max_turns"}).get("data"))
        v2 = (c.req("data-prj-config-load", {"id": "keep_full_max_tokens"}).get("data"))
        v3 = (c.req("data-prj-config-load", {"id": "compress_token_threshold"}).get("data"))
        if v1 != "2":
            raise TestError(f"keep_full_max_turns={v1!r}，期望 '2'")
        if v2 != "24000":
            raise TestError(f"keep_full_max_tokens={v2!r}，期望 '24000'")
        if v3 != "20000":
            raise TestError(f"compress_token_threshold={v3!r}，期望 '20000'")
    finally:
        _h.restore_prj_config(c, snap)  # 原本无该键 → 删除（不再写回空串）


def case_prompt_edit_restore():
    """四.3 提示词编辑：data-prompt save/load 往返。

    注：旧断言"清空后回退嵌入默认"发生在 **server 组装期**，persist 层（data-prompt-load）
    无回退（清空即返回空串）；此处按消息面真实语义断言（save→load 往返 + 清空→空）。

    快照-还原（51 §6-8）：先读原值，finally 写回（原本空/缺失 → 写回空串 = persist 层未设置）。
    """
    orig = c.req("data-prompt-load", {"id": "tool_usage_prompt"}).get("data")
    try:
        c.req("data-prompt-save", {"data": {"key": "tool_usage_prompt", "value": "测试工具使用说明XYZ"}})
        v = c.req("data-prompt-load", {"id": "tool_usage_prompt"}).get("data")
        if v != "测试工具使用说明XYZ":
            raise TestError(f"prompt 未返回编辑值: {v!r}")
        c.req("data-prompt-save", {"data": {"key": "tool_usage_prompt", "value": ""}})
        v2 = c.req("data-prompt-load", {"id": "tool_usage_prompt"}).get("data")
        if v2 not in ("", None):
            raise TestError(f"清空后 persist 层应返回空串（回退在 server 组装期）: {v2!r}")
    finally:
        c.req("data-prompt-save", {"data": {"key": "tool_usage_prompt", "value": orig or ""}})


def case_security_dirs():
    """四.4 项目安全目录：data-prj-security save/list/delete 往返。

    唯一口径（与前端 SecurityConfig.vue / api/config.js 一致，14-安全域 §2.3）：
    **一条信任目录 = 一个 key**（前端生成的不透明 id，非目录路径），
    value = JSON 字符串 {"dir","writable"}；list 返回平铺 map（key 已剥 security- 前缀）。
    """
    k = "dir-systest-a"
    v1 = r'{"dir":"E:\\ckptest-sec-a","writable":true}'
    v2 = r'{"dir":"E:\\ckptest-sec-a","writable":false}'
    # 快照-还原（51 §6-8）：同名条目原本可能已存在 → 跑前快照，finally 写回/删除。
    orig = (c.req("data-prj-security-list", {}).get("list") or {}).get(k)
    try:
        # 增
        c.req("data-prj-security-save", {"data": {"key": k, "value": v1}})
        lst = c.req("data-prj-security-list", {}).get("list") or {}
        if lst.get(k) != v1:
            raise TestError(f"安全目录未按条目键往返: {lst!r}")
        # 改（同 key 换 value = 勾选 writable）
        c.req("data-prj-security-save", {"data": {"key": k, "value": v2}})
        lst2 = c.req("data-prj-security-list", {}).get("list") or {}
        if lst2.get(k) != v2:
            raise TestError(f"安全目录更新未生效: {lst2!r}")
        # 删
        c.req("data-prj-security-delete", {"id": k})
        lst3 = c.req("data-prj-security-list", {}).get("list") or {}
        if k in lst3:
            raise TestError("安全目录删除未生效")
    finally:
        if orig is None:
            c.req("data-prj-security-delete", {"id": k})       # 原本无此条目 → 删除
        else:
            c.req("data-prj-security-save", {"data": {"key": k, "value": orig}})  # 原本有 → 写回原值


def case_workdir_recent_dirs():
    """四.6 最近目录：gui.recent.list 返回 {dirs}（列表能力仍在）。

    注：原锁文件（~/.chonkpilot/recent/）与"运行中目录被锁排除"语义已移除，改由 usr config
    `recent_dirs` 自由键承载，故仅校验返回结构。
    """
    res = c.req("gui.recent.list", {})
    dirs = res.get("dirs") if isinstance(res, dict) else None
    if not isinstance(dirs, list):
        raise TestError(f"gui.recent.list 未返回 dirs 列表: {res!r}")


def case_session_workdir():
    """四.7 会话与工作目录关系：会话落库后 work_dir = 当前工作目录（实例级绑定）。"""
    sid = "prj-sess-%d" % int(time.time() * 1000)
    c.req("data-session-ensure-session", {"session_id": sid})
    res = c.req("data-session-get", {"id": sid})
    sess = res.get("data") if isinstance(res, dict) else None
    if not isinstance(sess, dict):
        raise TestError(f"data-session-get 未返回会话: {res!r}")
    wd = sess.get("work_dir") or ""
    if wd and os.path.normcase(os.path.normpath(wd)) != os.path.normcase(os.path.normpath(WS)):
        raise TestError(f"会话 work_dir={wd!r}，期望 {WS!r}")
    c.req("data-session-delete", {"id": sid})


def main():
    ok = 0
    total = 0
    c.console(clear=True)
    total += 1; ok += run_case("四.1 上下文压缩配置（落库 + 传播）", case_context_compression)
    total += 1; ok += run_case("四.3 提示词编辑（save/load 往返）", case_prompt_edit_restore)
    total += 1; ok += run_case("四.4 项目安全目录（save/list/delete）", case_security_dirs)
    total += 1; ok += run_case("四.6 最近目录（gui.recent.list）", case_workdir_recent_dirs)
    total += 1; ok += run_case("四.7 会话与工作目录关系", case_session_workdir)
    errs = c.console()
    for e in errs.get("entries", []):
        if e.get("level") in ("error",):
            print(f"  [CONSOLE-ERROR] {e.get('text')}")
    print(f"\n项目配置结合测试：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
