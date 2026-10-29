# -*- coding: utf-8 -*-
"""项目配置页签 + 代码知识库（插件页）回归（FP 203-242 / 281-285 可自动化项）。

覆盖：
  K1 项目配置：8 页签（安全 / 上下文管理 / 代码知识库 / 文件历史 / 文件树 / 群聊 / 日志 / 配置导入导出）
  K2 安全页签：信任目录列表 + 添加按钮 + 添加出现行
  K3 上下文管理：说明文案 + 保留轮数/token 阈值输入 + 快速阈值/保存按钮
  K4 上下文管理-总结提示词：自动加载（继承值非空）+ 来源标注 + 重置入口
  K5 代码知识库（codegraph 段）：插件页 iframe 渲染 + 控件/状态入口齐备 + workdir 回填
  K6 代码知识库（vfts 段）：插件页 iframe 渲染 + 控件/引擎状态入口齐备
  L205 系统目录对话框（无法自动化）

迁移口径：
  - 2026-09-15：页签名 = zh-CN i18n（ProjectConfig.vue + locales/zh-CN/projectConfig.json）。
  - 「通用能力（提示词）」页签**已按 spec CFG-009 (D2) 摘除**（项目配置提示词仅余摘要提示词，
    见 36-配置配置 CFG-008/009）→ K4 改断言存活的「总结提示词自动加载 + 来源标注」（同强度）。
  - 2026-10-26（D-71）：主仓 CodegraphConfig.vue / VftsConfig.vue 下线，两页签**合并为
    「代码知识库」**，内容 = codegraph 插件自持页面（`/plugins/codegraph/webui/config.html`，
    同源 iframe）→ K5/K6 改为在该 iframe 内断言（控件/文案/workdir 回填 + 状态与索引入口）。
    **索引终效与状态真值**（indexedFiles / 文件清单 / 清除）由 `run_index_gate` G3/G4/G7/G8/G9 覆盖。

前置：chonkpilot.exe --test-port=2345 已启动（harness 复用或自起）。
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

# 8 个页签（zh-CN 标签，英文为旧名兼容备选）
TAB_FAMILIES = [
    ("安全 / Security", r"^(安全|Security)$"),
    ("上下文 / Context", r"^(上下文管理|上下文|Context)$"),
    # 2026-10-26（D-71）：CodeGraph + Vfts 两页签合并为「代码知识库」（插件自持 iframe 页面）
    ("代码知识库 / Code KB", r"^(代码知识库|Code KB)$"),
    ("文件历史 / History", r"^(文件历史|历史|History)$"),
    # 2026-10-16（UILAYOUT-1 批）新增两页签：文件树 / 群聊（位于「文件历史」之后、「日志」之前）
    ("文件树 / File Tree", r"^(文件树|File Tree|Filetree)$"),
    ("群聊 / Group Chat", r"^(群聊|Group Chat)$"),
    ("日志 / Log", r"^(日志|Log)$"),
    ("配置导入/导出 / Config IO", r".*(配置导入/导出|Config IO|Import).*"),
]


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


def iframe_scope(inner):
    """在「项目配置 → 代码知识库」插件 iframe 内执行 inner（变量 d = iframe document）。

    2026-10-26（D-71）：codegraph/vfts 两页合并为「代码知识库」，内容 = codegraph 插件自持页面
    `webui/config.html`（同源 → 可直接访问 contentDocument）。
    """
    return eval_js("""(() => {
      const ps = [...document.querySelectorAll('.project-config-panel')].filter(e => e.getBoundingClientRect().width > 0);
      const P = ps[ps.length - 1];
      if (!P) return 'no-panel';
      const f = P.querySelector('iframe');
      if (!f) return 'no-frame';
      const d = f.contentDocument;
      if (!d) return 'no-doc';
      %s
    })()""" % inner)


def open_code_kb_tab(max_wait=30):
    """打开「项目设置 → 代码知识库」并等 iframe 就绪（`load` 动作回填 workdir）；返回 workdir。"""
    open_project_cfg()
    if switch_tab(r"^(代码知识库|Code KB)$") != "ok":
        raise TestError("未找到「代码知识库」页签")
    deadline = time.time() + max_wait
    last = None
    while time.time() < deadline:
        try:
            last = iframe_scope("return (d.getElementById('workdir')||{}).value||'';")
        except Exception as e:  # noqa: BLE001  （iframe 重挂载期 eval 可能瞬断 → 继续轮询）
            last = "eval:%s" % e
        if isinstance(last, str) and last:
            time.sleep(0.5)
            return last
        time.sleep(0.5)
    raise TestError("「代码知识库」iframe 未就绪（workdir 未回填：%r）" % (last,))


def case_tabs():
    open_project_cfg()
    labels = tab_labels()
    if len(labels) != len(TAB_FAMILIES):
        raise TestError(f"项目配置应恰含 {len(TAB_FAMILIES)} 页签: {labels}")
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
    if "保留完整对话内容的最近轮数" not in txt or "简化区 Token 压缩阈值" not in txt:
        raise TestError(f"上下文管理页缺压缩说明/字段文案: {txt[:120]!r}")
    # 保留完整轮数 + token 阈值输入（2026-09-27：改手动保存 —— 改后点【保存】才落库）
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
    """K4（原「通用能力-提示词加载」迁移）：总结提示词自动加载 + 来源标注 + 重置入口。

    spec CFG-009：项目配置「通用能力（工具提示词）」UI 已摘除（D2），提示词页仅余摘要提示词
    （CFG-008）。**2026-09-26：只读展示已移除** → 内容改在**编辑弹框**（`TextEditDialog`，
    `.text-edit-dialog-body` 内 textarea）查看。故本用例断言：面板有「编辑」按钮 → 点开弹框
    内容非空（自动加载继承系统级/内置默认值）、来源标注可见、「重置」（取消项目覆盖）入口存在。
    """
    open_project_cfg()
    if switch_tab(r"^(上下文管理|上下文|Context)$") != "ok":
        raise TestError("未找到上下文管理页签")
    # 只读展示已移除：编辑入口 = 面板内「编辑」按钮（弹框内查看内容）
    r = panel_scope("""
      const hs=[...P.querySelectorAll('.prompt-editor-header')];
      const h=hs[0];
      const b=h?[...h.querySelectorAll('.b-btn')].find(x=>/编辑|Edit/i.test(x.textContent.trim())):null;
      if(!b)return 'no-btn';b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return 'ok';""")
    if r != "ok":
        raise TestError("总结提示词缺「编辑」按钮：%r" % r)
    if not wait_el(".text-edit-dialog-body"):
        raise TestError("总结提示词编辑弹框未打开")
    val = eval_js("""(() => {
      const ts=[...document.querySelectorAll('.text-edit-dialog-body textarea')];
      const t=ts[ts.length-1];
      return t ? (t.value || '') : ''; })()""") or ""
    if not val.strip():
        raise TestError("总结提示词内容为空（应自动加载继承值）")
    hints = panel_scope("return P ? [...P.querySelectorAll('.field-hint')].map(e => e.textContent.trim()) : []") or []
    joined = " | ".join(hints)
    if not any(k in joined for k in ("当前来源", "继承", "覆盖", "inherit", "override")):
        raise TestError(f"总结提示词缺来源标注: {joined[:160]!r}")
    btns = panel_scope("return P ? [...P.querySelectorAll('.b-btn')].map(b => b.textContent.trim()) : []") or []
    if not any(("重置" in b) or ("Reset" in b) for b in btns):
        raise TestError(f"总结提示词缺「重置」入口: {btns}")
    # 收尾：关闭弹框，避免外溢到下一用例
    eval_js("""(() => {
      const R=[...document.querySelectorAll('.dialog-shell')].pop();
      if(!R)return 'no';
      const b=R.querySelector('.dialog-btn-close');
      if(!b)return 'no-btn';
      b.dispatchEvent(new MouseEvent('click',{bubbles:true}));
      return 'ok'; })()""")
    time.sleep(0.4)


def case_codegraph():
    """K5 代码知识库（codegraph 段）：插件页 iframe 渲染 + 控件/状态入口齐备 + workdir 回填。

    2026-10-26（D-71）改口径：主仓 CodegraphConfig.vue 已下线，页签内容 = codegraph 插件
    `webui/config.html`（同源 iframe）。原 `.cg-state-text` 状态文案与主仓「重建索引/重试失败/
    清除索引/保存」按钮组随该页移除 → 等价强度断言改为「状态可观测 + 维护入口齐备」：
      ① 页内文案：符号图谱（codegraph）/ 索引扩展名 / 排除目录 / 叠加 .gitignore；
      ② 控件齐备：cgEnabled / cgExts / cgSkip / cgGitignore / 【保存并应用】/【清除索引】；
      ③ 状态与索引入口：【读取状态】（codegraph_status + vfts_status）/【立即索引】；
      ④ workdir 自动回填（插件实例解析成功）。
    （索引终效与状态真值由 `run_index_gate` G3/G4/G7/G8 覆盖，此处不重复。）
    """
    wd = open_code_kb_tab()
    if not re.match(r"^[A-Za-z]:[\\/]", wd):
        raise TestError("workdir 疑似未回填（应为绝对路径）：%r" % (wd,))
    txt = iframe_scope("return d.body.textContent||'';") or ""
    for want in ("符号图谱（codegraph）", "索引扩展名", "排除目录", "叠加 .gitignore"):
        if want not in txt:
            raise TestError("代码知识库 codegraph 段缺文案 %r（workdir=%r）" % (want, wd))
    ctrl = iframe_scope("""return JSON.stringify({
      enabled: !!d.getElementById('cgEnabled'), exts: !!d.getElementById('cgExts'),
      skip: !!d.getElementById('cgSkip'), gitignore: !!d.getElementById('cgGitignore'),
      save: ((d.getElementById('cgSave')||{}).textContent||'').trim(),
      clear: ((d.getElementById('cgClear')||{}).textContent||'').trim(),
      status: ((d.getElementById('loadStatus')||{}).textContent||'').trim(),
      index: ((d.getElementById('indexNow')||{}).textContent||'').trim()});""")
    if not isinstance(ctrl, dict):
        raise TestError("codegraph 控件探测失败：%r" % (ctrl,))
    miss = [k for k in ("enabled", "exts", "skip", "gitignore") if not ctrl.get(k)]
    if miss:
        raise TestError("codegraph 缺控件 %r：%r" % (miss, ctrl))
    for k, want in (("save", "保存并应用"), ("clear", "清除索引"),
                    ("status", "读取状态"), ("index", "立即索引")):
        if ctrl.get(k) != want:
            raise TestError("codegraph 按钮 %s 文案=%r，期望 %r" % (k, ctrl.get(k), want))


def case_vfts_status():
    """K6 代码知识库（vfts 段）：插件页 iframe 渲染 + 控件/引擎状态入口齐备。

    同 K5 口径（D-71 合并页签）。原主仓 `.vf-state-text` 状态文案 + `.vf-stat` 统计明细已随
    VftsConfig.vue 下线 → 等价断言“状态可观测”：`vfStatus`（引擎状态）与 `loadStatus`
    （读取状态，含 vfts_status）入口齐备；统计真值由 `run_index_gate` G9 覆盖。
    """
    wd = open_code_kb_tab()
    txt = iframe_scope("return d.body.textContent||'';") or ""
    for want in ("全文检索（vfts）", "启用 vfts", "索引 Office / PDF", "文档单文件上限（MB）"):
        if want not in txt:
            raise TestError("代码知识库 vfts 段缺文案 %r（workdir=%r）" % (want, wd))
    ctrl = iframe_scope("""return JSON.stringify({
      enabled: !!d.getElementById('vfEnabled'), exts: !!d.getElementById('vfExts'),
      skip: !!d.getElementById('vfSkip'), gitignore: !!d.getElementById('vfGitignore'),
      docs: !!d.getElementById('vfDocs'), docmax: !!d.getElementById('vfDocMaxMB'),
      save: ((d.getElementById('vfSave')||{}).textContent||'').trim(),
      status: ((d.getElementById('vfStatus')||{}).textContent||'').trim()});""")
    if not isinstance(ctrl, dict):
        raise TestError("vfts 控件探测失败：%r" % (ctrl,))
    miss = [k for k in ("enabled", "exts", "skip", "gitignore", "docs", "docmax") if not ctrl.get(k)]
    if miss:
        raise TestError("vfts 缺控件 %r：%r" % (miss, ctrl))
    if ctrl.get("save") != "保存并应用" or ctrl.get("status") != "引擎状态":
        raise TestError("vfts 按钮文案不符（save=%r status=%r）" % (ctrl.get("save"), ctrl.get("status")))


def main():
    c.wait_ready()
    c.console(clear=True)
    total = 0
    ok = 0
    total += 1; ok += run_case("K1 项目配置 8 页签（安全/上下文管理/代码知识库/文件历史/文件树/群聊/日志/配置导入导出）", case_tabs)
    total += 1; ok += run_case("K2 安全页签：添加信任目录", case_security)
    total += 1; ok += run_case("K3 上下文管理：输入 + 快速阈值 + 保存", case_context)
    total += 1; ok += run_case("K4 总结提示词自动加载 + 来源标注 + 重置", case_summary_prompt_load)
    total += 1; ok += run_case("K5 代码知识库（codegraph）：插件页文案/控件/状态入口 + workdir 回填", case_codegraph)
    total += 1; ok += run_case("K6 代码知识库（vfts）：插件页文案/控件/引擎状态入口", case_vfts_status)
    # 与其它套件统一口径：计数汇总 + 退出码（0=全过）——见 51-FP与测试映射 §1
    print("\n项目配置断言：%d/%d 通过, %d 失败" % (ok, total, total - ok), flush=True)
    print("RESULT:", ok == total)
    return 0 if ok == total else 1


if __name__ == "__main__":
    sys.exit(main())
