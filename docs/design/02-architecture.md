# 架构

## 技术栈

| 层 | 选型 |
|---|---|
| 语言 | Go 1.22+ |
| HTTP | Gin |
| DB 驱动 | pgx (v5) |
| 查询 | sqlc |
| 缓存 / 队列 | go-redis |
| 数据库 | PostgreSQL |
| 迁移 | golang-migrate |

选 sqlc 而不是 GORM：GORM 的错误要到运行时才暴露，改了表结构编译仍能通过。
sqlc 让 SQL 成为编译期契约。代价是多一步 `sqlc generate`。

## 分层

```
cmd/
  api/main.go        组装依赖，无业务逻辑
  worker/main.go
internal/
  handler/       HTTP：绑定、校验、序列化
  service/       业务编排、事务边界、状态机转移
  repository/    sqlc 生成 + 手写封装
  domain/        纯结构体 + 枚举 + 校验
  analyzer/      AI 分析：Prompt、解析、置信度
  fingerprint/   日志归一化与指纹
  queue/         Redis 任务队列
  middleware/    限流、日志、恢复、匿名 session
  worker/        异步消费者
```

依赖方向单向向内：`handler → service → repository → domain`。
`fingerprint` 和 `analyzer` 不依赖 DB / Redis / HTTP，所以能脱离容器测试。

## Analyze 请求流程

分两个阶段。同步段只做归一化和同类匹配，AI 调用异步。

### 阶段 1 — 同步（目标 < 200ms）

```
POST /api/v1/incidents/analyze
  1. 限流（Redis 滑动窗口）
  2. 校验日志长度与编码
  3. fingerprint.Normalize(rawLog)
  4. fingerprint.Compute(normalized)
  5. 查同 fingerprint 的历史 Incident
  6. INSERT incident (status = ANALYZING)
  7. Redis LPUSH analyze:queue {incident_id}
  8. 202 Accepted { id, status: "ANALYZING" }

前端轮询 GET /api/v1/incidents/:id（1s 间隔）
```

### 阶段 2 — 异步（目标 < 30s）

```
worker 消费 analyze:queue
  1. 调 LLM（超时 25s）
  2. 解析 JSON，校验 schema
  3. 失败最多重试 2 次
  4. 事务内写 incident_analysis + incident_events，更新 status
  5. 超限则 status = FAILED，写事件记录原因
```

### 为什么异步

LLM 调用 P50 约 3–8s，P99 可能 30s+。同步等待会让用户看着转圈，
且用户关闭浏览器就等于分析丢失。异步之后：

- 立即拿到 ID，前端可以展示进度、刷新、走开再回来
- 任务持久化，失败可重试，与用户是否在线解耦
- 100 个人同时点 Analyze 时，超额请求排队而不是打满 LLM 配额
- worker 无状态，加机器就是加并发

不用 goroutine 直接跑是因为它活在进程内存里，重启即丢，且无法跨实例分配负载。

不用 Kafka / RabbitMQ 是因为已经在用 Redis 做限流和缓存，不引入第二个中间件。
代价是队列语义更弱（无消费位点、无重放），见 [可靠性](./07-reliability.md)。

## 状态机

```
ANALYZING ──┬──> OPEN ──> INVESTIGATING ──> RESOLVED
            └──> FAILED ──(重试)──> ANALYZING
```

转移只在 service 层发生，每次转移写一条 `incident_events`。
非法转移（如 `RESOLVED → ANALYZING`）返回 `409 Conflict`。

把状态变更当事件流存，天然得到 Timeline 和审计日志。

## 组件拓扑

```
Browser ──HTTP──> api (Go, :8080) ──> PostgreSQL (:5432)
                       │                  Redis (:6379)
                       │
                 worker (Go, 无端口) ──> LLM API
```

api 与 worker 共享代码和领域层，但作为不同进程运行。
分进程的好处是可以独立重启、扩容、观测，worker 挂了不影响用户访问。

## 目录结构

```
DevLens/
  cmd/api, cmd/worker
  internal/{handler,service,repository,domain,analyzer,fingerprint,queue,middleware,worker}
  migrations/
  web/
  docker-compose.yml
  sqlc.yaml
```