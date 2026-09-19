# 产品边界

## 一句话定位

开发者粘贴一段错误日志 / stack trace，DevLens 返回结构化诊断，
并找出历史上出现过的同类问题。

## 核心场景

```
打开网址 → 点 Try Demo → 粘一段错误 → 点 Analyze → 看到结果
```

这条路径必须零配置、零登录就能走完，它是整个项目的验收标准。

## v1 范围

### 页面（4 个）

| # | 页面 | 职责 |
|---|---|---|
| 1 | Landing | 定位 + 三个能力卡片 + 入口 |
| 2 | Analyze | 核心页面：左输入，右结构化结果 |
| 3 | Incident List | 列表 + 过滤 |
| 4 | Incident Detail | 原始日志 + AI 分析 + Timeline |

### 诊断输出字段

前后端共同依赖这个结构：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | int64 | Incident ID |
| `title` | string | 从日志提炼的短标题 |
| `severity` | enum | `LOW` / `MEDIUM` / `HIGH` / `CRITICAL` |
| `category` | string | 如 `Database / Timeout` |
| `summary` | string | 根因描述 |
| `possible_causes` | string[] | 候选原因 |
| `evidence` | Evidence[] | 日志中的具体证据 |
| `suggested_actions` | string[] | 排查步骤 |
| `related_incident_ids` | int64[] | 同类历史问题 |
| `confidence` | float 0-1 | 置信度 |

`evidence` 的结构：

```json
{ "key": "request timeout", "value": "5s", "source_line": 12 }
```

`possible_causes` 用复数是因为 AI 对日志的判断本质是猜测。
给单一根因会让用户误以为确定；给候选列表加 `confidence`，
用户才能自己判断。这是产品设计上的取舍，不是技术限制。

## v1 不做

| 不做 | 原因 |
|---|---|
| 做成 Sentry | 范围太大 |
| 多 Agent / MCP 编排 | 复杂度高，收益不明显 |
| 微服务 | 单体足够 |
| Kubernetes | 部署复杂度与项目价值无关 |
| 真实日志接入（SDK / Webhook） | v1 用粘贴，接入是 v2 的事 |
| 用户注册登录 | Demo 免登录，鉴权用匿名 session |
| 团队 / 权限 / RBAC | 无协作场景 |
| Embedding 语义检索 | v1 用 fingerprint 精确匹配 |
| 告警通知 | 与核心流程无关 |
| 日志聚合 / 全文检索 | 收益低 |

## Landing 文案

```
AI Diagnosis        - 把非结构化日志变成结构化诊断
Related Incidents   - 同类错误自动归类
Incident History    - 从 OPEN 到 RESOLVED 的完整生命周期
```

## 完成标准

1. 陌生人拿到网址，60 秒内走完 Demo，不需要额外解释
2. 粘贴的日志能被归到某个 `category`，`confidence` 有意义的波动
3. 相同类型的错误，第二次提交时能命中 `related_incidents`