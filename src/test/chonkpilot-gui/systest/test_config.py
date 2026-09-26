"""回归测试：config/preview 组（51-FP与测试映射「preview 项目配置 / 用户配置 / 场景配置」）。

覆盖：
- 项目配置（filetree 配置图标 → ide.db → ProjectConfig 6 页签 + security 页签交互）
- 用户配置（config-open → settings tab → ConfigDialog 4 页签 + 保存按钮）
- 场景配置（scenario-open → scenario tab）
（知识库 kb-open 入口与 .kb-list-container 无前端实现，2026-09-04 移除旧 C7 断言）
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from drive import GUIClient, Checker  # noqa: E402

from harness import ensure_locale, free_port, snapshot_config, restore_config  # noqa: E402  动态端口；语言确定性（C20 按中文文案断言）；套件级配置快照-还原

PORT = free_port()
DATA_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "_cfg_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


def main():
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    open(os.path.join(DATA_DIR, "chonkpilot.db"), "w").close()

    gui = GUIClient(port=PORT, data_dir=DATA_DIR)
    c = Checker()
    # 套件级配置快照-还原（51 §6-8）：prj 走独立 --data-dir，usr 主库不受其隔离 → 跑前快照、finally 还原。
    snap = None
    try:
        gui.start()
        snap = snapshot_config(gui)
        # 语言确定性（51 §6-8，与 test_task/test_chat/test_richtext 同法）：本套件 C20 按**中文文案**
        # 断言（`'API Key' in io-secret-warn`；en-US 文案为 "...such as API keys ..."，大小写不同）→
        # 不依赖他套件/机器 usr 库遗留的 `ui.locale`（实测 2026-09-23：usr locale=en-US 时 C20 必红）。
        ensure_locale(gui, "zh-CN")
        for _ in range(30):
            if J(gui, "document.querySelector('.panel-inner')"):
                break
            time.sleep(0.5)
        time.sleep(0.5)

        # ── 项目配置（ide.db → ProjectConfig） ──
        # 页签集 = security/context/codegraph/vfts/history/log + configIO 共 7 个（ProjectConfig.vue tabsConfig；
        # 2026-09-19 新增「日志」页签；2026-09-20 批 3 · ⑯ 末位新增「配置导入/导出」页签 → 6 → 7）
        J(gui, "window.mq.emit('project-config-open'); 'ok'")
        time.sleep(1.0)
        pc = J(gui, "!!document.querySelector('.project-config-panel')")
        tabs = J(gui, "document.querySelectorAll('.project-config-panel .b-tabs-item').length")
        c.check("C1 项目配置页打开 + 7 页签", bool(pc) and int(tabs) == 7, f"pc={pc} tabs={tabs}")

        # C1b 日志页签（索引 5）→ 日志级别选择 + 日志目录入口（有路径 或 明确提示）
        J(gui, "document.querySelectorAll('.project-config-panel .b-tabs-item')[5]?.click(); 'ok'")
        time.sleep(0.9)
        logPage = J(gui, "!!document.querySelector('.project-config-panel .log-root')")
        logSel = J(gui, "document.querySelectorAll('.project-config-panel .log-root .b-select').length")
        logTxt = J(gui, "document.querySelector('.project-config-panel .log-root')?.textContent || ''")
        c.check("C1b 日志页签（级别选择 + 日志目录入口）",
                bool(logPage) and int(logSel) >= 1 and ('日志目录' in logTxt or 'Log directory' in logTxt),
                f"page={logPage} sel={logSel}")

        # C2 security 页签（默认首屏，索引 0）→ 信任目录表 + 空态
        J(gui, "document.querySelectorAll('.project-config-panel .b-tabs-item')[0]?.click(); 'ok'")
        time.sleep(0.8)
        secTab = J(gui, "document.querySelectorAll('.project-config-panel .b-tabs-item')[0]?.classList.contains('active')")
        tableHead = J(gui, "document.querySelectorAll('.project-config-panel .b-table-inline th').length")
        bodyTxt = J(gui, "document.body.textContent")
        emptyTxt = '暂无信任目录' in bodyTxt or 'No trust' in bodyTxt or 'empty' in bodyTxt.lower()
        c.check("C2 security 页签 + 表头 + 空态", bool(secTab) and int(tableHead) >= 3, f"sec={secTab} th={tableHead} empty={emptyTxt}")

        # C3 security 添加 → 新增一行（可编辑 dir 输入框）
        J(gui, "(()=>{const b=document.querySelector('.project-config-panel .tab-toolbar button');if(b)b.click();return !!b})()")
        time.sleep(0.8)
        rows = J(gui, "document.querySelectorAll('.project-config-panel .b-table-inline tbody tr').length")
        inputs = J(gui, "document.querySelectorAll('.project-config-panel .security-dir-row .b-input').length")
        c.check("C3 security 添加按钮 → 新增行 + 输入框", int(rows) >= 1 and int(inputs) >= 1, f"rows={rows} inputs={inputs}")

        # C3b 输入目录 → 变更保存（无 JS 错误）
        gui.console(clear=True)
        J(gui, """(()=>{const el=document.querySelector('.project-config-panel .security-dir-row .b-input');if(!el)return false;const s=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;s.call(el,'D:\\\\trust');el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return true})()""")
        time.sleep(0.8)
        errs = [e.get('text') for e in (gui.console() or {}).get('entries', []) if e.get('level') == 'error']
        c.check("C3b security 目录输入即保存（无错误）", len(errs) == 0, repr(errs[:2]))

        # ── 用户配置（config-open → settings-llm 页签） ──
        gui.console(clear=True)
        J(gui, "window.mq.emit('config-open'); 'ok'")
        time.sleep(1.2)
        cfg = J(gui, "!!document.querySelector('.code-view .special-tab.settings-page')")
        cfgHead = J(gui, "document.querySelectorAll('.settings-page .b-table thead th').length")
        addBtn = J(gui, "document.querySelectorAll('.settings-page .config-toolbar-actions .b-btn').length")
        c.check("C5 用户配置页打开 + 表格 + 添加按钮", bool(cfg) and int(cfgHead) >= 4 and int(addBtn) >= 1,
                f"cfg={cfg} th={cfgHead} add={addBtn}")

        # C5b 交互：点「添加 LLM」→ 打开编辑对话框（不崩），取消关闭
        #（原「env 页签切换」口径失效：用户配置已由 ConfigDialog 4 页签改为逐页设置页，
        #  SettingsLLMPage 无页签；保存 = 列表即落盘 CFG-015-S01 + 编辑对话框）
        J(gui, "document.querySelector('.settings-page .config-toolbar-actions button')?.click(); 'ok'")
        time.sleep(1.0)
        editor = J(gui, "!!document.querySelector('.dialog-shell') && !!document.querySelector('.dialog-shell .form-layout')")
        errs = [e.get('text') for e in (gui.console() or {}).get('entries', []) if e.get('level') == 'error']
        c.check("C5b 添加 LLM → 编辑对话框打开（不崩）", bool(editor) and len(errs) == 0,
                f"editor={editor} err={repr(errs[:2])}")
        J(gui, """(()=>{const bs=Array.from(document.querySelectorAll('.dialog-shell .dialog-footer button'));if(bs[0])bs[0].click();return bs.length})()""")
        time.sleep(0.6)

        # ── 场景配置（scenario-open） ──
        gui.console(clear=True)
        J(gui, "window.mq.emit('scenario-open'); 'ok'")
        time.sleep(1.0)
        scen = J(gui, "document.querySelectorAll('.dialog-content').length")
        scenBody = J(gui, "document.body.textContent")
        hasScenario = '场景' in scenBody or 'Scenario' in scenBody
        errs = [e.get('text') for e in (gui.console() or {}).get('entries', []) if e.get('level') == 'error']
        c.check("C6 场景配置 tab 打开", int(scen) >= 1 and bool(hasScenario) and len(errs) == 0, f"scen={scen} err={repr(errs[:2])}")

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error'
                and 'method not implemented' not in (e.get('text') or '')
                and 'SetActiveSessionID' not in (e.get('text') or '')]
        c.check("无前端错误（config 组）", len(errs) == 0, repr(errs[:3]))

        # ── 批 3 · ⑯ 配置 导入/导出 + 恢复出厂（页签索引 6；usr 全局配置） ──
        # C20 页签与三区：导出（密钥警示 + 排除密钥默认关）/ 导入（粘贴框）/ 恢复厂（范围文案）
        J(gui, "document.querySelectorAll('.project-config-panel .b-tabs-item')[6]?.click(); 'ok'")
        time.sleep(1.0)
        io_root = J(gui, "!!document.querySelector('.config-io-root')")
        io_export = J(gui, "!!document.querySelector('.config-io-root .io-export')")
        io_import = J(gui, "!!document.querySelector('.config-io-root .io-import')")
        io_reset = J(gui, "!!document.querySelector('.config-io-root .io-reset')")
        warn_txt = J(gui, "document.querySelector('.config-io-root .io-secret-warn')?.textContent || ''")
        excl_on = J(gui, "!!document.querySelector('.config-io-root .io-exclude-switch .b-switch.is-checked')")
        paste_box = J(gui, "!!document.querySelector('.config-io-root .io-import textarea')")
        scope_txt = J(gui, "document.querySelector('.config-io-root .io-reset-scope')?.textContent || ''")
        c.check("C20 配置导入/导出页签（导出/导入/恢复厂三区 + 密钥警示 + 排除密钥默认关）",
                bool(io_root) and bool(io_export) and bool(io_import) and bool(io_reset)
                and 'API Key' in warn_txt and (not excl_on) and bool(paste_box)
                and ('不含项目级配置' in scope_txt or 'Project-level' in scope_txt),
                f"root={io_root} exp={io_export} imp={io_import} rst={io_reset} warn={warn_txt[:20]!r} "
                f"exclOn={excl_on} paste={paste_box}")

        # 批 3（⑯）新增 checks 的对话框定位：页面可能同时存在多个 `.dialog-shell`
        # （如 C5b 打开过的 LLM 编辑框未关）→ 必须取**最上层且可见**的那个，否则会读到陈旧对话框。
        # 判定用 getBoundingClientRect（对 position:fixed 也有效，不能用 offsetParent）。
        def _top_dialog_text():
            return J(gui, "(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                          ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                          "const d=ds[ds.length-1];return d?(d.querySelector('.dialog-body')?.textContent||''):''})()")

        def _top_dialog_click(idx):
            return J(gui, "(()=>{const ds=[...document.querySelectorAll('.dialog-shell')]"
                          ".filter(d=>{const r=d.getBoundingClientRect();return r.width>0&&r.height>0});"
                          "const d=ds[ds.length-1];if(!d)return -1;"
                          "const bs=d.querySelectorAll('.dialog-body button');"
                          "if(bs[%d])bs[%d].click();return bs.length})()" % (idx, idx))

        # C21 非法 JSON 导入：人话提示 + 原始详情可展开（不静默、不落库）
        J(gui, "(()=>{const el=document.querySelector('.config-io-root .io-import textarea');"
               "if(!el)return false;const s=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;"
               "s.call(el,'{bad json');el.dispatchEvent(new Event('input',{bubbles:true}));return true})()")
        time.sleep(0.3)
        J(gui, "(()=>{const bs=[...document.querySelectorAll('.config-io-root .io-import button')];"
               "const b=bs.find(x=>/解析并导入|Parse and import/.test(x.textContent||''));if(b)b.click();return !!b})()")
        time.sleep(0.9)
        imp_err = J(gui, "document.querySelector('.config-io-root .io-import-error')?.textContent || ''")
        detail = J(gui, "!!document.querySelector('.config-io-root .io-import-error details pre')")
        c.check("C21 导入非法 JSON → 人话提示 + 可展开原始详情",
                ('JSON' in imp_err) and bool(detail), f"err={imp_err[:60]!r} detail={detail}")

        # C21b 合法 JSON（含未知键）→ 确认弹窗写明「键覆盖」影响范围；取消 → 不落盘
        J(gui, "(()=>{const el=document.querySelector('.config-io-root .io-import textarea');"
               "if(!el)return false;const s=Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set;"
               "s.call(el,JSON.stringify({retryCount:3,bogus_key:1}));el.dispatchEvent(new Event('input',{bubbles:true}));return true})()")
        time.sleep(0.3)
        J(gui, "(()=>{const bs=[...document.querySelectorAll('.config-io-root .io-import button')];"
               "const b=bs.find(x=>/解析并导入|Parse and import/.test(x.textContent||''));if(b)b.click();return !!b})()")
        time.sleep(0.9)
        dlg_txt = _top_dialog_text()
        c.check("C21b 导入确认弹窗写明影响范围（键覆盖 + 数量/键名）",
                ('键覆盖' in dlg_txt or 'overwritten' in dlg_txt) and 'retryCount' in dlg_txt,
                f"dlg={dlg_txt[:80]!r}")
        _top_dialog_click(0)  # 取消
        time.sleep(0.6)
        no_result = J(gui, "!!document.querySelector('.config-io-root .io-import-result')")
        c.check("C21b 取消导入 → 不写入、无结果提示", not no_result, f"result={no_result}")

        # C22 恢复出厂：二次确认 + 清空前自动备份（<data-dir>/backup/ 落快照文件）
        backup_dir = os.path.join(DATA_DIR, "backup")
        before_files = set(os.listdir(backup_dir)) if os.path.isdir(backup_dir) else set()
        J(gui, "(()=>{const b=document.querySelector('.config-io-root .io-reset button');if(b)b.click();return !!b})()")
        time.sleep(0.8)
        first_txt = _top_dialog_text()
        _top_dialog_click(1)  # 第一次确认
        time.sleep(0.8)
        second = _top_dialog_text()
        _top_dialog_click(1)  # 第二次确认
        time.sleep(2.5)
        after_files = sorted(set(os.listdir(backup_dir)) - before_files) if os.path.isdir(backup_dir) else []
        env_ok = False
        if after_files:
            with open(os.path.join(backup_dir, after_files[-1]), "r", encoding="utf-8") as fh:
                blob = json.load(fh)
            env_ok = blob.get("app") == "chonkpilot" and isinstance(blob.get("data"), dict)
        reset_txt = J(gui, "document.querySelector('.config-io-root .io-reset-result')?.textContent || ''")
        c.check("C22 恢复出厂二次确认 + 清空前自动备份（backup/ 快照 + 提示路径）",
                bool(first_txt) and bool(second) and first_txt != second and len(after_files) >= 1
                and env_ok and ('备份' in reset_txt or 'backed up' in reset_txt.lower()),
                f"cnf1={first_txt[:24]!r} cnf2={second[:24]!r} files={after_files[-1:]!r} env={env_ok}")
    finally:
        if snap is not None:
            restore_config(gui, snap)  # 还原 usr + prj；须在 gui.stop() 前
        gui.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()
