# 同类问题检索

用户提交一条日志，系统要回答：这个问题以前出现过吗？

## 两条路线

**A. Fingerprint 精确匹配**（v1 采用）

```
提交 → normalize → hash → WHERE fingerprint = $1
```

一次索引查询，延迟 < 5ms，同输入必同结果。局限是只能匹配归一化后
完全一致的日志。

**B. Embedding 语义检索**（v2）

```
提交 → embedding 模型 → 向量 → pgvector 相似度 top-K
```

能匹配"措辞不同、根因相同"，但每次提交多一次模型调用
（延迟 100–500ms），且结果不确定 —— 模型版本变了就会变。

## v1 选 A 的理由

Demo 的叙事是"粘一条错误 → 得到诊断 → 看到历史上出现过 3 次"。
只要同类日志重复提交能命中，故事就完整，A 已经能做到。

引入 B 意味着两次 AI 调用（嵌入 + 诊断），失败模式翻倍
（嵌入超时怎么办、重试哪一步），而它解决的问题当前并不紧迫。

## 实现

```sql
SELECT id, title, severity, status, created_at
FROM incidents
WHERE fingerprint = $1
  AND id <> $2
  AND status <> 'FAILED'
ORDER BY created_at DESC
LIMIT 5;
```

`id <> $2` 排除自己，否则刚插入的记录会匹配到自己。
`LIMIT 5` 是因为 UI 上超过 5 条没有意义。

`is_recurring` 在插入前查一次计数得出，为 true 时 UI 打 RECURRING 标记，
用户立刻明白这不是新问题。

## 已知局限

fingerprint 匹配不到但确实是同类错误的情况，v1 会漏。可能的缓解方向：

1. 放宽归一化规则 —— 会引入错合，见 [Fingerprint](./04-fingerprint.md)
2. 用 `normalized` 字段做 SQL 粗召回
3. v2 的向量检索

当前选择是**确定性优先于召回率**：用户看到"没有相关历史"可以接受，
看到"错误的相关历史"才会失去信任。

## v2 设想

```sql
CREATE EXTENSION vector;

CREATE TABLE incident_embeddings (
    incident_id BIGINT PRIMARY KEY REFERENCES incidents(id) ON DELETE CASCADE,
    embedding   vector(1536) NOT NULL,
    model       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON incident_embeddings
    USING hnsw (embedding vector_cosine_ops);
```

检索时合并两路结果：fingerprint 命中权重 1.0，向量相似度 > 0.85 权重 0.7，
去重后排序。混合检索比纯向量好，因为 fingerprint 命中的是"确定同类"，
向量命中的是"可能同类"，两者语义强度不同。

表名用 `incident_embeddings` 而不是 `embeddings`，因为未来可能还要
对评论做检索，泛化的名字会变成债。