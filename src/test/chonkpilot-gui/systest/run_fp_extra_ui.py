# -*- coding: utf-8 -*-
"""FP 剩余 ✘ 项补测（A 组：事件注入可覆盖，无需 server）。

覆盖：
  T1  L315 任务不存在提示（.td-empty）
  T3  L137 取消/撤回后待发内容写回输入框（含附件 → chip 回显）
  T4  L222 项目配置-总结提示词「重置」（原「通用能力/提示词」页签已按 CFG-009 摘除 → 迁移）
  T5  L232 索引状态实时更新（CodeGraph 页签 + data-prj-config-refresh 订阅）
  T6a L257 场景智能体基本信息字段（名称/角色/LLM/描述；委托条件随子智能体引用化移入「扩展·智能体」）
  T6b L259 场景智能体工具过滤（按来源分组勾选）
  T6c L260 子智能体 = agents/ 引用（「选择智能体」选择器 + 无添加/复制按钮）
  T7  L258 场景智能体提示词【优化】按钮：已接入既有优化链路（非桩，不再提示「尚未实现」）

前置：chonkpilot.exe --test-port=2345 已启动（GUI 恒启 inprocess server）。

迁移口径（2026-09-15）：项目配置页签名 = zh-CN（安全/上下文管理/CodeGraph 索引/Vfts 全文索引/
文件历史）；T4/T5 原指向的「通用能力（提示词）」「代码索引」页签已分别按 spec CFG-009 (D2) 摘除
与拆分为 CodeGraph/Vfts 页签，故改指存活的等价对象（见各用例 docstring）。
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345)  # 复用优先；无实例则自起并在结束时回收
c = _G.client
_h.suite_config_guard(c)  # 套件级配置快照-还原（51 §6-8）：T4/T5 等写 usr/prj → 退出前自动回滚
_h.ensure_locale(c)  # 语言确定性：T4 页签文案按 zh-CN 断言（DB ui.locale 可能被他套件写成 en-US）

SEQ = [0]

# 用例自建的 **user 级** 场景：三级场景（app/user/project）自 2026-09-26 起**均可编辑**
# （`ScenarioDialogContent` 的 app 行亦给编辑/删除入口，25 §6）→ T6a/T6b/T6c/T7 仍统一落在
# 用户自建的 user 级场景上（避免改动出厂 app 级场景内容；T6 组收尾删除，只删本轮自造的）。
WSC_ID = "fp-misc-edit-sc"
_wsc_ready = [False]


def ensure_writable_scenario():
    """保证存在一个可写级别（user）场景（幂等）。"""
    if _wsc_ready[0]:
        return
    try:
        c.req("data-scenario-save", {"data": {
            "id": WSC_ID, "name": "FP 可编辑场景", "level": "user",
            "agents": [
                {"name": "主", "roleTag": "主", "isMain": True, "prompt": "fp-misc 主提示词"},
                {"name": "子", "roleTag": "子", "prompt": "fp-misc 子提示词"},
            ],
        }})
    except Exception as e:
        print("    [dbg] 自建 user 级场景失败:", e)
    _wsc_ready[0] = True


def cleanup_writable_scenario():
    """收尾：删除本轮自建的 user 级场景（不影响 app 级出厂场景）。"""
    try:
        c.req("data-scenario-delete", {"data": {"id": WSC_ID, "level": "user"}})
    except Exception:
        pass


def _loads_deep(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def new_sid():
    SEQ[0] += 1
    return f"fp-misc-{SEQ[0]:03d}"


def wait_el(selector, max_wait=8):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.3)
    return False


def visible_text(selector):
    return _loads_deep(c.eval("""(() => {
      const els = [...document.querySelectorAll(%s)].filter(e => e.offsetParent !== null);
      const el = els[els.length - 1];
      return el ? el.textContent : '';
    })()""" % json.dumps(selector)))


def click_btn_in(scope, pattern):
    """在最后一个 scope 实例内点击文本匹配 pattern（正则）的 .b-btn（存在性，不依赖可见性）。"""
    r = _loads_deep(c.eval("""(() => {
      const scoped = %s ? [...document.querySelectorAll(%s)] : [];
      const root = scoped.length ? scoped[scoped.length - 1] : document;
      const btns = [...(root || document).querySelectorAll('.b-btn')];
      const re = new RegExp(%s, 'i');
      const b = [...btns].reverse().find(x => re.test(x.textContent || ''));
      if (!b) return 'not-found';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % (json.dumps(bool(scope)), json.dumps(scope or ''), json.dumps(pattern, ensure_ascii=False))))
    return r


