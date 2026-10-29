# -*- coding: utf-8 -*-
"""run_server_deps 造数夹具（**mq-only**，替代已废除的 /call 造数）。

按 61-消息一览 §3.2a「会话运行时写原语」经消息面落库（处理方 = persist）：
  data-session-ensure-session → ensure-turn → append-message(×N) → complete-turn
不直连数据库、不调 window.go（/call 已废除）。

前置：目标 IDE 实例（默认 --test-port=2346）与 run_server_deps 为同一实例；
      mock_llm.py 监听 127.0.0.1:8901（B6/B8/B10 的实时回合用）。

用法：
    python _seed_deps.py [port]        # port 缺省 2346

幂等：目标会话消息数已达目标则跳过；否则先 delete（级联删 turns/messages）再重建，
      避免重复追加导致 B4「other 恰好 6 条」类断言失真。
"""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, dialog_id_for  # noqa: E402

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 2346
import harness as _h  # 按需加载 + 结束即回收
_G = _h.acquire_gui(PORT)  # 复用优先（run_server_deps 起的 2346 实例 → owned=False，不被回收）
c = _G.client

# 与 run_server_deps.py 保持一致（B1/B2/B3/B4/B7 用主会话；B4 用 other；B9 用子会话）
SID_MAIN = "deps-main-002"
SID_OTHER = "deps-other-002"
SUB_ID = "sub-7897f487ae9dd2cc"

# 主会话需 > 600 条：首屏 300 + auto-preload 300 后仍 has_more=true，
# 滚顶 loadMore 才有「更早批次」可拉（run_server_deps B1/B2 依赖该增长路径与
# 「已到最早」终态）。350 轮 × 2 = 700 条；other 3 轮 × 2 = 6 条（B4 断言恰好 6）。
MAIN_PAIRS = 350
OTHER_PAIRS = 3
SUB_PAIRS = 2


def history_count(sid, dlg=""):
    """当前会话已落库消息数（data-session-history 全量拉取）。

    会话尚不存在时该读主题会因一树一库缺 `dialog_id` 报 `dialog_required`
    （驱动自愈查不到会话 → 不自举）→ 视为 0 条，交由 ensure 走建库路径。

    `dlg`：显式对话库（子会话与父会话同库；驱动自愈只能按根会话推导，子会话必须由调用方给）。
    """
    payload = {"session_id": sid, "target_messages": 1000000, "target_bytes": 1 << 30}
    if dlg:
        payload["dialog_id"] = dlg
    try:
        res = c.req("data-session-history", payload)
    except TestError:
        return 0
    msgs = ((res or {}).get("messages") or {}).get("messages") or []
    return len(msgs)


def _with_dlg(payload, dlg):
    """载荷显式带 `dialog_id`（子会话写路径不经驱动自愈）。"""
    if dlg:
        payload = dict(payload)
        payload["dialog_id"] = dlg
    return payload


def seed_session(sid, pairs, assistant_fmt, parent_id="", dlg=""):
    """建会话 + pairs 轮（每轮 user + assistant 两条，落终态）。"""
    c.req("data-session-ensure-session", _with_dlg(
        {"session_id": sid, "parent_session_id": parent_id}, dlg))
    for i in range(pairs):
        turn_id = "%s-t%04d" % (sid, i)
        c.req("data-session-ensure-turn", _with_dlg({"turn_id": turn_id, "session_id": sid}, dlg))
        c.req("data-session-append-message", _with_dlg({
            "turn_id": turn_id,
            "msg": {"role": "user", "kind": "text-user", "content": "user-%s-%03d" % (sid, i)},
        }, dlg))
        c.req("data-session-append-message", _with_dlg({
            "turn_id": turn_id,
            "msg": {"role": "assistant", "content": assistant_fmt % i},
        }, dlg))
        c.req("data-session-complete-turn", _with_dlg({
            "turn_id": turn_id, "status": "done", "finish_reason": "stop",
        }, dlg))


def ensure(sid, pairs, assistant_fmt, parent_id="", dlg=""):
    if history_count(sid, dlg) >= pairs * 2:
        print("  [skip] %s 已有足量消息" % sid)
        return
    try:
        c.req("data-session-delete", _with_dlg({"id": sid}, dlg))  # 级联清理旧数据（幂等）
    except TestError:
        pass  # 会话不存在（一树一库缺 dialog_id）→ 无需删除
    # 删除会级联删对话库 → 驱动缓存的 dialog_id 已失效；显式 dlg 的调用不受影响，
    # 无 dlg 时清掉缓存，避免后续写路径继续注入已删除的库（"对话库不存在"）。
    if not dlg:
        c._dlg_cache.pop(sid, None)
    seed_session(sid, pairs, assistant_fmt, parent_id, dlg)
    print("  [seed] %s -> %d 条" % (sid, history_count(sid, dlg)))


def main():
    c.wait_ready()
    print("造数（mq-only，port=%d）：" % PORT)
    # 主会话：普通文本历史（B1/B2/B3/B4；B7 无工具消息时用例自带跳过分支）
    ensure(SID_MAIN, MAIN_PAIRS, "assistant-text-%03d")
    # other 会话：末条须含 "other text"（B4 断言最新内容）
    ensure(SID_OTHER, OTHER_PAIRS, "other text %d")
    # 子会话（带 parent，非顶层）：B9 只读跟随 viewer
    # 一树一库口径（42 (284)）：子会话行与**父会话同库** → 显式给父会话的 dialog_id；
    # 不给的话驱动会按子会话 id 推一个独立库（前端经父会话反推 → 读不到该子会话）。
    ensure(SUB_ID, SUB_PAIRS, "sub text %d", parent_id=SID_MAIN, dlg=dialog_id_for(SID_MAIN))
    print("造数完成。")


if __name__ == "__main__":
    main()
