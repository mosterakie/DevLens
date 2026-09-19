# 里程碑

| # | 里程碑 | 产出 | 可验证 |
|---|---|---|---|
| M0 | 骨架 | 仓库结构、docker-compose、迁移 | ✅ 已完成 |
| M1 | 纯逻辑 | fingerprint + domain（状态机） | ✅ 已完成，`go test ./...` 全绿 |
| M2 | 同步链路 | POST /analyze、fingerprint 命中同类 | ✅ 已完成 |
| M3 | 异步链路 | worker + 分析器 + 写 analysis | ✅ 已完成 |
| M4 | 前端 4 页 | Landing / Analyze / List / Detail | ✅ 已完成 |
| M5 | 加固 | 限流、超时、重试、`/readyz`、`/metrics` | ✅ 已完成 |
| M6 | 部署 | 公网可访问 | 陌生人 60 秒走完 Demo |

顺序不要调换，尤其 M1 要在 M2 之前 —— fingerprint 是后面所有设计的基础，
写错了要返工。

## M1 ✅

已实现：

```
internal/fingerprint/
  normalize.go        16 条规则表 + 截断
  hash.go             SHA-256 截断到 128 位
internal/domain/
  status.go           状态枚举 + CanTransitionTo
  severity.go         严重程度枚举 + ParseSeverity
  incident.go         Incident / Analysis / Evidence + 校验
```

实现中确认的两个细节，已回写到 04-fingerprint.md：

- 占位符必须全小写。转小写若发生在替换之后，第二次调用会改写占位符，
  幂等性被破坏。幂等性测试正是抓到这一点的原因。
- 转小写若发生在匹配之前，ISO8601 的正则必须匹配小写 `t`/`z`，
  否则时间戳会退化成 `<n>-<n>-<n>` 并被后续规则进一步拆散。

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