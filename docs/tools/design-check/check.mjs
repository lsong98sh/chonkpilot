#!/usr/bin/env node
/**
 * 设计字典校验器
 *
 * 用法：node check.mjs <设计文件目录>
 *
 * 读一组 Markdown 卡片 → 建图（对象流转图 + 每对象状态图）→ 输出计数与缺口清单。
 * 卡片格式约定（见 docs/业务系统设计-思路与方法.md §7.4）：
 *   ### <ID> · <名称>
 *   - 字段名：值
 *   - 多值字段：
 *     - 值1
 *     - 值2
 */

import fs from 'node:fs';
import path from 'node:path';

const ID_RE = /\b([A-Z]{3}-\d+)\b/g;

// ---------- 解析 ----------

function parseCards(text) {
  const cards = [];
  let cur = null;
  let listKey = null;

  for (const raw of text.split(/\r?\n/)) {
    const line = raw.replace(/\s+$/, '');

    const h = line.match(/^###\s+([A-Z]{3}-\d+)\s*[·:：]\s*(.*)$/);
    if (h) {
      cur = { id: h[1], name: h[2].trim(), fields: {} };
      cards.push(cur);
      listKey = null;
      continue;
    }
    if (!cur) continue;

    const f = line.match(/^-\s*([^:：]+)[:：]\s*(.*)$/);
    if (f) {
      const key = f[1].trim();
      const val = f[2].trim();
      if (val === '') {
        cur.fields[key] = [];
        listKey = key;
      } else {
        cur.fields[key] = val;
        listKey = null;
      }
      continue;
    }

    const li = line.match(/^\s+-\s*(.+)$/);
    if (li && listKey) {
      cur.fields[listKey].push(li[1].trim());
    }
  }
  return cards;
}

function loadDir(dir) {
  const cards = [];
  for (const f of fs.readdirSync(dir).filter((x) => x.endsWith('.md'))) {
    cards.push(...parseCards(fs.readFileSync(path.join(dir, f), 'utf8')));
  }
  return cards;
}

// ---------- 小工具 ----------

const vals = (v) => (Array.isArray(v) ? v : v === undefined || v === '' ? [] : [v]);
const flat = (v) => vals(v).join('\n');
const isYes = (v) => String(v).trim() === '是';
const blank = (v) => v === undefined || flat(v).trim() === '';

const of = (cards, p) => cards.filter((c) => c.id.startsWith(p + '-'));

// ---------- 校验 ----------

function run(cards) {
  const find = { 重号: [], 缺必填字段: [], 悬空引用: [], 幽灵对象: [], 断头状态: [], 死路状态: [], 不可达状态: [], 无业务理由的边: [] };

  const seen = new Map();
  for (const c of cards) {
    if (seen.has(c.id)) find.重号.push(`${c.id}（${seen.get(c.id)} 与 ${c.name}）`);
    else seen.set(c.id, c.name);
  }
  const ids = new Set(cards.map((c) => c.id));

  const required = {
    OBJ: ['层级', '创建来源', '一句话定义'],
    STA: ['所属对象', '是否初始', '是否终态'],
    TRN: ['所属对象', '从状态', '到状态', '触发动作'],
    ACT: ['所属对象', '谁发起'],
  };
  for (const c of cards) {
    const need = required[c.id.slice(0, 3)];
    if (!need) continue;
    for (const k of need) if (blank(c.fields[k])) find.缺必填字段.push(`${c.id} 缺「${k}」`);
  }

  // 悬空引用 + 无业务理由
  for (const c of cards) {
    for (const [k, v] of Object.entries(c.fields)) {
      for (const m of flat(v).matchAll(ID_RE)) {
        if (m[1] !== c.id && !ids.has(m[1])) find.悬空引用.push(`${c.id} 的「${k}」引用了不存在的 ${m[1]}`);
      }
    }
    if ((c.id.startsWith('ACT-') || c.id.startsWith('TRN-')) && blank(c.fields['业务理由'])) {
      find.无业务理由的边.push(c.id);
    }
  }

  // 幽灵对象：无有效创建来源
  const EXT = /外部|导入|初始化|同步/;
  for (const o of of(cards, 'OBJ')) {
    const src = o.fields['创建来源'];
    if (blank(src)) { find.幽灵对象.push(`${o.id}（无创建来源）`); continue; }
    const s = flat(src).trim();
    if (EXT.test(s)) continue;
    const m = s.match(/^([A-Z]{3}-\d+)/);
    if (!m || !ids.has(m[1])) find.幽灵对象.push(`${o.id}（创建来源无效：${s}）`);
  }

  // 状态图
  const states = of(cards, 'STA');
  const trans = of(cards, 'TRN');
  const outEdges = new Map();
  const inEdges = new Map();
  for (const t of trans) {
    const a = String(t.fields['从状态'] ?? '').trim();
    const b = String(t.fields['到状态'] ?? '').trim();
    if (!outEdges.has(a)) outEdges.set(a, []);
    outEdges.get(a).push(b);
    if (!inEdges.has(b)) inEdges.set(b, []);
    inEdges.get(b).push(a);
  }

  for (const o of of(cards, 'OBJ')) {
    const mine = states.filter((s) => String(s.fields['所属对象'] ?? '').trim() === o.id);
    const inits = mine.filter((s) => isYes(s.fields['是否初始']));
    if (inits.length !== 1) find.断头状态.push(`${o.id} 的初始状态数量 = ${inits.length}（应为 1）`);

    // BFS 可达性
    if (inits.length === 1) {
      const seenS = new Set([inits[0].id]);
      const q = [inits[0].id];
      while (q.length) {
        for (const nx of outEdges.get(q.shift()) ?? []) {
          if (!seenS.has(nx) && nx) { seenS.add(nx); q.push(nx); }
        }
      }
      for (const s of mine) if (!seenS.has(s.id)) find.不可达状态.push(`${s.id}（${s.name}，从初始态走不到）`);
    }
  }

  for (const s of states) {
    if (!isYes(s.fields['是否终态']) && (outEdges.get(s.id) ?? []).length === 0) {
      find.死路状态.push(`${s.id}（${s.name}，非终态但没有出边）`);
    }
  }

  return find;
}

// ---------- 输出 ----------

const dir = process.argv[2];
if (!dir) {
  console.error('用法：node check.mjs <设计文件目录>');
  process.exit(2);
}

const cards = loadDir(dir);
const counts = Object.fromEntries(
  ['OBJ', 'STA', 'TRN', 'ACT'].map((p) => [p, of(cards, p).length])
);

console.log('='.repeat(52));
console.log(`设计字典校验　目录：${dir}`);
console.log(`卡片数：对象 ${counts.OBJ} · 状态 ${counts.STA} · 状态迁移 ${counts.TRN} · 动作 ${counts.ACT}`);
console.log('='.repeat(52));

const find = run(cards);
let bad = 0;
for (const [k, v] of Object.entries(find)) {
  const ok = v.length === 0;
  if (!ok) bad++;
  console.log(`\n${ok ? '✅' : '❌'} ${k} = ${v.length}`);
  for (const x of v) console.log(`     · ${x}`);
}

console.log('\n' + '='.repeat(52));
console.log(bad === 0 ? '✅ 全部计数归零，可以过门' : `❌ ${bad} 项不为 0，本批未完成`);
process.exit(bad === 0 ? 0 : 1);
