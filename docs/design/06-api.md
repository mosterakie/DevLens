# API 契约

Base path `/api/v1`，响应统一 `application/json; charset=utf-8`。

## 错误格式

```json
{ "error": { "code": "INVALID_LOG", "message": "log must be between 100 and 65536 bytes" } }
```

| code | HTTP | 含义 |
|---|---|---|
| `INVALID_LOG` | 400 | 日志为空 / 过短 / 过长 |
| `NOT_FOUND` | 404 | Incident 不存在 |
| `INVALID_TRANSITION` | 409 | 非法状态转移 |
| `RATE_LIMITED` | 429 | 触发限流 |
| `INTERNAL` | 500 | 未预期错误 |

用 `code` 而不只靠 HTTP 状态码，是因为前端需要区分"被限流"和"日志格式不对"
—— 都是 4xx 但处理方式完全不同。`code` 给程序判断，`message` 可直接展示给用户。

## 端点

### POST /incidents/analyze

```json
{ "log": "goroutine 231 [IO wait]:\ncontext deadline exceeded..." }
```

`202 Accepted`：

```json
{
  "id": 1024,
  "status": "ANALYZING",
  "is_recurring": true,
  "related_incident_ids": [981, 742],
  "created_at": "2026-09-18T14:32:51Z"
}
```

返回 202 而不是 200，因为分析还在进行中。`related_incident_ids` 此时已有值，
因为 fingerprint 匹配是同步完成的，只有 AI 诊断是异步的。

### GET /incidents/:id

前端轮询这个接口。

```json
{
  "id": 1024,
  "title": "DB connection pool timeout",
  "raw_log": "...",
  "status": "OPEN",
  "severity": "HIGH",
  "category": "Database / Timeout",
  "is_recurring": true,
  "created_at": "2026-09-18T14:32:51Z",
  "analysis": {
    "summary": "The request exceeded the configured context deadline while waiting for a database connection.",
    "possible_causes": ["Database connection pool exhausted", "Slow query", "Network latency", "Connection leak"],
    "evidence": [
      { "key": "request timeout", "value": "5s", "source_line": 12 },
      { "key": "pool size", "value": "20", "source_line": 18 },
      { "key": "active connections", "value": "20", "source_line": 19 }
    ],
    "suggested_actions": [
      "Inspect DB pool utilization",
      "Check slow queries",
      "Verify connections are released",
      "Compare with previous deployment"
    ],
    "confidence": 0.84,
    "model": "gpt-4o-mini",
    "prompt_version": "v3"
  },
  "related_incidents": [
    { "id": 981, "title": "Payment timeout", "severity": "HIGH", "status": "RESOLVED" }
  ]
}
```

分析未完成时返回 `status = "ANALYZING"` 和 `analysis: null`（仍是 200）。
这里不用 425，否则前端轮询要多一个异常分支。

### GET /incidents

```
GET /api/v1/incidents?status=OPEN&severity=HIGH&limit=20&cursor=...
```

```json
{
  "items": [
    { "id": 1024, "title": "DB timeout", "severity": "HIGH", "status": "OPEN",
      "category": "Database / Timeout", "is_recurring": true, "created_at": "..." }
  ],
  "next_cursor": "eyJpZCI6MTAyNH0="
}
```

用 cursor 而不是 offset 分页：Incident 持续新增，offset 分页在列表头部
插入新数据时会漏记录。cursor 基于 `(created_at, id)`。

### GET /incidents/:id/events

```json
{
  "items": [
    { "event_type": "created", "to_status": "ANALYZING", "created_at": "..." },
    { "event_type": "analysis_completed", "from_status": "ANALYZING", "to_status": "OPEN", "created_at": "..." },
    { "event_type": "status_changed", "from_status": "OPEN", "to_status": "INVESTIGATING", "created_at": "..." }
  ]
}
```

### GET /incidents/:id/related

返回与该 Incident 指纹相同的其他记录，用于详情页展示同类问题。

```json
{
  "items": [
    { "id": 981, "title": "Payment timeout", "severity": "HIGH",
      "status": "RESOLVED", "created_at": "..." }
  ]
}
```

没有同类时返回空数组而不是 null，前端不必额外判空。

### PATCH /incidents/:id/status

```json
{ "status": "INVESTIGATING" }
```

非法转移返回 `409 INVALID_TRANSITION`。

做成子资源而不是 `PATCH /incidents/:id`，是因为状态转移有副作用
（校验合法性、写 event），和改标题这类幂等更新不是一回事。

### 健康检查

```
GET /healthz   200            进程存活
GET /readyz    200 / 503      就绪（含 DB + Redis 探测）
```

分开是因为容器编排需要区分"该重启我"和"该把流量摘走"——
DB 短暂不可用不该导致容器被杀。

## 限流

| 端点 | 限制 |
|---|---|
| `POST /analyze` | 10 次 / 分钟 / IP |
| `POST /analyze` 全局 | 200 次 / 分钟 |
| `GET /incidents/:id` | 120 次 / 分钟 / IP |
| 其他 GET | 100 次 / 分钟 / IP |

响应头带 `X-RateLimit-Limit` / `X-RateLimit-Remaining` / `Retry-After`。

轮询走 `GET /incidents/:id`，1s 间隔是 60 次/分钟，限额 120 留了余量。
这个数字和 `/analyze` 的 10 次/分钟必须一起设计，否则轮询会把自己限流掉。