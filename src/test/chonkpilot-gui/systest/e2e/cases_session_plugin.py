# -*- coding: utf-8 -*-
"""`seeded` 阶段：**session 插件**（2J 会话工具面 + SESSION-SEMANTIC-1）。

入口 = `plugin-list` 就绪 + `tools-list` 8 个 hot 工具；行为经 test-port 的
`chonk.mcp-tools-call` 真实走 gateway → 插件 → 宿主 `plugin/v1/session.*`：

* 建顶层会话（插件生成 `split-*`，mock LLM 跑完首轮）→ 列表可见 → 关键词检索命中 →
  历史读回原话 → 追发一轮 → 改名 → 打开窗口（`window_id`）；
* private 模式下点名他人 → `permission_denied`（不静默空结果）；
* 语义检索默认关闭（`semantic_disabled`）→ usr 配置启用后 `semantic` 命中 / 二次索引缓存 /
  `hybrid` 融合关键词分。embedding 由 `mock_llm.py` 的 `/v1/embeddings` 确定性提供。
"""

import json
import time

import harness as _h

from .framework import CaseFailure, case

SESSION_TOOLS = (
    "session_search", "session_list", "session_get", "session_history", "session_status",
    "session_create", "session_send", "session_rename", "session_open",
)

SEMANTIC_KEYS = (
    "plugins.session.semantic.enabled",
    "plugins.session.semantic.base-url",
    "plugins.session.semantic.model",
    "plugins.session.semantic.max-embed-per-call",
    "plugins.session.semantic.min-score",
    "plugins.session.semantic.auto-index",
    "plugins.session.semantic.hnsw-threshold",
)


def _raw(env, name, args=None):
    """调插件工具，返回 (原始应答, 拼接文本)；不把 isError 当异常。"""
    res = env.req("chonk.mcp-tools-call", {
        "name": name, "arguments": dict(args or {}),
        "tool_call_display_name": "e2e-session", "work_dir": env.workspace}) or {}
    text = "\n".join(str(b.get("text") or "") for b in (res.get("content") or [])
                     if isinstance(b, dict) and b.get("type") == "text")
    return res, text


def _call(env, name, args=None):
    """调插件工具并解析 JSON；工具级错误抛 CaseFailure（保留宿主稳定错误码）。"""
    res, text = _raw(env, name, args)
    if res.get("isError"):
        raise CaseFailure("%s 返回 isError：%s" % (name, text[:500]))
    try:
        return json.loads(text) if text.strip() else {}
    except ValueError:
        raise CaseFailure("%s 返回非 JSON：%s" % (name, text[:500]))


def _wait_call(env, name, args, pred, desc, timeout=20):
    def probe():
        try:
            body = _call(env, name, args)
        except Exception:
            return None
        return body if pred(body) else None
    return env.wait(probe, desc, timeout=timeout, interval=0.8)


@case("S-SESS-01", "插件平台",
      "session 插件：就绪 / 9 工具 / 建会话-列表-检索-历史-追发-改名-打开",
      stage="seeded", order=451,
      fp="2J 会话插件 · 28 §3.8 · 61 §7.2 plugin-list",
      desc="plugin-list status=session running；tools-list 含 9 个 session_* hot 工具且 manifest 有配置视图；"
           "session_create 建顶层会话（mock 跑完首轮，state=done，返回 split-* / dialog_id / text）；"
           "session_list 可见；session_search 命中带 turn_id/message_id；session_history 读回原话；"
           "session_send 追发完成；session_rename 改标题并回读；session_open 返回 window_id；"
           "private 下 targets 点名他人 → permission_denied。")
