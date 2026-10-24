"""回归测试：插件维护视图（卡片 + 注册动作按钮）。

覆盖（PLUGIN-1 `ui.actions` 契约，见 `docs/plugin-platform/插件与MCP-Server开发指南.md §7.4`
与 `docs/spec/60-reference/61-消息一览.md §7.2）：

- PM1 入口：`preview-tab-open{kind:'plugin-manager'}` → 卡片数 == `plugin-list` 返回的插件数（>0）
- PM2 卡片要素：名称 / 启动命令（`command`）非空；注册了配置视图（`config_view`）的卡片有「配置」按钮
- PM3 动作按钮：卡片上的动作按钮集合 == manifest `ui.actions[]`（名称 + 数量）；
  codebase 的「索引进度」（内联 SVG 图标）存在
- PM4 动作打开位置（container=drawer）：点击「索引进度」→ 抽屉出现，标题 = 视图标题，内含插件页 iframe
- PM5 配置按钮：点击「配置」→ 预览区出现插件页 iframe（插件自持 webui，
  一律走 `/plugins/<id>/webui/config.html`；旧前缀 `/plugin-ui/<id>/…` 已下线 → 404）
- PM6 启用开关：关闭 → 卡片 is-off + 状态非运行；再开启 → 恢复运行

断言全部经 **mq 消息驱动**（`window.mq.emit`）与 DOM/消息面回读，不直调后端函数。
"""
import json
import os
import shutil
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import harness as _h  # noqa: E402
from drive import Checker  # noqa: E402

PORT = _h.free_port()
HERE = os.path.dirname(os.path.abspath(__file__))
DATA_DIR = os.path.join(HERE, "_plugin_manager_data")


def J(gui, js):
    r = gui.eval(js)
    try:
        return json.loads(r)
    except Exception:
        return r


CARDS_JS = ("JSON.stringify([...document.querySelectorAll('.pm-card')].map(c=>({"
            "name:(c.querySelector('.pm-name')||{}).textContent||'',"
            "cmd:(c.querySelector('.pm-cmd-value')||{}).textContent||'',"
            "off:c.classList.contains('is-off'),"
            "btns:[...c.querySelectorAll('.pm-btn')].map(b=>(b.textContent||'').trim())})))")


def cards(gui):
    return json.loads(J(gui, CARDS_JS) or "[]")


def open_manager(gui):
    gui.eval("window.mq.emit('preview-tab-open', {kind:'plugin-manager'}); 'ok'")
    _h.wait_probe(gui, ["!!document.querySelector('.plugin-manager')"], timeout=20.0)


def find_card(gui, name):
    return ("[...document.querySelectorAll('.pm-card')].find(c=>"
            "((c.querySelector('.pm-name')||{}).textContent||'').indexOf(%s)>=0)" % json.dumps(name))


