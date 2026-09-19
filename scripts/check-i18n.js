#!/usr/bin/env node
// 校验界面词条的完整性。
//
// 漏翻不会让页面报错，只会让某种语言下直接显示词条的 key，
// 这类问题很容易在提交前被忽略，所以单独做成可执行的检查。
//
// 用法：node scripts/check-i18n.js

const fs = require('fs');
const path = require('path');

const SRC = path.join(__dirname, '..', 'web', 'i18n.js');

// 技术术语在中文界面里保留英文是刻意的选择，
// 校验时应当排除，否则会把它们误报成漏翻。
const TECH_TERMS = /\b(Incident|Incidents|AI|ID|API|URL|DevLens|RECURRING|English|HTTP|JSON)\b/g;
// 占位符形如 {n} {from} {to}，不是待翻译的内容。
const PLACEHOLDERS = /\{[a-zA-Z_]+\}/g;

let failures = 0;
function check(name, ok, detail) {
  if (!ok) failures++;
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}`);
  if (!ok && detail) console.log('        ' + detail);
}

function loadMessages() {
  const src = fs.readFileSync(SRC, 'utf8');
  const start = src.indexOf('const MESSAGES = ');
  const end = src.indexOf('const STORAGE_KEY');
  if (start < 0 || end < 0) {
    throw new Error('无法在 i18n.js 中定位 MESSAGES，检查是否改动了结构');
  }
  const literal = src.slice(start + 'const MESSAGES = '.length, end).trim().replace(/;$/, '');
  return eval('(' + literal + ')');
}

const MESSAGES = loadMessages();
const langs = Object.keys(MESSAGES);

check('至少有两种语言', langs.length >= 2, `实际 ${langs.length}: ${langs.join(', ')}`);

const baseline = langs[0];
const baselineKeys = Object.keys(MESSAGES[baseline]).sort();

for (const lang of langs.slice(1)) {
  const keys = Object.keys(MESSAGES[lang]).sort();
  const missing = baselineKeys.filter((k) => !(k in MESSAGES[lang]));
  const extra = keys.filter((k) => !(k in MESSAGES[baseline]));

  check(`${lang} 不缺词条`, missing.length === 0, missing.join(', '));
  check(`${lang} 没有多余词条`, extra.length === 0, extra.join(', '));
}

// 中文词条里不应残留未翻译的英文整词。
for (const lang of langs) {
  if (!lang.startsWith('zh')) continue;
  const suspicious = [];
  for (const [key, value] of Object.entries(MESSAGES[lang])) {
    const stripped = value.replace(TECH_TERMS, '').replace(PLACEHOLDERS, '');
    // 连续 4 个以上字母视为可能是没翻的英文。
    if (/[A-Za-z]{4,}/.test(stripped)) suspicious.push(`${key} = ${value}`);
  }
  check(`${lang} 无未翻译的英文残留`, suspicious.length === 0, suspicious.join('\n        '));
}

// 每种语言的词条都不该是空串。
for (const lang of langs) {
  const empty = Object.entries(MESSAGES[lang]).filter(([, v]) => !String(v).trim());
  check(`${lang} 无空词条`, empty.length === 0, empty.map(([k]) => k).join(', '));
}

console.log('');
if (failures === 0) {
  console.log(`检查通过：${langs.join(' / ')}，各 ${baselineKeys.length} 条词条`);
  process.exit(0);
}
console.log(`${failures} 项未通过`);
process.exit(1);