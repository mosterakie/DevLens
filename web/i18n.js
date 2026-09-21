// 界面文案的中英对照。
//
// 术语上的取舍：技术名词（incident、severity、fingerprint）在中文里
// 保留英文或采用业界通用译法，不硬造新词。像 "Incident" 译为"事件"
// 会让工程师觉得别扭，所以中文界面里直接用 "Incident"。

const MESSAGES = {
  'zh-Hans': {
    'nav.home': '首页',
    'nav.analyze': '分析',
    'nav.incidents': 'Incident 列表',
    'nav.lang': '语言',

    'landing.title': '更快看懂生产环境的报错。',
    'landing.lede': '粘贴一段日志，得到结构化诊断，并找出历史上出现过的同类问题。',
    'landing.cta.analyze': '立即试用',
    'landing.cta.browse': '浏览 Incident',
    'landing.card.diagnosis.title': 'AI 诊断',
    'landing.card.diagnosis.body': '把非结构化的日志变成严重程度、分类、可能原因和可核对的证据。',
    'landing.card.related.title': '同类问题',
    'landing.card.related.body': '日志会被归一化成指纹，所以即使时间戳、地址和行号变了，同一类故障依然能被认出来。',
    'landing.card.history.title': 'Incident 历史',
    'landing.card.history.body': '每条记录都保留原始日志，以及从提交到解决的状态时间线。',
    'landing.sample.title': '可以拿这段日志试一下',
    'landing.sample.load': '载入这段示例',

    'analyze.title': '分析日志',
    'analyze.log': '错误日志',
    'analyze.placeholder': '在这里粘贴日志……',
    'analyze.submit': '开始分析',
    'analyze.sample': '载入示例',
    'analyze.clear': '清空',
    'analyze.result': '分析结果',
    'analyze.empty': '提交一段日志后，这里会显示诊断结果。',
    'analyze.analyzing': '正在分析',
    'analyze.loading': '加载中',
    'analyze.still': '仍在分析中，稍后刷新页面即可。',
    'analyze.check': '查看 Incident',
    'analyze.needlog': '请先粘贴一段日志。',
    'analyze.toolong': '日志超过 64KB，请精简到与故障相关的部分。',
    'analyze.size': '当前 {size}，上限 64KB',
    'analyze.sizeok': '当前 {size}',
    'analyze.overlimit': '{size} / 上限 64KB，超出后无法提交',
    'analyze.redact': '提交前会自动脱敏：密码、token、密钥等凭据不会离开本服务，日志内容也不会原样发给模型。',
    'analyze.payload': '请求体过大。日志字段限 64KB，请精简后重试。',
    'analyze.rejected': '日志被拒绝：',
    'analyze.ratelimited': '请求过于频繁，请稍后再试。',
    'analyze.failed': '请求失败。',

    'result.summary': '概要',
    'result.causes': '可能原因',
    'result.evidence': '证据',
    'result.actions': '建议排查',
    'result.confidence': '置信度',
    'result.related': '同类问题',
    'result.line': '第 {n} 行',
    'result.view': '查看 Incident',
    'result.failed.note': '分析失败。原始日志仍然保留，可以重试。',
    'result.norelated': '没有其他记录与它的指纹相同。',
    'result.relatedfailed': '无法加载同类问题。',

    'list.title': 'Incident 列表',
    'list.status': '全部状态',
    'list.severity': '全部严重程度',
    'list.refresh': '刷新',
    'list.empty': '还没有记录。可以到分析页提交一段日志。',
    'list.loading': '加载中……',
    'list.more': '加载更多',
    'list.col.id': 'ID',
    'list.col.title': '标题',
    'list.col.severity': '严重程度',
    'list.col.status': '状态',
    'list.col.tags': '标签',
    'list.failed': '加载失败。',

    'detail.notfound': 'URL 里没有 Incident ID。',
    'detail.loading': '加载中……',
    'detail.move': '变更为',
    'detail.ai': 'AI 分析',
    'detail.rawlog': '原始日志',
    'detail.timeline': '时间线',
    'detail.evidence': '证据',
    'detail.causes': '可能原因',
    'detail.actions': '建议排查',
    'detail.confidence': '置信度',
    'detail.related': '同类问题',
    'detail.incident': 'Incident',
    'detail.created': '创建于',
    'detail.statuschange': '状态变更失败。',
    'detail.event.created': 'Incident 已创建',
    'detail.event.analysis_done': 'AI 分析完成',
    'detail.event.analysis_failed': 'AI 分析失败',
    'detail.event.status': '状态变更：{from} → {to}',

    'common.unknown': '未知',
    'common.pending': '待分析',
    'common.retry': '重试',
  },

  'en': {
    'nav.home': 'Home',
    'nav.analyze': 'Analyze',
    'nav.incidents': 'Incidents',
    'nav.lang': 'Language',

    'landing.title': 'Understand production errors faster.',
    'landing.lede': 'Paste a log, get a structured diagnosis, and find incidents that looked like this before.',
    'landing.cta.analyze': 'Try it',
    'landing.cta.browse': 'Browse incidents',
    'landing.card.diagnosis.title': 'AI Diagnosis',
    'landing.card.diagnosis.body': 'Turns an unstructured log into severity, category, likely causes and checkable evidence.',
    'landing.card.related.title': 'Related Incidents',
    'landing.card.related.body': 'Logs are normalized into a fingerprint, so the same failure is recognized even when timestamps, addresses and line numbers change.',
    'landing.card.history.title': 'Incident History',
    'landing.card.history.body': 'Every incident keeps its raw log and a status timeline from submission to resolution.',
    'landing.sample.title': 'Try it with a log like this',
    'landing.sample.load': 'Load this sample',

    'analyze.title': 'Analyze a log',
    'analyze.log': 'Error log',
    'analyze.placeholder': 'Paste your log here...',
    'analyze.submit': 'Analyze',
    'analyze.sample': 'Load sample',
    'analyze.clear': 'Clear',
    'analyze.result': 'Analysis',
    'analyze.empty': 'Submit a log to see the diagnosis.',
    'analyze.analyzing': 'Analyzing',
    'analyze.loading': 'Loading',
    'analyze.still': 'Still analyzing. Refresh this page in a moment.',
    'analyze.check': 'Check incident',
    'analyze.needlog': 'Paste a log first.',
    'analyze.toolong': 'Log exceeds 64KB. Keep only the parts related to the failure.',
    'analyze.size': '{size} / 64KB limit',
    'analyze.sizeok': '{size}',
    'analyze.overlimit': '{size} / 64KB limit — too large to submit',
    'analyze.redact': 'Credentials are redacted before submission: passwords, tokens and keys never leave this service, and the log is not sent to the model verbatim.',
    'analyze.payload': 'Request body too large. The log field is limited to 64KB — trim it and retry.',
    'analyze.rejected': 'Log rejected: ',
    'analyze.ratelimited': 'Too many requests. Wait a moment and try again.',
    'analyze.failed': 'Request failed.',

    'result.summary': 'Summary',
    'result.causes': 'Possible causes',
    'result.evidence': 'Evidence',
    'result.actions': 'Suggested investigation',
    'result.confidence': 'AI confidence',
    'result.related': 'Related incidents',
    'result.line': 'line {n}',
    'result.view': 'View incident',
    'result.failed.note': 'Analysis failed. The raw log is still stored and can be retried.',
    'result.norelated': 'No earlier incident shares this fingerprint.',
    'result.relatedfailed': 'Could not load related incidents.',

    'list.title': 'Incidents',
    'list.status': 'All statuses',
    'list.severity': 'All severities',
    'list.refresh': 'Refresh',
    'list.empty': 'No incidents yet. Submit a log from the Analyze page.',
    'list.loading': 'Loading...',
    'list.more': 'Load more',
    'list.col.id': 'ID',
    'list.col.title': 'Title',
    'list.col.severity': 'Severity',
    'list.col.status': 'Status',
    'list.col.tags': 'Tags',
    'list.failed': 'Failed to load incidents.',

    'detail.notfound': 'No incident id in the URL.',
    'detail.loading': 'Loading...',
    'detail.move': 'Move to',
    'detail.ai': 'AI analysis',
    'detail.rawlog': 'Original log',
    'detail.timeline': 'Timeline',
    'detail.evidence': 'Evidence',
    'detail.causes': 'Possible causes',
    'detail.actions': 'Suggested investigation',
    'detail.confidence': 'Confidence',
    'detail.related': 'Related incidents',
    'detail.incident': 'Incident',
    'detail.created': 'created',
    'detail.statuschange': 'Status change failed.',
    'detail.event.created': 'Incident created',
    'detail.event.analysis_done': 'AI analysis completed',
    'detail.event.analysis_failed': 'AI analysis failed',
    'detail.event.status': 'Status changed: {from} → {to}',

    'common.unknown': 'Unknown',
    'common.pending': 'PENDING',
    'common.retry': 'Retry',
  },
};

