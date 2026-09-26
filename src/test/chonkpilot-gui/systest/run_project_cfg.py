# -*- coding: utf-8 -*-
"""项目配置页签 + 索引进度状态回归（FP 203-242 / 281-285 可自动化项）。

覆盖：
  K1 项目配置：5 页签（安全 / 上下文管理 / CodeGraph 索引 / Vfts 全文索引 / 自动提交）
  K2 安全页签：信任目录列表 + 添加按钮 + 添加出现行
  K3 上下文管理：说明文案 + 保留轮数/token 阈值输入 + 快速阈值/保存按钮
  K4 上下文管理-总结提示词：自动加载（继承值非空）+ 来源标注 + 恢复默认入口
  K5 CodeGraph 索引：状态文案 + 重建/重试/清除/保存 + 扩展名/排除目录输入
  K6 Vfts 全文索引：索引状态/统计呈现（旧「索引进度对话框」已移除 → 见下）
  L205 系统目录对话框（无法自动化）；全文索引 L238-242（无独立页签 → 已并入 Vfts 页签）

迁移口径（2026-09-15）：
  - 页签名 = zh-CN i18n（ProjectConfig.vue + locales/zh-CN/projectConfig.json）：
    安全 / 上下文管理 / CodeGraph 索引 / Vfts 全文索引 / 自动提交；
    旧英文名 Security/Context/Prompt/Code Index 已不匹配。
  - 「通用能力（提示词）」页签**已按 spec CFG-009 (D2) 摘除**（项目配置提示词仅余摘要提示词，
    见 36-配置配置 CFG-008/009）→ K4 改断言存活的「总结提示词自动加载 + 来源标注」（同强度）。
  - 「代码索引」页签已拆为 CodeGraph / Vfts 两个页签（CFG-010/012）→ K5 断言 CodeGraph 页。
  - 「索引进度对话框」（`codebase-config-preview` + `codebase-index-status-open`）随数据库查看器
    一并移除（全仓无消费者）→ K6 改断言 **Vfts 页签的索引状态/统计呈现**（同强度：状态可观测）。

前置：chonkpilot.exe --test-port=2345 已启动（workDir 有已建 vfts 索引 → K6 统计明细）。
"""
import json
import os
import re
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import ChonkClient, TestError, run_case

import harness as _h  # 按需加载 + 结束即回收（见 harness.py / 51-FP与测试映射 §测试资源规范）
_G = _h.acquire_gui(2345, work_dir=os.path.join(os.path.dirname(os.path.abspath(__file__)), "ws"))
c = _G.client
# 套件级配置快照-还原（51 §6-8）：K2-K5 会写 prj（security-dir-*/上下文阈值/codegraph.vfts status）
# 与经前端隐式落的 `opened-files`/`layout.*` → 退出前自动回滚到跑前状态。
_h.suite_config_guard(c)
_h.ensure_locale(c)  # 语言确定性：页签/状态文案按 zh-CN 断言（DB ui.locale 可能被他套件写成 en-US）

# 5 个页签（zh-CN 标签，英文为旧名兼容备选）
TAB_FAMILIES = [
    ("安全 / Security", r"^(安全|Security)$"),
    ("上下文 / Context", r"^(上下文管理|上下文|Context)$"),
    ("CodeGraph 索引", r".*CodeGraph.*"),
    ("Vfts 全文索引", r".*Vfts.*"),
    ("自动提交 / History", r"^(自动提交|历史|History)$"),
]

INDEX_STATE_WORDS = ("已停用", "未初始化", "索引构建中", "就绪", "出错",
                     "disabled", "uninitialized", "indexing", "ready", "error")


def deep_loads(v):
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            v = json.loads(v)
        except Exception:
            return v
    return v


def wait_el(selector, max_wait=8):
    deadline = time.time() + max_wait
    while time.time() < deadline:
        if c.exists(selector).get("count", 0) > 0:
            return True
        time.sleep(0.3)
    return False


def visible_text(selector):
    return deep_loads(c.eval("""(() => {
      const els = [...document.querySelectorAll(%s)].filter(e => e.offsetParent !== null);
      const el = els[els.length - 1];
      return el ? el.textContent : '';
    })()""" % json.dumps(selector)))


def eval_js(js, timeout=5000):
    return deep_loads(c.eval(js, timeout))


def open_project_cfg():
    c.mq_emit("project-config-open")
    if not wait_el(".project-config-panel"):
        raise TestError("项目配置 tab 未打开")
    time.sleep(0.5)


def panel_scope(inner):
    """在最后一个项目配置面板实例内执行 inner（变量 P = 面板根）。"""
    return eval_js("""(() => {
      const ps = [...document.querySelectorAll('.project-config-panel')];
      const P = ps[ps.length - 1];
      if (!P) return null;
      %s
    })()""" % inner)


