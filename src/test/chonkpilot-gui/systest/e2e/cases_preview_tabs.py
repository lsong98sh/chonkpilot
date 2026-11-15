# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑭b：**预览页签栏 / 选中浮条**（迁移自 run_preview_ui P6/P8/P9）。

* 选中预览正文 → 浮动操作条出现（`.preview-selection-bar`）；
* 打开多个固定页签至溢出 → `.tb-more` 绝对定位贴右、多余页签不隐藏；
* 弹框列出全部页签且顺序一致；选中项移到显示首位，内容面板顺序不变；
* 拖预览分隔条改变可用宽 → 按钮出现/消失与几何判定始终一致。

夹具：本轮自建文本页签（唯一名前缀），结束时全部关闭，避免污染重启阶段的页签集。
"""

import json
import time

from .framework import case

TABBAR_JS = ("(()=>{const bar=document.querySelector('.code-view .tb-bar.tb-bottom');"
             "if(!bar)return null;const tabs=[...bar.querySelectorAll('.tb-tab')];"
             "const inner=bar.querySelector('.tb-inner');const btn=bar.querySelector('.tb-more');"
             "const last=tabs[tabs.length-1];"
             "const padRight=parseFloat(getComputedStyle(bar).paddingRight)||0;"
             "const br=bar.getBoundingClientRect();const availRight=br.right-padRight;"
             "const btr=btn?btn.getBoundingClientRect():null;"
             "return JSON.stringify({n:tabs.length,barW:Math.round(br.width),"
             "innerScroll:inner?inner.scrollWidth:-1,"
             "need:last?last.getBoundingClientRect().right>availRight+0.5:false,"
             "btn:!!btn,btnPos:btn?getComputedStyle(btn).position:'',"
             "btnRightGap:btr?Math.round(br.right-btr.right):null,"
             "hiddenClass:bar.querySelectorAll('.tb-tab.tb-hidden').length,"
             "displayNone:tabs.filter(t=>getComputedStyle(t).display==='none').length,"
             "visHidden:tabs.filter(t=>getComputedStyle(t).visibility==='hidden').length,"
             "zeroWidth:tabs.filter(t=>t.getBoundingClientRect().width<=0).length,"
             "names:tabs.map(t=>((t.querySelector('.tb-name')||{}).textContent||'').trim())})})()")


def _tabbar(env):
    return env.jsj(TABBAR_JS) or {}


def _active_name(env):
    return env.js("""(()=>{const t=document.querySelector('.code-view .tb-tab.active');
      return t?((t.querySelector('.tb-name')||{}).textContent||'').trim():''})()""") or ""


def _panel_order(env):
    return env.jsj("JSON.stringify([...document.querySelectorAll('.code-view .tab-panel')]"
                   ".map(p=>{const e=p.querySelector('.file-path');"
                   "return e?e.textContent.trim():''}))") or []


def _preview_handle_idx(env):
    """「紧邻 .code-view 左边界」的竖向 handle 索引（无则 -1）。"""
    v = env.js("""(()=>{
      const cv=document.querySelector('.code-view'); if(!cv) return -1;
      const cr=cv.getBoundingClientRect();
      const hs=[...document.querySelectorAll('.split-handle.vertical')];
      let best=-1,bd=1e9;
      hs.forEach((h,i)=>{const r=h.getBoundingClientRect();
        if(!(r.width>0&&r.height>0)) return;
        const d=Math.abs(r.right-cr.left);
        if(d<bd){bd=d;best=i;}});
      return bd<48?best:-1;
    })()""")
    return -1 if v is None else int(v)


def _drag_preview_split(env, idx, dx):
    """synthetic 鼠标拖拽预览 pane 左侧分隔条（正 dx → 预览变宽）。"""
    r = env.js("""(()=>{
      const hs=[...document.querySelectorAll('.split-handle.vertical')];
      const h=hs[%d]; if(!h) return 'no-handle';
      const r=h.getBoundingClientRect();
      const x=r.left+r.width/2, y=r.top+r.height/2;
      h.dispatchEvent(new MouseEvent('mousedown',{clientX:x,clientY:y,bubbles:true,cancelable:true,buttons:1}));
      for(let i=1;i<=5;i++){
        window.dispatchEvent(new MouseEvent('mousemove',{clientX:x+%d*i/5,clientY:y,bubbles:true,cancelable:true,buttons:1}));
      }
      window.dispatchEvent(new MouseEvent('mouseup',{clientX:x+%d,clientY:y,bubbles:true,cancelable:true}));
      return 'ok';
    })()""" % (idx, dx, dx))
    env.check("派发预览分隔条拖拽（dx=%d）" % dx, r == "ok", r)
    time.sleep(0.9)


def _close_tabs(env, names):
    env.js("""(()=>{const want=%s;
      [...document.querySelectorAll('.code-view .tb-bar.tb-bottom .tb-tab')]
        .filter(t=>want.includes(((t.querySelector('.tb-name')||{}).textContent||'').trim()))
        .forEach(t=>{const b=t.querySelector('.tb-close'); if(b) b.click();});
      return 'ok';})()""" % json.dumps(sorted(names)))
    env.wait(lambda: True if not any(
        n in (_tabbar(env).get("names") or []) for n in names) else None,
        "关闭本轮自建预览页签", timeout=20)


@case("S-PREVIEW-02", "预览", "选中浮条 / 页签溢出按钮与弹框 / 选中置首不改面板顺序 / 宽度变化联动",
      stage="seeded", order=84, fp="33 预览区 · 页签栏（run_preview_ui P6/P8/P9）",
      desc="选中预览正文 → `.preview-selection-bar` 出现；打开多个固定页签到溢出 → `.tb-more` "
           "绝对定位贴右、页签不被隐藏；弹框列出全部且顺序一致，选中项移到显示首位而内容面板"
           "顺序不变；拖分隔条加宽/缩窄 → 按钮出现/消失与几何判定一致；结束后关闭本轮页签。")
def s_preview_02_tabs_and_selection(env):
    rid = env.run_id
    sel_rel = "e2e_prev_sel_%s.txt" % rid
    env.write_text(sel_rel, "E2E-SELECT-BODY-%s\nsecond line for selection\n" % rid)

    errs_before = len(((env.client.console() or {}).get("entries") or []))

    # 1) 选中浮条：先等面板内容稳定两次采样，再建立选区（防抖动吞掉 selectionchange）
    env.emit("file-open", {"path": env.posix(env.path(sel_rel)), "temporary": False})
    env.wait(lambda: True if _active_name(env) == sel_rel else None,
             "选中浮条夹具页签激活", timeout=20)

    def _stable():
        return env.jsj("JSON.stringify((()=>{const t=document.querySelector("
                       "'.code-view .tb-tab.active');"
                       "const ps=[...document.querySelectorAll('.tab-panel')]"
                       ".filter(p=>getComputedStyle(p).display!=='none');"
                       "const p=ps[ps.length-1];const c=p?p.querySelector('.source-code'):null;"
                       "return [t?((t.querySelector('.tb-name')||{}).textContent||'').trim():'',"
                       "c?(c.textContent||'').length:-1]})())") or []

    prev = None
    for _ in range(30):
        cur = _stable()
        if cur == prev and cur and int(cur[1]) > 0:
            break
        prev = cur
        time.sleep(0.3)

    bar = None
    for attempt in range(3):
        env.js("""(()=>{const ps=[...document.querySelectorAll('.tab-panel')]
          .filter(p=>getComputedStyle(p).display!=='none');
          const p=ps[ps.length-1];const el=p?p.querySelector('.source-code'):null;
          if(!el) return 'no-el';
          const range=document.createRange();range.selectNodeContents(el);
          const sel=window.getSelection();sel.removeAllRanges();sel.addRange(range);
          document.dispatchEvent(new Event('selectionchange'));return 'selected';})()""")
        bar = env.wait(lambda: True if env.js(
            "!!document.querySelector('.preview-selection-bar')") is True else None,
            "选中文本 → 浮条出现（第 %d 次）" % (attempt + 1), timeout=8)
        if bar:
            break
    env.check("预览选中文本 → 浮动操作条", bar is True)
    env.js("window.getSelection()?.removeAllRanges(); 'ok'")

    # 2) 打开 12 个固定页签 → 溢出
    overflow = ["e2e_ov_%02d_%s.txt" % (i, rid) for i in range(1, 13)]
    for i, rel in enumerate(overflow):
        env.write_text(rel, "E2E-OVERFLOW-%02d-%s\n" % (i + 1, rid))
        env.emit("file-open", {"path": env.posix(env.path(rel)), "temporary": False})
        env.wait(lambda rel=rel: True if rel in (_tabbar(env).get("names") or []) else None,
                 "固定页签 %s 打开" % rel, timeout=20)

    st = env.wait(lambda: (lambda s: s if s.get("need") and s.get("btn") else None)(_tabbar(env)),
                  "页签溢出（需要且出现 ... 按钮）", timeout=20)
    env.check("溢出按钮出现且与几何判定一致",
              st.get("btn") is True and st.get("need") is True, st)
    env.check("溢出按钮绝对定位", st.get("btnPos") == "absolute", st.get("btnPos"))
    gap = st.get("btnRightGap")
    env.check("溢出按钮贴外层右边界",
              gap is not None and abs(int(gap)) <= 2, gap)
    env.check("内层内容宽 > 外层可用宽",
              int(st.get("innerScroll") or 0) > int(st.get("barW") or 0) + 8, st)
    env.check("多余页签不被隐藏/零宽",
              not (st.get("hiddenClass") or st.get("displayNone")
                   or st.get("visHidden") or st.get("zeroWidth")), st)
    names = st.get("names") or []
    env.check("页签数 ≥ 13", int(st.get("n") or 0) >= 13, st.get("n"))

    # 3) 弹框列出全部页签（顺序一致），选中第 5 项 → 移到首位，面板顺序不变
    env.js("document.querySelector('.code-view .tb-bar.tb-bottom .tb-more')"
           "?.dispatchEvent(new MouseEvent('click',{bubbles:true})); 'ok'")
    env.wait(lambda: True if env.js("!!document.querySelector('.tb-more-pop')") is True else None,
             "溢出弹框出现", timeout=15)
    pop = env.jsj("JSON.stringify([...document.querySelectorAll('.tb-more-item .tb-more-name')]"
                  ".map(n=>n.textContent.trim()))") or []
    env.eq("弹框列出全部页签且顺序一致", pop, names)

    pick = 4
    picked = pop[pick]
    expected = [picked] + names[:pick] + names[pick + 1:]
    panels_before = _panel_order(env)
    env.js("""(()=>{const it=document.querySelectorAll('.tb-more-item')[%d];
      if(it) it.dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok';})()""" % pick)
    env.wait(lambda: True if not env.js("!!document.querySelector('.tb-more-pop')") else None,
             "选中后弹框关闭", timeout=10)
    after = _tabbar(env).get("names") or []
    env.eq("选中项移到显示首位、其余相对顺序顺移", after, expected)
    env.check("选中后激活该页签", _active_name(env) == picked, _active_name(env))
    env.eq("选中重排不改内容面板顺序", _panel_order(env), panels_before)

    # 4) 可用宽变化 → 按钮出现/消失与几何判定一致
    idx = _preview_handle_idx(env)
    diag = env.jsj("""JSON.stringify((()=>{
      const cv=document.querySelector('.code-view');
      const cr=cv?cv.getBoundingClientRect():null;
      const hs=[...document.querySelectorAll('.split-handle,.split-resizer')].map(h=>{
        const r=h.getBoundingClientRect();
        return {cls:h.className, w:Math.round(r.width), h:Math.round(r.height),
                l:Math.round(r.left), r:Math.round(r.right), t:Math.round(r.top)};});
      return {cv: cr?{l:Math.round(cr.left),r:Math.round(cr.right)}:null, hs};})())""") or {}
    env.note("预览分隔条诊断", json.dumps(diag, ensure_ascii=False)[:600])
    env.check("找到预览 pane 左侧分隔条", idx >= 0, idx)
    if idx >= 0:
        _drag_preview_split(env, idx, -700)   # 预览变宽
        wide = _tabbar(env)
        env.check("加宽后按钮出现/消失与几何一致",
                  bool(wide.get("btn")) == bool(wide.get("need")), wide)
        _drag_preview_split(env, idx, 700)    # 恢复原宽
        back = _tabbar(env)
        env.check("恢复宽度后按钮存在性与判定一致",
                  bool(back.get("btn")) == bool(back.get("need")), back)
        _drag_preview_split(env, idx, 300)    # 更窄 → 必溢出
        narrow = _tabbar(env)
        env.check("更窄后必溢出且按钮出现",
                  narrow.get("need") is True and narrow.get("btn") is True, narrow)
        _drag_preview_split(env, idx, -300)   # 还原

    env.check("溢出弹框无残留", not env.js("!!document.querySelector('.tb-more-pop')"))

    # 5) 清理本轮页签（先关溢出页签，再关浮条夹具；不影响既有页签）
    _close_tabs(env, overflow + [sel_rel])

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"][errs_before:]
    env.check("预览页签/浮条组无新增前端错误", len(errs) == 0, errs[:3])