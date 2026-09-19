# DevLens

把一段错误日志变成结构化诊断，并找出历史上出现过的同类问题。

## 它是怎么工作的

提交的日志先经过归一化，算出一个指纹；同指纹的历史记录会被找出来，
这一步是同步的，所以提交后立刻能看到"这个问题以前出现过"。
AI 诊断放到队列里异步执行，因为模型调用耗时不可控。

```
POST /analyze ──> 归一化 ──> 指纹 ──> 查同类 ──> 入库 ──> 入队
                                                    │
                                                    └──> 202 Accepted
                                                              │
                            轮询 GET /incidents/:id <─────────┘
                                    ▲
                                    │
                        worker 消费队列 ──> AI 诊断 ──> 落库 ──> OPEN
```

## 运行

需要 PostgreSQL 和 Redis。用 Docker 的话：

```bash
docker compose up -d
```

已经有这两个服务的话，跳过上面这步，直接配好连接信息。

```bash
psql "$DATABASE_URL" -f migrations/0001_init.up.sql

go run ./cmd/api      # :8080
go run ./cmd/worker   # 无端口
```

配置见 `.env.example`。

### 配 LLM

`LLM_API_KEY` 留空时 worker 会退回本地的启发式分析器（关键词匹配）。
这样整条链路在没有外部依赖的情况下也能端到端跑通，便于本地开发和验收。

接真实模型时，在 `.env` 里填：

```bash
LLM_API_KEY=sk-xxxxxxxx
LLM_MODEL=deepseek-flash
LLM_BASE_URL=https://api.deepseek.com
LLM_JSON_MODE=true
```

key 从 https://platform.deepseek.com/api_keys 获取。

`LLM_BASE_URL` 用 `https://api.deepseek.com`，它同时兼容
`/chat/completions` 和 `/v1/chat/completions` 两种路径。

`LLM_JSON_MODE=true` 会让服务端保证输出合法 JSON。DeepSeek 的 JSON Output
要求 prompt 里出现 "json" 字样并给出格式示例，系统提示词已经满足。
如果换成不支持该参数的兼容服务，设为 `false`。

`.env` 已在 `.gitignore` 里，不会被提交。

## 试一下

```bash
curl -X POST http://127.0.0.1:8080/api/v1/incidents/analyze \
  -H 'Content-Type: application/json' \
  -d '{"log":"ERROR: context deadline exceeded\ngoroutine 231 [IO wait]:\ndatabase/sql.(*DB).conn(...)"}'
```

返回 202 和一个 id。然后轮询：

```bash
curl http://127.0.0.1:8080/api/v1/incidents/1
```

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/incidents/analyze` | 提交日志，返回 202 |
| GET | `/api/v1/incidents/:id` | 查询详情，分析中时 `analysis` 为 null |
| GET | `/api/v1/incidents` | 列表，游标分页 |
| GET | `/api/v1/incidents/:id/related` | 同类问题（按指纹匹配） |
| GET | `/api/v1/incidents/:id/events` | 状态变更时间线 |
| PATCH | `/api/v1/incidents/:id/status` | 状态转移 |
| GET | `/healthz` `/readyz` | 存活与就绪 |
| GET | `/metrics` | Prometheus 指标 |

## 开发

```bash
go test ./...        # 单元测试
go vet ./...
gofmt -l .
```

`-race` 需要 gcc，没装的话在 CI 里跑。

## 结构

```
cmd/api, cmd/worker
internal/
  handler/       HTTP：绑定、校验、序列化
  service/       业务编排、事务边界、状态机
  repository/    SQL 与数据访问
  domain/        纯结构体、枚举、校验
  analyzer/      AI 诊断与响应解析
  fingerprint/   日志归一化与指纹
  queue/         Redis 队列与限流计数
  middleware/    日志、恢复、限流、请求 ID
migrations/
docs/design/     设计记录
```

依赖方向单向向内，`fingerprint` 和 `analyzer` 不依赖 DB / Redis / HTTP，
可以脱离容器测试。

## 设计记录

`docs/design/` 记录了各部分的取舍，重点是这几份：

- [产品边界](docs/design/01-product-scope.md) — 做什么、不做什么
- [架构](docs/design/02-architecture.md) — 为什么 AI 分析要异步
- [Fingerprint](docs/design/04-fingerprint.md) — 同类判定怎么做
- [可靠性](docs/design/07-reliability.md) — 限流、超时、重试、AI 判错怎么办