def tab_labels():
    return eval_js("JSON.stringify([...document.querySelectorAll('.project-config-panel .b-tabs-item')].map(e => e.textContent.trim()))") or []


def switch_tab(*patterns):
    """按正则候选切换页签（命中首个）；返回 'ok' / 'no-tab'。"""
    js = """(() => {
      const ps = [...document.querySelectorAll('.project-config-panel')];
      const root = ps[ps.length - 1];
      if (!root) return 'no-panel';
      const items = [...root.querySelectorAll('.b-tabs-item')];
      const res = %s.map(p => new RegExp(p, 'i'));
      const t = items.find(x => res.some(re => re.test(x.textContent.trim())));
      if (t) { t.dispatchEvent(new MouseEvent('click', { bubbles: true })); return 'ok'; }
      return 'no-tab';
    })()""" % json.dumps(list(patterns))
    r = eval_js(js)
    time.sleep(0.9)
    return r


def case_tabs():
    open_project_cfg()
    labels = tab_labels()
    if len(labels) != 5:
        raise TestError(f"项目配置应恰含 5 页签: {labels}")
    misses = [name for name, pat in TAB_FAMILIES if not any(re.search(pat, lb) for lb in labels)]
    if misses:
        raise TestError(f"缺页签 {misses}: {labels}")


def case_security():
    open_project_cfg()
    if switch_tab(r"^(安全|Security)$") != "ok":
        raise TestError("未找到安全页签")
    if not wait_el(".list-container"):
        raise TestError("安全页签未渲染")
    # 添加按钮
    add_btn = panel_scope("""return [...P.querySelectorAll('.b-btn')].some(b => /add|new|添加|新增/i.test(b.textContent));""")
    if not add_btn:
        raise TestError("安全页签缺添加按钮")
    # 快照-还原（51 §6-8）：`security-add` 会落一条项目级信任目录 → finally 还原
    # （删除本用例新增的条目；原有条目若有变动则写回原值）。
    before = dict(c.req("data-prj-security-list", {}).get("list") or {})
    try:
        # 触发添加 → 列表出现新行（确认弹窗/输入；直接断言 add 事件链路无异常）
        c.mq_emit("security-add")
        time.sleep(0.8)
        if not c.exists(".security-dir-row").get("count", 0) > 0:
            raise TestError("添加信任目录后未出现行")
    finally:
        after = dict(c.req("data-prj-security-list", {}).get("list") or {})
        for k in after:
            if k not in before:
                c.req("data-prj-security-delete", {"id": k})
        for k, v in before.items():
            if after.get(k) != v:
                c.req("data-prj-security-save", {"data": {"key": k, "value": v}})


def case_context():
    open_project_cfg()
    if switch_tab(r"^(上下文管理|上下文|Context)$") != "ok":
        raise TestError("未找到上下文管理页签")
    txt = panel_scope("return P ? P.textContent : ''") or ""
    if not txt.strip():
        raise TestError("上下文管理页签内容为空")
    if "保留完整对话内容的轮次" not in txt or "简化区 Token 压缩阈值" not in txt:
        raise TestError(f"上下文管理页缺压缩说明/字段文案: {txt[:120]!r}")
    # 保留完整轮数 + token 阈值输入（blur 即落库）
    inputs = panel_scope("return P ? P.querySelectorAll('input.b-input').length : 0")
    if not inputs or inputs < 2:
        raise TestError(f"上下文管理应有 ≥2 个输入框，实际 {inputs}")
    btns = panel_scope("return P ? [...P.querySelectorAll('.b-btn')].map(b => b.textContent.trim()) : []") or []
    joined = " | ".join(btns)
    if not any(k in joined for k in ("保存", "Save")):
        raise TestError(f"上下文管理缺保存入口: {joined}")
    if not any(k in joined for k in ("64K", "128K", "256K", "512K", "1M")):
        raise TestError(f"上下文管理缺快速阈值按钮: {joined}")


