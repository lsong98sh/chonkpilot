# -*- coding: utf-8 -*-
"""左侧资源面板「项目记忆」模式（第 4 页签，2026-09-26）端到端验收。

用户口径：在左侧 filetree 顶部「知识库」之后增加「项目记忆」；点击按配置的项目记忆列出对应
文件列表，可点击查看。

覆盖（全部经**既有消息面**驱动，零新增主题）：
  MT1 页签与列表：段顺序 = 项目 · 知识库 · 项目记忆 · 会话；切「项目记忆」→ 列出**全部已启用**
      类别（8 项目级 + 1 user 级「用户偏好」），每行 = 类别名 + 级别 + 预估 tokens。
      数据来源 = `data-memory-list`（前端 `dataClient.list('memory')`）。
  MT2 项目级条目查看：点击「项目概要」行 → 既有 `file-open`（temporary）→ 预览区打开该 `.md`
      且内容非空（markdown 渲染，含预置哨兵）。
  MT3 用户偏好查看（user 级 / 工作目录之外）：点击「用户偏好」行 → 既有 `data-memory-read`
      + **只读弹框**（`.mem-view-text`，非空含哨兵）；不用 file-open（filesys 越界校验）。
  MT4 逐类开关（`memory.category.<类别名>`）：关闭「开发规范」→ 该行从清单消失（按配置列出）。
  MT5 记忆库总开关关闭（`memory.enabled=false`）：面板**空态提示**「记忆库未启用」+ 跳转
      「上下文管理」入口；清单空。**（关闭态不请求 data-memory-list 由前端单测
      statusbarMemoryEntry.test.js / memoryTree.test.js 守护，本套件断言空态表现。）**

隔离与还原：自起隔离实例（独立 work-dir / data-dir / HOME，不读写本机 ~/.chonkpilot）；
配置写入/还原走 `harness.suite_config_guard`（套件级 usr+prj 全量兜底），见 51 §6-8。

运行：python run_memory_tree.py
"""
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from chonk_client import TestError, run_case  # noqa: E402

import harness as _h  # noqa: E402

# 8 个预置项目级类别（persist memoryCategorySpecs）+ 唯一 user 级「用户偏好」
PROJ_CATS = ["项目概要", "共同库", "开发规范", "构建发布规则",
             "接口库", "测试规范", "典型参照", "用户决策"]
USER_PREF = "用户偏好"
GATED_CAT = "开发规范"            # MT4 关闭的类别
PROJ_SENT = "MT-PROJ-SENT-" + str(int(time.time()))   # 项目级条目哨兵
PREF_SENT = "MT-PREF-SENT-" + str(int(time.time()))   # 用户偏好哨兵


def plain(v):
    """解包 eval 结果的 JSON 字符串（测试通道可能双重编码）。"""
    for _ in range(3):
        if not isinstance(v, str):
            return v
        try:
            p = json.loads(v)
        except Exception:
            return v
        if p == v:
            return v
        v = p
    return v


def evi(tag, **kw):
    print("[EVIDENCE] " + json.dumps({"case": tag, **kw}, ensure_ascii=False), flush=True)