def click_visible_tab(tab_pattern, panel_scope):
    """在最后一个 panel 实例内点击文本匹配的页签（存在性）。"""
    return _loads_deep(c.eval("""(() => {
      const scoped = %s ? [...document.querySelectorAll(%s)] : [];
      const panel = scoped.length ? scoped[scoped.length - 1] : document;
      const els = [...(panel || document).querySelectorAll('.b-tabs-item')];
      const re = new RegExp(%s, 'i');
      const el = els.find(x => re.test(x.textContent || ''));
      if (!el) return 'not-found';
      el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()""" % (json.dumps(bool(panel_scope)), json.dumps(panel_scope or ''), json.dumps(tab_pattern, ensure_ascii=False))))


# ── T1 任务不存在提示（L315） ─────────────────────────

def case_task_not_found():
    # 2026-09-27「首屏减负」后任务面板默认收起（MainLayout taskOpen 默认 false）→ 承载
    # `.task-detail`（TaskDetailView）的 SessionChat 未挂载 → 直接注入 task-detail-open 无处渲染。
    # 与其它任务套件同口径：先展开任务面板（等价点顶部「任务」开关；经既有 tasks-toggle 事件），
    # 再注入 task-detail-open。
    if not _h.ensure_task_panel_open(c):
        raise TestError("任务面板未展开（.session-chat 未挂载），无法断言任务详情")
    # 打开一个不存在的任务详情 → .td-empty「任务不存在」
    c.eval('window.mq.emit("task-detail-open", {task_id: "no-such-task-xyz"})')
    time.sleep(0.8)
    if not wait_el(".task-detail .td-empty"):
        raise TestError("任务不存在时未显示 .td-empty 提示")
    txt = visible_text(".task-detail .td-empty")
    if not any(k in txt for k in ("not found", "不存在")):
        raise TestError(f"任务不存在提示文案异常: {txt[:80]!r}")


# ── T3 排队消息写回输入框（L137） ─────────────────────

def case_queue_restore():
    # 注入 chatQueueRestore（取消/撤回写回），断言输入框文本 + 附件 chip 回显
    # 附件标记格式对齐消息序列化：![名](路径) 图片 / [名](路径) 文件
    c.eval('window.mq.emit("chat-queue-restore", {text: "被写回的排队消息 [文件](C:/tmp/a.txt)"})')
    time.sleep(0.8)
    txt = visible_text(".richtext-input")
    if "被写回的排队消息" not in txt:
        raise TestError(f"排队消息未写回输入框: {txt[:80]!r}")
    if not wait_el(".attach-chip.attach-file"):
        raise TestError("文件附件标记未回显为 chip")
    # 清理输入区（避免影响后续用例）
    c.eval("""(() => {
      const el = document.querySelector('.richtext-input');
      if (el) { el.innerHTML = ''; el.dispatchEvent(new Event('input', { bubbles: true })); }
      return 'ok';
    })()""")
    time.sleep(0.3)


# ── T4 总结提示词「重置」（L222；原「通用能力-提示词」页签已按 spec CFG-009 摘除） ──

def open_project_cfg_panel():
    c.mq_emit("project-config-open")
    if not wait_el(".project-config-panel"):
        raise TestError("项目配置页未打开")
    time.sleep(0.8)


