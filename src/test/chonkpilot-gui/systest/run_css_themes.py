# -*- coding: utf-8 -*-
"""L4 CSS 专项：三主题（light / dark / nord）× 全界面显示确认。

目标（用户 2026-11-07 口径）：每个界面在三种主题下显示正常，含插件界面。
本套件做两件事：

1. **截图取证**：`_css_themes_<ts>/<theme>/NN-name.png`（逐界面）。
2. **计算样式审计**（同一时刻采集，作为视觉复核的索引）：
   - 暗色主题下的「浅底大块」= 未跟随主题的硬编码 `#fff` 容器（CSS 专项主信号）；
   - 文本对比度（WCAG，按前景色/有效底色计算，<4.0 记异常）；
   - 文本容器裁切（overflow:hidden 且 scroll 尺寸溢出）；
   - 每个 iframe（插件自服务页）的 `data-theme` / `body` 底色，验证主题下发链路。

界面清单（每主题）：
  - 布局：默认空态 / 项目树 / 会话 / 记忆 / 扩展（5 个子 tab）
  - 预览：Markdown 渲染 / 代码渲染
  - 功能页：settings-llm（一览+默认模型）/ settings-mcp / settings-paths（系统/用户/项目）
           / settings-params / settings-tool-async / settings-tool-sandbox
           / settings-project（全部页签）/ scenario / plugin-manager
           / status-memory / status-compress
  - 弹窗：添加 LLM / 添加 MCP
  - 插件页：plugin-list 里每个插件的 config_view / views（iframe，逐页）

驱动口径（50 §1 / 52）：一律经既有 mq 主题（`theme-select` / `preview-tab-open` / `file-open` /
`filetree-mode-select` / `config-add-*`）与 DOM 回读，不直调组件方法。

运行：
    python run_css_themes.py                 # 三主题全量
    python run_css_themes.py --themes light  # 单主题侦察
退出码：0 = 无 audit 异常；1 = 有异常（截图与报告仍完整落盘）。
"""

import argparse
import datetime
import json
import os
import shutil
import sys
import time
from collections import Counter

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # noqa: E402
import ask_center as ac_mod  # noqa: E402
from chonk_client import ChonkClient, TestError  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
THEMES = ("light", "dark", "nord")

# 预览区功能页（kind, 根选择器, 等待选择器）
PAGES = (
    ("settings-llm", ".settings-page", ".settings-page"),
    ("settings-mcp", ".settings-page", ".settings-page"),
    ("settings-paths", ".settings-page", ".settings-page"),
    ("settings-params", ".settings-page", ".settings-page"),
    ("settings-tool-async", ".settings-page", ".settings-page"),
    ("settings-tool-sandbox", ".settings-page", ".settings-page"),
    ("settings-project", ".project-config-panel", ".project-config-panel"),
    ("scenario", ".code-view .dialog-content", ".code-view .dialog-content"),
    ("plugin-manager", ".plugin-manager", ".plugin-manager"),
    ("status-memory", ".task-detail", ".task-detail"),
    ("status-compress", ".task-detail", ".task-detail"),
)

EXPLORER_MODES = (
    ("project", "项目"),
    ("sessions", "会话"),
    ("memory", "记忆"),
    ("extensions", "扩展"),
)

