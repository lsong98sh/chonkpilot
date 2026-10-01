# -*- coding: utf-8 -*-
"""ExplorerPane 双栈 + KnowledgeTree + PrimitivePanel 端到端验收。

用例（2026-10-01，P3：原「知识库/工具」两页签合并为「扩展」页 = 5 子 tab 知识/技能/工具/命令/智能体
+ 右侧【级别】popup 四级；四级均可写）：
 C1 四段切换（项目/会话/记忆/扩展）v-show 生效 + 扩展页 5 子 tab + 级别选择器
 C2 扩展页各子 tab 单类型过滤（知识=resources/技能=skills）+ 根行命名「<子tab> -级别」
 C3 扩展页「工具」子 tab：tools→core 展开 → *.tool.md 行出现
 C4 单击 *.tool.md → preview PrimitivePanel（四页签 Tabs、顶部标题非空、保存/恢复按钮）
C5 切「描述」页签编辑 → dirty → 恢复还原 & 磁盘字节不变；meta 键值表单；「参数」JSON Schema 树
 C6 右键 tools 类型目录 → 菜单含 新建目录 与 新建工具
 C7 新建工具(名称 smoke_it) → tools 下出现 smoke_it.tool.md & 磁盘文件存在([meta])
 C8 编辑并保存 smoke_it（desc 写入）→ 磁盘含新描述；删除文件 → 行消失 & 磁盘删除
  C10 KB 目录全 UI 写链路（I-73 修复）：新建子目录 → 树行出现 + 自动进入内联改名 →
      改名 smoke_dir1 → F2 改目录名 smoke_dir2（目录语义，不追加 .md）→ 右键「重命名」
      改回 smoke_dir1 → 删除目录
  C11 KB F2 重命名文件（保留 .tool.md 后缀，zz_find ↔ file_find 往返）
  C12 扩展页 技能/命令/知识 三子 tab 分别新建（.skill/.prompt/.resource 契约模板）+ 删除清理
  C9 项目级知识库（`<workDir>/.chonkpilot/capability`）tools/core/demo.tool.md → PrimitivePanel；
     右键 typed 目录菜单含新建目录/新建工具
  C13 项目级知识库 新建工具 → 编辑保存 → 删除（项目级完整写链路）
  C14 项目私有级（prjusr）端到端：预置落盘 `<data-dir>/<project-id>/capability/tools/*.tool.md`
      → 扩展页-工具-【项目私有】树行**可见**；切【系统级】展开 app tools（core 为对照）→
      该资产**不可见**（按级隔离 / 不越权）。独立 `--data-dir` 临时目录 → prjusr 零残留。

迁移口径（2026-09-15）：
  - 扩展页根行 = "<子tab文案> -<级别>"（如系统级「知识 -系统」/「工具 -系统」）；旧根行名 "capability" 已移除。
  - 项目侧原语路径：`@mcp/` 旧名已按 spec 60-名词约定（P1-3）迁移为
    `<workDir>/.chonkpilot/capability`（= 项目级知识库根）。项目**文件树**不暴露 `.chonkpilot`
    （实测 filetree 仅列非点目录），且 `data-knowledge-read/list` 只认四级 capability 根内的路径
    （persist `kbRootOf`）→ 项目级原语改用**知识库树的「项目」级**驱动（C9/C13），
    夹具随之为 `<ws_kb>/.chonkpilot/capability/tools/core/demo.tool.md`。

分层口径（2026-10-01，P1 目录扁平化）：capability 根下 = **6 个扁平子目录**
  `prompts/`（命令）`tools/`（工具）`resources/`（知识）`skills/`（技能）`agents/`（智能体）
  `scenarios/`（场景）+ 系统级 `executors/`（内置执行器 exe）；旧 `knowledge/{skills,prompts,resources}`
  归并层**已删除**。故系统级夹具路径 = `<CAP_DIR>/{skills,prompts,resources}/...`；工具仍在 `<CAP_DIR>/tools/`。
"""
import json
import os
import shutil
import subprocess
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, run_case
import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）

ROOT = r"e:\BizWorks\chonkpilot"
GUI_EXE = os.path.join(ROOT, r"dist\desktop\chonkpilot.exe")
CAP_DIR = os.path.join(ROOT, r"dist\desktop\capability")
BASE_WS = os.path.join(ROOT, r"src\test\chonkpilot-gui\systest\ws")
WORK_DIR = os.path.join(ROOT, r"src\test\chonkpilot-gui\systest\ws_kb")
# 独立 --data-dir（临时目录）：desktop 缺省不带 --data-dir 时 prjusr 落 **机器 ~/.chonkpilot/data**，
# 会污染真实用户数据；显式指向临时目录后 prj/prjusr 同根 → 项目私有级（prjusr）capability 根落在该
# 临时目录下（<DD>/<project-id>/capability），随 tmp_dir 回收 → 零残留 + 天然隔离（C14）。
DATA_DIR = _h.tmp_dir("ck-kb-dd-")
PORT = _h.free_port()  # 动态端口：不与他套件/机器上的固定端口实例争用
TOOLS_DIR = os.path.join(CAP_DIR, "tools")
SMOKE_FILE = os.path.join(TOOLS_DIR, "smoke_it.tool.md")
DIR_SMOKE = os.path.join(TOOLS_DIR, "smoke_dir1")
DIR_SMOKE2 = os.path.join(TOOLS_DIR, "smoke_dir2")
# 「新建目录」默认名 = i18n fileTree.new_folder（zh-CN "新建目录"）；中断残留需在用例前后清理
DIR_DEFAULT_NAME = "新建目录"
DIR_DEFAULT = os.path.join(TOOLS_DIR, DIR_DEFAULT_NAME)
CORE_FIND = os.path.join(CAP_DIR, "tools", "core", "file_find.tool.md")
# 非 tools 原语现位于 capability 根下**扁平**子目录（skills/prompts/resources；旧 knowledge/ 归并层已删）
SKILL_FILE = os.path.join(CAP_DIR, "skills", "sk_s1.skill.md")
PROMPT_FILE = os.path.join(CAP_DIR, "prompts", "pr_s1.prompt.md")
RES_FILE = os.path.join(CAP_DIR, "resources", "re_s1.resource.md")
# 项目级知识库根（spec 60-名词约定 §115 / persist CapProjectRoot）
PRJ_CAP = os.path.join(WORK_DIR, ".chonkpilot", "capability")
PJ_DIR = os.path.join(PRJ_CAP, "tools")
PJ_FILE = os.path.join(PJ_DIR, "pj_smoke.tool.md")

CLEANUPS = [SMOKE_FILE, SKILL_FILE, PROMPT_FILE, RES_FILE, PJ_FILE]

# 左侧资源面板模式 → 激活段文案（zh-CN / en-US 两种）。
# 2026-10-01（P3）：分段改 4 段「项目 / 会话 / 记忆 / 扩展」（原「知识库」「工具」合并进「扩展」页；
# 「项目记忆」文案改「记忆」）。legacy 名 `knowledge`/`tools` 仍可作 switch_mode 入参 → 映射到扩展页子 tab。
MODE_LABELS = {
    "project": ("Project", "项目"),
    "sessions": ("Sessions", "会话"),
    "memory": ("Memory", "记忆"),
    "extensions": ("Extensions", "扩展"),
    # legacy（扩展页子 tab 入口）
    "knowledge": ("Extensions", "扩展"),
    "tools": ("Extensions", "扩展"),
}

# 扩展页子 tab（mode=extensions 的 ext）→ 子 tab 文案（zh-CN / en-US）
EXT_LABELS = {
    "resource": ("Resources", "知识"),
    "skill": ("Skill", "技能"),
    "tool": ("Tools", "工具"),
    "prompt": ("Command", "命令"),
    "agent": ("Agent", "智能体"),
}

# legacy 名 → 扩展页子 tab
LEGACY_EXT = {"knowledge": "resource", "tools": "tool"}


def read_until(path, needles, timeout=8):
    """轮询读取文件直到内容含全部 needles（消除"文件刚创建即读"的竞态：持久层先建后写）。

    返回最终内容；超时返回最后一次读到的内容（由调用方断言报错）。
    """
    deadline = time.time() + timeout
    data = ""
    while time.time() < deadline:
        try:
            with open(path, encoding="utf-8") as f:
                data = f.read()
        except OSError:
            data = ""
        if all(n in data for n in needles):
            return data
        time.sleep(0.3)
    return data


