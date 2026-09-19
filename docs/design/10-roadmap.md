# 里程碑

| # | 里程碑 | 产出 | 可验证 |
|---|---|---|---|
| M0 | 骨架 | 仓库结构、docker-compose、迁移 | `docker compose up` + `migrate up` 成功 |
| M1 | 纯逻辑 | fingerprint + domain（状态机） | `go test -race ./internal/...` 全绿 |
| M2 | 同步链路 | POST /analyze、fingerprint 命中同类 | 能看到 `related_incident_ids` |
| M3 | 异步链路 | worker + LLM 调用 + 写 analysis | 轮询看到 status=OPEN 和诊断 |
| M4 | 前端 4 页 | Landing / Analyze / List / Detail | 能独立走完 Demo |
| M5 | 加固 | 限流、超时、重试、`/readyz`、`/metrics` | 429 可复现，指标有值 |
| M6 | 部署 | 公网可访问 | 陌生人 60 秒走完 Demo |

顺序不要调换，尤其 M1 要在 M2 之前 —— fingerprint 是后面所有设计的基础，
写错了要返工。

## M1

```
internal/fingerprint/
  normalize.go        规则表驱动
  hash.go             SHA-256 + 截断
  normalize_test.go   表驱动 + 幂等 + 反向断言
internal/domain/
  status.go           状态枚举 + ValidTransition
  incident.go         纯结构体 + 校验
```

完成标准：`go test -race ./internal/...` 全绿。

## M2

```
migrations/0001_init.up.sql
internal/repository/
internal/service/incident.go   事务：查同类 → 插入 → 入队
internal/handler/incident.go
internal/queue/redis.go
```

完成标准：同一段日志提交两次，第二次 `is_recurring=true`。

## M3

```
internal/analyzer/
  prompt.go    模板 + 版本号
  client.go    调用、超时、重试
  parse.go     剥 fence、schema 校验、置信度降级
internal/worker/analyze.go
cmd/worker/main.go
```

完成标准：非法 JSON 的假应答能重试后标 FAILED，且 `raw_response` 有值。

## M4

前端先用最简方案（HTML + 少量 JS，或 Svelte / Preact），
不引入重型构建链。

Analyze 页的交互：

```
1. 点 Analyze → 按钮禁用，显示 Analyzing 和已用时
2. 拿到 id，开始 1s 轮询
3. analysis 非空 → 停止轮询，渲染
4. 超过 60s → 提示仍在分析，停止轮询
5. incident_id 写进 URL，可分享可刷新
```

第 5 条让结果页可以分享，看到结果的人能直接把链接发给别人。

## M5

```
internal/middleware/
  ratelimit.go   Redis 滑动窗口，fail-open
  requestid.go
  recover.go
  logger.go
```

## 排到 v2 的

| 功能 | 原因 |
|---|---|
| Embedding 语义检索 | v1 精确匹配已足够，见 [同类检索](./05-related-incidents.md) |
| `analysis_feedback` 表 | 有价值但非必需 |
| 真实日志接入 | v1 用粘贴 |
| 用户注册登录 | Demo 免登录 |
| 全文检索 | 收益低 |

## 从 fingerprint 开始

它是纯函数，不需要 DB / Redis / LLM 就能写完并验证。
在没有基础设施的情况下就能产出可验证的代码，是启动项目最省力的切入点。

```bash
git switch -c feat/fingerprint
# 写 internal/fingerprint/normalize.go 和测试
go test -race ./internal/fingerprint/...
```

## 进度记录

每完成一个里程碑打个 tag：

```bash
git tag -a m1-fingerprint -m "fingerprint + domain 完成"
```