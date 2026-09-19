// 前端只用原生 API，不引入构建步骤。

const API = '/api/v1';

// 语言变化时重新请求或重绘页面，各页自行注册。
const RERENDER_HOOKS = [];
function onLangChange(fn) { RERENDER_HOOKS.push(fn); }

async function requestJSON(path, options) {
  const res = await fetch(API + path, options);
  const body = await res.json().catch(() => null);
  if (!res.ok) {
    const msg = body && body.error ? body.error.message : res.statusText;
    const err = new Error(msg);
    err.code = body && body.error ? body.error.code : 'UNKNOWN';
    err.status = res.status;
    throw err;
  }
  return body;
}

function getJSON(path) { return requestJSON(path); }

function postJSON(path, payload) {
  return requestJSON(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

function patchJSON(path, payload) {
  return requestJSON(path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function severityBadge(sev) {
  const I = window.DevLensI18n;
  if (!sev) return el('span', 'badge', I.t('common.pending'));
  return el('span', 'badge sev-' + sev, sev);
}

function statusBadge(status) {
  return el('span', 'badge status', status);
}

function recurringBadge() {
  return el('span', 'badge recurring', 'RECURRING');
}

function formatTime(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  return isNaN(d) ? iso : d.toLocaleString(window.DevLensI18n.getLang());
}

function showError(container, message) {
  container.innerHTML = '';
  container.appendChild(el('div', 'alert error', message));
}

function linkIncident(id, label) {
  const a = el('a', null, label || ('#' + id));
  a.href = 'detail.html?id=' + id;
  return a;
}

// spinnerLine 生成"转圈 + 文案"的一行。
function spinnerLine(text) {
  const wrap = el('div');
  wrap.appendChild(el('span', 'spinner'));
  wrap.appendChild(document.createTextNode(text));
  return wrap;
}

// renderAnalysis 渲染一份诊断结果，analyze 页和 detail 页共用。
//
// 两处的排版要求一致，分开写必然出现某一处漏改字段。
function renderAnalysis(container, a) {
  const I = window.DevLensI18n;

  container.appendChild(el('h3', null, I.t('result.summary')));
  container.appendChild(el('p', null, a.summary));

  if (a.possible_causes && a.possible_causes.length) {
    container.appendChild(el('h3', null, I.t('result.causes')));
    const ul = el('ul', 'plain');
    a.possible_causes.forEach(c => ul.appendChild(el('li', null, c)));
    container.appendChild(ul);
  }

  if (a.evidence && a.evidence.length) {
    container.appendChild(el('h3', null, I.t('result.evidence')));
    a.evidence.forEach(e => {
      const box = el('div', 'evidence');
      box.appendChild(el('div', 'mono', e.key + ': ' + e.value));
      box.appendChild(el('div', 'line', I.t('result.line', { n: e.source_line })));
      container.appendChild(box);
    });
  }

  if (a.suggested_actions && a.suggested_actions.length) {
    container.appendChild(el('h3', null, I.t('result.actions')));
    const ul = el('ul', 'plain');
    a.suggested_actions.forEach(s => ul.appendChild(el('li', null, s)));
    container.appendChild(ul);
  }

  container.appendChild(el('h3', null, I.t('result.confidence')));
  const conf = el('div', 'confidence');
  const bar = el('div', 'bar');
  const fill = el('span');
  fill.style.width = Math.round(a.confidence * 100) + '%';
  bar.appendChild(fill);
  conf.appendChild(bar);
  conf.appendChild(el('span', null,
    a.confidence.toFixed(2) + ' · ' + a.model + ' · ' + a.prompt_version));
  container.appendChild(conf);
}