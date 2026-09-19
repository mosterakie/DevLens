# 部署与运行

## 本地开发

```bash
docker compose up -d      # postgres + redis
go run ./cmd/api          # :8080
go run ./cmd/worker       # 无端口
```

## docker-compose.yml

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: devlens
      POSTGRES_PASSWORD: devlens
      POSTGRES_DB: devlens
    ports: ["5432:5432"]
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U devlens"]
      interval: 5s

  redis:
    image: redis:7-alpine
    command: ["redis-server", "--appendonly", "yes"]
    ports: ["6379:6379"]
    volumes:
      - redisdata:/data

volumes:
  pgdata:
  redisdata:
```

`--appendonly yes` 是必须的。默认 Redis 只在 RDB 快照点持久化，
崩溃会丢掉队列里未处理的任务。

## 环境变量

```bash
# 必填
DATABASE_URL=postgres://devlens:devlens@localhost:5432/devlens?sslmode=disable
REDIS_URL=redis://localhost:6379/0
LLM_API_KEY=sk-...
LLM_MODEL=gpt-4o-mini
LLM_BASE_URL=https://api.openai.com/v1

# 可选
PORT=8080
LOG_LEVEL=info
PROMPT_VERSION=v1
RATE_LIMIT_ANALYZE_PER_MIN=10
RATE_LIMIT_GLOBAL_PER_MIN=200
ANALYZE_TIMEOUT=25s
```

启动时校验必填项，缺 `LLM_API_KEY` 就立刻退出并说明缺哪个，
不要等到第一个请求才 500。

## 迁移

```bash
migrate -path ./migrations -database "$DATABASE_URL" up
```

迁移在 api 启动时自动执行，用 advisory lock 防止多实例并发迁移。
生产环境更稳妥的做法是独立 job。

## Dockerfile

```dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/api /api
COPY --from=build /out/worker /worker
COPY --from=build /src/migrations /migrations
USER nonroot:nonroot
ENTRYPOINT ["/api"]
```

`CGO_ENABLED=0` 得到静态二进制，可以跑在 distroless 上（没有 shell，
攻击面小），非 root 用户是最小权限。

## 部署目标

| 方案 | 说明 |
|---|---|
| Fly.io | 推荐。能跑 api + worker 两个进程组，Postgres / Redis 有托管件 |
| Railway | 简单，但 Redis 要额外配 |
| Render | 免费层会休眠，demo 体验差 |
| VPS + compose | 完全可控，要自己运维 |

选 Fly.io 的理由是它能同时跑两个进程，且中间件都是托管的，
部署配置的复杂度不至于盖过项目本身。

```toml
# fly.toml
[[services]]
  internal_port = 8080
  [[services.http_checks]]
    path = "/readyz"

[processes]
  api = "/api"
  worker = "/worker"
```

## 可观测性

v1 需要的最小集：

1. 结构化 JSON 日志，带 `request_id` 贯穿 api 到 worker
2. `/metrics`（Prometheus）：提交量、分析耗时直方图、LLM 失败分类、队列积压
3. `/healthz` 和 `/readyz` 分离

`request_id` 是重点。异步链路的排障如果没有它，
用户报"分析结果不对"时根本定位不到 worker 的日志。

## 日志里不要打原始日志内容

`raw_log` 可能包含用户数据、凭据、内网地址。结构化日志里只打
`incident_id`，需要内容时去 DB 取。

## 成本控制

LLM 是唯一的可变成本。全局限流 200/min、限制 max tokens、
输入日志截断至 2000 字符参与分析（`raw_log` 仍完整存储）、
记录每次调用的 token 数。