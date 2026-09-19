# 数据模型

## 表清单

| 表 | 职责 | v1 |
|---|---|---|
| `users` | 用户 | 建表，只 seed 一个 demo 用户 |
| `incidents` | 核心实体 | ✅ |
| `incident_analysis` | AI 诊断结果 | ✅ |
| `incident_events` | 状态变更 / Timeline | ✅ |
| `comments` | 人工讨论 | 建表，暂不重点做 |
| `embeddings` | 语义向量 | v2 |

## 几个设计决定

### users

Demo 免登录，所以只 seed 一个用户，代码路径不涉及注册登录：

```sql
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         CITEXT UNIQUE NOT NULL,
    password_hash TEXT,                  -- 允许 NULL，demo 用户无密码
    name          TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`password_hash` 允许 NULL 是为了避免给 demo 用户编造假 hash。

### 枚举用 ENUM 不用 TEXT

`severity` 和 `status` 是闭集合，错误值应该被数据库拒绝，而不是等 Go 代码
`switch` 落到 default 才发现。代价是加值需要 `ALTER TYPE ... ADD VALUE`，
且不能回滚 —— 这几个枚举基本不会变，可以接受。

`category` 例外，它是开放集合（`Database / Timeout` 这类层级串），
用 `TEXT`，因为 AI 可能产出新分类。

### fingerprint 用 BYTEA

存二进制而不是 hex 字符串，省一半空间，比较也更快。
配部分索引，因为 FAILED 的记录没有有效分析，不该出现在同类结果里：

```sql
CREATE INDEX idx_incidents_fingerprint
    ON incidents (fingerprint)
    WHERE status <> 'FAILED';
```

## Schema

```sql
CREATE TYPE severity_t AS ENUM ('LOW', 'MEDIUM', 'HIGH', 'CRITICAL');
CREATE TYPE status_t   AS ENUM ('ANALYZING', 'OPEN', 'INVESTIGATING', 'RESOLVED', 'FAILED');

CREATE TABLE incidents (
    id            BIGSERIAL PRIMARY KEY,
    title         TEXT NOT NULL DEFAULT '',
    raw_log       TEXT NOT NULL,
    normalized    TEXT NOT NULL,          -- 归一化后的日志
    fingerprint   BYTEA NOT NULL,         -- 16 字节，见 04
    severity      severity_t,             -- NULL 直到分析完成
    category      TEXT,
    status        status_t NOT NULL DEFAULT 'ANALYZING',
    is_recurring  BOOLEAN NOT NULL DEFAULT false,
    created_by    BIGINT REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incident_analysis (
    incident_id       BIGINT PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
    summary           TEXT NOT NULL,
    possible_causes   JSONB NOT NULL DEFAULT '[]',
    evidence          JSONB NOT NULL DEFAULT '[]',
    suggested_actions JSONB NOT NULL DEFAULT '[]',
    model             TEXT NOT NULL,
    prompt_version    TEXT NOT NULL,
    confidence        REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    raw_response      TEXT NOT NULL,      -- 原始 LLM 输出，排障用
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incident_events (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,   -- status_changed / analysis_completed / comment
    from_status status_t,
    to_status   status_t,
    payload     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE comments (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    author_id   BIGINT REFERENCES users(id),
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## 为什么这么定

**`incident_analysis` 用 `incident_id` 做主键（一对一）。**
一个 Incident 只需一份分析，重试时 `ON CONFLICT (incident_id) DO UPDATE`
天然幂等，不用额外去重逻辑。

**存 `raw_response`。** 解析失败时这是唯一的排障线索。

**存 `model` 和 `prompt_version`。** 改了 prompt 之后，
旧数据的 `confidence` 还能不能和新数据比？有版本号才能回答。

**`normalized` 和 `raw_log` 都存。** 一个给用户看原貌，一个用于调试归一化规则。

**用 JSONB 而不是多张关联表。** `possible_causes` / `evidence` / `suggested_actions`
从不单独查询、从不跨 Incident 聚合，用 JSONB 换来一次写入一次读出。

## 索引

| 索引 | 用途 |
|---|---|
| `incidents (fingerprint) WHERE status <> 'FAILED'` | 找同类，热路径 |
| `incidents (status, created_at DESC)` | 列表过滤 + 排序 |
| `incidents (created_by, created_at DESC)` | 我的提交 |
| `incident_events (incident_id, created_at)` | Timeline |

## 迁移

只向前，`down` 里不做破坏性操作。Enum 变更单独迁移，不与建表混在一起。