# 计算样式审计 JS（在宿主页面执行）。返回 JSON 文本。
AUDIT_JS = r"""
(function(){
  const VW=innerWidth, VH=innerHeight, AREA=Math.max(1,VW*VH);
  function parse(c){const m=String(c||'').match(/rgba?\(([^)]+)\)/);if(!m)return null;
    const p=m[1].split(',').map(s=>parseFloat(s.trim()));
    return {r:p[0],g:p[1],b:p[2],a:p.length>3?p[3]:1};}
  function lum(c){if(!c)return 0;const f=v=>{v/=255;return v<=0.03928?v/12.92:Math.pow((v+0.055)/1.055,2.4)};
    return 0.2126*f(c.r)+0.7152*f(c.g)+0.0722*f(c.b);}
  function ratio(a,b){const l1=lum(a),l2=lum(b);const hi=Math.max(l1,l2),lo=Math.min(l1,l2);
    return (hi+0.05)/(lo+0.05);}
  function effBg(el){let n=el;while(n&&n.nodeType===1){const cs=getComputedStyle(n);
    const c=parse(cs.backgroundColor);if(c&&c.a>0.5)return c;
    if(cs.backgroundImage&&cs.backgroundImage!=='none')return null;n=n.parentElement;}
    return parse(getComputedStyle(document.body).backgroundColor);}
  function sel(el){let s=el.tagName.toLowerCase();if(el.id)s+='#'+el.id;
    const cls=(typeof el.className==='string'?el.className:'').trim().split(/\s+/).filter(Boolean).slice(0,3);
    if(cls.length)s+='.'+cls.join('.');return s;}
  function visible(el){const r=el.getBoundingClientRect();if(r.width<1||r.height<1)return false;
    const cs=getComputedStyle(el);if(cs.display==='none'||cs.visibility==='hidden')return false;
    if(parseFloat(cs.opacity||'1')<0.05)return false;
    return r.bottom>0&&r.top<VH&&r.right>0&&r.left<VW;}
  const lightBg=[],darkBg=[],lowContrast=[],clipped=[];
  for(const el of document.querySelectorAll('body *')){
    if(!visible(el))continue;
    const tag=el.tagName;
    if(['IMG','SVG','CANVAS','VIDEO','IFRAME','PATH','USE','STYLE','SCRIPT'].indexOf(tag)>=0)continue;
    const cs=getComputedStyle(el);const r=el.getBoundingClientRect();const a=(r.width*r.height)/AREA;
    const bg=parse(cs.backgroundColor);
    if(bg&&bg.a>0.8&&a>0.004&&r.width>=12&&r.height>=12){const L=lum(bg);
      if(L>0.72)lightBg.push({sel:sel(el),bg:cs.backgroundColor,area:+a.toFixed(3),text:(el.textContent||'').trim().slice(0,30)});
      else if(L<0.03)darkBg.push({sel:sel(el),bg:cs.backgroundColor,area:+a.toFixed(3)});
    }
    const hasText=[...el.childNodes].some(n=>n.nodeType===3&&n.textContent.trim());
    if(hasText){const fg=parse(cs.color);const eb=effBg(el);
      if(fg&&eb&&fg.a>0.5){const rr=ratio(fg,eb);const fs=parseFloat(cs.fontSize)||13;
        const bold=(parseInt(cs.fontWeight)||400)>=600;const big=fs>=18||(bold&&fs>=14);
        const min=big?3:4.0;
        if(rr<min)lowContrast.push({sel:sel(el),fg:cs.color,bg:'rgb('+eb.r+','+eb.g+','+eb.b+')',
          ratio:+rr.toFixed(2),fs:fs,text:(el.textContent||'').trim().slice(0,40)});
      }}
    if((cs.overflowX==='hidden'||cs.overflowY==='hidden')&&
       cs.textOverflow!=='ellipsis'&&
       (!cs.webkitLineClamp||cs.webkitLineClamp==='none')&&
       (el.scrollWidth-el.clientWidth>8||el.scrollHeight-el.clientHeight>8)&&
       (el.textContent||'').trim().length>0){
      // 滚动容器本体（内部有 overflow:auto/scroll 的子节点）不算「裁切」：
      // 消息列表/表单滚动区属正常滚动，避免把可滚动区域误报成裁切。
      const hasScroller=[...el.querySelectorAll('*')].some(d=>{
        const s=getComputedStyle(d);
        return ((s.overflowY==='auto'||s.overflowY==='scroll')&&d.scrollHeight>d.clientHeight+2)||
               ((s.overflowX==='auto'||s.overflowX==='scroll')&&d.scrollWidth>d.clientWidth+2);
      });
      const selfScroll=((cs.overflowY==='auto'||cs.overflowY==='scroll')&&
                        el.scrollHeight>el.clientHeight+2);
      if(!hasScroller&&!selfScroll)clipped.push({sel:sel(el),sw:el.scrollWidth,cw:el.clientWidth,
        sh:el.scrollHeight,ch:el.clientHeight,text:(el.textContent||'').trim().slice(0,30)});
    }
  }
  const cap=a=>a.slice(0,40);

  // 子文档（插件 iframe）同样做「暗色浅底 / 低对比」审计：插件界面也属本次验收范围。
  function auditDoc(doc, win){
    const out={lightBg:0,lowContrast:0,clipped:0,samples:{lightBg:[],lowContrast:[]}};
    if(!doc||!doc.body||!win)return out;
    const W=win.innerWidth||doc.documentElement.clientWidth,V=win.innerHeight||doc.documentElement.clientHeight;
    const A=Math.max(1,W*V);
    const bodyBg=parse(getComputedStyle(doc.body).backgroundColor);
    for(const el of doc.body.querySelectorAll('*')){
      const r=el.getBoundingClientRect();
      if(r.width<1||r.height<1||r.bottom<0||r.top>V||r.right<0||r.left>W)continue;
      const cs=win.getComputedStyle(el);
      if(cs.display==='none'||cs.visibility==='hidden')continue;
      if(['IMG','SVG','CANVAS','VIDEO','IFRAME','PATH','USE'].indexOf(el.tagName)>=0)continue;
      const bg=parse(cs.backgroundColor);
      if(bg&&bg.a>0.8&&(r.width*r.height)/A>0.004&&r.width>=12&&r.height>=12&&lum(bg)>0.72){
        out.lightBg++;if(out.samples.lightBg.length<6)
          out.samples.lightBg.push({sel:sel(el),bg:cs.backgroundColor});
      }
      const hasText=[...el.childNodes].some(n=>n.nodeType===3&&n.textContent.trim());
      if(hasText){const fg=parse(cs.color);
        let eb=bodyBg;let n=el;
        while(n&&n.nodeType===1){const c1=parse(win.getComputedStyle(n).backgroundColor);
          if(c1&&c1.a>0.5){eb=c1;break}if(win.getComputedStyle(n).backgroundImage!=='none'){eb=null;break}
          n=n.parentElement;}
        if(fg&&eb&&fg.a>0.5){const rr=ratio(fg,eb);const fs=parseFloat(cs.fontSize)||13;
          const bold=(parseInt(cs.fontWeight)||400)>=600;
          if(rr<(fs>=18||(bold&&fs>=14)?3:4.0)){out.lowContrast++;
            if(out.samples.lowContrast.length<6)out.samples.lowContrast.push(
              {sel:sel(el),fg:cs.color,ratio:+rr.toFixed(2),text:(el.textContent||'').trim().slice(0,30)});}}}
      if((cs.overflowX==='hidden'||cs.overflowY==='hidden')&&
         (el.scrollWidth-el.clientWidth>8||el.scrollHeight-el.clientHeight>8)&&
         (el.textContent||'').trim().length>0)out.clipped++;
    }
    return out;
  }

  const iframes=[...document.querySelectorAll('iframe')].map(f=>{
    const o={src:(f.getAttribute('src')||'').slice(0,140),
      w:Math.round(f.getBoundingClientRect().width),h:Math.round(f.getBoundingClientRect().height)};
    try{const d=f.contentDocument;
      if(d&&d.documentElement){o.docTheme=d.documentElement.getAttribute('data-theme');
        o.ck=f.contentWindow.ckTheme?f.contentWindow.ckTheme.current():null;
        o.bodyBg=getComputedStyle(d.body).backgroundColor;o.readyState=d.readyState;
        const sub=auditDoc(d,f.contentWindow);o.counts=sub;o.inner=sub.samples;}
    }catch(e){o.err=String(e).slice(0,80);}
    return o;
  });
  const root=getComputedStyle(document.documentElement);
  return JSON.stringify({
    theme:document.documentElement.getAttribute('data-theme'),
    scheme:root.colorScheme,
    vars:{bg:root.getPropertyValue('--bg-primary').trim(),
          ckbg:root.getPropertyValue('--ck-bg').trim(),
          lfbg:root.getPropertyValue('--lf-bg').trim(),
          text:root.getPropertyValue('--text-primary').trim(),
          cktext:root.getPropertyValue('--ck-text').trim()},
    counts:{lightBg:lightBg.length,lowContrast:lowContrast.length,clipped:clipped.length},
    lightBg:cap(lightBg),darkBg:cap(darkBg),
    lowContrast:cap(lowContrast.sort((a,b)=>a.ratio-b.ratio)),
    clipped:cap(clipped),iframes});
})()
"""


