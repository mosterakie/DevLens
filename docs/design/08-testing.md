# 测试

原则是优先覆盖三类东西：纯逻辑（fingerprint、状态机、LLM 响应解析）、
容易出错的集成点（事务、幂等）、关键路径的端到端。
覆盖率数字本身不是目标。

| 层 | 范围 | 依赖 |
|---|---|---|
| 单元 | fingerprint、状态机、解析 | 无 |
| 集成 | repository、事务、队列 | testcontainers |
| E2E | 完整 analyze 流程 | mock LLM |

## fingerprint

最重要的一个包。表驱动测试覆盖 [归一化规则](./04-fingerprint.md)，另外需要：

- 空日志 / 纯空白 → 报错
- 非 UTF-8 字节 → 不 panic
- 超长输入 → 不 panic
- 幂等性：`normalize(normalize(x)) == normalize(x)`，不幂等说明规则之间有干扰

还有一类容易被忽略的测试 —— **不同的错误不能被合并**：

```go
func TestDistinctErrorsHaveDifferentHash(t *testing.T) {
    cases := []string{
        "connection refused",
        "connection reset",
        "context deadline exceeded",
        "context canceled",
    }
    seen := map[string]string{}
    for _, c := range cases {
        h := hex.EncodeToString(fingerprint.Compute(fingerprint.Normalize(c)))
        if prev, ok := seen[h]; ok {
            t.Errorf("%q and %q collapsed to same hash", prev, c)
        }
        seen[h] = c
    }
}
```

只测"相同的相同"会漏掉"归一化过松导致全部合并"的 bug。这类反向断言
比正向断言更能防止规则退化。

## 状态机

穷举合法和非法转移，非法的要确认返回错误：

```go
{ANALYZING,     OPEN,          false},
{ANALYZING,     FAILED,        false},
{OPEN,          INVESTIGATING, false},
{INVESTIGATING, RESOLVED,      false},
{RESOLVED,      ANALYZING,     true},   // 不能回到分析中
{RESOLVED,      OPEN,          true},   // 不能重开
{FAILED,        ANALYZING,     false},  // 重试允许
```

同时验证"写 event"这个副作用，转移不写 event 是常见 bug。

## Repository / 事务

用 testcontainers 起真实的 PG 和 Redis。需要覆盖：

- `FindRelatedIncidents` 排除自己（`id <> $2`）
- `FindRelatedIncidents` 排除 `FAILED` 的记录
- `ON CONFLICT` 的幂等性：同一个 incident 处理两次只留一行
- 事务回滚：写 analysis 失败时，incident 状态不能被改

最后一条最容易漏，状态更新和 analysis 写入必须在同一个事务里。

## LLM 客户端

用接口隔离，测试注入假实现，不调真实 API：

```go
type Analyzer interface {
    Analyze(ctx context.Context, in AnalyzeInput) (*Analysis, error)
}
```

| 假应答 | 验证行为 |
|---|---|
| 正常 JSON | 解析成功 |
| 被 ```json 包裹 | 能剥掉 fence |
| JSON 缺字段 | 校验失败，触发重试 |
| 非法 JSON | 重试后标 FAILED |
| 超时 | 被 context 取消 |
| confidence 高但 evidence 为空 | 置信度被降级 |

markdown fence 那项是实际中最常遇到的 —— LLM 很爱在 JSON 外面包代码围栏。

## API 契约

固化几个前端依赖的行为：

- `POST /analyze` 返回 202
- 日志过短返回 400
- 触发限流返回 429
- 分析中 `GET /incidents/:id` 返回 200 且 `analysis` 为 null

最后一条尤其要测，防止有人把它"优化"成 425 打挂前端轮询。

## E2E

一个就够：提交 → 轮询到 OPEN → 检查 events 至少有两条。

## 命令

```bash
go test ./...                     # 单元
go test -race ./...               # 竞态检测，需要 gcc
go test -tags=integration ./...   # 集成，需要 docker
```

`-race` 需要 CGO 和一个 C 编译器。Windows 开发机上如果没有 gcc，
本地跑不了，这条要在 CI 里保证执行 —— api 和 worker 共享代码，
竞态可能只在并发下暴露，本地串行测试看不到。

## 不测的

前端视觉、Gin 框架自身行为、sqlc 生成的代码。