def main():
    g = _h.acquire_gui(_h.free_port(),
                       work_dir=_h.tmp_dir("memtree-ws-"),
                       data_dir=_h.tmp_dir("memtree-dd-"),
                       home=_h.tmp_home())
    c = g.client
    _h.suite_config_guard(c)   # 套件级快照-还原（51 §6-8）
    _h.ensure_locale(c)        # 语言确定性（段文案断言按 zh-CN）
    c.wait_ready(60)
    print("[setup] gui=%d work_dir=%s" % (g.port, g.work_dir), flush=True)

    pass_n = 0
    total_n = 0

    # ── 助手 ─────────────────────────────────────────────
    def js(expr, timeout=6000):
        return plain(c.eval(expr, timeout))

    def prj():
        r = c.req("data-prj-config-list", {})
        return (r.get("list") or {}) if isinstance(r, dict) else {}

    def prj_save(k, v):
        c.req("data-prj-config-save", {"data": {"key": k, "value": v}})

    def ensure_explorer(timeout=15):
        """filetree 区默认隐藏（MainLayout v-if）→ 点 toolbar 文件树切换钮使其挂载。"""
        end = time.time() + timeout
        while time.time() < end:
            if (int(js("document.querySelectorAll('.explorer-seg-btn').length") or 0)) > 0:
                return True
            js("(function(){const btns=Array.from(document.querySelectorAll('button.b-btn'));"
               "const b=btns.find(n=>{const tt=(n.getAttribute('title')||'')+(n.textContent.trim());"
               "return tt.indexOf('File Tree')>=0||tt.indexOf('文件树')>=0});"
               "if(!b)return false;b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()")
            time.sleep(0.6)
        return False

    def segs():
        return js("Array.from(document.querySelectorAll('.explorer-seg-btn')).map(n=>({t:n.textContent.trim(),a:n.classList.contains('active')}))") or []

    def switch_mode(want):
        # 前端内部事件（不经总线回显）→ 必须在页面内 emit（同 test_session_drawer / run_explore_kb）
        js("window.mq.emit('filetree-mode-select', %s); 'ok'" % json.dumps({"mode": want}))
        want_labels = {"project": ("项目", "Project"), "knowledge": ("知识库", "Knowledge"),
                       "memory": ("项目记忆", "Project Memory"), "sessions": ("会话", "Sessions")}[want]
        end = time.time() + 8
        while time.time() < end:
            act = [s["t"] for s in segs() if s["a"]]
            if any(x in want_labels for x in act):
                return True
            time.sleep(0.3)
        return False

    def mode_active():
        return [s["t"] for s in segs() if s["a"]]

    def mem_rows():
        return js("(function(){return [...document.querySelectorAll('.memory-pane .mem-row')].map(n=>({"
                  "c:n.getAttribute('data-category'),l:n.getAttribute('data-level'),"
                  "name:(n.querySelector('.mem-row-name')||{}).textContent||'',"
                  "lv:(n.querySelector('.mem-row-level')||{}).textContent||'',"
                  "tk:(n.querySelector('.mem-row-tokens')||{}).textContent||''}))})()") or []

    def vis_count(sel):
        return int(js("([...document.querySelectorAll(%s)].filter(e=>e.getBoundingClientRect().width>0)).length"
                      % json.dumps(sel)) or 0)

    def poll(fn, timeout=12, interval=0.3):
        end = time.time() + timeout
        while time.time() < end:
            if fn():
                return True
            time.sleep(interval)
        return False

    def click_mem_row(cat):
        return js("(function(){const el=[...document.querySelectorAll('.memory-pane .mem-row')]"
                  ".find(n=>n.getAttribute('data-category')===%s);if(!el)return false;"
                  "el.dispatchEvent(new MouseEvent('click',{bubbles:true}));return true})()" % json.dumps(cat))

    def empty_title():
        return js("(function(){const e=document.querySelector('.memory-pane .mem-empty-title');"
                  "return e?(e.textContent||'').trim():''})()") or ""

    def close_dialog():
        js("(function(){const b=document.querySelector('.dialog-shell .dialog-btn-close');"
           "if(b)b.dispatchEvent(new MouseEvent('click',{bubbles:true}));return !!b})()")

    # ── 夹具：启用记忆库 + 预置条目内容（哨兵）────────────────
    prj_save("memory.enabled", "true")
    # 经既有 data-memory-save 写两类哨兵（项目级 / user 级），并使清单/预置文件落盘
    c.req("data-memory-save", {"data": {"category": "项目概要", "content": PROJ_SENT}})
    c.req("data-memory-save", {"data": {"category": USER_PREF, "content": PREF_SENT}})
    time.sleep(1.0)

    # ── MT1 页签与列表 ──────────────────────────────────
    def mt1():
        if not ensure_explorer():
            raise TestError("filetree 区未能挂载（toolbar 文件树按钮点击无效）")
        labels = [s["t"] for s in segs()]
        want = ["项目", "知识库", "项目记忆", "会话"]
        if labels[:4] != want and labels[:4] != ["Project", "Knowledge", "Project Memory", "Sessions"]:
            raise TestError("左侧页签顺序异常（应 项目·知识库·项目记忆·会话）：%r" % labels)
        if not switch_mode("memory"):
            raise TestError("切「项目记忆」失败 active=%r" % mode_active())
        if not poll(lambda: vis_count(".memory-pane") > 0, 8):
            raise TestError("项目记忆面板未显示")
        if not poll(lambda: len(mem_rows()) >= 9, 12):
            raise TestError("项目记忆清单未列出全部启用类别（应 8 项目级 + 用户偏好）：%r" % mem_rows())
        rows = mem_rows()
        cats = {r["c"] for r in rows}
        for cat in PROJ_CATS + [USER_PREF]:
            if cat not in cats:
                raise TestError("清单缺类别 %s：%r" % (cat, sorted(cats)))
        # 行结构：类别名 + 级别 + 预估 tokens
        for r in rows:
            if not r["name"] or not r["lv"] or r["tk"] == "":
                raise TestError("行未含 类别名/级别/tokens：%r" % r)
        levels = {r["c"]: r["l"] for r in rows}
        if levels.get(USER_PREF) != "user" or levels.get("项目概要") != "project":
            raise TestError("级别标注异常：%r" % levels)
        evi("MT1", segs=labels, rows=len(rows), user_level=levels.get(USER_PREF))

    # ── MT2 项目级条目 → 预览区打开（内容非空）──────────────
    def mt2():
        if not switch_mode("memory"):
            raise TestError("MT2 切项目记忆失败")
        if not click_mem_row("项目概要"):
            raise TestError("点击「项目概要」行失败")
        ok = poll(lambda: vis_count(".markdown-preview") > 0, 12)
        if not ok:
            raise TestError("项目级条目未在预览区打开（无可见 .markdown-preview）")
        txt = js("(function(){const e=[...document.querySelectorAll('.markdown-preview')]"
                 ".find(n=>n.getBoundingClientRect().width>0);return e?(e.textContent||''):''})()") or ""
        if len(txt.strip()) == 0:
            raise TestError("预览区内容为空")
        if PROJ_SENT not in txt:
            raise TestError("预览内容未含预置哨兵（读到的是其它文件？）：%r" % txt[:160])
        evi("MT2", preview_len=len(txt), has_sentinel=True)

    # ── MT3 用户偏好（user 级）→ 只读弹框（内容非空）─────────
    def mt3():
        if not switch_mode("memory"):
            raise TestError("MT3 切项目记忆失败")
        if not click_mem_row(USER_PREF):
            raise TestError("点击「用户偏好」行失败")
        ok = poll(lambda: vis_count(".mem-view-text") > 0, 12)
        if not ok:
            raise TestError("「用户偏好」未打开只读弹框（无可见 .mem-view-text）")
        txt = js("(function(){const e=[...document.querySelectorAll('.mem-view-text')]"
                 ".find(n=>n.getBoundingClientRect().width>0);return e?(e.textContent||''):''})()") or ""
        if len(txt.strip()) == 0 or PREF_SENT not in txt:
            raise TestError("只读弹框内容为空或未含预置哨兵：%r" % txt[:160])
        # 只读：无编辑/保存入口
        btns = js("Array.from(document.querySelectorAll('.dialog-shell .text-edit-footer button')).map(n=>n.textContent.trim())") or []
        if btns:
            raise TestError("只读弹框不应含编辑/保存入口：%r" % btns)
        evi("MT3", dialog_len=len(txt), has_sentinel=True)
        close_dialog()
        poll(lambda: vis_count(".mem-view-text") == 0, 6)

    # ── MT4 逐类开关：关闭「开发规范」→ 该行消失 ──────────────
    def mt4():
        if not switch_mode("memory"):
            raise TestError("MT4 切项目记忆失败")
        if not poll(lambda: len(mem_rows()) >= 9, 8):
            raise TestError("MT4 前置：清单未就绪：%r" % [r["c"] for r in mem_rows()])
        prj_save("memory.category." + GATED_CAT, "false")
        ok = poll(lambda: GATED_CAT not in {r["c"] for r in mem_rows()}, 10)
        if not ok:
            raise TestError("关闭 %s 后该行仍在清单（未按配置列出）：%r" % (GATED_CAT, [r["c"] for r in mem_rows()]))
        # 对照：其它类别仍在
        cats = {r["c"] for r in mem_rows()}
        if "项目概要" not in cats or USER_PREF not in cats:
            raise TestError("关闭单类后其它类别不应消失：%r" % sorted(cats))
        evi("MT4", gated=GATED_CAT, rows=len(cats))

    # ── MT5 总开关关闭 → 空态提示 + 清单空 ────────────────────
    def mt5():
        if not switch_mode("memory"):
            raise TestError("MT5 切项目记忆失败")
        prj_save("memory.enabled", "false")
        ok = poll(lambda: len(mem_rows()) == 0, 10)
        if not ok:
            raise TestError("关闭记忆库后清单未清空：%r" % [r["c"] for r in mem_rows()])
        ok = poll(lambda: ("记忆库未启用" in empty_title()) or ("not enabled" in empty_title().lower()), 10)
        if not ok:
            raise TestError("关闭记忆库后未显示空态提示：%r" % empty_title())
        # 入口仍在 + 跳转「上下文管理」
        if not vis_count(".memory-pane"):
            raise TestError("关闭记忆库后项目记忆面板仍应可进入（入口保留）")
        jump = js("(function(){const e=[...document.querySelectorAll('.memory-pane button.b-btn')]"
                  ".find(n=>n.getBoundingClientRect().width>0);return e?(e.textContent||'').trim():''})()") or ""
        if not jump:
            raise TestError("关闭态空态缺少跳转入口按钮")
        evi("MT5", empty=empty_title(), jump=jump, rows=0)

    for name, fn in [
        ("MT1 页签顺序 + 按配置列出启用类别", mt1),
        ("MT2 项目级条目 → 预览区打开（内容非空）", mt2),
        ("MT3 用户偏好(user 级) → 只读弹框（内容非空）", mt3),
        ("MT4 逐类开关 → 行按配置增减", mt4),
        ("MT5 记忆库总开关关闭 → 空态提示 + 跳转入口", mt5),
    ]:
        total_n += 1
        pass_n += run_case(name, fn)

    errs = c.console()
    for e in (errs.get("entries") or []):
        if e.get("level") == "error":
            print("  [CONSOLE-ERROR] %s" % e.get("text"))

    print("\n项目记忆面板：%d/%d 通过, %d 失败" % (pass_n, total_n, total_n - pass_n), flush=True)
    print("RESULT: %s" % (pass_n == total_n), flush=True)
    return 0 if pass_n == total_n else 1


if __name__ == "__main__":
    sys.exit(main())