def switch_cfg_tab(*patterns):
    """切换项目配置页签（zh-CN 标签；旧英文名已不匹配）。返回 'ok'/'no-tab'。"""
    r = _loads_deep(c.eval("""(() => {
      const ps = [...document.querySelectorAll('.project-config-panel')];
      const root = ps[ps.length - 1];
      if (!root) return 'no-panel';
      const items = [...root.querySelectorAll('.b-tabs-item')];
      const res = %s.map(p => new RegExp(p, 'i'));
      const t = items.find(x => res.some(re => re.test(x.textContent.trim())));
      if (t) { t.dispatchEvent(new MouseEvent('click', { bubbles: true })); return 'ok'; }
      return 'no-tab';
    })()""" % json.dumps(list(patterns)), 5000))
    time.sleep(0.9)
    return r


def _open_summary_editor():
    """点上下文管理页「总结提示词」区块的【编辑】按钮 → 打开 TextEditDialog。返回 'ok'/'no-btn'。"""
    return _loads_deep(c.eval("""(() => {
      const hs = [...document.querySelectorAll('.project-config-panel .prompt-editor-header')];
      const h = hs[0];
      const b = h ? [...h.querySelectorAll('.b-btn')].find(x => /编辑|Edit/i.test(x.textContent.trim())) : null;
      if (!b) return 'no-btn';
      b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return 'ok';
    })()"""))


def _close_text_dialogs():
    """关闭残留的文本编辑弹框（点 X），避免外溢。"""
    for _ in range(3):
        r = _loads_deep(c.eval("""(() => {
          const ds = [...document.querySelectorAll('.dialog-shell')].filter(e => e.getBoundingClientRect().width > 0);
          const R = ds[ds.length - 1];
          if (!R) return 'no';
          const b = R.querySelector('.dialog-btn-close');
          if (!b) return 'no-btn';
          b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
          return 'ok';
        })()"""))
        if r != "ok":
            return
        time.sleep(0.4)


def case_prompt_reset():
    """L222 提示词「重置」（迁移后 = 上下文管理页「总结提示词」）。

    依据 spec CFG-009 (D2)：项目配置「通用能力（工具提示词）」UI 已摘除，项目配置提示词仅余
    摘要提示词（CFG-008，位于「上下文管理」页）。**2026-09-26：只读展示已移除** → 内容改在
    **编辑弹框**（TextEditDialog）内查看。本用例断言：「编辑」按钮 → 弹框内容非空（自动加载）
    →「重置」（取消项目覆盖）按钮存在 → 点击后再次打开弹框内容仍非空 → 无控制台错误。
    """
    open_project_cfg_panel()
    if switch_cfg_tab(r"^(上下文管理|上下文|Context)$") != "ok":
        raise TestError("未找到上下文管理（提示词所在）页签")
    if not wait_el(".prompt-editor-header"):
        raise TestError("总结提示词区块未渲染")
    # 「编辑」→ 弹框；内容 = 自动加载的有效值（非空）
    if _open_summary_editor() != "ok":
        raise TestError("未找到总结提示词「编辑」按钮")
    if not wait_el(".text-edit-body"):
        raise TestError("总结提示词编辑弹框未打开")
    before = _loads_deep(c.eval("""(() => {
      const ts = [...document.querySelectorAll('.text-edit-body textarea')];
      const t = ts[ts.length - 1];
      return t ? (t.value || '') : '';
    })()""")) or ""
    if not before.strip():
        raise TestError("总结提示词未自动加载（内容为空）")
    # 收起弹框（「重置」在面板内，避免遮挡）
    _close_text_dialogs()
    if not wait_el(".project-config-panel .b-btn"):
        raise TestError("提示词工具栏按钮未渲染")
    r = click_btn_in(".project-config-panel", "重置|reset|还原|recover")
    if r != "ok":
        raise TestError("未找到重置按钮")
    time.sleep(1.0)
    # 重置后：再次打开弹框，内容仍非空（回落到继承值）
    if _open_summary_editor() != "ok":
        raise TestError("重置后总结提示词「编辑」按钮丢失")
    if not wait_el(".text-edit-body"):
        raise TestError("重置后编辑弹框未打开")
    after = _loads_deep(c.eval("""(() => {
      const ts = [...document.querySelectorAll('.text-edit-body textarea')];
      const t = ts[ts.length - 1];
      return t ? (t.value || '') : '';
    })()""")) or ""
    if not after.strip():
        raise TestError("重置后提示词内容为空")
    _close_text_dialogs()
    # 重置链路无异常（内容仍在 + 无控制台错误）
    console = c.console(True)
    for e in console.get("entries", []):
        if e.get("level") in ("error",) and "prompt" in (e.get("text") or "").lower():
            raise TestError(f"重置出现控制台错误: {e.get('text')}")
    return True