def cleanup_fixtures():
    """清除可能的中断残留（夹具幂等：旧版失败运行会留下 新建目录 / smoke_dir1 / smoke_dir2 / 目标文件）。"""
    for p in list(CLEANUPS) + [os.path.join(TOOLS_DIR, "zz_find.tool.md"),
                               os.path.join(TOOLS_DIR, "smoke_dir1.md"),
                               os.path.join(TOOLS_DIR, "smoke_dir2.md")]:
        try:
            if os.path.exists(p):
                os.remove(p)
        except OSError:
            pass
    for d in (DIR_SMOKE, DIR_SMOKE2, DIR_DEFAULT, os.path.join(PRJ_CAP, DIR_DEFAULT_NAME),
              os.path.join(TOOLS_DIR, "smoke_dir1.md"), os.path.join(TOOLS_DIR, "smoke_dir2.md")):
        if os.path.isdir(d):
            try:
                shutil.rmtree(d)
            except OSError:
                pass
        elif os.path.isfile(d):
            try:
                os.remove(d)
            except OSError:
                pass


def unwrap(res):
    for _ in range(3):
        if not isinstance(res, str) or not res:
            return res
        try:
            parsed = json.loads(res)
        except Exception:
            return res
        res = parsed
    return res


def is_new_of_type(x):
    """匹配「新建工具/技能/提示词/资源」或 'New Tool/...' 类型菜单项。"""
    zh = "新建" in x and any(k in x for k in ("工具", "技能", "提示词", "资源"))
    en = x.startswith("New") and any(k in x for k in ("Tool", "Skill", "Prompt", "Resource"))
    return zh or en


