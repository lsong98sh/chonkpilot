# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ⑭：**预览区**（迁移自 test_preview.py P1–P4）。

* Markdown 渲染（h1 / strong / inline code）
* 源码 ↔ 预览切换（code-show-source / code-show-preview）
* 不支持/二进制文件 → Hex View 退化
* 临时页签双击 → 固定（is-temporary 消失）

夹具使用本测试点自建的 markdown（含 h1 / 粗体 / 行内代码），避免依赖演示 README 的具体排版。
"""

import json
import os
import time

from .framework import case


def _active(env):
    return env.jsj("JSON.stringify((()=>{const t=document.querySelector("
                   "'.code-view .tb-tab.active');if(!t)return null;"
                   "return {name:(t.querySelector('.tb-name')||{}).textContent?.trim()||'',"
                   "temp:t.classList.contains('is-temporary'),"
                   "render:t.getAttribute('data-render')||''}})())")


def _visible_panel_js(expr):
    """在当前**可见** tab-panel 内求值（历史页签可能保留同名隐藏副本）。"""
    return ("(()=>{const ps=[...document.querySelectorAll('.tab-panel')]"
            ".filter(p=>getComputedStyle(p).display!=='none');"
            "const p=ps.length?ps[ps.length-1]:null;"
            "if(!p)return null;%s})()" % expr)


@case("S-PREVIEW-01", "预览", "Markdown 渲染 / 源码预览切换 / 二进制 Hex / 临时页签双击固定",
      stage="seeded", order=82, fp="33 预览区（test_preview P1–P4）",
      desc="打开含 h1/粗体/行内代码的 markdown → 预览渲染；`code-show-source` → `.source-code` "
           "含原文，`code-show-preview` → 回到渲染；打开二进制 → `.hex-view` 首行十六进制；"
           "临时页签双击 → 去掉 is-temporary。")
def s_preview_01(env):
    md_rel = "e2e_prev_%s.md" % env.run_id
    bin_rel = "e2e_prev_%s.bin" % env.run_id
    env.write_text(md_rel, "# 预览标题\n\n这是**加粗**正文与 `代码`。\n")
    with open(env.path(bin_rel), "wb") as f:
        f.write(bytes([0x48, 0x65, 0x00, 0x01, 0xFF, 0x0A]))

    env.emit("file-open", {"path": env.posix(env.path(md_rel)), "temporary": True})
    env.wait(lambda: True if (_active(env) or {}).get("name") == md_rel else None,
             "Markdown 页签激活", timeout=20)

    def _preview():
        return env.jsj("JSON.stringify(%s)" % _visible_panel_js("""const c=p.querySelector('.code-preview-content')||p;
          return {h1:(c.querySelector('h1')||{}).textContent||'',
                  strong:c.querySelectorAll('strong').length,
                  code:c.querySelectorAll('code').length}"""))

    md = env.wait(lambda: _preview() if (_preview() or {}).get("strong", 0) >= 1 else None,
                  "Markdown 渲染（strong 出现）", timeout=25)
    env.check("Markdown h1 渲染", "预览标题" in (md.get("h1") or ""), md.get("h1"))
    env.check("Markdown strong ≥1", int(md.get("strong") or 0) >= 1, md.get("strong"))
    env.check("Markdown inline code ≥1", int(md.get("code") or 0) >= 1, md.get("code"))

    def _src():
        return str(env.js(_visible_panel_js(
            "const c=p.querySelector('.source-code');return c?c.textContent:''")) or "")

    env.js("window.mq.emit('code-show-source'); 'ok'")
    src = env.wait(lambda: _src() if "预览标题" in _src() else None,
                   "源码视图显示原文", timeout=20)
    env.check("源码视图含 Markdown 原文", "预览标题" in src and "**加粗**" in src, src[:80])

    env.js("window.mq.emit('code-show-preview'); 'ok'")
    env.wait(lambda: True if not _src() else None,
             "切回预览（源码视图移除）", timeout=20)
    env.check("切回预览后恢复渲染", int((_preview() or {}).get("strong") or 0) >= 1)

    # 二进制 → hex
    env.emit("file-open", {"path": env.posix(env.path(bin_rel)), "temporary": True})
    env.wait(lambda: True if (_active(env) or {}).get("name") == bin_rel else None,
             "二进制页签激活", timeout=20)

    def _hex():
        return str(env.js(_visible_panel_js(
            "const c=p.querySelector('.hex-row .hex-bytes');return c?c.textContent:''")) or "")

    hexv = env.wait(lambda: _hex() if "48" in _hex() and "65" in _hex() else None,
                    "二进制打开为 hex 视图（首行 hex 数据）", timeout=25)
    env.check("hex 视图含 48/65 十六进制",
              ("48" in hexv) and ("65" in hexv), hexv[:80])

    # 临时页签双击 → 固定
    env.emit("file-open", {"path": env.posix(env.path(md_rel)), "temporary": True})
    info = env.wait(lambda: _active(env) if (_active(env) or {}).get("name") == md_rel
                    else None, "Markdown 临时页签激活", timeout=20)
    env.check("打开时为临时页签", (info or {}).get("temp") is True, info)
    env.js("document.querySelector('.code-view .tb-tab.active')"
           "?.dispatchEvent(new MouseEvent('dblclick',{bubbles:true})); 'ok'")
    env.wait(lambda: True if (_active(env) or {}).get("temp") is False else None,
             "双击后转固定页签", timeout=15)
    env.check("双击后 is-temporary 消失", (_active(env) or {}).get("temp") is False, _active(env))

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and "method not implemented" not in (e.get("text") or "")]
    env.check("预览组无前端错误", len(errs) == 0, errs[:3])

    for rel in (md_rel, bin_rel):
        try:
            os.remove(env.path(rel))
        except OSError:
            pass
