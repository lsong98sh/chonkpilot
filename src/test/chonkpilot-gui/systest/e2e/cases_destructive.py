# -*- coding: utf-8 -*-
"""**破坏性测试点**（stage=`destructive`，默认不纳入全量；永远最后执行）。

运行方式：
* `python run_e2e.py --with-destructive`            全量 + 破坏性（同一流程，破坏性在最后）
* `python run_e2e_destructive.py`                   只跑破坏性（独立工作区/独立 HOME）
* `run_e2e_all.bat`                                 先跑常规全量，再跑独立破坏性批

注意：破坏性用例可清空 usr/prj 配置，**不得**与常规测试点混跑在前；本文件只放此类用例。
"""

import json
import os
import time

from .framework import case


def _visible_dialogs(env):
    return int(env.js("(()=>[...document.querySelectorAll('.dialog-shell')]"
                      ".filter(d=>{const r=d.getBoundingClientRect();"
                      "return r.width>0&&r.height>0}).length)()") or 0)


def _top_dialog_text(env):
    return str(env.js("(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                      ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                      "const d=ds[ds.length-1];"
                      "return d?(d.querySelector('.dialog-body')?.textContent||''):''})()") or "")


def _top_dialog_click(env, idx):
    return env.js("(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                  ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                  "const d=ds[ds.length-1];if(!d)return -1;"
                  "const bs=d.querySelector('.dialog-body')?.querySelectorAll('button')||[];"
                  "if(bs[%d])bs[%d].click();return bs.length})()" % (idx, idx))


@case("D-CFG-01", "破坏性", "恢复出厂：二次确认 + 清空前自动备份（usr 清空 / 项目级保留）",
      stage="destructive", order=900, fp="33 配置导入导出 C22 · 51 §6-8（破坏性）",
      desc="配置 IO 页 →「恢复出厂」→ 两次确认（文案不同）→ `<data_root>/backup/` 新增 JSON "
           "快照（app=chonkpilot + data 字典）→ 结果提示含备份；usr 哨兵被清、prj 项目级配置保留。"
           "【破坏性】会清空用户级全局配置，默认全量不跑。")
def d_cfg_01_factory_reset(env):
    mark = "E2E-DESTR-%s" % env.run_id
    env.req("data-user-config-save", {"data": {"retryCount": 7}})
    env.req("data-prj-config-save", {"data": {"key": "e2e.destructive", "value": mark}})
    env.req("gui.ui.save", {"ui": {"theme": "dark"}})
    env.wait(lambda: env.usr().get("retryCount") in (7, "7")
             and env.prj().get("e2e.destructive") == mark,
             "破坏性前置（usr retryCount / prj 哨兵）", timeout=20)

    env.js("window.mq.emit('project-config-open'); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.project-config-panel')") is True else None,
        "项目配置面板打开", timeout=25)
    env.js("document.querySelectorAll('.project-config-panel .b-tabs-item')[7]?.click(); 'ok'")
    env.wait(lambda: True if env.js(
        "!!document.querySelector('.config-io-root .io-reset')") is True else None,
        "配置 IO 页就绪", timeout=20)

    backup_dir = os.path.join(env.data_root, "backup")
    before = {}
    if os.path.isdir(backup_dir):
        for n in os.listdir(backup_dir):
            try:
                before[n] = os.path.getmtime(os.path.join(backup_dir, n))
            except OSError:
                pass

    env.js("(()=>{const b=document.querySelector('.config-io-root .io-reset button');"
           "if(b)b.click();return !!b})()")
    env.wait(lambda: True if _visible_dialogs(env) >= 1 else None,
             "恢复出厂第一次确认", timeout=15)
    first = _top_dialog_text(env)
    env.check("第一次确认框有文案", bool(first.strip()), first[:80])
    _top_dialog_click(env, 1)
    time.sleep(0.7)

    second = ""
    end = time.time() + 15
    while time.time() < end:
        second = _top_dialog_text(env)
        if second.strip() and second != first:
            break
        time.sleep(0.3)
    env.check("第二次确认框出现且文案不同", bool(second.strip()) and second != first,
              "first=%r second=%r" % (first[:50], second[:50]))
    _top_dialog_click(env, 1)

    # 新增备份快照（按 mtime 找最新；同时接受同名覆盖 → 用 mtime 比较）
    def newest_new():
        if not os.path.isdir(backup_dir):
            return ""
        rows = []
        for n in os.listdir(backup_dir):
            p = os.path.join(backup_dir, n)
            try:
                mt = os.path.getmtime(p)
            except OSError:
                continue
            if n not in before or mt > before[n] + 0.001:
                rows.append((mt, p))
        return max(rows)[1] if rows else ""

    path = env.wait(lambda: newest_new() or None, "备份快照文件生成", timeout=30)
    env.check("备份文件存在", bool(path) and os.path.isfile(path), path)
    blob = {}
    try:
        with open(path, "r", encoding="utf-8") as f:
            blob = json.load(f)
    except Exception as e:
        env.note("备份 JSON 解析失败", str(e))
    env.eq("备份 app = chonkpilot", blob.get("app"), "chonkpilot")
    env.check("备份 data 为字典", isinstance(blob.get("data"), dict),
              str(type(blob.get("data"))))

    def _res_text():
        return str(env.js("document.querySelector('.config-io-root .io-reset-result')"
                          "?.textContent || ''") or "")

    res = env.wait(lambda: _res_text() if ("备份" in _res_text()
                                           or "backed up" in _res_text().lower()) else None,
                   "恢复厂结果提示含备份", timeout=30)
    env.check("结果提示含备份路径/文案", bool(res), str(res)[:120])

    # usr 被清（哨兵不在 / theme 回默认）；项目级配置按范围说明保留
    def usr_reset():
        u = env.usr()
        return u if str(u.get("retryCount")) != "7" and u.get("theme") != "dark" else None
    env.wait(usr_reset, "usr 配置已清空（retryCount/theme 复原）", timeout=30)
    env.eq("prj 项目级配置保留（范围=usr）", env.prj().get("e2e.destructive"), mark)

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"]
    env.check("恢复出厂无前端错误", len(errs) == 0, errs[:3])