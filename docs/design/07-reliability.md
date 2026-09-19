# 可靠性

## 限流

用 Redis 滑动窗口：

```
ZREMRANGEBYSCORE key 0 (now - window)   清理过期
ZCARD key                                计数
ZADD key now now                         记录本次
EXPIRE key window
```

不用固定窗口是因为它在边界处允许 2 倍突刺（第 59 秒 10 次 + 第 61 秒 10 次）。
不用令牌桶是因为令牌桶允许突发，而 LLM 调用需要的是平滑。

限流分两个维度：IP 防单用户刷，全局保护 LLM 配额（IP 可以被换掉，全局计数是兜底）。

**Redis 挂掉时 fail-open（放行），只记日志。** 限流是保护性设施而不是功能性设施，
fail-closed 会让整个产品不可用 —— 那是拿可用性换保护。代价是这段时间可能
打爆 LLM 配额，缓解手段是 LLM 客户端自身有并发上限。

## 超时

| 层 | 超时 |
|---|---|
| HTTP 读请求体 | 10s |
| Handler 总超时 | 15s |
| DB 查询 | 3s |
| Redis 命令 | 500ms |
| LLM 调用 | 25s |
| LLM 连接建立 | 5s |
| 整个分析任务 | 60s |

LLM 取 25s 而不是 30s，是因为整个任务预算 60s，要留出 2 次重试加 DB 写入的空间。

`context.WithTimeout` 需要从 handler 一路传到 repository，
任何一处忘记传 `ctx`，超时就失效。

## 重试

| 错误类型 | 重试 |
|---|---|
| 网络超时 | ✅ |
| 429 | ✅ 带退避 |
| 500 / 502 / 503 | ✅ |
| 400 / 401 / 403 | ❌ 重试也不会成功 |
| JSON 解析失败 | ✅ 换 prompt 后重试 |

退避用指数加 jitter：

```go
delay := time.Second * (1 << attempt)
jitter := time.Duration(rand.Int63n(int64(delay) / 2))
time.Sleep(delay + jitter)
```

加 jitter 是因为多个 worker 同时失败会同时重试，形成新的尖峰。
最多重试 2 次，超过就标 FAILED。

LLM 返回非法 JSON 是这类系统特有的失败模式，处理方式是逐步收紧约束：

```
尝试 1  正常 prompt
尝试 2  追加 "Respond with ONLY valid JSON, no markdown fences"
尝试 3  temperature=0 + 更严格的 schema 提示
失败    标记 FAILED，保留 raw_response
```

## 幂等

worker 可能重复消费同一条消息，所以处理必须幂等。
最终靠 DB 的唯一约束，而不是应用层检查 ——
应用层"先查再写"有 TOCTOU 竞态，两个 worker 可能同时判断"没有"。

```sql
INSERT INTO incident_analysis (...) VALUES (...)
ON CONFLICT (incident_id) DO UPDATE SET ...
```

## AI 判错了怎么办

AI 判错是必然的，所以设计目标不是消除它，而是控制它的影响。

### 产品上不给出单一定论

`possible_causes` 用复数，配 `confidence` 字段。如果 AI 直接说"根因是 X"，
用户可能就信了；说"可能原因是 [X, Y, Z]，置信度 0.84"，用户会自己判断。

`evidence` 字段引用日志原文，让输出可验证 ——
用户能核对"AI 说的 5s 超时确实在日志第 12 行"。
没有 evidence 的诊断是不可验证的断言。

### 让判错可度量

收集用户反馈（准确 / 不准确 / 修正），存表：

```sql
CREATE TABLE analysis_feedback (
    id                 BIGSERIAL PRIMARY KEY,
    incident_id        BIGINT NOT NULL REFERENCES incidents(id),
    rating             SMALLINT,          -- -1 / +1
    corrected_category TEXT,
    corrected_severity severity_t,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

有了这张表才能回答"准确率多少""哪类最容易判错""改 prompt 后有没有变好"。
v1 可以先不做，但这是让模型效果可迭代的前提。

### 保留人工修正的痕迹

用户可以手动改分析结果。改的时候写 `incident_events` 记录原值，
AI 原始输出保留在 `raw_response` 里不覆盖。直接覆盖会失去
"AI 当时说了什么"的记录，之后就没法评估了。

### 高置信度必须有证据

解析后加一条业务规则：如果 `confidence > 0.9` 但 `evidence` 为空，
降级置信度。这是代码里的校验，不能只靠 prompt 约束。

## Redis 队列的已知局限

| 局限 | 影响 | 缓解 |
|---|---|---|
| 无消费确认 | worker 崩溃会丢任务 | `BRPOPLPUSH` 到 processing list，完成后 LREM |
| 无消费位点 | 无法重放 | v1 不需要 |
| 无死信队列 | 反复失败的任务堆积 | 直接标 FAILED，靠 DB 查询发现 |
| 单点无 HA | Redis 挂则无法入队 | AOF 持久化 + 启动时扫超时任务 |

兜底机制是 worker 启动时重新入队超时的任务：

```sql
SELECT id FROM incidents
WHERE status = 'ANALYZING' AND created_at < now() - interval '5 minutes';
```

## 优雅关闭

收到 SIGTERM 后：停止接受新请求，等当前任务完成，再关闭连接池。
不优雅关闭会导致 worker 写到一半被杀，虽然幂等设计能缓解，
但清理在途任务是必要的。