def loads(v):
    for _ in range(4):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


class Ctx:
    def __init__(self, client, out, theme):
        self.c = client
        self.out = out
        self.theme = theme
        self.records = []
        self.shots = 0

    def ev(self, js, timeout=12000):
        return loads(self.c.eval(js, timeout))

    def mq(self, topic, payload=None):
        if payload is None:
            self.c.mq_emit(topic)
        else:
            self.c.mq_emit(topic, payload)

    def wait(self, expr, timeout=12.0, desc=""):
        end = time.time() + timeout
        while time.time() < end:
            try:
                if self.ev(expr):
                    return True
            except Exception:
                pass
            time.sleep(0.3)
        raise TestError("[%s] 等待超时：%s" % (self.theme, desc or expr))

    def wait_visible(self, sel, timeout=12.0):
        return self.wait("(function(){const es=[...document.querySelectorAll(%s)];"
                         "return es.some(e=>e.getBoundingClientRect().width>0)})()"
                         % json.dumps(sel), timeout, "可见 " + sel)

    def close_all(self):
        self.mq("preview-tab-close-all")
        self.mq("dialog-close")
        time.sleep(0.5)

    def shot(self, name):
        self.shots += 1
        safe = name.replace("/", "_").replace(" ", "_")
        path = os.path.join(self.out, self.theme, "%02d-%s.png" % (self.shots, safe))
        try:
            self.c.screenshot(path)
        except Exception as e:
            print("    [warn] screenshot %s 失败: %s" % (safe, e), flush=True)
            return None
        audit = self.audit(safe)
        return path, audit

    def audit(self, name):
        data = None
        for _ in range(3):
            try:
                data = self.ev(AUDIT_JS, 25000)
            except Exception as e:
                data = {"error": str(e)}
            if isinstance(data, dict):
                break
            time.sleep(0.6)
        if not isinstance(data, dict):
            data = {"raw": data}
        self.records.append({"screen": name, "audit": data})
        c = data.get("counts") or {}
        flags = []
        if self.theme != "light" and c.get("lightBg"):
            flags.append("浅底块=%s" % c["lightBg"])
        if c.get("lowContrast"):
            flags.append("低对比=%s" % c["lowContrast"])
        if c.get("clipped"):
            flags.append("裁切=%s" % c["clipped"])
        bad_iframes = [f for f in (data.get("iframes") or [])
                       if f.get("docTheme") and f.get("docTheme") != self.theme]
        if bad_iframes:
            flags.append("iframe主题不一致=%d" % len(bad_iframes))
        inner_light = sum(((f.get("counts") or {}).get("lightBg") or 0)
                          for f in (data.get("iframes") or []))
        inner_contrast = sum(((f.get("counts") or {}).get("lowContrast") or 0)
                             for f in (data.get("iframes") or []))
        if self.theme != "light" and inner_light:
            flags.append("iframe浅底=%d" % inner_light)
        if inner_contrast:
            flags.append("iframe低对比=%d" % inner_contrast)
        print("    [audit] %-40s %s" % (name, "  ".join(flags) if flags else "ok"), flush=True)
        return data

    def click_tab(self, scope, label):
        js = ("(function(){const R=[...document.querySelectorAll(%s)].find(e=>e.getBoundingClientRect().width>0);"
              "if(!R)return 'no-root';const t=[...R.querySelectorAll('.b-tabs-item')]"
              ".find(x=>x.textContent.trim()==%s);if(!t)return 'no-tab';t.click();return 'ok';})()"
              % (json.dumps(scope), json.dumps(label)))
        r = self.ev(js)
        time.sleep(0.7)
        return r

    def tab_labels(self, scope):
        return self.ev("(function(){const R=[...document.querySelectorAll(%s)]"
                       ".find(e=>e.getBoundingClientRect().width>0);if(!R)return [];"
                       "return [...R.querySelectorAll('.b-tabs-item')].map(t=>t.textContent.trim());})()"
                       % json.dumps(scope)) or []

    def open_page(self, kind, root, wait=15.0):
        self.close_all()
        self.mq("preview-tab-open", {"kind": kind})
        self.wait_visible(root, wait)


