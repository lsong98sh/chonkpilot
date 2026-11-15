# -*- coding: utf-8 -*-
"""`seeded` 阶段测试点 ㉑：**截图能力门控 + 截图链路**（迁移自 test_screenshot.py S0–S5）。

* 所选 LLM 未声明 `capabilities:["vision"]` → 截图按钮禁用、点击不产生 overlay；
* 声明 vision → 按钮可用；点击 → 全屏预览 overlay；
* 拖拽选区 → canvas 裁剪 → 附件 chip + `<data_root>/screenshot` 落盘 png；
* 附件可删除；双击 overlay 取消且不产生附件；无前端错误。
"""

import json
import os
import time

from .framework import case

LLM_TAG_JS = "document.querySelector('.input-actions-left .b-tag')?.textContent?.trim() || ''"
SHOT_BTN_JS = """(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-left .icon-btn'));
  const b=bs.find(x=>{
    const t=(x.getAttribute('title')||'').toLowerCase();
    return t.indexOf('screenshot')>=0||t.indexOf('截图')>=0;});
  return {found:!!b,disabled:b?(!!b.disabled||b.classList.contains('is-disabled')):null,
          title:b?(b.getAttribute('title')||''):''};})()"""


def _shot_btn(env):
    return env.jsj("JSON.stringify(%s)" % SHOT_BTN_JS) or {}


def _click_shot(env):
    return env.js("""(()=>{const bs=Array.from(document.querySelectorAll('.input-actions-left .icon-btn'));
      const b=bs.find(x=>{const t=(x.getAttribute('title')||'').toLowerCase();
        return t.indexOf('screenshot')>=0||t.indexOf('截图')>=0;});
      if(!b)return 'no-btn';b.click();return 'ok';})()""")


def _seed_llm(env, name, caps):
    llms = [{"name": name, "protocol": "openai", "apiKey": "", "model": "m",
             "baseUrl": "http://127.0.0.1:1", "temperature": 0.7,
             "maxOutputToken": 4096, "maxContextToken": 128000,
             "thinking": True, "reasoningEffort": "", "maxToolIterations": 20,
             "capabilities": caps}]
    env.req("data-user-config-save", {"data": {"llms": llms, "defaultLLM": name}})
    env.publish("chat-select-llm", {"name": name})
    env.wait(lambda: True if str(env.js(LLM_TAG_JS) or "") == name else None,
             "LLM tag 切到 %s" % name, timeout=15)


def _uploads(env, sub="tmp/uploads"):
    d = os.path.join(env.data_root, *sub.split("/"))
    return set(f for f in os.listdir(d) if not f.startswith(".")) if os.path.isdir(d) else set()


@case("S-SHOT-01", "截图", "截图能力门控（vision）/ overlay / 拖选裁剪上传 / 附件删除 / 双击取消",
      stage="seeded", order=400, fp="33 主 chat 操作行截图（test_screenshot S0–S5）",
      desc="未声明 vision → 截图按钮 disabled 且点击无 overlay；声明 vision → 按钮可用、点击出现"
           "`.screenshot-overlay`；拖选 → 附件 chip + `<data_root>/screenshot` 新增 png；"
           "附件删除；再次截图后双击取消 → overlay 关闭且无附件。结束后还原 usr llms 并选回 mock。")