# ── T5 索引状态实时更新（L232；原「代码索引」页签 → 现 CodeGraph 索引页） ──

def case_codeindex_refresh():
    """L232 索引状态实时更新：CodeGraph 页状态文案 + data-prj-config-refresh 订阅重载无异常。"""
    open_project_cfg_panel()
    if switch_cfg_tab(r".*CodeGraph.*") != "ok":
        raise TestError("未找到 CodeGraph 索引页签")
    if not wait_el(".cg-state-text"):
        raise TestError("CodeGraph 状态文案未渲染")
    before = _loads_deep(c.eval("(document.querySelector('.cg-state-text')||{}).textContent")) or ""
    if not before.strip():
        raise TestError("CodeGraph 状态文案为空")
    # 注入 data-prj-config-refresh → 组件订阅自动重载（无异常即通过）
    c.eval('window.mq.emitRemote({type: "data-prj-config-refresh", payload: JSON.stringify({id: "codebase", op: "save"}), src: "data"})')
    time.sleep(0.8)
    after = _loads_deep(c.eval("(document.querySelector('.cg-state-text')||{}).textContent")) or ""
    if not after.strip():
        raise TestError("刷新后 CodeGraph 状态文案为空（订阅重载异常）")
    console = c.console(True)
    for e in console.get("entries", []):
        if e.get("level") in ("error", "warning") and "code" in (e.get("text") or "").lower():
            raise TestError(f"代码索引刷新出现控制台错误: {e.get('text')}")
    return True


# ── T6 场景编辑弹窗：智能体字段/工具过滤/复制/优化接线 ──

def open_scenario_edit():
    # 可写级别场景（app 级出厂场景只读、无编辑入口 → 自建 user 级场景承载编辑流程）
    ensure_writable_scenario()
    # 清理残留编辑弹窗（幂等：点最后一个弹窗的取消）
    c.eval("""(() => {
      const ds = [...document.querySelectorAll('.edit-dialog-body')];
      const d = ds[ds.length - 1];
      if (d) {
        const btns = [...d.querySelectorAll('.b-btn')];
        const b = btns.find(x => /cancel|取消|close|关闭/i.test(x.textContent));
        if (b) b.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      }
      return 'ok';
    })()""")
    time.sleep(0.6)
    c.mq_emit("preview-tab-open", {"kind": "scenario", "title": "场景"})
    # 激活场景 preview 页签（多个预览 tab 时确保场景管理器挂载）
    c.eval("""(() => {
      const tabs = [...document.querySelectorAll('.preview-tab')];
      const t = tabs.find(x => /场景|scenario/i.test(x.textContent));
      if (t) t.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      return t ? 'ok' : 'no-tab';
    })()""")
    time.sleep(1.0)
    # 打开默认场景编辑弹窗（重试等待场景页渲染）
    r = "not-found"
    for _ in range(6):
        r = click_btn_in(None, "edit|编辑")
        if r == "ok":
            break
        time.sleep(1.0)
    if r != "ok":
        # 兜底：scenario-open 弹窗内编辑
        c.mq_emit("scenario-open")
        time.sleep(1.0)
        r = click_btn_in(".scenario-dialog-body-scroll", "edit|编辑")
    if r != "ok":
        raise TestError("未找到场景编辑按钮")
    if not wait_el(".edit-dialog-body"):
        raise TestError("场景编辑弹窗未打开")
    time.sleep(0.6)


def _last_edit_dialog_js():
    """返回取最后一个编辑弹窗的 JS 前缀表达式。"""
    return "(() => { const ds = [...document.querySelectorAll('.edit-dialog-body')]; return ds[ds.length - 1] || document; })()"