# ── 工作区夹具 ───────────────────────────────────────────────

def seed_workspace(ws):
    os.makedirs(os.path.join(ws, "src"), exist_ok=True)
    with open(os.path.join(ws, "README.md"), "w", encoding="utf-8") as f:
        f.write("# CSS Theme Fixture\n\n"
                "本文件用于三主题下的 Markdown 渲染确认。\n\n"
                "## 二级标题\n\n"
                "- 列表项 A\n- 列表项 B\n- 列表项 C\n\n"
                "[链接](https://example.com)\n\n"
                "```python\nprint('hello theme')\n```\n\n"
                "| 主题 | 底色 |\n| --- | --- |\n| light | #f8f9fa |\n| dark | #1e1e2e |\n\n"
                "> 引用块：用于确认引用底色/边框。\n")
    with open(os.path.join(ws, "src", "sample.py"), "w", encoding="utf-8") as f:
        f.write("# -*- coding: utf-8 -*-\n"
                "def greet(name):\n"
                "    return f'hello {name}'\n\n"
                "if __name__ == '__main__':\n"
                "    print(greet('chonkpilot'))\n")
    with open(os.path.join(ws, "data.json"), "w", encoding="utf-8") as f:
        json.dump({"theme": "light", "themes": ["light", "dark", "nord"], "n": 3},
                  f, ensure_ascii=False, indent=2)
    with open(os.path.join(ws, "notes.txt"), "w", encoding="utf-8") as f:
        f.write("plain text fixture\n" * 40)
    # 图片预览样本（可选）
    src_img = os.path.join(_h._REPO_ROOT, "image", "chonkpilot.png")
    if os.path.exists(src_img):
        shutil.copy2(src_img, os.path.join(ws, "logo.png"))
    # git 仓库（文件历史页非空）
    try:
        import subprocess
        subprocess.run(["git", "init", "-q"], cwd=ws, timeout=20,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(["git", "add", "-A"], cwd=ws, timeout=20,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        subprocess.run(["git", "-c", "user.name=css", "-c", "user.email=css@test",
                        "commit", "-qm", "css fixture"], cwd=ws, timeout=20,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except Exception:
        pass


# ── 主题 ────────────────────────────────────────────────────

def apply_theme(ctx, theme, timeout=12.0):
    ctx.mq("theme-select", {"themeId": theme})
    end = time.time() + timeout
    while time.time() < end:
        try:
            cur = ctx.ev("document.documentElement.getAttribute('data-theme')")
            if cur == theme:
                time.sleep(0.6)
                return True
        except Exception:
            pass
        time.sleep(0.3)
    raise TestError("主题未切换：want=%s" % theme)


# ── 界面遍历 ───────────────────────────────────────────────

CHAT_MD = (
    "## 主题渲染检查\n\n"
    "这是一段 **粗体**、`行内代码` 与 [链接](https://example.com) 的混排。\n\n"
    "- 列表一\n- 列表二\n\n"
    "```python\nprint('hello dark')\n```\n\n"
    "| 主题 | 底色 |\n| --- | --- |\n| dark | #1e1e2e |\n\n"
    "> 引用块用于确认底色与左边框。\n"
)


def walk_chat_messages(ctx):
    """经 mq 注入一轮带 markdown/代码/工具卡片的对话（复用 run_chat_ui 口径，不依赖 LLM）。"""
    sid = "css-theme-%s" % ctx.theme
    ctx.mq("session-changed", {"session_id": sid})
    time.sleep(0.8)
    ctx.mq("message-send", {"sessionId": sid, "text": "CSS 三主题检查：给一段带代码块的回答"})
    time.sleep(0.6)
    for typ, payload in (
        ("reason", {"text": "先检查标题 / 列表 / 代码块 / 表格的底色与前景。"}),
        ("text", {"text": CHAT_MD}),
        ("tool-call", {"tool_call_id": "css-tc-1", "tool": "file_read",
                       "arguments": "{\"targets\":[{\"path\":\"README.md\"}],\"tool_call_display_name\":\"读取\"}",
                       "simplified": "file_read", "purpose": "读取"}),
    ):
        d = {"session": sid, "session_id": sid, "type": typ}
        d.update(payload)
        ctx.mq("llm-receive", d)
        time.sleep(0.5)
    ctx.mq("tool-pair", {"session_id": sid, "tool_id": "css-tc-1", "status": "done"})
    ctx.mq("llm-complete", {"session": sid, "status": "complete"})
    try:
        ctx.wait("document.querySelectorAll('.message-item').length>=1", 10, "chat 消息")
        time.sleep(1.0)
        ctx.shot("chat.populated")
    except TestError as e:
        print("    [warn] 对话注入未渲染：%s" % e, flush=True)
    return sid


def walk_layout(ctx, theme):
    ctx.close_all()
    ctx.shot("layout.default")
    sid = walk_chat_messages(ctx)

    for mode, label in EXPLORER_MODES:
        ctx.mq("filetree-mode-select", {"mode": mode})
        time.sleep(0.8)
        ctx.shot("explorer.%s" % mode)
        if mode == "extensions":
            # 扩展页 5 个子 tab（知识/技能/工具/命令/智能体）
            subs = ctx.ev("JSON.stringify([...document.querySelectorAll('.ext-subtab')]"
                          ".map(e=>e.textContent.trim()))")
            for sub in (subs or []):
                js = ("(()=>{const t=[...document.querySelectorAll('.ext-subtab')]"
                      ".find(e=>e.textContent.trim()===%s);if(!t)return 'no';t.click();return 'ok';})()"
                      % json.dumps(sub))
                if ctx.ev(js) == "ok":
                    time.sleep(0.8)
                    ctx.shot("explorer.extensions.%s" % sub.replace("/", "_"))

    # 预览：Markdown / 代码
    ctx.close_all()
    ctx.mq("file-open", {"path": os.path.join(ctx_ws[0], "README.md").replace("\\", "/")})
    ctx.wait_visible(".code-view", 15)
    time.sleep(1.0)
    ctx.shot("preview.markdown")

    ctx.mq("file-open", {"path": os.path.join(ctx_ws[0], "src", "sample.py").replace("\\", "/")})
    time.sleep(1.0)
    ctx.shot("preview.code")
    return sid


ctx_ws = [""]


def walk_pages(ctx):
    for kind, root, _wait in PAGES:
        try:
            ctx.open_page(kind, root, 18 if kind.startswith("status-") else 15)
        except TestError as e:
            print("    [warn] 打开 %s 失败：%s" % (kind, e), flush=True)
            continue
        time.sleep(0.8)
        tabs = ctx.tab_labels(root)
        if len(tabs) <= 1:
            ctx.shot("page.%s" % kind)
            continue
        for i, label in enumerate(tabs):
            if i:
                if ctx.click_tab(root, label) != "ok":
                    continue
                time.sleep(0.8)
            ctx.shot("page.%s.%02d-%s" % (kind, i, label.replace("/", "_")))


def walk_dialogs(ctx):
    for kind, topic, root in (
        ("llm-add", "config-add-llm", ".edit-dialog"),
        ("mcp-add", "config-add-mcp", ".mcp-dialog"),
    ):
        page = "settings-llm" if kind == "llm-add" else "settings-mcp"
        try:
            ctx.open_page(page, ".settings-page", 15)
            ctx.mq(topic)
            ctx.wait_visible(".dialog-shell", 15)
            ctx.wait_visible(root, 15)
            time.sleep(0.6)
            ctx.shot("dialog.%s" % kind)
        except TestError as e:
            print("    [warn] 打开弹窗 %s 失败：%s" % (kind, e), flush=True)
        finally:
            ctx.mq("dialog-close")
            time.sleep(0.5)


def walk_wizard(ctx):
    """场景向导（DialogShell + wizard-root）：三主题下弹窗底色/表单/步骤条。"""
    try:
        ctx.close_all()
        ctx.mq("agent-wizard", {"reason": "missing",
                                "work_dir": ctx_ws[0],
                                "spec_path": os.path.join(ctx_ws[0], ".chonkpilot", "project_spec.md")})
        ctx.wait_visible(".dialog-shell .wizard-root", 20)
        time.sleep(1.0)
        ctx.shot("dialog.wizard.step1")
        labels = ctx.ev("JSON.stringify([...document.querySelectorAll('.wz-rail-label')]"
                        ".map(e=>e.textContent.trim()))") or []
        if labels:
            idx = len(labels) - 1
            ctx.ev("(()=>{const es=[...document.querySelectorAll('.wz-rail-item')];"
                   "if(es[%d]){es[%d].click();return 'ok'}return 'no'})()" % (idx, idx))
            time.sleep(0.9)
            ctx.shot("dialog.wizard.last")
    except TestError as e:
        # 场景向导的 `_wizardOpen` 是模块级防重入 flag：DialogShell 若被 `dialog-close`
        # 隐藏而未卸载，后续主题再发 `agent-wizard` 会被直接忽略。重载页面复位模块态后重试一次
        # （主题由 usr 配置重新应用，不会丢）。
        print("    [warn] 向导未打开（%s）→ 重载复位后重试" % e, flush=True)
        try:
            _h.reload_and_settle(ctx.c, max_wait=90)
            cur = ctx.ev("document.documentElement.getAttribute('data-theme')")
            if cur != ctx.theme:
                ctx.mq("theme-select", {"themeId": ctx.theme})
                time.sleep(1.2)
            ctx.mq("agent-wizard", {"reason": "missing",
                                    "work_dir": ctx_ws[0],
                                    "spec_path": os.path.join(ctx_ws[0], ".chonkpilot", "project_spec.md")})
            ctx.wait_visible(".dialog-shell .wizard-root", 20)
            time.sleep(1.0)
            ctx.shot("dialog.wizard.step1")
            labels = ctx.ev("JSON.stringify([...document.querySelectorAll('.wz-rail-label')]"
                            ".map(e=>e.textContent.trim()))") or []
            if labels:
                idx = len(labels) - 1
                ctx.ev("(()=>{const es=[...document.querySelectorAll('.wz-rail-item')];"
                       "if(es[%d]){es[%d].click();return 'ok'}return 'no'})()" % (idx, idx))
                time.sleep(0.9)
                ctx.shot("dialog.wizard.last")
        except Exception as e2:
            print("    [warn] 向导重试仍失败：%s" % e2, flush=True)
    finally:
        ctx.mq("dialog-close")
        time.sleep(0.5)


def walk_layout_editor(ctx):
    """布局编辑器（平台基座 LfLayoutEditor）：设计态面板是硬编码浅色的重灾区。"""
    try:
        ctx.close_all()
        ctx.ev("(()=>{const b=document.querySelector('.lp-btn');if(!b)return 'no-btn';b.click();return 'ok'})()")
        time.sleep(0.5)
        r = ctx.ev("(()=>{const t=[...document.querySelectorAll('.lp-item')]"
                   ".find(e=>e.textContent.trim()==='编辑当前布局');if(!t)return 'no-item';t.click();return 'ok'})()")
        if r != "ok":
            raise TestError("布局菜单项不可用：%s" % r)
        ctx.wait_visible(".editor-mask .layout-editor", 30)
        time.sleep(1.2)
        ctx.shot("dialog.layout-editor")
    except TestError as e:
        print("    [warn] 布局编辑器未打开：%s" % e, flush=True)
    finally:
        # 布局编辑器不是 DialogShell（自管 .editor-mask），必须点它自己的关闭按钮
        ctx.ev("(()=>{const b=document.querySelector('.editor-mask .ed-head .lf-btn.close');"
               "if(b){b.click();return 'ok'}return 'no'})()")
        time.sleep(0.4)
        if ctx.ev("!!document.querySelector('.editor-mask')"):
            ctx.ev("document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));'ok'")
            time.sleep(0.4)


def walk_drawer(ctx):
    """抽屉（LfDrawerHost）：插件动作「索引进度」→ 抽屉 + 插件 iframe。"""
    try:
        ctx.open_page("plugin-manager", ".plugin-manager")
        time.sleep(0.6)
        r = ctx.ev("(()=>{const bs=[...document.querySelectorAll('.pm-btn')];"
                   "const b=bs.find(x=>x.textContent.trim()==='索引进度');"
                   "if(!b)return 'no-btn';b.click();return 'ok'})()")
        if r != "ok":
            raise TestError("索引进度动作不可用：%s" % r)
        ctx.wait_visible(".drawer", 20)
        time.sleep(1.0)
        ctx.shot("drawer.codegraph-index")
        # 关抽屉：可能有多层 → 逐个点关闭并等 `.drawer` 归零（否则后续截图都带白色抽屉）
        for _ in range(4):
            ctx.ev("(()=>{const b=document.querySelector('.drawer .d-btn');"
                   "if(b){b.click();return 'ok'}return 'no'})()")
            time.sleep(0.4)
            if not ctx.ev("!!document.querySelector('.drawer')"):
                break
    except TestError as e:
        print("    [warn] 抽屉未打开：%s" % e, flush=True)


def walk_windows(ctx, sid):
    """独立窗口：对话窗口 + Ask 中心（验证主题在独立 WebView2 文档中的落地）。"""
    # 对话窗口（独立窗口）：先确保有一个真实会话可开窗
    try:
        real_sid = ""
        try:
            cr = ctx.c.req("session-create", {}, timeout=20) or {}
            real_sid = ((cr.get("session") or {}).get("session_id") or cr.get("session_id")
                        or (cr.get("session") or {}).get("id") or "")
        except Exception:
            real_sid = ""
        r = ctx.c.req("gui.window.open-chat", {"session_id": real_sid or sid}, timeout=20) or {}
        wid = r.get("window_id") or ""
        if wid:
            wc = ChonkClient(base=ctx.c.base, timeout=30, window_id=wid)
            wctx = Ctx(wc, ctx.out, ctx.theme)
            wctx.wait("!!document.querySelector('.statusbar')", 25, "对话窗口挂载")
            time.sleep(1.2)
            wctx.shot("window.chat")
            ctx.records.extend(wctx.records)
            try:
                wc.publish("gui.window.status", {"command": "close"})
            except Exception:
                pass
            time.sleep(0.6)
    except Exception as e:
        print("    [warn] 对话窗口未打开：%s" % e, flush=True)

    # Ask 中心（独立待办窗口）：注入一条待办 → 截图 + 主题落地断言
    try:
        aid = "css-ask-%s" % ctx.theme
        wid, ac = ac_mod.inject(ctx.c, {
            "ask_id": aid, "question": "CSS 三主题检查：请选择一项",
            "options": ["选项 A", "选项 B"], "custom": True})
        time.sleep(0.8)
        actx = Ctx(ac, ctx.out, ctx.theme)
        actx.shot("window.ask")
        # 跨窗口主题经 usr 配置异步加载：给有界等待，避免把「首屏尚未应用」误报为不跟随。
        cur = ""
        end = time.time() + 8
        while time.time() < end:
            cur = actx.ev("document.documentElement.getAttribute('data-theme')")
            if cur == ctx.theme:
                break
            time.sleep(0.4)
        if cur != ctx.theme:
            print("    [warn] ask 窗口主题未跟随：want=%s got=%s" % (ctx.theme, cur), flush=True)
        ctx.records.extend(actx.records)
        ac_mod.close_center(ac, ctx.c, 8)
    except Exception as e:
        print("    [warn] Ask 中心未打开：%s" % e, flush=True)
    finally:
        try:
            ctx.c.req("gui.ask.remove", {"ask_id": "css-ask-%s" % ctx.theme})
        except Exception:
            pass


def walk_plugins(ctx, plugins):
    if not plugins:
        print("    [warn] plugin-list 为空 → 跳过插件页", flush=True)
        return
    for p in plugins:
        pid = p.get("id") or ""
        pages = []
        cfg = p.get("config_view")
        if isinstance(cfg, dict) and (cfg.get("id") or cfg.get("entry")):
            pages.append(("config", cfg.get("title") or "配置"))
        for v in (p.get("views") or []):
            if isinstance(v, dict) and v.get("id"):
                pages.append((v.get("id"), v.get("title") or v.get("id")))
        for page_id, title in pages:
            name = "plugin.%s.%s" % (pid, page_id)
            try:
                ctx.close_all()
                ctx.mq("preview-tab-open",
                       {"kind": "plugin-page", "plugin": pid, "page": page_id, "title": title})
                ok = ctx.wait(
                    "(function(){const f=[...document.querySelectorAll('iframe.pph-frame')]"
                    ".find(x=>x.getBoundingClientRect().width>0);"
                    "if(!f)return false;const d=f.contentDocument;"
                    "return !!d&&d.readyState!=='loading';})()", 25.0, name)
                time.sleep(1.2)
                path, audit = ctx.shot(name)
                fr = (audit.get("iframes") or [])
                print("    [iframe] %-30s %s" % (name, json.dumps(fr[:2], ensure_ascii=False)[:220]),
                      flush=True)
            except TestError as e:
                print("    [warn] 插件页 %s 未就绪：%s" % (name, e), flush=True)
                try:
                    ctx.shot(name + ".placeholder")
                except Exception:
                    pass


def wait_plugin_list(client, timeout=90.0):
    """等插件清单收敛到「无 starting」（最坏超时返回最新快照）。"""
    end = time.time() + timeout
    env, ps = {}, []
    while time.time() < end:
        try:
            env = client.req("plugin-list", {}, timeout=20) or {}
        except Exception:
            env = {}
        ps = env.get("plugins") or []
        starting = [p for p in ps if p.get("status") in ("starting", "")]
        if ps and not starting:
            return env, ps
        time.sleep(0.6)
    return env, ps


# ── 主流程 ──────────────────────────────────────────────────

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--themes", default=",".join(THEMES))
    ap.add_argument("--out", default="")
    ap.add_argument("--exe", default="", help="指定被测 exe（缺省 dist/chonkpilot.exe）")
    ap.add_argument("--keep", action="store_true", help="保留临时工作区（调试用）")
    args = ap.parse_args()
    themes = [t.strip() for t in args.themes.split(",") if t.strip()]
    bad = [t for t in themes if t not in THEMES]
    if bad:
        raise SystemExit("未知主题：%r（可选 %s）" % (bad, ",".join(THEMES)))

    stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    out = args.out or os.path.join(HERE, "_css_themes_%s" % stamp)
    os.makedirs(out, exist_ok=True)
    for t in themes:
        os.makedirs(os.path.join(out, t), exist_ok=True)

    ws = _h.tmp_dir("ck-css-ws-")
    ctx_ws[0] = ws
    seed_workspace(ws)
    home = _h.tmp_home()
    g = _h.start_gui(port=_h.free_port(), work_dir=ws, home=home, ready_timeout=120,
                     exe=(args.exe or None))
    client = g.client
    print("[env] gui=%d work_dir=%s out=%s" % (g.port, ws, out), flush=True)
    _h.ensure_locale(client, "zh-CN")
    _h.wait_idle(client, max_wait=30.0)

    env, plugins = wait_plugin_list(client, 60.0)
    print("[plugins] %d 个：%s" % (
        len(plugins), ", ".join("%s(%s)" % (p.get("id"), p.get("status")) for p in plugins)),
        flush=True)
    with open(os.path.join(out, "plugin-list.json"), "w", encoding="utf-8") as f:
        json.dump(env, f, ensure_ascii=False, indent=2)

    all_records = {}
    try:
        for theme in themes:
            print("\n=== THEME %s ===" % theme, flush=True)
            ctx = Ctx(client, out, theme)
            apply_theme(ctx, theme)
            sid = walk_layout(ctx, theme)
            walk_pages(ctx)
            walk_dialogs(ctx)
            walk_wizard(ctx)
            walk_layout_editor(ctx)
            walk_drawer(ctx)
            # 插件进程首启有先后（实测 ~2s 全部 running）；本轮开始前已等过一次，
            # 这里再取最新快照，确保 views/config_view 字段已由 plugin.hello 上报完整。
            _, plugins_now = wait_plugin_list(client, 30.0)
            with open(os.path.join(out, theme, "plugin-list.json"), "w", encoding="utf-8") as f:
                json.dump(plugins_now, f, ensure_ascii=False, indent=2)
            walk_plugins(ctx, plugins_now)
            walk_windows(ctx, sid)
            all_records[theme] = ctx.records
            with open(os.path.join(out, theme, "audit.json"), "w", encoding="utf-8") as f:
                json.dump(ctx.records, f, ensure_ascii=False, indent=2)
    finally:
        try:
            apply_theme(Ctx(client, out, "light"), "light")
        except Exception:
            pass
        g.stop()

    # 汇总
    summary = {"themes": themes, "out": out, "gui": g.port, "work_dir": ws,
               "shots": {}, "issues": {}, "records": {}}
    for theme, recs in all_records.items():
        summary["shots"][theme] = len(recs)
        issues = []
        for r in recs:
            a = r["audit"] or {}
            c = a.get("counts") or {}
            if theme != "light" and c.get("lightBg"):
                issues.append({"screen": r["screen"], "kind": "light-bg",
                               "count": c["lightBg"], "items": (a.get("lightBg") or [])[:8]})
            if c.get("lowContrast"):
                issues.append({"screen": r["screen"], "kind": "low-contrast",
                               "count": c["lowContrast"], "items": (a.get("lowContrast") or [])[:8]})
            if c.get("clipped"):
                issues.append({"screen": r["screen"], "kind": "clipped",
                               "count": c["clipped"], "items": (a.get("clipped") or [])[:8]})
            for f in (a.get("iframes") or []):
                if f.get("docTheme") and f.get("docTheme") != theme:
                    issues.append({"screen": r["screen"], "kind": "iframe-theme",
                                   "items": [f]})
                sub = f.get("counts") or {}
                if theme != "light" and sub.get("lightBg"):
                    issues.append({"screen": r["screen"], "kind": "iframe-light-bg",
                                   "count": sub.get("lightBg"),
                                   "items": (f.get("inner") or {}).get("lightBg") or []})
                if sub.get("lowContrast"):
                    issues.append({"screen": r["screen"], "kind": "iframe-low-contrast",
                                   "count": sub.get("lowContrast"),
                                   "items": (f.get("inner") or {}).get("lowContrast") or []})
        summary["issues"][theme] = issues

    with open(os.path.join(out, "summary.json"), "w", encoding="utf-8") as f:
        json.dump(summary, f, ensure_ascii=False, indent=2)

    md = ["# CSS 三主题 e2e 报告", "",
          "- 时间：%s" % stamp,
          "- 工作区：`%s`" % ws,
          "- 截图：`%s/<theme>/`（每主题 %s 张）" % (out, summary["shots"].get(themes[0], 0)),
          ""]
    total = 0
    for theme in themes:
        issues = summary["issues"].get(theme) or []
        total += len(issues)
        # 按「同类问题签名」去重：单个问题在几十个界面重复出现（如状态栏弱化文字）时，
        # 报告只列一行 + 出现次数 + 首次界面，便于按【缺陷种类】而不是【界面】读。
        agg = Counter()
        first = {}
        for it in issues:
            items = it.get("items") or [{}]
            for x in items[:20]:
                sig = (it["kind"], x.get("sel") or x.get("src") or "",
                       x.get("ratio"), x.get("text") or "")
                agg[sig] += 1
                first.setdefault(sig, (it["screen"], it.get("count", 1)))
        md.append("## %s（异常记录 %d 条 / 去重 %d 类）" % (theme, len(issues), len(agg)))
        if not agg:
            md.append("- 无 audit 异常")
        for (kind, sel, ratio, text), n in agg.most_common(40):
            screen, cnt = first[(kind, sel, ratio, text)]
            extra = ""
            if ratio:
                extra += " ratio=%s" % ratio
            if text:
                extra += " “%s”" % text
            md.append("- `%s` **%s** ×%d（首见 `%s`，单屏最多 %s 处%s）"
                      % (sel or "-", kind, n, screen, cnt, extra))
        md.append("")
    md.append("> 自动审计是**索引**，最终以人工看图为验收；暗色主题优先看「浅底块」（未跟随主题的硬编码浅色）。")
    with open(os.path.join(out, "report.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(md))

    print("\n[out] %s" % out, flush=True)
    print("[summary] issues=%d  shots=%s" % (total, summary["shots"]), flush=True)
    if not args.keep:
        shutil.rmtree(ws, ignore_errors=True)
    return 0 if total == 0 else 1


if __name__ == "__main__":
    sys.exit(main())