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

# 监听地址。默认 127.0.0.1：进程只在回环上监听，对外由反向代理承担。
# 这样进程本身没有公网暴露面 —— 云安全组被误开放也不会直接可达。
# 容器或反代同机场景需要监听所有网卡时设为 0.0.0.0。
# 留空视为未设置，回退 127.0.0.1（刻意的失败安全方向）。
BIND_ADDR=127.0.0.1

# 允许提供 X-Forwarded-For 的代理地址（逗号分隔）。留空 = 不信任任何代理，
# 客户端 IP 取 TCP 对端地址。只有确实部署了反代才填，否则限流可被伪造头绕过。
# 例：TRUSTED_PROXIES=127.0.0.1
TRUSTED_PROXIES=
LOG_LEVEL=info
PROMPT_VERSION=v1
RATE_LIMIT_ANALYZE_PER_MIN=10
RATE_LIMIT_GLOBAL_PER_MIN=200
ANALYZE_TIMEOUT=25s
```

启动时校验必填项，缺 `DATABASE_URL` 就立刻退出并说明缺哪个，
不要等到第一个请求才 500。

配置文件有两个来源，优先级明确：

1. 真实环境变量（CI、生产、容器编排都用这个）
2. `.env` 文件，仅在对应变量尚未存在时生效

`.env` 只是本地开发的便利，不会被它覆盖真实环境变量，所以不会出现
"镜像里带了旧配置把线上参数顶掉"这类问题。把 `DOTENV_PATH` 设为空
字符串可以完全跳过读取。

## LLM 配置

用 DeepSeek 时：

```bash
LLM_API_KEY=sk-xxxxxxxx
LLM_MODEL=deepseek-flash
LLM_BASE_URL=https://api.deepseek.com
LLM_JSON_MODE=true
```

几点实现上的约束，都是查文档确认过的：

- `LLM_BASE_URL` 填 `https://api.deepseek.com`。该地址同时接受
  `/chat/completions` 和 `/v1/chat/completions`，两种都能用。
- `LLM_JSON_MODE=true` 会发送 `response_format: {"type":"json_object"}`。
  DeepSeek 的 JSON Output 要求 prompt 里出现 "json" 字样并提供格式示例，
  系统提示词已满足；换成不支持该参数的兼容服务时设为 false。
- `LLM_MAX_TOKENS` 默认 4096。实测分析一条普通日志约 2400 tokens，
  2048 会把输出截在半个字符串中间，而截断的 JSON 无法解析且重试无用。
- DeepSeek 文档说明 JSON Output 偶尔返回空 content，所以空响应必须
  触发重试。这一条有对应的测试。

## 迁移

api 与 worker 启动时都会应用迁移，用 PostgreSQL 的 advisory lock
保证多实例同时启动时只有一个真正执行，其余等待后直接跳过。

`internal/migrate` 是手写的，没有引入 golang-migrate：需求很窄——按
文件名顺序执行 up 脚本并记录版本，为此多一个依赖和它自己的一套约定
（如 dirty 状态处理）不值得。

### 几处刻意的设计

**整个迁移在一个事务里执行。** PostgreSQL 支持事务性 DDL，所以迁移
失败时结构不会被改一半、版本也不会被误记。失败的那条会完全回滚，
下次重试从头来。

**记录校验和。** `schema_migrations` 存了每个迁移内容的 SHA-256 前缀。
改了已应用的迁移再启动会报 `ErrChecksumMismatch`，而不是静默地让不同
环境的结构分叉。要改结构就新增一个迁移文件。

**只执行 `*.up.sql`，且命名必须匹配 `NNNN_name.up.sql`。** 命名不合规
的 `.sql` 文件会直接报错而不是被忽略——静默忽略会让"以为加了迁移但
其实没生效"变成很难查的问题。

### 从手动建库升级

如果库的结构是手动执行 `psql -f migrations/*.up.sql` 建的，没有
`schema_migrations` 记录，直接启动会失败（`CREATE TYPE` 之类的语句
遇到已存在的对象会报错）。这时需要把库标记为已应用某个版本：

```bash
go run ./cmd/devcheck -baseline 3
```

它会先校验结构——检查若干必须存在的表——通过后才写入记录。空库上
执行会被拒绝，因为把空库标成"已迁移"会让后续迁移建立在错误前提上，
而且报错会出现在很久以后。

确实需要跳过校验时加 `--force`，但这只应用于你清楚知道结构状态的
情况。

### 生产环境

启动时自动迁移适合单实例或滚动更新。如果发布流程要求迁移与代码部署
分离，把 `migrations/` 交给独立的 job 执行即可——`go run ./cmd/devcheck`
也会报告迁移状态，可以当作部署前的检查步骤。


## 监听地址与反向代理

api 默认只监听 `127.0.0.1`（`BIND_ADDR`），**不监听公网网卡**。

这是刻意的：进程本身没有公网暴露面。云安全组是配置在服务器之外的一层，
改错、误开放、或换机房时忘记收紧都不该让进程直接可达。把进程绑在回环上，
即使安全组放行了 8080，外部仍然连不上 —— 保护不再只依赖单一防线。

对外访问走同机反向代理（nginx / caddy），由它负责 TLS、域名和限流：

```nginx
server {
    listen 443 ssl;
    server_name devlens.example.com;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

三点注意：

- **必须设置 `TRUSTED_PROXIES`**，否则反代传的 IP 会被忽略。
  api 默认不信任任何代理，客户端 IP 一律取 TCP 对端地址 —— 这能防住
  伪造 `X-Forwarded-For` 绕过限流，但在反代后面会让所有用户都算成
  反代自己的 IP（限流误伤）。两种部署方式对应：

  ```bash
  # 反代与 api 同机
  TRUSTED_PROXIES=127.0.0.1
  # 反代在另一台机器
  TRUSTED_PROXIES=10.0.0.5
  ```

- nginx 要传 `X-Real-IP` / `X-Forwarded-For`（见上面的配置），
  否则限流看到的是反代地址。

- `BIND_ADDR=0.0.0.0` 只在反代位于另一台机器时才需要。那时**必须**靠安全组
  限制只有反代能连 api 端口，不能对全网开放 —— 否则 api 又直接暴露了。

## 直接暴露端口时（无反代）

没有反代、直接用 `http://IP:8080` 访问是可行的，改动只有一处：

```bash
BIND_ADDR=0.0.0.0
TRUSTED_PROXIES=        # 保持留空
```

`TRUSTED_PROXIES` 留空这件事在这个模式下**是安全前提，不是可选**：
入口没有任何代理，所有请求都直连 api，此时若信任 `X-Forwarded-For`，
任何调用方都能每次换一个伪造 IP 绕过限流（实测可复现）。留空后
`ClientIP()` 只认 TCP 对端地址，伪造头失效。

这个模式没有 TLS、也没有鉴权，任何能访问该端口的人都能提交日志并消耗
LLM 配额。仅建议用于临时演示；长期使用请走上面的反向代理模式。

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