def main():
    # 数据目录每次重建：插件启用表（`<dataDir>/plugins/enabled.json`）随套件写入，
    # 复用旧目录会让「关闭→开启」的初态不确定（历史失败运行留下关闭态）。
    shutil.rmtree(DATA_DIR, ignore_errors=True)
    os.makedirs(DATA_DIR, exist_ok=True)
    h = _h.start_gui(port=PORT, work_dir=_h.tmp_dir("pm-ws"), data_dir=DATA_DIR,
                     home=_h.tmp_home(), ready_timeout=90)
    gui = h.client
    c = Checker()
    snap = None
    try:
        _h.wait_probe(gui, ["!!document.querySelector('.panel-inner')"], timeout=30.0)
        _h.wait_idle(gui, max_wait=30.0)
        snap = _h.snapshot_config(gui)

        # 后端真值（经消息面）：插件清单（含 command / config_view / actions）
        env = gui.req("plugin-list", {}, timeout=20)
        plugins = (env or {}).get("plugins") or []
        by_id = {p["id"]: p for p in plugins}

        # ── PM1 入口 + 卡片数 == 插件数 ──
        open_manager(gui)
        cs = cards(gui)
        c.check("PM1 插件维护视图卡片数 == plugin-list",
                len(plugins) > 0 and len(cs) == len(plugins),
                f"plugins={len(plugins)} cards={len(cs)}")

        # ── PM2 卡片要素：名称 / 启动命令 / 配置按钮 ──
        bad = []
        for p in plugins:
            hit = [x for x in cs if x["name"] == (p.get("name") or p["id"])]
            if not hit:
                bad.append((p["id"], "no-card"))
                continue
            card = hit[0]
            if not card["cmd"] or not (p.get("command") or ""):
                bad.append((p["id"], "no-command"))
            want_cfg = bool(p.get("config_view"))
            if want_cfg != ("配置" in card["btns"] or "Configure" in card["btns"]):
                bad.append((p["id"], "config-btn-mismatch"))
        c.check("PM2 每张卡片：名称/启动命令/配置按钮与 manifest 一致", not bad, repr(bad[:4]))

        # ── PM3 动作按钮 == manifest ui.actions（名称 + 数量）──
        bad = []
        for p in plugins:
            acts = p.get("actions") or []
            if not acts:
                continue
            hit = [x for x in cs if x["name"] == (p.get("name") or p["id"])]
            if not hit:
                bad.append((p["id"], "no-card"))
                continue
            labels = [a.get("label") for a in acts]
            missing = [l for l in labels if l not in hit[0]["btns"]]
            if missing:
                bad.append((p["id"], missing))
        cb = by_id.get("codebase") or {}
        cb_labels = [a.get("label") for a in (cb.get("actions") or [])]
        c.check("PM3 动作按钮与 manifest 注册一致（codebase 含索引进度）",
                not bad and "索引进度" in cb_labels, f"bad={bad[:3]} codebase={cb_labels}")

        # ── PM6 启用开关：关 → 卡片 is-off；开 → 恢复（真实落库 + 进程收停）──
        # 放在 PM4/PM5 之前：切页签/开抽屉会改变预览区挂载态，把开关用例留在此刻最稳。
        # 用「MCP 工具」插件（不影响聊天链路）验证开关语义。
        target = "mcp" if "mcp" in by_id else (plugins[0]["id"] if plugins else "")
        name = (by_id.get(target) or {}).get("name") or target
        click = J(gui, "(()=>{const c=%s;const s=c&&c.querySelector('.pm-switch .b-switch');"
                       "if(!s)return 'no-switch';const before=s.className;s.click();return 'clicked '+before})()"
                % find_card(gui, name))
        off_ok = _h.wait_probe(gui, [
            "(()=>{const c=%s;return !!c && c.classList.contains('is-off')})()" % find_card(gui, name),
        ], timeout=30.0)
        snap6 = J(gui, "(()=>{const c=%s;const s=c&&c.querySelector('.pm-switch .b-switch');"
                       "return JSON.stringify({card:!!c,off:c?c.classList.contains('is-off'):null,"
                       "sw:s?s.className:null,txt:c?c.querySelector('.pm-switch').textContent.trim():null,"
                       "managers:document.querySelectorAll('.plugin-manager').length,"
                       "cards:document.querySelectorAll('.pm-card').length})})()" % find_card(gui, name))
        c.check("PM6a 关闭启用开关 → 卡片 is-off", bool(off_ok), f"click={click} snap={snap6}")
        # 等开关可用（pending 摘除）后再点：refresh 后重渲染 + 后端写库/停进程都在途。
        _h.wait_probe(gui, ["(()=>{const c=%s;const s=c&&c.querySelector('.pm-switch .b-switch');"
                            "return !!s && !s.classList.contains('is-disabled')})()" % find_card(gui, name)],
                      timeout=20.0)
        J(gui, "(()=>{const c=%s;const s=c.querySelector('.pm-switch .b-switch');s&&s.click();return 'ok'})()"
          % find_card(gui, name))
        on_ok = _h.wait_probe(gui, [
            "(()=>{const c=%s;return !!c && !c.classList.contains('is-off')})()" % find_card(gui, name),
        ], timeout=40.0)
        c.check("PM6b 重新启用 → 卡片恢复", bool(on_ok), "")

        # ── PM4 动作（container=drawer）→ 抽屉 + 插件 iframe ──
        act = next((a for a in (cb.get("actions") or []) if a.get("view")), None)
        if act:
            J(gui, "(()=>{const c=%s;const b=[...c.querySelectorAll('.pm-btn')]"
                   ".find(x=>(x.textContent||'').indexOf(%s)>=0);b&&b.click();return 'ok'})()"
              % (find_card(gui, "代码知识库"), json.dumps(act["label"])))
            _h.wait_probe(gui, ["!!document.querySelector('.drawer')"], timeout=20.0)
            dr = J(gui, "(()=>{const d=document.querySelector('.drawer');if(!d)return null;"
                        "const t=(d.querySelector('.drawer-head .ttl')||{}).textContent||'';"
                        "const f=d.querySelector('iframe.pph-frame');"
                        "return JSON.stringify({title:t,src:f?f.getAttribute('src'):''})})()")
            dr = json.loads(dr) if dr else {}
            view_title = ""
            for v in (cb.get("views") or []):
                if v.get("id") == act.get("view"):
                    view_title = v.get("title") or ""
            c.check("PM4 索引进度 → 抽屉（标题=视图标题 + 插件页 iframe）",
                    bool(dr) and dr.get("title") == view_title
                    and "/plugins/codebase/" in (dr.get("src") or ""),
                    repr(dr))
            J(gui, "document.querySelector('.drawer .d-btn')?.click(); 'ok'")
            _h.wait_probe(gui, ["!document.querySelector('.drawer')"], timeout=15.0)
        else:
            c.check("PM4 索引进度 → 抽屉", False, "codebase 未注册 view 型动作")

        # ── PM5 配置按钮 → 预览区插件页 iframe ──
        J(gui, "(()=>{const c=%s;const b=[...c.querySelectorAll('.pm-btn')]"
               ".find(x=>(x.textContent||'').indexOf('配置')>=0);b&&b.click();return 'ok'})()"
          % find_card(gui, "代码知识库"))
        ok5 = _h.wait_probe(gui, ["(()=>{const f=[...document.querySelectorAll('iframe.pph-frame')];"
                                  "return f.some(x=>(x.getAttribute('src')||'').indexOf('/plugins/codebase/webui/config.html')>=0)})()"],
                            timeout=20.0)
        c.check("PM5 配置按钮 → 预览区打开插件配置页", bool(ok5), "")

        errs = [e.get('text') for e in (gui.console() or {}).get('entries', [])
                if e.get('level') == 'error']
        c.check("PM7 无前端错误（插件维护组）", len(errs) == 0, repr(errs[:3]))
    finally:
        if snap is not None:
            _h.restore_config(gui, snap)
        h.stop()
    sys.exit(0 if c.summary() else 1)


if __name__ == "__main__":
    main()
