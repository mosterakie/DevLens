# v0.1.0

第一个可工作的版本。粘贴一段错误日志，得到结构化诊断，并找出历史上出现过的同类问题。

## 它能做什么

打开 `http://127.0.0.1:8080`，粘贴一段日志，点分析。几秒后看到严重程度、
分类、可能原因、带行号的证据、建议排查步骤，以及同类的历史记录。

**同类判定**不靠文本相似度。日志先经过归一化——抹掉时间戳、IP、UUID、
goroutine 号、文件行号、端口、内存地址和其余数字——再计算指纹。所以
同一类错误即使时间、地址、行号都变了，仍然能认出来。

**AI 分析是异步的**。提交后立刻返回 202 和一个 ID，模型调用在队列里
进行，前端轮询结果。这样用户不必盯着转圈，失败也能重试。

## 怎么跑起来

需要 PostgreSQL 和 Redis。最快的方式：

```bash
docker compose --profile full up
```

它会启动数据库、缓存、api 和 worker。`LLM_API_KEY` 留空时 worker 会
退回本地启发式分析器，整条链路仍能端到端跑通，只是判断依据变成关键词
匹配。

在已有的 PostgreSQL 与 Redis 上跑：

```bash
go run ./cmd/devcheck          # 检查依赖、配置、端口
go run ./cmd/api               # 一个窗口
go run ./cmd/worker            # 另一个窗口
```

配置见 `.env.example`，复制成 `.env` 即可，两个进程启动时都会读取。
真实环境变量优先于文件内容。

## 技术要点

- Go 1.27，Gin + pgx + go-redis，PostgreSQL 与 Redis
- 指纹用规则表归一化加 SHA-256 截断到 128 位
- api 与 worker 共享领域层，作为不同进程运行
- 限流用 Redis 滑动窗口，Redis 故障时放行而不是让核心功能不可用
- worker 启动时扫超时的分析任务重新入队，弥补队列没有消费确认

## 验证情况

139 个测试，含 19 个针对真实 PostgreSQL 的集成测试。CI 在每次 push 时
执行格式检查、vet、竞态检测、集成测试和镜像构建。

竞态检测只在这一版由 CI 覆盖——Windows 上 Go 的 cgo 只支持 gcc 不支持
MSVC，本地无法运行。

真实日志样本取自公开的 GitHub issue，覆盖 Go、Java、Python、Node.js
以及 DNS、数据库、网络等场景，用于验证归一化规则不会把无关故障混为
同类。

## 已知限制

诚实列出，避免误用。

- **没有部署到公网**。只在本地和容器里验证过。
- **数据库迁移在启动时自动执行**。用 advisory lock 防止多实例并发，
  校验和防止历史迁移被改动。从手动建库升级的场景需要先跑一次
  `go run ./cmd/devcheck -baseline <版本>`。
- **前端未经人工验收**。用无头浏览器确认过渲染，但没有真实的点击操作。
- **没有做压测**。高并发下的表现未知。
- **分类是模型的自由文本**，格式不统一。所以界面上叫"标签"，不做
  归一化，也不适合当过滤条件。
- **AI 判断可能出错**。产品上用候选原因列表加置信度呈现，并保留可
  核对的证据，让用户自己判断。置信度会随信息量波动：证据充分时给
  0.8 以上，信息不足时会降到 0.4 并说明缺什么。

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/incidents/analyze` | 提交日志，返回 202 |
| GET | `/api/v1/incidents/:id` | 详情，分析中时 `analysis` 为 null |
| GET | `/api/v1/incidents` | 列表，游标分页 |
| GET | `/api/v1/incidents/:id/related` | 同类问题 |
| GET | `/api/v1/incidents/:id/events` | 状态时间线 |
| PATCH | `/api/v1/incidents/:id/status` | 状态转移 |
| GET | `/healthz` `/readyz` `/metrics` | 健康检查与指标 |

## 设计记录

`docs/design/` 记录了各部分的取舍，重点看这几份：

- `01-product-scope.md` — 做什么、不做什么
- `02-architecture.md` — 为什么 AI 分析要异步
- `04-fingerprint.md` — 同类判定怎么做
- `07-reliability.md` — 限流、超时、重试、AI 判错怎么办
- `11-verification.md` — 实际跑过的验证与发现的问题