const STORAGE_KEY = 'devlens.lang';
const SUPPORTED = Object.keys(MESSAGES);

// 当前语言。初始值在 initLang 里按 localStorage > 浏览器偏好 > 默认 决定。
let currentLang = 'zh-Hans';

// 语言变化时重新渲染的回调。
const listeners = [];

function normalize(tag) {
  if (!tag) return null;
  const lower = String(tag).toLowerCase();
  const primary = lower.split(/[-_]/)[0];
  if (primary === 'zh') return 'zh-Hans';
  if (primary === 'en') return 'en';
  return null;
}

function detectInitial() {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    const normalized = normalize(saved);
    if (normalized) return normalized;
  } catch (_) {
    // 隐私模式下 localStorage 可能不可用，忽略即可。
  }
  for (const tag of navigator.languages || [navigator.language]) {
    const normalized = normalize(tag);
    if (normalized) return normalized;
  }
  return 'zh-Hans';
}

function t(key, vars) {
  const table = MESSAGES[currentLang] || MESSAGES['zh-Hans'];
  let s = table[key];
  if (s === undefined) {
    // 缺词条时回退到默认语言，再不行就把 key 原样显示，
    // 便于在界面上直接发现遗漏。
    s = MESSAGES['zh-Hans'][key];
    if (s === undefined) return key;
  }
  if (vars) {
    for (const k of Object.keys(vars)) {
      s = s.replaceAll('{' + k + '}', vars[k]);
    }
  }
  return s;
}

