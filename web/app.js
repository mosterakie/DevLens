// 前端只用原生 API，不引入构建步骤。

const API = '/api/v1';

async function getJSON(path) {
  const res = await fetch(API + path);
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

async function postJSON(path, payload) {
  const res = await fetch(API + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
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

async function patchJSON(path, payload) {
  const res = await fetch(API + path, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
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

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function severityBadge(sev) {
  if (!sev) return el('span', 'badge', 'PENDING');
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
  return isNaN(d) ? iso : d.toLocaleString();
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