def s_shot_01(env):
    orig_llms = env.usr().get("llms")
    orig_default = env.usr().get("defaultLLM")
    try:
        _seed_llm(env, "shot-novision", [])
        b0 = _shot_btn(env)
        env.check("截图按钮存在", bool(b0.get("found")), b0)
        _click_shot(env)
        time.sleep(1.2)
        env.check("未声明 vision → 按钮禁用 + 点击无 overlay",
                  bool(b0.get("disabled")) is True
                  and env.js("!!document.querySelector('.screenshot-overlay')") is not True,
                  b0)

        _seed_llm(env, "shot-vision", ["vision"])
        bv = _shot_btn(env)
        env.check("声明 vision → 截图按钮可用",
                  bool(bv.get("found")) and bv.get("disabled") is False, bv)

        before = _uploads(env, "screenshot")
        env.check("点击截图", _click_shot(env) == "ok")
        ov = env.wait(lambda: True if env.js(
            "!!document.querySelector('.screenshot-overlay')") is True else None,
            "截图预览 overlay", timeout=25)
        env.check("截图预览 overlay 出现", ov is True)

        env.wait(lambda: True if env.js(
            "(()=>{const i=document.querySelector('.screenshot-img');"
            "return !!i&&i.complete&&i.naturalWidth>0&&i.getBoundingClientRect().width>0})()")
            is True else None, "截图 img 解码完成", timeout=20)
        env.js("""(()=>{const ov=document.querySelector('.screenshot-overlay');
          const img=document.querySelector('.screenshot-img');
          if(!ov||!img)return 'no-overlay';
          const r=img.getBoundingClientRect();
          const mk=(t,x,y)=>new MouseEvent(t,{clientX:x,clientY:y,button:0,
            bubbles:true,cancelable:true,view:window});
          ov.dispatchEvent(mk('mousedown',r.left+40,r.top+40));
          ov.dispatchEvent(mk('mousemove',r.left+260,r.top+180));
          ov.dispatchEvent(mk('mouseup',r.left+260,r.top+180));
          return 'ok';})()""")
        chip = env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.attach-chip.attach-image:not(.pending)').length")
            or 0) >= 1 else None, "截图附件 chip（回执已到）", timeout=25)
        env.check("拖选裁剪 → 图片附件", chip is True)
        name = str(env.js("document.querySelector('.attach-chip.attach-image .attach-name')"
                          "?.textContent?.trim() || ''") or "")
        title = str(_shot_btn(env).get("title") or "")
        env.check("附件展示名 = 截图按钮文案", bool(name) and name == title, {"name": name, "btn": title})
        env.check("overlay 关闭", env.js("!document.querySelector('.screenshot-overlay')") is True)
        new_png = [f for f in (_uploads(env, "screenshot") - before) if f.endswith(".png")]
        env.check("截图落盘 <data_root>/screenshot 新增 png", bool(new_png), new_png[:3])

        env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.attach-chip.attach-image .attach-remove').length")
            or 0) >= 1 else None, "附件删除入口", timeout=15)
        env.js("document.querySelector('.attach-chip.attach-image .attach-remove')?.click(); 'ok'")
        env.wait(lambda: True if int(env.js(
            "document.querySelectorAll('.attach-chip').length") or 0) == 0 else None,
            "附件已删除", timeout=15)
        env.check("截图附件可删除", int(env.js(
            "document.querySelectorAll('.attach-chip').length") or 0) == 0)

        env.check("再次点击截图", _click_shot(env) == "ok")
        env.wait(lambda: True if env.js(
            "!!document.querySelector('.screenshot-overlay')") is True else None,
            "再次出现 overlay", timeout=25)
        env.js("document.querySelector('.screenshot-overlay')"
               "?.dispatchEvent(new MouseEvent('dblclick',{bubbles:true})); 'ok'")
        gone = env.wait(lambda: True if env.js(
            "!document.querySelector('.screenshot-overlay')") is True else None,
            "双击取消 overlay", timeout=15)
        env.check("双击取消 → overlay 关闭且无附件",
                  gone is True and int(env.js(
                      "document.querySelectorAll('.attach-chip').length") or 0) == 0)
    finally:
        if orig_llms is not None:
            restore = {"llms": orig_llms}
            if orig_default:
                restore["defaultLLM"] = orig_default
            env.req("data-user-config-save", {"data": restore})
        env.publish("chat-select-llm", {"name": "mock"})

    errs = [e.get("text") for e in ((env.client.console() or {}).get("entries") or [])
            if e.get("level") == "error"
            and all(k not in (e.get("text") or "")
                    for k in ("method not implemented", "SetActiveSessionID", "CaptureScreen"))]
    env.check("截图组无前端错误", len(errs) == 0, errs[:3])