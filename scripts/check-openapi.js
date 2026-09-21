#!/usr/bin/env node
// 比对 OpenAPI 声明的路径与实际注册的路由。
//
// 只检查路径集合，不校验字段与结构——完整校验要引入解析器并维护
// 一套映射，成本不抵收益。但"加了接口忘了写文档"这类最常见的不一致
// 能被这条挡住。
//
// 用法：node scripts/check-openapi.js

const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..');
const OPENAPI = path.join(ROOT, 'docs', 'openapi.yaml');
const MAIN = path.join(ROOT, 'cmd', 'api', 'main.go');

let failures = 0;
function fail(msg) {
  failures++;
  console.log('FAIL  ' + msg);
}
function pass(msg) {
  console.log('PASS  ' + msg);
}

// --- 从 Go 源码提取实际注册的路由 ---

function actualRoutes() {
  const src = fs.readFileSync(MAIN, 'utf8');
  const out = new Set();

  // 匹配形如：v1.GET("/incidents", ...)  r.GET("/healthz", ...)
  const re = /\b(?:v1|r|api)\.(GET|POST|PATCH|PUT|DELETE|HEAD)\("([^"]+)"/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    const method = m[1].toLowerCase();
    let p = m[2];
    // gin 的参数写法是 :id，OpenAPI 是 {id}
    p = p.replace(/:([A-Za-z_][A-Za-z0-9_]*)/g, '{$1}');
    // /api/v1 分组下的路由要补上前缀
    if (!p.startsWith('/api/') && p !== '/' && !p.startsWith('/healthz') &&
        !p.startsWith('/readyz') && !p.startsWith('/metrics')) {
      p = '/api/v1' + p;
    }
    out.add(method + ' ' + p);
  }
  return out;
}

// --- 从 OpenAPI 提取声明的路径 ---

function declaredRoutes() {
  const src = fs.readFileSync(OPENAPI, 'utf8');
  const out = new Set();

  const lines = src.split('\n');
  let inPaths = false;
  let currentPath = null;

  for (const line of lines) {
    if (/^paths:\s*$/.test(line)) { inPaths = true; continue; }
    if (inPaths && /^[a-zA-Z]/.test(line)) { inPaths = false; }

    if (!inPaths) continue;

    // 顶层路径：两空格缩进 + / 开头
    const pm = line.match(/^  (\/[^\s:]*):\s*$/);
    if (pm) { currentPath = pm[1]; continue; }

    // 方法：四空格缩进
    const mm = line.match(/^    (get|post|patch|put|delete|head):\s*$/);
    if (mm && currentPath) {
      out.add(mm[1] + ' ' + currentPath);
    }
  }

  return out;
}

const actual = actualRoutes();
const declared = declaredRoutes();

console.log('实际注册: ' + actual.size + ' 条');
console.log('OpenAPI:  ' + declared.size + ' 条');
console.log('');

// 只比对业务端点。根路径的静态文件服务（/、/index.html 等）
// 不属于 API 契约，不要求写进 OpenAPI。
const isBusinessRoute = (r) => r.includes('/api/v1/');

const missing = [...actual].filter(r => isBusinessRoute(r) && !declared.has(r));
const extra = [...declared].filter(r => !actual.has(r));

if (missing.length) {
  fail('以下路由已注册但 OpenAPI 未声明：');
  missing.forEach(r => console.log('        ' + r));
} else {
  pass('所有已注册的业务路由都在 OpenAPI 中声明');
}

if (extra.length) {
  fail('以下 OpenAPI 声明的路径没有对应的路由：');
  extra.forEach(r => console.log('        ' + r));
} else {
  pass('OpenAPI 没有声明不存在的路径');
}

console.log('');
if (failures === 0) {
  console.log('检查通过');
  process.exit(0);
}
console.log(failures + ' 项未通过');
process.exit(1);