def s_sess_01(env):
    statuses = _h.plugin_statuses(env.client)
    env.eq("session 插件 running", statuses.get("session"), "running")

    names = _h.wait_tools(env.client, SESSION_TOOLS, max_wait=120)
    canon = {n[5:] if n.startswith("self_") else n for n in names}
    env.check("9 个 session 工具进入工具面",
              all(t in canon for t in SESSION_TOOLS), sorted(canon))

    row = next((p for p in (env.req("plugin-list", {}).get("plugins") or [])
                if p.get("id") == "session"), {})
    env.check("manifest 提供配置视图", bool(row.get("config_view")), row)

    marker = "E2E-SESS-MARK 交付时间定在周五"
    made = _call(env, "session_create", {
        "title": "E2E 会话工具", "prompt": marker + "（请原样回显）", "wait_sec": 120})
    env.eq("session_create 首轮完成", made.get("state"), "done")
    sid = str(made.get("session_id") or "")
    env.check("返回 split-* / dialog_id",
              sid.startswith("split-") and bool(made.get("dialog_id")), made)
    env.check("首轮 text 含原话", marker in str(made.get("text") or ""), made.get("text"))

    lst = _call(env, "session_list", {"limit": 200})
    ids = {s.get("session_id") for s in (lst.get("sessions") or [])}
    env.check("session_list 含新建会话", sid in ids,
              {"sid": sid, "total": lst.get("total")})

    sr = _call(env, "session_search", {"query": "E2E-SESS-MARK", "limit": 20})
    hits = sr.get("hits") or []
    hit = next((h for h in hits if h.get("session_id") == sid), {})
    env.check("session_search 命中新会话", bool(hit), [h.get("session_id") for h in hits])
    env.check("命中带 turn_id / message_id / snippet",
              bool(hit.get("turn_id")) and bool(hit.get("message_id")) and bool(hit.get("snippet")),
              hit)

    hist = _call(env, "session_history", {"session_id": sid, "target_messages": 50})
    contents = [str(m.get("content") or "")
                for t in (hist.get("turns") or []) for m in (t.get("messages") or [])]
    env.check("session_history 读回原话", any(marker in c for c in contents),
              [c[:60] for c in contents[:4]])

    sent = _call(env, "session_send", {
        "session_id": sid, "prompt": "E2E-SESS-SEND 交付时间改到下周一", "wait_sec": 120})
    env.eq("session_send 追发完成", sent.get("state"), "done")
    env.eq("session_send 落在同一会话", sent.get("session_id"), sid)
    sr2 = _call(env, "session_search", {"query": "E2E-SESS-SEND", "session_id": sid})
    env.check("追发内容可检索", bool(sr2.get("hits")), sr2.get("hits"))

    ren = _call(env, "session_rename", {"session_id": sid, "title": "E2E 会话工具-改名"})
    env.check("session_rename 受理", bool(ren.get("ok", True)), ren)
    got = _wait_call(env, "session_get", {"session_id": sid},
                     lambda b: (b.get("session") or {}).get("title") == "E2E 会话工具-改名",
                     "标题回读为改名后的值", timeout=20)
    env.check("标题已更新", (got.get("session") or {}).get("title") == "E2E 会话工具-改名",
              (got.get("session") or {}).get("title"))

    op = _call(env, "session_open", {"session_id": sid})
    env.check("session_open 返回 window_id",
              bool(op.get("window_id")) and op.get("session_id") == sid, op)

    raw, txt = _raw(env, "session_search", {"query": "E2E", "targets": ["e2e-other-user"]})
    env.check("private 点名他人 → permission_denied",
              raw.get("isError") is True and "permission_denied" in txt.lower(), txt[:300])


@case("S-SESS-02", "插件平台",
      "session 语义检索：默认关闭 / 启用后 semantic 命中 / 向量缓存 / hybrid 融合",
      stage="seeded", order=452,
      fp="2J §8 向量语义检索（SESSION-SEMANTIC-1）",
      desc="未配置 embedding 时 mode=semantic → semantic_disabled；经 usr 配置启用（mock /v1/embeddings）后，"
           "对含「交付」的专属会话 semantic 检索命中且 semantic_score ≥ min-score、indexed>0；"
           "同一查询二次检索 indexed=0（向量已缓存）；hybrid 命中同时带 semantic_score / keyword_score；"
           "后台全量索引 ready 后无过滤 semantic 检索走 HNSW（approx=true，index.new_vectors>0）。")