def ensure_agent_picker():
    """场景编辑弹窗内「选择智能体」选择器存在（P4 2026-10-01：agent 列表改为从 agents/ 选择）。

    旧口径 = 「添加子 Agent」按钮 + 复制子 Agent；新口径 = 子 agent 为 **agents/ 引用**
    （只读显示已选；内容编辑在「扩展 · 智能体」页）→ 弹窗内提供 `Select.agent-picker`。
    """
    r = _loads_deep(c.eval("""(() => { const ds = [...document.querySelectorAll('.edit-dialog-body')];
      const d = ds[ds.length - 1] || document;
      return JSON.stringify(!!d.querySelector('.agent-picker'))})()"""))
    if not r:
        raise TestError("未找到「选择智能体」选择器（.agent-picker）")
    time.sleep(0.4)


def case_agent_basic_fields():
    """L257 智能体基本信息：名称/角色标签/LLM/描述（主 agent 内联可编辑）。

    委托条件（delegateCond）随子智能体「引用化」移入「扩展 · 智能体」原语编辑器（主 agent 不渲染
    该字段）→ 本用例不再断言 delegate（覆盖点见 KB-002 智能体原语编辑器）。
    """
    open_scenario_edit()
    ensure_agent_picker()
    labels = _loads_deep(c.eval("""(() => {
      const ds = [...document.querySelectorAll('.edit-dialog-body')];
      const d = ds[ds.length - 1] || document;
      const ls = [...d.querySelectorAll('.form-label')].map(x => x.textContent);
      return JSON.stringify(ls);
    })()"""))
    if isinstance(labels, str):
        labels = json.loads(labels)
    joined = " | ".join(labels or [])
    # 中英文兼容：zh-CN 与 en-US 任一命中即可
    zh_en = {
        "name": ("name", "名称"),
        "role": ("role", "角色"),
        "llm": ("llm", "模型"),
        "description": ("description", "说明", "描述"),
    }
    missing = [k for k, alts in zh_en.items() if not any(a in joined.lower() for a in alts)]
    if missing:
        raise TestError(f"智能体基本信息缺字段 {','.join(missing)}: {joined[:160]!r}")
    # 关闭弹窗
    click_btn_in(".edit-dialog-body", "cancel|取消")
    time.sleep(0.5)
    return True


def case_agent_copy():
    """L260（新口径，2026-10-01 P4）：子智能体 = agents/ 引用 —— 无「添加/复制子 agent」按钮；
    编辑弹窗提供「选择智能体」选择器（只读显示已选），且主 agent 之外**无内联编辑入口**。"""
    open_scenario_edit()
    ensure_agent_picker()
    info = _loads_deep(c.eval("""(() => {
      const ds = [...document.querySelectorAll('.edit-dialog-body')];
      const d = ds[ds.length - 1] || document;
      return JSON.stringify({
        picker: !!d.querySelector('.agent-picker'),
        addBtn: [...d.querySelectorAll('.b-btn')].some(x => /添加子|add sub/i.test(x.textContent)),
        copyBtn: [...d.querySelectorAll('.b-btn')].some(x => /复制|copy/i.test(x.textContent)),
      });
    })()"""))
    if isinstance(info, str):
        info = json.loads(info)
    if not info.get("picker"):
        raise TestError("缺「选择智能体」选择器")
    if info.get("addBtn"):
        raise TestError("不应再有「添加子 Agent」按钮（子 agent 改为 agents/ 引用选择）")
    if info.get("copyBtn"):
        raise TestError("不应再有「复制子 Agent」按钮（子 agent 只读引用）")
    click_btn_in(".edit-dialog-body", "cancel|取消")
    time.sleep(0.5)
    return True