class KB:
    """知识库/项目树 + preview 操作助手。"""

    def __init__(self, c):
        self.c = c

    def js(self, expr):
        return unwrap(self.c.eval(expr))

    # ── 树行 ──
    # 2026-09-29：知识库/工具两页签各挂一个 KnowledgeTree（v-show 同显）→ 一律只取**可见树**
    # （offsetParent!==null；隐藏页签的树行不计入），即"当前模式下的树"。
    def kb_rows(self):
        return self.js("Array.from(document.querySelectorAll('.knowledge-tree .tree-row')).filter(n=>n.offsetParent!==null).map(n => ({ t: n.textContent.trim().slice(0, 80), d: n.classList.contains('is-dir') }))") or []

    def ft_rows(self):
        return self.js("Array.from(document.querySelectorAll('.filetree-panel .tree-row')).map(n => ({ t: n.textContent.trim().slice(0, 80), d: n.classList.contains('is-dir') }))") or []

    def row_label(self, r):
        return r["t"].split("\u22ef")[0].strip()

    def click_kb_dir(self, name):
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.knowledge-tree .tree-row.is-dir')).filter(n=>n.offsetParent!==null);const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(name))

    def click_kb_root(self):
        """点击可见树根行（根行名含空格 "知识库 -级别"/"工具 -级别"，不能按首段匹配）。"""
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.knowledge-tree .tree-row.is-dir')).filter(n=>n.offsetParent!==null);const el=els[0];if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()")

    def click_kb_file(self, name):
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.knowledge-tree .tree-row:not(.is-dir)')).filter(n=>n.offsetParent!==null);const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(name))

    def click_ft_dir(self, name):
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.filetree-panel .tree-row.is-dir'));const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(name))

    def click_ft_file(self, name):
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.filetree-panel .tree-row:not(.is-dir)'));const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(name))

    def poll(self, fn, timeout=6, interval=0.35):
        end = time.time() + timeout
        while time.time() < end:
            if fn():
                return True
            time.sleep(interval)
        return False

    def wait_kb_row(self, name, is_dir=None):
        return self.poll(lambda: any(self.row_label(r) == name and (is_dir is None or r["d"] == is_dir) for r in self.kb_rows()))

    def wait_ft_row(self, name, is_dir=None):
        return self.poll(lambda: any(self.row_label(r) == name and (is_dir is None or r["d"] == is_dir) for r in self.ft_rows()))

    # ── 右键菜单 ──
    def _rclick_js(self, scope, name, is_dir):
        # 仅命中**可见**行（知识库/工具两树同挂 .knowledge-tree；隐藏页签不参与定位）
        cond = "true" if is_dir is None else f"n.classList.contains('is-dir')===({'true' if is_dir else 'false'})"
        return "(function(){const els=Array.from(document.querySelectorAll('%s .tree-row'));const el=els.find(n=>{if(n.offsetParent===null)return false;const t=n.textContent.trim().split(/[\\s\u22ef]+/)[0];return t===%s&&(%s)});if(!el)return false;const r=el.getBoundingClientRect();el.dispatchEvent(new MouseEvent('contextmenu',{bubbles:true,clientX:r.left+10,clientY:r.top+8}));return true})()" % (scope, json.dumps(name), cond)

    def rclick_kb_row(self, name, is_dir=None):
        return self.js(self._rclick_js(".knowledge-tree", name, is_dir))

    def rclick_ft_row(self, name, is_dir=None):
        return self.js(self._rclick_js(".filetree-panel", name, is_dir))

    def kb_menu_texts(self):
        return self.js("Array.from(document.querySelectorAll('.kb-ctx .kb-ctx-item')).map(n=>n.textContent.trim())") or []

    def ft_menu_texts(self):
        return self.js("Array.from(document.querySelectorAll('.context-menu .context-menu-item')).map(n=>n.textContent.trim())") or []

    def kb_click_menu(self, text):
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.kb-ctx .kb-ctx-item'));const el=els.find(n=>n.textContent.trim()===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(text))

    def ft_click_menu(self, text):
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.context-menu .context-menu-item'));const el=els.find(n=>n.textContent.trim()===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(text))

    def close_menus(self):
        # 点任意空白处：两树均注册了 document mousedown 关闭；此处发送 body mousedown
        self.js("document.body.dispatchEvent(new MouseEvent('mousedown',{bubbles:true}))")

    # ── 弹窗输入 / 确认 ──
    def set_dialog_input(self, val):
        return self.js("(function(){const el=document.querySelector('.dialog-shell .b-input');if(!el)return false;const s=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;s.call(el,%s);el.dispatchEvent(new Event('input',{bubbles:true}));return true})()" % json.dumps(val))

    def dialog_enter(self):
        return self.js("(function(){const el=document.querySelector('.dialog-shell .b-input');if(!el)return false;el.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',code:'Enter',bubbles:true}));return true})()")

    def click_primary(self):
        return self.js("(function(){const el=document.querySelector('.dialog-shell button.b-btn--primary');if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()")

    def click_any_button_text(self, cands):
        jl = json.dumps(cands, ensure_ascii=False)
        return self.js("(function(){const btns=Array.from(document.querySelectorAll('.prim-panel .prim-footer button.b-btn'));const el=btns.find(n=>%s.some(x=>n.textContent.trim()===x||n.textContent.trim().indexOf(x)>=0));if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % jl)

    # ── preview 面板 ──
    def ensure_explorer_visible(self, timeout=12):
        """filetree 区默认隐藏（MainLayout v-if）→ 点 toolbar 文件树切换钮使其挂载。"""
        end = time.time() + timeout
        while time.time() < end:
            if self.js("document.querySelectorAll('.explorer-seg').length") > 0:
                return True
            self.js("(function(){const btns=Array.from(document.querySelectorAll('button.b-btn'));const b=btns.find(n=>{const tt=(n.getAttribute('title')||'')+(n.textContent.trim());return tt.indexOf('File Tree')>=0||tt.indexOf('文件树')>=0});if(!b)return false;b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()")
            time.sleep(1.0)
        return False

    def switch_mode(self, want, ext=None):
        """切到 项目/会话/记忆/扩展（filetreeModeSelect，与 seg 点击同一事件）。

        2026-10-01（P3）：legacy 名 `knowledge`/`tools` 映射到**扩展页子 tab**
        （`{mode:'extensions', ext:'resource'|'tool'}`）；显式 `ext` 亦走扩展页。
        """
        if want in LEGACY_EXT:
            payload = {"mode": "extensions", "ext": LEGACY_EXT[want]}
        elif want == "extensions":
            payload = {"mode": "extensions"}
            if ext:
                payload["ext"] = ext
        else:
            payload = {"mode": want}
        self.js("window.mq.emit('filetree-mode-select', %s)" % json.dumps(payload))
        end = time.time() + 8
        while time.time() < end:
            seg = self.js("Array.from(document.querySelectorAll('.explorer-seg-btn')).map(n=>({t:n.textContent.trim(),a:n.classList.contains('active')}))") or []
            act = [s["t"] for s in seg if s["a"]]
            if act and any(x in MODE_LABELS.get(want, ()) for x in act):
                return True
            time.sleep(0.4)
        return False

    def ext_active(self):
        """扩展页当前激活子 tab 文案（无扩展页激活 → []）。"""
        return self.js("Array.from(document.querySelectorAll('.ext-subtab.active')).map(n=>n.textContent.trim())") or []

    def mode_active(self):
        seg = self.js("Array.from(document.querySelectorAll('.explorer-seg-btn')).map(n=>({t:n.textContent.trim(),a:n.classList.contains('active')}))") or []
        return [s["t"] for s in seg if s["a"]]

    def switch_kb_level(self, kind, label):
        """切知识库级别（系统/用户/项目；kb-level-select），等待根行标题匹配。
        完成条件：根行文本含 label（如 "项目"）。"""
        self.js("window.mq.emit('kb-level-select', %s)" % json.dumps({"kind": kind}))
        end = time.time() + 10
        while time.time() < end:
            rows = self.kb_rows()
            if rows and label in self.row_label(rows[0]):
                return True
            time.sleep(0.4)
        return False

    def ensure_kb_expanded(self, dirs, timeout=10):
        """依次确保**可见**树展开到 dirs 指定层级（先展开根，再逐级点击未展开的目录）。

        2026-09-29：知识库/工具各一树（v-show 同显）→ 只操作可见树（offsetParent!==null）；
        工具用例先 switch_mode("tools") 再调本函数即等价于展开工具树。
        """
        end = time.time() + timeout
        while time.time() < end:
            if not self.js("(function(){const r=Array.from(document.querySelectorAll('.knowledge-tree .tree-row.is-dir')).filter(n=>n.offsetParent!==null)[0];if(!r)return false;if(r.querySelector('.arrow-icon.expanded'))return true;r.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()"):
                time.sleep(0.4)
                continue
            ok = True
            for d in dirs:
                if not any(self.row_label(r) == d and r["d"] for r in self.kb_rows()):
                    ok = False
                    break
                if not self.js("(function(){const els=[...document.querySelectorAll('.knowledge-tree .tree-row.is-dir')].filter(n=>n.offsetParent!==null);const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef]+/)[0]===%s);if(!el)return false;if(el.querySelector('.arrow-icon.expanded'))return true;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(d)):
                    ok = False
                    break
            if ok:
                return True
            time.sleep(0.5)
        return False

    def enter_tools(self, dirs=("tools",), timeout=10):
        """切到「工具」页签（位于「会话」右侧）并展开指定目录链（先展开根）。

        工具树复用 KnowledgeTree（kinds=['tool']），根下即 `tools/`，故 dirs 默认 ("tools",)。
        """
        if not self.switch_mode("tools"):
            return False
        return self.ensure_kb_expanded(list(dirs), timeout)

    def _vis_panel(self):
        return self.js("(function(){const ps=Array.from(document.querySelectorAll('.prim-panel'));return ps.find(n=>n.offsetParent!==null)||(ps.length?ps[ps.length-1]:null)})()")

    def prim_panel_exists(self):
        # 只认可见面板（隐藏页签由外层容器 v-show，子面板自身 display 仍为 block → 用 offsetParent）
        return self._vis_panel() is not None and self.js("!!document.querySelector('.prim-panel')")

    def prim_scope(self, inner):
        """在可见的 .prim-panel 内执行 inner（内嵌 P 变量）。"""
        return self.js("(function(){const P=Array.from(document.querySelectorAll('.prim-panel')).find(n=>n.offsetParent!==null);if(!P)return null;" + inner + "})()")

    def prim_title_value(self):
        return self.prim_scope("const el=P.querySelector('.prim-title');return el?el.value:''") or ""

    def prim_active_path(self):
        return self.prim_scope("const el=P.querySelector('.prim-path');return el?el.textContent.trim():''") or ""

    # P3-C2（2026-09-24）：子页签由「源码/编辑」改为 Tabs 四页签 meta·描述·参数·正文
    # （`.b-tabs-header .b-tabs-item`；源码页签已移除 → 源码断言改为「顶部标题 + 路径」断言）。
    def prim_tab_names(self):
        return self.prim_scope("return Array.from(P.querySelectorAll('.b-tabs-header .b-tabs-item')).map(n=>n.textContent.trim())") or []

    def prim_click_tab(self, cands):
        jl = json.dumps(cands, ensure_ascii=False)
        return self.prim_scope("const els=Array.from(P.querySelectorAll('.b-tabs-header .b-tabs-item'));const el=els.find(n=>%s.some(x=>n.textContent.trim()===x));if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true" % jl)

    def prim_footer_text(self):
        return self.prim_scope("const el=P.querySelector('.prim-footer');return el?el.textContent.trim():''") or ""

    def prim_edit_info(self):
        return self.prim_scope("const b=P.querySelector('.prim-tab-body');if(!b)return {ok:false};return {ok:true,textareas:b.querySelectorAll('textarea').length,inputs:b.querySelectorAll('input.b-input').length,dirty:!!P.querySelector('.prim-dirty')}")

    def prim_set_ta(self, idx, val):
        return self.prim_scope("const b=P.querySelector('.prim-tab-body');if(!b)return false;const ta=b.querySelectorAll('textarea')[%d];if(!ta)return false;const s=Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype,'value').set;s.call(ta,%s);ta.dispatchEvent(new Event('input',{bubbles:true}));return true" % (idx, json.dumps(val)))

    def prim_get_ta(self, idx):
        return self.prim_scope("const b=P.querySelector('.prim-tab-body');if(!b)return null;const ta=b.querySelectorAll('textarea')[%d];return ta?ta.value:null" % idx)

    def prim_schema_rows(self):
        """参数页签的 JSON Schema 编辑器树行数（共通控件 JsonSchemaEditor）。"""
        return self.prim_scope("return P.querySelectorAll('.jse-row').length") or 0

    def prim_dirty(self):
        return self.prim_scope("return !!P.querySelector('.prim-dirty')") or False

    def prim_click_save(self):
        return self.click_any_button_text(["保存", "Save"])

    def prim_click_restore(self):
        return self.click_any_button_text(["恢复", "Restore"])

    def activate_bottom_tab(self, name):
        """在 preview 底部页签栏（CodeView）激活指定文件页签。"""
        jl = json.dumps(name)
        return self.js("(function(){const els=Array.from(document.querySelectorAll('.tb-bar.tb-bottom .tb-tab'));const el=els.find(n=>n.textContent.trim().split(/[\\s\u22ef×]+/)[0]===%s);if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % jl)

    # ── 内联改名 / F2 ──
    def inline_input(self):
        return self.js("(function(){const el=document.querySelector('.knowledge-tree .inline-edit-input')||document.querySelector('.filetree-panel .inline-edit-input');return el?el.value:null})()")

    def set_inline_input(self, val):
        return self.js("(function(){const el=document.querySelector('.knowledge-tree .inline-edit-input')||document.querySelector('.filetree-panel .inline-edit-input');if(!el)return false;const s=Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;s.call(el,%s);el.dispatchEvent(new Event('input',{bubbles:true}));return true})()" % json.dumps(val))

    def inline_enter(self):
        # TreeNode 内联输入用 @keyup.enter 确认
        return self.js("(function(){const el=document.querySelector('.knowledge-tree .inline-edit-input')||document.querySelector('.filetree-panel .inline-edit-input');if(!el)return false;el.dispatchEvent(new KeyboardEvent('keyup',{key:'Enter',code:'Enter',bubbles:true}));return true})()")

    def press_f2(self):
        return self.js("document.dispatchEvent(new KeyboardEvent('keydown',{key:'F2',code:'F2',bubbles:true}))")


def prepare_ws():
    if os.path.isdir(WORK_DIR):
        shutil.rmtree(WORK_DIR)
    # 项目级原语夹具：spec 60-名词约定（P1-3）项目级能力根 = <workDir>/.chonkpilot/capability
    # （旧名 @mcp 已迁移；data-knowledge-* 只认四级 capability 根内路径）
    os.makedirs(os.path.join(PJ_DIR, "core"))
    demo = os.path.join(PJ_DIR, "core", "demo.tool.md")
    with open(demo, "w", encoding="utf-8") as f:
        f.write("# demo\n\n[meta]\nname=demo\nkind=smoke\n\n[description]\nDemo tool for project tree smoke.\n\n[parameters]\nproperties:\n    q:\n        type: string\nrequired:\n    - q\n\n[content]\nRun a demo.\n")


# 应用级原语夹具：C4/C5 打开并编辑 `file_diff.tool.md` —— 产品的原语保存/恢复路径会把契约
# 原文按**规范化形态**回写（实测多出 1 字节尾换行）→ 该文件属于 **dist 产物**（dist-desktop/capability），
# 套件跑完必须**按字节还原**，否则 capability 与契约源静默分叉（`sync-contracts.ps1 -Check` 会报漂移）。
APP_CONTRACT = os.path.join(CAP_DIR, "tools", "core", "file_diff.tool.md")


def snapshot_app_contract():
    try:
        with open(APP_CONTRACT, "rb") as f:
            return f.read()
    except OSError:
        return None


def restore_app_contract(buf):
    """按字节还原 dist 产物内的契约文件（不改变任何断言；仅清除测试副作用）。"""
    if buf is None or not os.path.exists(APP_CONTRACT):
        return
    with open(APP_CONTRACT, "rb") as f:
        cur = f.read()
    if cur != buf:
        with open(APP_CONTRACT, "wb") as f:
            f.write(buf)
        print("[cleanup] 还原应用级契约字节: %s（%d -> %d 字节）"
              % (os.path.relpath(APP_CONTRACT, ROOT), len(cur), len(buf)))


def main():
    prepare_ws()
    cleanup_fixtures()
    app_contract_before = snapshot_app_contract()
    # 按需自起 + 登记回收（finally / 进程退出 / 信号三条路径都会清）
    h = _h.start_gui(port=PORT, work_dir=WORK_DIR, data_dir=DATA_DIR, ready_timeout=120)
    passed = failed = 0
    c = None
    cfg_snap = None
    try:
        c = ChonkClient(base=f"http://127.0.0.1:{PORT}")
        c.wait_ready(120)
        # 套件级配置快照-还原（51 §6-8）：usr 层兜底（prj 落自持 ws_kb）。
        # 显式快照 + finally 还原（不用 suite_config_guard 的 atexit 兜底）：本套件在 finally 里
        # 先 `h.stop()` 回收自起 GUI，若还原留到 atexit（晚于 stop）→ client 已随进程消失 →
        # 打印「套件级配置还原失败 …10061」。故还原**必须先于 h.stop()**（见下方 finally）。
        cfg_snap = _h.snapshot_config(c)
        _h.ensure_locale(c)  # 语言确定性：知识库根行/菜单按 zh-CN 断言（DB ui.locale 可被写成 en-US）
        c.console(clear=True)
        kb = KB(c)

        # ── C1 四段切换（项目/会话/记忆/扩展，2026-10-01 P3） ──
        def c1():
            assert kb.ensure_explorer_visible(), "filetree 区未能挂载（toolbar 文件树按钮点击无效）"
            assert kb.switch_mode("extensions"), f"切扩展失败 active={kb.mode_active()}"
            seg = kb.js("Array.from(document.querySelectorAll('.explorer-seg-btn')).map(n=>({t:n.textContent.trim(),a:n.classList.contains('active')}))") or []
            if not seg:
                raise AssertionError("explorer seg 不存在")
            active = [s for s in seg if s["a"]]
            assert len(active) == 1, f"应恰有一个 active：{seg}"
            labels = {s["t"] for s in seg}
            assert labels & {"Project", "项目"}, f"缺「项目」分段：{seg}"
            assert labels & {"Sessions", "会话"}, f"缺「会话」分段（P3-C1 迁入左侧导航）：{seg}"
            assert labels & {"Memory", "记忆"}, f"缺「记忆」分段（原「项目记忆」，P3 改名）：{seg}"
            assert labels & {"Extensions", "扩展"}, f"缺「扩展」分段（P3：知识库+工具合并）：{seg}"
            # 段顺序：项目 → 会话 → 记忆 → 扩展（原「知识库」「工具」一级分段已删）
            seg_txts = [s["t"] for s in seg]
            idx = lambda names: next((i for i, x in enumerate(seg_txts) if x in names), -1)
            i_proj, i_sess = idx(("Project", "项目")), idx(("Sessions", "会话"))
            i_mem, i_ext = idx(("Memory", "记忆")), idx(("Extensions", "扩展"))
            assert i_proj >= 0 and i_sess > i_proj and i_mem > i_sess and i_ext > i_mem, \
                f"分段顺序应为 项目→会话→记忆→扩展：{seg_txts}"
            assert not (labels & {"Knowledge", "知识库", "Tools", "工具"}), f"不应再有「知识库/工具」一级分段：{seg}"
            # v-show 四体（项目/会话/记忆/扩展）
            assert kb.js("document.querySelectorAll('.explorer-body').length") == 4, "应有四个 .explorer-body（v-show 四体）"
            vis = kb.js("Array.from(document.querySelectorAll('.explorer-body')).filter(n=>getComputedStyle(n).display!=='none').length") or 0
            assert vis == 1, f"v-show 应恰有一个可见：{vis}"
            # 扩展页 = 5 子 tab（知识/技能/工具/命令/智能体）+ 右侧【级别】
            subs = kb.js("Array.from(document.querySelectorAll('.ext-subtab')).map(n=>n.textContent.trim())") or []
            assert subs == ["知识", "技能", "工具", "命令", "智能体"], f"扩展页应 5 子 tab（知识/技能/工具/命令/智能体）：{subs}"
            assert kb.js("!!document.querySelector('.ext-level-btn')"), "扩展页缺【级别】选择器"
            # 仅 1 棵 KnowledgeTree（扩展页复用；按子 tab 单类型）
            assert kb.js("document.querySelectorAll('.knowledge-tree').length") == 1, "应恰有 1 棵 KnowledgeTree（扩展页复用）"
            assert kb.js("!!document.querySelector('.filetree-panel')"), "项目树不存在"
            # seg 点击「项目」→ 切换（filetreeModeSelect 链路）
            kb.js("(function(){const els=Array.from(document.querySelectorAll('.explorer-seg-btn'));const el=els.find(n=>n.textContent.trim()==='Project'||n.textContent.trim()==='项目');if(!el)return false;el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()")
            ok = kb.poll(lambda: kb.mode_active() and (kb.mode_active()[0] == "Project" or kb.mode_active()[0] == "项目"))
            assert ok, f"seg 切项目失败 active={kb.mode_active()}"
            # 再经 filetreeModeToggle（休眠态，此处直发事件验其仍生效）→ 扩展
            kb.js("window.mq.emit('filetree-mode-toggle')")
            ok = kb.poll(lambda: kb.mode_active() and (kb.mode_active()[0] == "Extensions" or kb.mode_active()[0] == "扩展"))
            assert ok, f"toggle 切扩展失败 active={kb.mode_active()}"

        # ── C2 扩展页各子 tab 单类型过滤 + 根行命名 ──
        def c2():
            # 扩展页子 tab 单类型：知识=resources / 技能=skills / 工具=tools / 命令=prompts / 智能体=agents。
            # 根行名 = "<子tab文案> -<级别>"（如「知识 -系统」）。
            for ext, dirname in (("resource", "resources"), ("skill", "skills")):
                assert kb.switch_mode("extensions", ext=ext), f"切扩展页子 tab {ext} 失败"
                rows = kb.kb_rows()
                assert rows, f"扩展页（{ext}）树无行（根未加载）"
                root_label = kb.row_label(rows[0])
                assert root_label.startswith(EXT_LABELS[ext][0]) or root_label.startswith(EXT_LABELS[ext][1]), \
                    f"根行名异常（{ext}）：{root_label!r}"
                assert kb.click_kb_root(), f"点击根失败：{root_label}"
                # 展开到类型目录（切换子 tab 会重挂载树；ensure 幂等展开根 + 类型目录）
                assert kb.ensure_kb_expanded([dirname]), f"扩展页（{ext}）展开 {dirname} 失败"
                # 仅当前子 tab 的类型目录可见（其余类型目录不显示）
                dirlabels = {kb.row_label(r) for r in kb.kb_rows() if r["d"]}
                assert dirname in dirlabels, f"{ext} 子 tab 应显示 {dirname} 目录：{dirlabels}"
                for other in ("tools", "prompts", "resources", "skills", "agents"):
                    if other != dirname:
                        assert other not in dirlabels, f"{ext} 子 tab 不应显示 {other}：{dirlabels}"

        # ── C3 工具页签：tools→core → *.tool.md（2026-09-29 由知识库迁到独立「工具」页签）──
        def c3():
            assert kb.enter_tools(), "切「工具」页签 / 展开 tools 失败"
            ok = kb.wait_kb_row("core", True)
            assert ok, "tools 未展开出 core"
            assert kb.click_kb_dir("core"), "点击 core 失败"
            ok = kb.poll(lambda: any(r and kb.row_label(r).endswith(".tool.md") for r in kb.kb_rows()))
            assert ok, "core 下未出现 *.tool.md 行"
            files = [kb.row_label(r) for r in kb.kb_rows() if not r["d"]]
            assert files, "无原语文件行"

        # ── C4 打开原语 → PrimitivePanel（顶部标题 / 四页签 Tabs / 底部保存·恢复） ──
        def c4():
            assert kb.enter_tools(("tools", "core")), "切「工具」页签 / 展开 tools/core 失败"
            assert kb.click_kb_file("file_diff.tool.md"), "点击 file_diff.tool.md 失败"
            ok = kb.poll(lambda: kb.prim_panel_exists())
            assert ok, "PrimitivePanel 未打开"
            names = kb.prim_tab_names()
            assert names == ["meta", "描述", "参数", "正文"], f"应有 meta/描述/参数/正文 四页签：{names}"
            assert kb.prim_active_path().endswith("file_diff.tool.md"), f"标题路径异常：{kb.prim_active_path()!r}"
            assert kb.prim_title_value(), "顶部标题为空"
            ftxt = kb.prim_footer_text()
            assert ("保存" in ftxt or "Save" in ftxt) and ("恢复" in ftxt or "Restore" in ftxt), f"footer 缺保存/恢复：{ftxt!r}"

        # ── C5 页签：描述（编辑 + dirty + 恢复，不改磁盘）/ meta（键值表单）/ 参数（JSON Schema 树） ──
        def c5():
            assert kb.prim_click_tab(["描述", "Description"]), "切换描述页签失败"
            ok = kb.poll(lambda: (kb.prim_edit_info() or {}).get("ok"))
            assert ok, "描述页签未就绪"
            before = open(os.path.join(CAP_DIR, "tools", "core", "file_diff.tool.md"), "rb").read()
            orig = kb.prim_get_ta(0)
            assert orig, "description 为空"
            kb.prim_set_ta(0, orig + " [SMOKE_EDIT]")
            ok = kb.poll(lambda: kb.prim_dirty())
            assert ok, "编辑后未显示未保存(dirty)"
            kb.prim_click_restore()
            ok = kb.poll(lambda: not kb.prim_dirty())
            assert ok, "恢复后 dirty 未清除"
            assert kb.prim_get_ta(0) == orig, "恢复后 description 未还原"
            after = open(os.path.join(CAP_DIR, "tools", "core", "file_diff.tool.md"), "rb").read()
            assert before == after, "恢复后磁盘文件被改动"
            # meta 页签：键=值 输入行
            assert kb.prim_click_tab(["meta", "Meta"]), "切换 meta 页签失败"
            ok = kb.poll(lambda: (kb.prim_edit_info() or {}).get("inputs", 0) >= 2)
            assert ok, f"meta 页签应有键值输入：{kb.prim_edit_info()}"
            # 参数页签：JSON Schema 编辑器（树形层级）
            assert kb.prim_click_tab(["参数", "Parameters"]), "切换参数页签失败"
            ok = kb.poll(lambda: kb.prim_schema_rows() >= 1)
            assert ok, "参数页签未渲染 JSON Schema 树"

        # ── C6 右键 tools → 新建目录/新建工具（工具页签） ──
        def c6():
            assert kb.enter_tools(), "切「工具」页签 / 展开 tools 失败"
            assert kb.rclick_kb_row("tools", True), "右键 tools 失败"
            ok = kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            assert ok, "右键菜单未弹出"
            texts = kb.kb_menu_texts()
            joined = " | ".join(texts)
            assert any(("新建目录" in x or x == "New Folder") for x in texts), f"缺 新建目录：{joined}"
            assert any(is_new_of_type(x) for x in texts), f"缺 新建工具：{joined}"
            kb.close_menus()
            time.sleep(0.3)

        # ── C7 新建工具 smoke_it（工具页签） ──
        def c7():
            assert kb.enter_tools(), "切「工具」页签 / 展开 tools 失败"
            assert kb.rclick_kb_row("tools", True), "右键 tools(2) 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            texts = kb.kb_menu_texts()
            hit = next((x for x in texts if is_new_of_type(x)), None)
            assert hit, f"未找到 新建工具 菜单项：{texts}"
            assert kb.kb_click_menu(hit), "点击 新建工具 失败"
            ok = kb.poll(lambda: kb.js("!!document.querySelector('.dialog-shell .b-input')"))
            assert ok, "名称输入框未弹出"
            assert kb.set_dialog_input("smoke_it"), "输入名称失败"
            assert kb.dialog_enter(), "回车确认失败"
            ok = kb.poll(lambda: os.path.exists(SMOKE_FILE))
            assert ok, "smoke_it.tool.md 未在磁盘生成"
            data = read_until(SMOKE_FILE, ["[meta]", "[description]"])
            assert "[meta]" in data and "[description]" in data, "模板缺契约分区"
            ok = kb.wait_kb_row("smoke_it.tool.md", False)
            assert ok, "smoke_it.tool.md 未出现在树行"
            # 新建后应自动打开 preview（可见面板，非首个 DOM 面板）
            ok = kb.poll(lambda: kb.prim_panel_exists() and kb.prim_active_path().replace("\\", "/").endswith("smoke_it.tool.md"))
            if not ok:
                print("  [note] 新建后 preview 未自动聚焦（尝试手工打开）")
                assert kb.click_kb_file("smoke_it.tool.md"), "点击 smoke 行失败"
                kb.poll(lambda: kb.prim_panel_exists())

        # ── C8 编辑保存 + 删除清理（工具页签） ──
        def c8():
            assert kb.enter_tools(), "切「工具」页签 / 展开 tools 失败"
            # 先经底部页签栏激活 smoke 文件页签（新建后不保证自动聚焦）
            assert kb.activate_bottom_tab("smoke_it.tool.md"), "preview 底部无 smoke 页签"
            ok = kb.poll(lambda: kb.prim_active_path().replace("\\", "/").endswith("smoke_it.tool.md"))
            assert ok, f"smoke 页签激活失败，活动面板={kb.prim_active_path()}"
            assert kb.prim_click_tab(["描述", "Description"]), "C8 切描述页签失败"
            kb.poll(lambda: (kb.prim_edit_info() or {}).get("ok"))
            active_path = kb.prim_active_path().replace("\\", "/")
            assert active_path.endswith("smoke_it.tool.md"), f"活动面板不是 smoke 文件：{active_path}"
            new_desc = "Smoke description saved via GUI " + str(int(time.time()))
            kb.prim_set_ta(0, new_desc)
            kb.poll(lambda: kb.prim_dirty())
            assert kb.prim_click_save(), "点击保存按钮失败"
            ok = kb.poll(lambda: not kb.prim_dirty())
            assert ok, "保存后 dirty 未清除"
            data = open(active_path.replace("/", os.sep), encoding="utf-8").read()
            assert new_desc in data, "磁盘文件未写入新描述"
            # 删除
            assert kb.rclick_kb_row("smoke_it.tool.md", False), "右键 smoke 文件失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            texts = kb.kb_menu_texts()
            hit = next((x for x in texts if x in ("删除", "Delete")), None)
            assert hit, f"菜单无 删除：{texts}"
            assert kb.kb_click_menu(hit), "点击删除失败"
            ok = kb.poll(lambda: kb.js("!!document.querySelector('button.b-btn--primary')"))
            assert ok, "确认删除弹窗未出现"
            assert kb.click_primary(), "点击确认失败"
            ok = kb.poll(lambda: not os.path.exists(SMOKE_FILE))
            assert ok, "磁盘文件未删除"
            ok = kb.poll(lambda: not any(kb.row_label(r) == "smoke_it.tool.md" for r in kb.kb_rows()))
            assert ok, "树行未移除 smoke_it"

        # ── C9 项目级知识库：demo.tool.md → PrimitivePanel；typed 目录右键菜单 ──
        def c9():
            """项目级原语预览 + 右键菜单（迁移后 = 知识库树「项目」级）。

            迁移说明（2026-09-15）：项目侧旧路径 `@mcp/tools/...` 已按 spec 60-名词约定（P1-3）
            迁移为 `<workDir>/.chonkpilot/capability/{tools,skills,prompts,resources,agents,scenarios}`（2026-10-01：P1 扁平化，旧 `knowledge/` 归并层已删）；
            项目**文件树**不暴露 `.chonkpilot`，且 data-knowledge-{list,read} 只认四级 capability
            根内路径（persist `kbRootOf`）→ 改由知识库树「项目」级驱动。
            """
            assert kb.switch_mode("tools"), "C9 切「工具」页签失败"
            assert kb.switch_kb_level("project", "项目"), f"切项目级失败：{[kb.row_label(r) for r in kb.kb_rows()]}"
            assert kb.ensure_kb_expanded(["tools", "core"]), "项目级 tools/core 未展开"
            ok = kb.poll(lambda: any(kb.row_label(r) == "demo.tool.md" and not r["d"] for r in kb.kb_rows()))
            assert ok, "项目级 core 下无 demo.tool.md"
            # 右键 typed 目录验证菜单
            assert kb.rclick_kb_row("tools", True), "右键项目 tools 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            txts = kb.kb_menu_texts()
            assert any(("新建目录" in x or x == "New Folder") for x in txts), f"项目菜单缺 新建目录：{txts}"
            assert any(is_new_of_type(x) for x in txts), f"项目菜单缺 新建工具：{txts}"
            kb.close_menus()
            time.sleep(0.3)
            assert kb.click_kb_file("demo.tool.md"), "点击 demo.tool.md 失败"
            ok = kb.poll(lambda: kb.prim_panel_exists())
            assert ok, "项目级 demo 未打开 PrimitivePanel"
            assert kb.prim_active_path().replace("\\", "/").endswith("demo.tool.md"), "demo 面板路径异常"
            assert kb.prim_title_value(), "demo 顶部标题为空"

        # ── C10 知识库目录写链路（全 UI 路径，I-73 两处缺陷回归）──
        def c10():
            """知识库「目录」写链路全走 UI（I-73 修复后口径，覆盖两处缺陷）：

              ① 右键「新建目录」→ 目录落盘 → **树行自动出现** → **自动进入内联改名**
                 （输入框预填落盘名、全选）——旧实现 `createPrimitiveDir` 返回字符串被当
                 `{path}` 二次解包 → target 恒空 → 直接 return（无刷新/无内联改名）；
              ② **目录语义分流**：内联改名 / F2 / 右键「重命名」三条路径作用于目录行时都必须
                 走 `data-knowledge-rename-dir`——旧实现 F2 与右键走 `renamePrimitive`（文件
                 语义）→ 目标名被追加 `.md`（实测 `Access is denied`）→ 目录改名不可用；
                 除磁盘断言外，同时断言**无 error toast**（`.b-message--error`）。
              覆盖不降：磁盘写入 / 树行可见 / 改名生效 / 删除 四段齐备（新增 F2 与右键两条目录改名路径）。
            """
            # ① 右键「新建目录」→ 落盘 + 树行出现 + 自动内联改名
            assert kb.enter_tools(), "切「工具」页签 / 展开 tools 失败"
            assert kb.rclick_kb_row("tools", True), "右键 tools 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            texts = kb.kb_menu_texts()
            hit = next((x for x in texts if x in ("新建目录", "New Folder")), None)
            assert hit, f"菜单无 新建目录：{texts}"
            assert kb.kb_click_menu(hit), "点击 新建目录 失败"
            ok = kb.poll(lambda: os.path.isdir(DIR_DEFAULT))
            assert ok, f"新建目录未在磁盘生成：{DIR_DEFAULT}"
            # 「新建目录出现在树行」的判定口径（I-73① 修复后）：新目录行**立即进入内联改名**，
            # 行 label 被输入框顶替（textContent 为空）→ 不能按 label 文本匹配（旧断言必失败）。
            # 改为断言「存在一个**目录语义**的行、其内联输入框预填 = 落盘名」——同时覆盖
            # 树行可见 + 目录语义 + 预填名，强度不降。
            ok = kb.poll(lambda: kb.js(
                "(function(){const el=document.querySelector('.knowledge-tree .inline-edit-input');"
                "if(!el)return false;const row=el.closest('.tree-row');"
                "return !!row&&row.classList.contains('is-dir')&&el.value===%s})()"
                % json.dumps(DIR_DEFAULT_NAME)))
            assert ok, f"新建目录未在树中以目录行进入内联改名：{[kb.row_label(r) for r in kb.kb_rows()]}"
            ok = kb.poll(lambda: kb.inline_input() == DIR_DEFAULT_NAME)
            assert ok, f"新建后未自动进入内联改名（或默认名不符）：{kb.inline_input()!r}"
            assert kb.js("document.querySelectorAll('.b-message--error').length") == 0, "新建目录弹出了错误提示"
            # ② 内联改名 smoke_dir1（目录语义）
            assert kb.set_inline_input("smoke_dir1"), "设置新名失败"
            assert kb.inline_enter(), "内联确认失败"
            ok = kb.poll(lambda: os.path.isdir(DIR_SMOKE) and not os.path.exists(DIR_DEFAULT))
            assert ok, "smoke_dir1 目录未在磁盘生成"
            assert not os.path.exists(DIR_SMOKE + ".md"), "目录改名被按文件语义追加 .md"
            ok = kb.wait_kb_row("smoke_dir1", True)
            assert ok, f"smoke_dir1 未出现在树行：{[kb.row_label(r) for r in kb.kb_rows()]}"
            # ③ F2 改目录名（缺陷② 路径一）：目录语义，不得追加 .md
            assert kb.click_kb_dir("smoke_dir1"), "单击 smoke_dir1 失败"
            assert kb.press_f2(), "F2 发送失败"
            ok = kb.poll(lambda: kb.inline_input() == "smoke_dir1")
            assert ok, f"F2 未进入目录改名或初始值不对：{kb.inline_input()!r}"
            assert kb.set_inline_input("smoke_dir2"), "设置新名失败(F2)"
            assert kb.inline_enter(), "内联确认失败(F2)"
            ok = kb.poll(lambda: os.path.isdir(DIR_SMOKE2) and not os.path.exists(DIR_SMOKE))
            assert ok, "F2 目录改名 smoke_dir2 未在磁盘生效"
            assert not os.path.exists(DIR_SMOKE2 + ".md"), "F2 目录改名按文件语义追加 .md"
            ok = kb.wait_kb_row("smoke_dir2", True)
            assert ok, f"smoke_dir2 未出现在树行：{[kb.row_label(r) for r in kb.kb_rows()]}"
            assert kb.js("document.querySelectorAll('.b-message--error').length") == 0, "F2 目录改名弹出了错误提示"
            # ④ 右键「重命名」目录行（缺陷② 路径二）→ 改回 smoke_dir1
            assert kb.rclick_kb_row("smoke_dir2", True), "右键 smoke_dir2 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            rtexts = kb.kb_menu_texts()
            rhit = next((x for x in rtexts if x in ("重命名", "Rename")), None)
            assert rhit, f"目录菜单无 重命名：{rtexts}"
            assert kb.kb_click_menu(rhit), "点击 重命名(目录) 失败"
            ok = kb.poll(lambda: kb.inline_input() == "smoke_dir2")
            assert ok, f"右键重命名未进入内联改名或初始值不对：{kb.inline_input()!r}"
            assert kb.set_inline_input("smoke_dir1"), "设置新名失败(右键)"
            assert kb.inline_enter(), "内联确认失败(右键)"
            ok = kb.poll(lambda: os.path.isdir(DIR_SMOKE) and not os.path.exists(DIR_SMOKE2))
            assert ok, "右键目录改名未在磁盘生效"
            assert not os.path.exists(DIR_SMOKE + ".md"), "右键目录改名按文件语义追加 .md"
            assert kb.js("document.querySelectorAll('.b-message--error').length") == 0, "右键目录改名弹出了错误提示"
            # ⑤ 删除目录清理（UI 右键菜单 + 确认）
            ok = kb.wait_kb_row("smoke_dir1", True)
            assert ok, f"smoke_dir1 未回到树行：{[kb.row_label(r) for r in kb.kb_rows()]}"
            assert kb.rclick_kb_row("smoke_dir1", True), "右键 smoke_dir1 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            dtexts = kb.kb_menu_texts()
            dhit = next((x for x in dtexts if x in ("删除", "Delete")), None)
            assert dhit, f"菜单无 删除(目录)：{dtexts}"
            assert kb.kb_click_menu(dhit), "点击删除(目录)失败"
            ok = kb.poll(lambda: kb.js("!!document.querySelector('button.b-btn--primary')"))
            assert ok, "删除目录确认弹窗未出现"
            assert kb.click_primary(), "点击确认失败"
            ok = kb.poll(lambda: not os.path.exists(DIR_SMOKE))
            assert ok, "smoke_dir1 目录未删除"
            ok = kb.poll(lambda: not any(kb.row_label(r) == "smoke_dir1" for r in kb.kb_rows()))
            assert ok, "smoke_dir1 树行未移除"

        # ── C11 工具页签：F2 重命名文件（保留 .tool.md 后缀，改回原名） ──
        def c11():
            # 确保 core 展开（C10 刷新 tools 后 core 可能被重建收起；仅当目标行不可见才点击）
            assert kb.enter_tools(), "切「工具」页签 / 展开 tools 失败"

            def has_kb(name, is_dir):
                return any(kb.row_label(r) == name and (is_dir is None or r["d"] == is_dir) for r in kb.kb_rows())

            if not has_kb("file_find.tool.md", False):
                if not has_kb("core", True):
                    assert kb.click_kb_dir("tools"), "点击 tools 失败"
                    assert kb.wait_kb_row("core", True), "core 不可见"
                assert kb.click_kb_dir("core"), "点击 core 失败"
                assert kb.wait_kb_row("file_find.tool.md", False), "file_find.tool.md 不可见"
            assert kb.click_kb_file("file_find.tool.md"), "单击 file_find.tool.md 失败"
            kb.poll(lambda: kb.prim_panel_exists())
            assert kb.press_f2(), "F2 发送失败"
            ok = kb.poll(lambda: kb.inline_input() == "file_find.tool.md")
            assert ok, f"F2 未进入编辑或初始值不对：{kb.inline_input()!r}"
            assert kb.set_inline_input("zz_find.tool.md"), "设置新名失败"
            assert kb.inline_enter(), "内联确认失败"
            ok = kb.poll(lambda: os.path.exists(os.path.join(CAP_DIR, "tools", "core", "zz_find.tool.md")) and not os.path.exists(CORE_FIND))
            assert ok, "改名 zz_find.tool.md 未生效"
            ok = kb.wait_kb_row("zz_find.tool.md", False)
            assert ok, "树行未出现 zz_find.tool.md"
            # 改回原名（清理）
            assert kb.click_kb_file("zz_find.tool.md"), "单击 zz_find 失败"
            assert kb.press_f2()
            kb.poll(lambda: kb.inline_input() == "zz_find.tool.md")
            assert kb.set_inline_input("file_find.tool.md")
            assert kb.inline_enter()
            ok = kb.poll(lambda: os.path.exists(CORE_FIND) and not os.path.exists(os.path.join(CAP_DIR, "tools", "core", "zz_find.tool.md")))
            assert ok, "改回 file_find.tool.md 未生效"
            ok = kb.wait_kb_row("file_find.tool.md", False)
            assert ok, "树行未恢复 file_find.tool.md"

        # ── C12 类型目录新建 技能/提示词/资源 模板（扩展页 5 子 tab，2026-10-01 P3） ──
        def c12():
            # 夹具幂等：清除上一次失败运行残留的目标文件（否则存在性轮询会命中陈旧内容）
            cleanup_fixtures()
            # 扩展页各子 tab 单类型：技能→skills / 命令→prompts / 知识→resources
            cases = [
                ("skills", "skill", "New Skill", "新建技能", "sk_s1", SKILL_FILE, ".skill.md"),
                ("prompts", "prompt", "New Prompt", "新建提示词", "pr_s1", PROMPT_FILE, ".prompt.md"),
                ("resources", "resource", "New Resource", "新建资源", "re_s1", RES_FILE, ".resource.md"),
            ]
            for dirname, ext, en_label, _zh_label, fname, path, suffix in cases:
                assert kb.switch_mode("extensions", ext=ext), f"{dirname} 切扩展页子 tab 失败"
                # 幂等展开根（切换子 tab 会重挂载树 → 需重新展开）
                assert kb.ensure_kb_expanded([]), f"扩展页（{ext}）根展开失败"
                assert kb.wait_kb_row(dirname, True), f"{dirname} 目录不可见"
                assert kb.rclick_kb_row(dirname, True), f"右键 {dirname} 失败"
                kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
                texts = kb.kb_menu_texts()
                hit = next((x for x in texts if is_new_of_type(x) and (en_label in x or ("技能" in x and "新建" in x) or ("提示词" in x and "新建" in x) or ("资源" in x and "新建" in x))), None)
                assert hit, f"{dirname} 菜单无新建项：{texts}"
                assert kb.kb_click_menu(hit), f"点击 {hit} 失败"
                ok = kb.poll(lambda: kb.js("!!document.querySelector('.dialog-shell .b-input')"))
                assert ok, f"{dirname} 名称输入框未弹出"
                assert kb.set_dialog_input(fname), f"输入 {fname} 失败"
                assert kb.dialog_enter(), "回车确认失败"
                ok = kb.poll(lambda: os.path.exists(path))
                assert ok, f"{suffix} 文件未生成：{path}"
                needles = ["[description]"] + (["[arguments]"] if dirname == "prompts" else [])
                data = read_until(path, needles)
                assert "[description]" in data, f"{suffix} 模板缺 [description]"
                if dirname == "prompts":
                    assert "[arguments]" in data, "prompt 模板缺 [arguments]"
                ok = kb.poll(lambda: any(kb.row_label(r) == fname + suffix for r in kb.kb_rows()))
                assert ok, f"{fname}{suffix} 未出现在树行"
            # 清理三个文件（右键删除；逐子 tab 切换 + 展开类型目录）
            for fname, suffix, ext, dirname in [("sk_s1", ".skill.md", "skill", "skills"), ("pr_s1", ".prompt.md", "prompt", "prompts"), ("re_s1", ".resource.md", "resource", "resources")]:
                label = fname + suffix
                assert kb.switch_mode("extensions", ext=ext), f"{label} 切扩展页子 tab 失败"
                assert kb.ensure_kb_expanded([dirname]), f"{label} 扩展页展开 {dirname} 失败"
                assert kb.rclick_kb_row(label, False), f"右键 {label} 失败"
                kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
                dtexts = kb.kb_menu_texts()
                dhit = next((x for x in dtexts if x in ("删除", "Delete")), None)
                assert dhit, f"{label} 菜单无删除：{dtexts}"
                assert kb.kb_click_menu(dhit), f"点击删除 {label} 失败"
                kb.poll(lambda: kb.js("!!document.querySelector('button.b-btn--primary')"))
                assert kb.click_primary(), f"确认删除 {label} 失败"
                ok = kb.poll(lambda: not any(kb.row_label(r) == label for r in kb.kb_rows()))
                assert ok, f"{label} 树行未移除"
                assert not os.path.exists({"sk_s1": SKILL_FILE, "pr_s1": PROMPT_FILE, "re_s1": RES_FILE}[fname]), f"{label} 磁盘未删除"

        # ── C13 项目级知识库：新建工具 → 保存 → 删除（项目级写链路）──
        def c13():
            """项目级原语完整写链路（迁移后 = 知识库树「项目」级，见 C9 迁移说明）。"""
            assert kb.switch_mode("tools"), "C13 切「工具」页签失败"
            assert kb.switch_kb_level("project", "项目"), "C13 切项目级失败"
            assert kb.ensure_kb_expanded(["tools"]), "C13 项目级 tools 未展开"
            # 右键 tools → 新建工具
            assert kb.rclick_kb_row("tools", True), "右键项目 tools 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            txts = kb.kb_menu_texts()
            hit = next((x for x in txts if is_new_of_type(x) and ("Tool" in x or "工具" in x)), None)
            assert hit, f"项目 tools 菜单无 新建工具：{txts}"
            assert kb.kb_click_menu(hit), "点击 新建工具 失败"
            ok = kb.poll(lambda: kb.js("!!document.querySelector('.dialog-shell .b-input')"))
            assert ok, "项目新建名称输入框未弹出"
            assert kb.set_dialog_input("pj_smoke"), "输入 pj_smoke 失败"
            assert kb.dialog_enter(), "回车确认失败"
            ok = kb.poll(lambda: os.path.exists(PJ_FILE))
            assert ok, f"pj_smoke.tool.md 未在项目级 {PJ_DIR} 生成"
            data = read_until(PJ_FILE, ["[meta]", "[description]"])
            assert "[meta]" in data and "[description]" in data, "项目模板缺契约分区"
            ok = kb.wait_kb_row("pj_smoke.tool.md", False)
            assert ok, "pj_smoke.tool.md 未出现在项目级树行"
            # 打开新建文件（与 C7 同口径：新建后 preview 是否**自动聚焦**不作硬断言，
            # 未自动打开则显式单击树行——本用例的覆盖目标是"项目级写链路"，非"自动聚焦"）
            if not kb.activate_bottom_tab("pj_smoke.tool.md"):
                assert kb.click_kb_file("pj_smoke.tool.md"), "单击 pj_smoke 行失败"
                ok = kb.poll(lambda: kb.activate_bottom_tab("pj_smoke.tool.md"))
                assert ok, "项目级 smoke 未打开 preview 页签"
            ok = kb.poll(lambda: kb.prim_active_path().replace("\\", "/").endswith("pj_smoke.tool.md"))
            assert ok, f"活动原语面板不是 pj_smoke：{kb.prim_active_path()}"
            assert kb.prim_click_tab(["描述", "Description"]), "C13 切描述页签失败"
            kb.poll(lambda: (kb.prim_edit_info() or {}).get("ok"))
            pj_desc = "Project primitive saved via GUI " + str(int(time.time()))
            kb.prim_set_ta(0, pj_desc)
            kb.poll(lambda: kb.prim_dirty())
            assert kb.prim_click_save(), "C13 点击保存失败"
            ok = kb.poll(lambda: not kb.prim_dirty())
            assert ok, "C13 保存后 dirty 未清除"
            assert pj_desc in open(PJ_FILE, encoding="utf-8").read(), "项目文件未写入新描述"
            # 删除清理
            assert kb.rclick_kb_row("pj_smoke.tool.md", False), "右键 pj_smoke 失败"
            kb.poll(lambda: len(kb.kb_menu_texts()) > 0)
            dtexts = kb.kb_menu_texts()
            dhit = next((x for x in dtexts if x in ("删除", "Delete")), None)
            assert dhit, f"项目菜单无删除：{dtexts}"
            assert kb.kb_click_menu(dhit), "点击项目删除失败"
            kb.poll(lambda: kb.js("!!document.querySelector('button.b-btn--primary')"))
            assert kb.click_primary(), "确认项目删除失败"
            ok = kb.poll(lambda: not os.path.exists(PJ_FILE))
            assert ok, "pj_smoke.tool.md 磁盘未删除"
            ok = kb.poll(lambda: not any(kb.row_label(r) == "pj_smoke.tool.md" for r in kb.kb_rows()))
            assert ok, "pj_smoke.tool.md 项目级树行未移除"

        # ── C14 项目私有级（prjusr）端到端：预置落盘 → UI 可见；切系统级不可见（按级隔离）──
        def c14():
            """项目私有级（prjusr = 四级能力面最具体一级）端到端 L4：

              预置：查询 prjusr 能力根（`<data-dir>/<project-id>/capability`，见模块头 DATA_DIR 说明），
                    直接落盘 1 个工具契约（`tools/prjusr_smoke.tool.md`）——不要求走 UI 新建；
              正例：扩展页「工具」子 tab → 级别【项目私有】→ 该资产在树行**可见**；
              反证：切回【系统级】→ 展开 app 的 tools（有 app 内容作对照）→ 该资产**不可见**
                    （证明按级隔离 / 不越权，而非"树没加载"）。
            """
            assert kb.switch_mode("extensions", ext="tool"), "C14 切扩展页-工具失败"
            # 预置落盘：prjusr 根 + tools/<name>.tool.md
            r = c.req("data-knowledge-root", {"kind": "prjusr"}) or {}
            prjusr_root = (r.get("root") or "").replace("\\", "/")
            assert prjusr_root, f"C14 prjusr 根解析失败：{r}"
            tool_dir = os.path.join(prjusr_root.replace("/", os.sep), "tools")
            os.makedirs(tool_dir, exist_ok=True)
            fixture = os.path.join(tool_dir, "prjusr_smoke.tool.md")
            with open(fixture, "w", encoding="utf-8") as f:
                f.write("# prjusr_smoke\n\n[meta]\nname=prjusr_smoke\nkind=smoke\n\n"
                        "[description]\n项目私有级工具（C14 L4 夹具）\n\n[parameters]\nproperties: {}\nrequired: []\n\n"
                        "[content]\nnoop.\n")
            try:
                # 正例：级别=项目私有 → 树行可见
                assert kb.switch_kb_level("prjusr", "项目私有"), \
                    f"C14 切项目私有级失败：{[kb.row_label(x) for x in kb.kb_rows()]}"
                assert kb.ensure_kb_expanded(["tools"]), "C14 项目私有级 tools 未展开"
                ok = kb.poll(lambda: any(kb.row_label(x) == "prjusr_smoke.tool.md" for x in kb.kb_rows()))
                assert ok, f"项目私有级资产未在树行出现：{[kb.row_label(x) for x in kb.kb_rows()]}"
                # 反证：级别=系统 → 展开 app tools（有 app 内容为对照）→ 看不到该 prjusr 资产
                assert kb.switch_kb_level("app", "系统"), \
                    f"C14 切系统级失败：{[kb.row_label(x) for x in kb.kb_rows()]}"
                assert kb.ensure_kb_expanded(["tools"]), "C14 系统级 tools 未展开"
                # 对照：app 级 tools 下应有 core 目录（证明树确已加载，反证非"没渲染"）
                assert kb.wait_kb_row("core", True), "C14 系统级 tools 未列出 core（树未加载，反证无意义）"
                ok = kb.poll(lambda: not any(kb.row_label(x) == "prjusr_smoke.tool.md" for x in kb.kb_rows()),
                             timeout=3)
                assert ok, "系统级不应看到项目私有级资产（按级隔离失败）"
            finally:
                try:
                    os.remove(fixture)
                except OSError:
                    pass

        for name, fn in [
            ("C1 四段切换（项目/会话/记忆/扩展）+ 扩展页 5 子 tab", c1),
            ("C2 扩展页子 tab 单类型过滤 + 根行命名", c2),
            ("C3 tools→core→*.tool.md", c3),
            ("C4 打开原语→四页签 Tabs", c4),
            ("C5 页签 dirty/恢复 + meta/参数（JSON Schema）", c5),
            ("C6 右键类型目录菜单", c6),
            ("C7 新建工具 smoke_it", c7),
            ("C8 保存+删除清理", c8),
            ("C10 目录全UI链路（新建+内联改名+F2/右键目录改名+删除）", c10),
            ("C11 F2 重命名文件(保留后缀)", c11),
            ("C12 技能/命令/知识 三子 tab 新建模板 + 删除", c12),
            ("C9 项目级原语预览（扩展页-项目级）", c9),
            ("C13 项目级新建+保存+删除", c13),
            ("C14 项目私有级端到端（prjusr 可见 + 系统级不可见）", c14),
        ]:
            if run_case(name, fn):
                passed += 1
            else:
                failed += 1
                cons = c.console() or {}
                for e in (cons.get("entries") or [])[-8:]:
                    print("   console:", e.get("level"), str(e.get("text"))[:200])
        print(f"TOTAL passed={passed} failed={failed}")
        return 0 if failed == 0 else 1
    finally:
        # ① 先还原套件级配置（要用 client）；② 再回收自起 GUI；③ 清夹具/还原产物字节。
        if cfg_snap is not None and c is not None:
            _h.restore_config(c, cfg_snap)
        h.stop()
        cleanup_fixtures()
        restore_app_contract(app_contract_before)


if __name__ == "__main__":
    sys.exit(main())