def s_sess_02(env):
    try:
        raw, txt = _raw(env, "session_search", {"query": "什么时候交付", "mode": "semantic"})
        env.check("未启用 → semantic_disabled",
                  raw.get("isError") is True and "semantic_disabled" in txt.lower(), txt[:300])

        made = _call(env, "session_create", {
            "title": "E2E 语义检索",
            "prompt": "E2E-SEM-DELIVERY 交付时间定在周五（语义检索夹具）",
            "wait_sec": 120})
        env.eq("语义会话首轮完成", made.get("state"), "done")
        sid = str(made.get("session_id") or "")
        env.check("语义会话 id", bool(sid), made)

        env.req("data-user-config-save", {"data": {
            "plugins.session.semantic.enabled": True,
            "plugins.session.semantic.base-url": "http://127.0.0.1:%d/v1" % env.mock_port,
            "plugins.session.semantic.model": "mock-embed",
            "plugins.session.semantic.max-embed-per-call": 16,
            "plugins.session.semantic.min-score": "0.5",
            "plugins.session.semantic.auto-index": True,
            "plugins.session.semantic.hnsw-threshold": 1,
        }})
        env.client.mq_emit("config-refresh")
        time.sleep(0.6)

        arg = {"query": "什么时候交付", "mode": "semantic", "session_id": sid, "limit": 10}
        sem = _call(env, "session_search", arg)
        env.eq("semantic 模式回传", sem.get("search_mode"), "semantic")
        hits = sem.get("hits") or []
        env.check("semantic 命中目标会话",
                  bool(hits) and all(h.get("session_id") == sid for h in hits), hits[:2])
        env.check("semantic_score ≥ min-score",
                  bool(hits) and float(hits[0].get("semantic_score") or 0) >= 0.5, hits[:1])
        env.check("首次检索补写向量", int(sem.get("indexed") or 0) > 0, sem.get("indexed"))

        sem2 = _call(env, "session_search", arg)
        env.eq("二次检索不再补索引", int(sem2.get("indexed") or 0), 0)

        hy = _call(env, "session_search", {
            "query": "交付", "mode": "hybrid", "session_id": sid, "limit": 10})
        env.eq("hybrid 模式回传", hy.get("search_mode"), "hybrid")
        hh = hy.get("hits") or []
        env.check("hybrid 命中带 semantic_score",
                  bool(hh) and float(hh[0].get("semantic_score") or 0) > 0, hh[:1])
        env.check("hybrid 命中带 keyword_score",
                  bool(hh) and float(hh[0].get("keyword_score") or 0) > 0, hh[:1])

        # 全量索引 + HNSW：后台索引 ready 后，**无过滤** semantic 检索应走近似路径。
        def probe_hnsw():
            try:
                body = _call(env, "session_search", {
                    "query": "什么时候交付", "mode": "semantic", "limit": 10})
            except Exception:
                return None
            idx = body.get("index") or {}
            if body.get("approx") and idx.get("state") == "ready":
                return body
            return None
        hn = env.wait(probe_hnsw, "全量索引 ready + HNSW 命中", timeout=30, interval=0.8)
        hidx = hn.get("index") or {}
        env.check("HNSW 命中目标会话",
                  any(h.get("session_id") == sid for h in (hn.get("hits") or [])),
                  (hn.get("hits") or [])[:2])
        env.check("全量索引写入新向量", int(hidx.get("new_vectors") or 0) > 0, hidx)
        env.check("HNSW 回传 approx=true", hn.get("approx") is True, hn.get("approx"))
    finally:
        # 配置快照-还原：删掉本用例写入的 usr 键（不影响后续测试点的 private/默认行为）。
        for k in SEMANTIC_KEYS:
            try:
                env.req("data-user-config-delete", {"id": k})
            except Exception:
                pass


@case("S-SESS-03", "插件平台",
      "session_status：异步会话的持久状态 / 最终回复（父轮已结束、无在飞工具）",
      stage="seeded", order=453,
      fp="2J §7.1 session_status（SESSION-STATUS-1）",
      desc="session_create(wait_sec=0) → 立即 running；随后在**另一个调用**里 session_status 仍读到 running；"
           "session_status(wait_sec=30) 等到 done（库内 turn_status=complete 归一），并返回最终 assistant "
           "文本（短慢流 STATUS-4）；终态后再次查询仍可从库读出（不依赖发起轮次 / task_status / inbox）。")
def s_sess_03(env):
    made = _call(env, "session_create", {
        "title": "E2E 状态查询",
        "prompt": "E2E-SESS-STATUS call slow-status",
        "wait_sec": 0,
    })
    env.eq("异步受理 state=running", made.get("state"), "running")
    sid = str(made.get("session_id") or "")
    env.check("异步返回 session_id", bool(sid), made)

    st0 = _call(env, "session_status", {"session_id": sid})
    env.eq("持久状态仍为 running", st0.get("state"), "running")

    st = _call(env, "session_status", {"session_id": sid, "wait_sec": 30})
    env.eq("等待到终态 done", st.get("state"), "done")
    env.check("turn_status 终态",
              st.get("turn_status") in ("done", "complete"), st.get("turn_status"))
    env.check("最终 assistant 文本", "STATUS-4" in str(st.get("text") or ""), st.get("text"))
    env.check("wait 期间实际等待", int(st.get("waited_ms") or 0) > 0, st.get("waited_ms"))

    # 父轮早已结束，再次查询仍可读（无 in-flight 工具 / 无 inbox 依赖）。
    st2 = _call(env, "session_status", {"session_id": sid})
    env.eq("终态后再次查询", st2.get("state"), "done")
    env.check("再次查询仍返回最终文本",
              "STATUS-4" in str(st2.get("text") or ""), st2.get("text"))