# DevLens

[![CI](https://github.com/mosterakie/DevLens/actions/workflows/ci.yml/badge.svg)](https://github.com/mosterakie/DevLens/actions/workflows/ci.yml)

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
docker build -t devlens:local .
docker compose up -d
```

已经有这两个服务的话，跳过上面这步，直接配好连接信息。

```bash
psql "$DATABASE_URL" -f migrations/0001_init.up.sql

go run ./cmd/api      # :8080
go run ./cmd/worker   # 无端口
```

配置见 `.env.example`，复制成 `.env` 即可，api 和 worker 启动时会自动读取。
真实环境变量优先于文件内容，所以 CI 里直接导出变量就能覆盖本地配置。

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
go test -short ./...       # 单元测试
go test ./...              # 含集成测试，需要 PostgreSQL
go vet ./...
gofmt -l .
node scripts/check-i18n.js # 界面词条完整性
```

`-race` 需要在 CI 中执行。Windows 上 Go 的 cgo 只支持 gcc 不支持 MSVC，
即使本机装了 Visual Studio 也无法运行竞态检测。

CI 配置见 `.github/workflows/ci.yml`，包含竞态检测、集成测试和镜像构建。

集成测试需要一个专用的测试库（默认 `devlens_test`）：

```bash
createdb -O devlens devlens_test
```

它每次都会清空该库的全部表，所以库名不以 `_test` 结尾时会直接拒绝运行。

界面支持中英双语，默认跟随浏览器，可在页头切换。诊断内容也会用同一种
语言生成：提交时的语言记入 `incidents.lang`，worker 读取它来构造提示词。

`scripts/check-i18n.js` 校验两份词条的 key 集合一致、中文没有未翻译的
英文残留、没有空词条。漏翻不会报错，只会让界面上显示词条的 key，
所以单独做成可执行的检查。

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