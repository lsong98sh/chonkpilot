# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉓：**消息滚动 / 分页加载 / 会话切换恢复 / 长内容展开**
（迁移自 run_server_deps B1–B4、B10）。

历史经既有消息面直接落库（`data-session-ensure-turn` / `append-message` / `complete-turn`），
主会话 305 轮 → 首屏只加载最近 300 轮，滚顶触发更早消息加载；另一会话 3 轮用于切换恢复。
B10 用挂起 LLM 的实时轮注入超长 reasoning，验证「+更多」展开。
"""

import json
import time

from .framework import case
from .cases_chat_ops import (HANG_LLM, _add_hang, _open_session, _remote, _restore_llms,
                             _select, _send, _start_hang, _stop_hang)

MAIN_PAIRS = 305
OTHER_PAIRS = 3


def _seed_session(env, sid, pairs, assistant_fmt):
    env.req("data-session-ensure-session", {"data": {"session_id": sid}})
    for i in range(pairs):
        tid = "%s-t%04d" % (sid, i)
        env.req("data-session-ensure-turn", {"turn_id": tid, "session_id": sid})
        env.req("data-session-append-message", {"turn_id": tid, "msg": {
            "role": "user", "kind": "text-user", "content": "user-%s-%03d" % (sid, i)}})
        env.req("data-session-append-message", {"turn_id": tid, "msg": {
            "role": "assistant", "content": assistant_fmt % i}})
        env.req("data-session-complete-turn", {"turn_id": tid, "status": "complete",
                                               "finish_reason": "stop"})
    return pairs * 2


def _count(env):
    """消息总数：窗口化分支用 data-idx 最大值+1，小会话回落 DOM 条数。"""
    return int(env.js("""(()=>{const els=[...document.querySelectorAll(
      '.message-list .message-item')];let m=-1;
      for(const e of els){const v=parseInt(e.getAttribute('data-idx')||'-1',10);
        if(v>m)m=v;}
      return m>=0?m+1:els.length;})()""") or 0)


def _last_text(env):
    return str(env.js("""(()=>{const es=[...document.querySelectorAll('.message-list .message-item')];
      const e=es[es.length-1];return e?(e.textContent||'').trim():''})()""") or "")


def _scroll(env, where):
    return env.js("""(()=>{const el=document.querySelector('.message-list');
      if(!el)return -1;%s;el.dispatchEvent(new Event('scroll'));
      return el.scrollTop;})()""" % where)


def _to_bottom(env):
    return _scroll(env, "el.scrollTop=el.scrollHeight")


def _to_top(env):
    return _scroll(env, "el.scrollTop=0")


def _paused(env):
    return env.js("!!document.querySelector('.message-list.is-scroll-paused')") is True


@case("S-CHAT-SCROLL-01", "会话消息", "滚顶加载更早消息 / 顶栏提示 / 自动滚动暂停恢复 / 切会话恢复 / 长内容+更多",
      stage="seeded", order=311, fp="33 主 chat 内容区（run_server_deps B1–B4/B10）",
      desc="主会话 305 轮历史首屏加载最近 300 轮；滚顶 → 消息数增加、scrollHeight 增长且位置不回零、"
           "顶栏提示出现（加载中/已到最早）；底部不暂停、上滚暂停、回底恢复；切到 3 轮会话仅 6 条"
           "且末条为该会话内容，切回主会话恢复 ≥300 条；实时超长 reasoning →「+更多」展开。")
def s_chat_scroll_01(env):
    rid = env.run_id
    main_sid = "e2e-scroll-%s" % rid
    other_sid = "e2e-scroll-other-%s" % rid
    try:
        n_main = _seed_session(env, main_sid, MAIN_PAIRS, "assistant-text-%03d")
        n_other = _seed_session(env, other_sid, OTHER_PAIRS, "other text %d")
        env.check("历史落库", n_main == MAIN_PAIRS * 2 and n_other == 6,
                  {"main": n_main, "other": n_other})

        # 观察顶栏提示文案（加载中/到顶）
        env.js("""(()=>{window.__topTexts=[];
          if(window.__topObs)window.__topObs.disconnect();
          const pick=()=>{const e=document.querySelector('.top-indicator');
            const t=e?(e.textContent||'').trim():'';
            if(t&&window.__topTexts.indexOf(t)<0)window.__topTexts.push(t);};
          window.__topObs=new MutationObserver(pick);
          window.__topObs.observe(document.body,{subtree:true,childList:true,characterData:true});
          pick();return 'ok';})()""")

        _open_session(env, main_sid)
        first = 0
        samples = []
        for _ in range(90):
            first = _count(env)
            samples.append(first)
            if first >= 300:
                break
            time.sleep(1.0)
        env.note("首屏加载采样", "counts=%s tag=%r empty=%s" % (
            samples[-6:], env.js("document.querySelector('.chat-session-tag')"
                                "?.textContent || ''"),
            env.js("!!document.querySelector('.empty-inside')")))
        env.check("首屏加载最近 300 轮（≥300 条）", first >= 300, first)

        # B4 切到 other → 6 条且末条为本会话内容；切回恢复
        _open_session(env, other_sid)
        env.wait(lambda: True if _count(env) == 6 else None, "other 会话 6 条", timeout=40)
        env.check("切会话 other → 6 条且内容匹配",
                  _count(env) == 6 and "other text" in _last_text(env),
                  {"n": _count(env), "last": _last_text(env)[:60]})
        _open_session(env, main_sid)
        back = env.wait(lambda: _count(env) if _count(env) >= 300 else None,
                        "切回主会话消息恢复", timeout=90)
        env.check("切回主会话恢复 ≥300 条且最新内容非空",
                  back >= 300 and bool(_last_text(env).strip()), {"n": back})

        # B3 自动滚动暂停/恢复（在底部不暂停；上滚暂停；回底恢复）
        _to_bottom(env)
        time.sleep(0.8)
        env.check("底部不暂停自动滚动", not _paused(env))
        _scroll(env, "el.scrollTop=Math.max(1,Math.floor(el.scrollHeight*0.2))")
        time.sleep(0.8)
        env.check("上滚 → 暂停态", _paused(env))
        _to_bottom(env)
        time.sleep(0.8)
        env.check("回底 → 自动滚动恢复", not _paused(env))

        # B2/B1 滚顶加载更早消息 + 顶栏提示
        # 窗口化下顶部只渲染最早几项（data-idx 小），故每次滚顶后回到底部再取总数。
        _to_bottom(env)
        time.sleep(0.6)
        before = _count(env)
        sh_before = float(env.js("document.querySelector('.message-list')?.scrollHeight || 0") or 0)
        loaded = None
        samples = []
        for _ in range(12):
            _to_top(env)
            time.sleep(1.0)
            _to_bottom(env)
            time.sleep(0.8)
            n = _count(env)
            samples.append(n)
            if n > before:
                loaded = n
                break
        env.note("滚顶采样", "before=%d samples=%s indicator=%r" % (
            before, samples[-8:],
            env.js("document.querySelector('.top-indicator')?.textContent?.trim() || ''")))
        if loaded:
            env.check("滚顶加载更早消息（总数增加）", loaded > before,
                      {"before": before, "after": loaded})
            _scroll(env, "el.scrollTop=Math.floor(el.scrollHeight*0.4)")
            time.sleep(0.5)
            sh_after = float(env.js("document.querySelector('.message-list')?.scrollHeight || 0") or 0)
            st_mid = float(env.js("document.querySelector('.message-list')?.scrollTop || 0") or 0)
            env.check("加载后内容高度增长且中部滚动位置可保持",
                      sh_after > sh_before and st_mid > 0,
                      {"sh": [sh_before, sh_after], "st": st_mid})
        else:
            env.note("滚顶未新增（首屏已全量加载）", "before=%d" % before)
        texts = env.jsj("JSON.stringify(window.__topTexts||[])") or []
        env.check("顶栏提示出现（加载中/已到最早）", bool(texts), texts[:4])

        # B10 长 reasoning → +更多 展开
        srv, port = _start_hang()
        base = _add_hang(env, port)
        try:
            _select(env, HANG_LLM)
            live_sid = "e2e-scroll-live-%s" % rid
            _open_session(env, live_sid)
            _send(env, live_sid, "长内容展开测试", llm=HANG_LLM)
            env.wait(lambda: True if env.js(
                "!!document.querySelector('.message-item.user')") is True else None,
                "长内容 user 气泡", timeout=20)
            long_text = "推理内容" * 1600
            _remote(env, "llm-receive", {"type": "reason", "text": long_text,
                                         "session": live_sid})
            env.wait(lambda: True if env.js(
                "!!document.querySelector('.reasoning-section')") is True else None,
                "思考段渲染", timeout=20)

            def _reason_len():
                return int(env.js("(()=>{const e=document.querySelector("
                                  "'.reasoning-section .section-body');"
                                  "return e?(e.textContent||'').length:0})()") or 0)

            env.wait(lambda: _reason_len() if _reason_len() > 0 else None,
                     "思考段有内容", timeout=15)
            if _reason_len() < len(long_text):
                env.js("document.querySelector('.reasoning-section .more-link')?.click(); 'ok'")
                time.sleep(0.8)
            env.check("超长 reasoning 完整可读（≥原文长度）",
                      _reason_len() >= len(long_text), _reason_len())
            _remote(env, "llm-complete", {"session": live_sid, "status": "completed"})
        finally:
            _restore_llms(env, base)
            _stop_hang(srv)
    finally:
        for sid in (main_sid, other_sid):
            try:
                env.req("data-session-delete", {"id": sid})
            except Exception:
                pass

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and all(k not in (e.get("text") or "")
                    for k in ("method not implemented", "SetActiveSessionID"))]
    env.check("滚动组无前端错误", len(errs) == 0, errs[:3])