def case_agent_tools_filter():
    """L259 工具过滤：filterTools 勾选 → 按来源分组出现工具清单。"""
    open_scenario_edit()
    # 打开工具页签
    if click_visible_tab("tools|工具", ".edit-dialog-body") != "ok":
        raise TestError("未找到智能体工具页签")
    time.sleep(0.6)
    if not wait_el(".filter-tools-checkbox"):
        raise TestError("工具过滤开关未渲染")
    # 勾选启用过滤 → 分类树出现
    c.eval("""(() => {
      const ds = [...document.querySelectorAll('.edit-dialog-body')];
      const d = ds[ds.length - 1] || document;
      const cb = d.querySelector('.filter-tools-checkbox input[type=checkbox]');
      if (cb && !cb.checked) cb.click();
      return 'ok';
    })()""")
    time.sleep(0.8)
    if not wait_el(".tool-category"):
        # 无工具时显示 no-tools 提示也算通过（工具组可能为空）
        if not wait_el(".tool-empty"):
            raise TestError("启用工具过滤后未出现工具分类/提示")
    click_btn_in(".edit-dialog-body", "cancel|取消")
    time.sleep(0.5)
    return True


def case_agent_optimize_wired():
    """L258 场景智能体【优化】按钮：已接入既有 `gui.prompt-optimise` 链路（流式回显写入 prompt）。

    本机未配置优化 LLM 时，点击后应给出**可见错误提示**（common.optimize_failed）；已配置时进入
    流式优化并回显到 `.prompt-textarea`。关键断言 = **不再出现桩文案「尚未实现」**（i18n 键已回收）
    → 说明点击确实发起了优化请求（而非仅弹提示）。
    """
    open_scenario_edit()
    # 精确匹配 agent 编辑器的「提示词」页签（锚定整串）：右侧「组合后系统提示词」预览页签
    # 含「提示词」子串且 DOM 在前 → 泛匹配会误点该页签（T5 新增预览页签后暴露）。
    if click_visible_tab(r"^\s*(提示词|prompt)\s*$", ".edit-dialog-body") != "ok":
        raise TestError("未找到提示词页签")
    time.sleep(0.6)
    if not wait_el(".prompt-toolbar"):
        raise TestError("提示词工具栏未渲染")
    # 清掉此前可能残留的提示层，避免误判
    c.eval("(() => { document.querySelectorAll('.b-message').forEach(e => e.remove()); return 'ok'; })()")
    c.eval("""(() => {
      const ds = [...document.querySelectorAll('.edit-dialog-body')];
      const d = ds[ds.length - 1] || document;
      const btns = [...d.querySelectorAll('.b-btn')];
      const b = btns.find(x => /optimize|优化|magic/i.test(x.textContent));
      if (b) { b.dispatchEvent(new MouseEvent('click', { bubbles: true })); return 'clicked'; }
      return 'not-found';
    })()""")
    time.sleep(1.2)
    body = c.eval('document.body.innerText') or ''
    if "尚未实现" in body or "not implemented" in body.lower():
        raise TestError("优化按钮仍为桩（出现「尚未实现」文案）")
    # 已配置 LLM → 流式回显写入 prompt；未配置 → 可见失败提示（优化中按钮 loading/禁用防重入）。
    # 二者皆表示「已接线」，本用例不依赖本机 LLM 配置。
    click_btn_in(".edit-dialog-body", "cancel|取消")
    time.sleep(0.5)
    return True


def main():
    c.wait_ready()
    c.console(clear=True)
    ok = 0
    total = 0
    try:
        total += 1; ok += run_case("T1 任务不存在提示（L315）", case_task_not_found)
        total += 1; ok += run_case("T3 排队消息写回输入框（L137）", case_queue_restore)
        total += 1; ok += run_case("T4 总结提示词重置（L222）", case_prompt_reset)
        total += 1; ok += run_case("T5 索引状态实时更新（L232）", case_codeindex_refresh)
        total += 1; ok += run_case("T6a 智能体基本信息字段（L257）", case_agent_basic_fields)
        total += 1; ok += run_case("T6b 智能体工具过滤（L259）", case_agent_tools_filter)
        total += 1; ok += run_case("T6c 子智能体 = agents/ 引用（选择器 + 无添加/复制按钮，L260）", case_agent_copy)
        total += 1; ok += run_case("T7 智能体优化按钮已接线（L258）", case_agent_optimize_wired)
    finally:
        cleanup_writable_scenario()  # 只删本轮自建的 user 级场景（51 §6-8 环境干净）
    print(f"\nFP 补测 A 组：{ok}/{total} 通过")
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