function getLang() { return currentLang; }

function setLang(lang) {
  const normalized = normalize(lang) || 'zh-Hans';
  if (normalized === currentLang) return;
  currentLang = normalized;
  try { localStorage.setItem(STORAGE_KEY, normalized); } catch (_) {}
  document.documentElement.lang = normalized;
  applyStatic();
  for (const fn of listeners) {
    try { fn(normalized); } catch (e) { console.error(e); }
  }
}

function onLangChange(fn) { listeners.push(fn); }

// applyStatic 把带 data-i18n 的元素文本换成当前语言。
//
// data-i18n 用于 textContent，data-i18n-placeholder 用于 placeholder，
// data-i18n-aria 用于 aria-label。
function applyStatic(root) {
  const scope = root || document;

  scope.querySelectorAll('[data-i18n]').forEach(el => {
    el.textContent = t(el.getAttribute('data-i18n'));
  });
  scope.querySelectorAll('[data-i18n-placeholder]').forEach(el => {
    el.setAttribute('placeholder', t(el.getAttribute('data-i18n-placeholder')));
  });
  scope.querySelectorAll('[data-i18n-aria]').forEach(el => {
    el.setAttribute('aria-label', t(el.getAttribute('data-i18n-aria')));
  });
}

function initLang() {
  currentLang = detectInitial();
  document.documentElement.lang = currentLang;
  applyStatic();
  return currentLang;
}

// apiLang 把界面语言转成后端认识的语言标识。
function apiLang() { return currentLang; }

window.DevLensI18n = {
  t, getLang, setLang, onLangChange, initLang, applyStatic, apiLang,
  supported: SUPPORTED,
  displayName: (l) => (l === 'en' ? 'English' : '中文'),
};