def case_summary_prompt_load():
    """K4（原「通用能力-提示词加载」迁移）：总结提示词自动加载 + 来源标注 + 恢复默认入口。

    spec CFG-009：项目配置「通用能力（工具提示词）」UI 已摘除（D2），提示词页仅余摘要提示词
    （CFG-008）。故本用例断言该存活提示词的**自动加载**（继承系统级/内置默认 → 内容非空）、
    来源标注可见、以及「恢复默认」（取消项目覆盖）入口存在——与原用例同强度（提示词内容非空）。
    """
    open_project_cfg()
    if switch_tab(r"^(上下文管理|上下文|Context)$") != "ok":
        raise TestError("未找到上下文管理页签")
    if not wait_el(".project-config-panel .prompt-editor"):
        raise TestError("总结提示词编辑器未渲染")
    val = panel_scope("""const ta = P.querySelector('textarea.prompt-editor');
      return ta ? (ta.value || '') : '';""") or ""
    if not val.strip():
        raise TestError("总结提示词内容为空（应自动加载继承值）")
    hints = panel_scope("return P ? [...P.querySelectorAll('.field-hint')].map(e => e.textContent.trim()) : []") or []
    joined = " | ".join(hints)
    if not any(k in joined for k in ("当前来源", "继承", "覆盖", "inherit", "override")):
        raise TestError(f"总结提示词缺来源标注: {joined[:160]!r}")
    btns = panel_scope("return P ? [...P.querySelectorAll('.b-btn')].map(b => b.textContent.trim()) : []") or []
    if not any(("恢复默认" in b) or ("Reset" in b) for b in btns):
        raise TestError(f"总结提示词缺「恢复默认」入口: {btns}")


def case_codegraph():
    """K5（原「代码索引」页签迁移）：CodeGraph 索引页控件 + 状态文案。"""
    open_project_cfg()
    if switch_tab(r".*CodeGraph.*") != "ok":
        raise TestError("未找到 CodeGraph 索引页签")
    if not wait_el(".cg-state-text"):
        raise TestError("CodeGraph 页未渲染状态文案")
    state = (eval_js("(document.querySelector('.cg-state-text')||{}).textContent") or "").strip()
    if not state:
        raise TestError("CodeGraph 状态文案为空")
    if not any(w in state for w in INDEX_STATE_WORDS):
        raise TestError(f"CodeGraph 状态文案异常: {state!r}")
    btns = panel_scope("return P ? [...P.querySelectorAll('.b-btn')].map(b => b.textContent.trim()) : []") or []
    joined = " | ".join(btns)
    for k in ("重建索引", "重试失败", "清除索引", "保存"):
        if k not in joined:
            raise TestError(f"CodeGraph 缺按钮 {k}: {joined}")
    for label in ("参与索引的扩展名", "排除目录"):
        if label not in (panel_scope("return P ? P.textContent : ''") or ""):
            raise TestError(f"CodeGraph 缺字段 {label}")


def case_vfts_status():
    """K6（原「索引进度对话框」迁移）：Vfts 全文索引页的状态/统计呈现。

    旧链路 `codebase-index-status-open` + `.codebase-config-preview`（状态栏图标 → 索引进度对话框）
    已整体移除（全仓无消费者，statusbar 仅保留设置入口）→ 改为断言现行「索引进度/状态」的
    可见承载：Vfts 页签状态文案 + 就绪态下的统计明细（已索引文件/分块数/分词器/生效扩展名）。
    """
    open_project_cfg()
    if switch_tab(r".*Vfts.*") != "ok":
        raise TestError("未找到 Vfts 全文索引页签")
    if not wait_el(".vf-state-text"):
        raise TestError("Vfts 页未渲染状态文案")
    state = (eval_js("(document.querySelector('.vf-state-text')||{}).textContent") or "").strip()
    if not state:
        raise TestError("Vfts 状态文案为空")
    if not any(w in state for w in INDEX_STATE_WORDS):
        raise TestError(f"Vfts 状态文案异常: {state!r}")
    if "就绪" in state or "ready" in state.lower():
        stats = eval_js("JSON.stringify([...document.querySelectorAll('.vf-stat')].map(e => e.textContent.trim()))") or []
        joined = " | ".join(stats if isinstance(stats, list) else [str(stats)])
        for k in ("已索引文件", "分块数", "分词器", "生效扩展名"):
            if k not in joined:
                raise TestError(f"Vfts 就绪态缺统计项 {k}: {joined[:200]!r}")


def main():
    c.wait_ready()
    c.console(clear=True)
    total = 0
    ok = 0
    total += 1; ok += run_case("K1 项目配置 5 页签（安全/上下文管理/CodeGraph/Vfts/自动提交）", case_tabs)
    total += 1; ok += run_case("K2 安全页签：添加信任目录", case_security)
    total += 1; ok += run_case("K3 上下文管理：输入 + 快速阈值 + 保存", case_context)
    total += 1; ok += run_case("K4 总结提示词自动加载 + 来源标注 + 恢复默认", case_summary_prompt_load)
    total += 1; ok += run_case("K5 CodeGraph 索引：状态 + 控件", case_codegraph)
    total += 1; ok += run_case("K6 Vfts 索引状态/统计呈现", case_vfts_status)
    # 与其它套件统一口径：计数汇总 + 退出码（0=全过）——见 51-FP与测试映射 §1
    print("\n项目配置断言：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
