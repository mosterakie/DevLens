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

## 实现：hybrid（精确指纹 + 词集相似度）

v1 上线后实测发现：只用精确指纹会漏掉大量真正同类的问题。
同一个 PostgreSQL 连接故障，换了 IP、环境名和文案（`error=eof` →
`error=unexpected eof`、`retrying in 1s` → `backing off`）之后，
归一化结果不同、指纹不同，**完全匹配不上**。

归一化规则只处理机器生成的差异（时间戳、IP、端口、UUID、行号、数字），
自由文本的措辞差异它无能为力 —— 而措辞恰恰是最容易变的部分。
但放宽归一化会引入错合（见 [Fingerprint](./04-fingerprint.md)），
方向 1 被排除；向量检索是 v2 的事。于是采用方向 2 的扩展版本：

### 两路信号，语义强度不同

| 路 | 判据 | 语义 | 代价 |
|---|---|---|---|
| 精确 | `fingerprint = $1` | **确定同类** | 走索引，一次查询 |
| 相似 | 归一化后词集 Jaccard ≥ 0.5 | **可能同类** | 粗召回 200 行，Go 侧算 |

精确路是主路：走 `fingerprint` 索引，结果确定，不受规模影响，
**永远不会因为候选上限而丢失**。相似路是补充召回。

粗召回在 SQL 侧只做限行（`ORDER BY created_at DESC LIMIT 200`），
相似度在 Go 侧算 —— 用 SQL 重写一遍归一化语义必然产生偏差，
而且 Jaccard 无法建索引。上限 200 是明确的取舍：提交路径是同步的，
用户正在等响应，不能读全表。

### 合并规则

去重、精确优先、`created_at DESC`、整体 `LIMIT 5`。
实现放在 `fingerprint.Merge`（叶子包），service 与 worker **共用同一份**，
保证喂给模型的历史和用户看到的是同一批。

同一 ID 两路都命中时保留 exact：精确是确定的信号，降级它会损失信息。

### API 必须暴露匹配原因

`related` 列表每项带 `match` 字段（`exact` / `similar`）。这不是装饰：
`05` 的核心诉求是不能让用户看到"错误的相关历史"，而把"确定同类"和
"可能同类"渲染成一模一样等于把推测当事实。UI 用不同徽标区分，
worker 喂给模型时也标注 `[确定同类]` / `[可能同类]`。

`is_recurring` 的语义随之放宽：v1 是"指纹完全相同的记录已存在"，
现在是"极可能有同类历史已存在"（两路任一命中即置位）。
可能出现"标了 RECURRING 但 related 里只有 similar 项"的组合，
这是刻意的 —— 徽标说的是"可能不是新问题"，match 字段负责给出确定性。

## 已知局限

### 阈值是从小样本定的，且 Jaccard 对长度不对称敏感

阈值 0.5 的原始依据是**手工截取的 4 行日志摘录**，Jaccard ≈ 0.67，
无关对照 ≈ 0.00。但真实库里的记录是**十几到几十行**：

- 同一类故障，短记录（4 行）对长记录（18 行）的 Jaccard 只有 **0.35**，低于阈值而漏召回。
- 原因是 Jaccard 的分母是并集：一条日志比另一条长得多时，分数被稀释。

实测全库配对分布支持**不下调阈值**：≥0.5 的配对全部落在真正相关的簇上；
而 0.30–0.50 区间混着仅共享 `panic`/`goroutine` 这类泛化字样的记录，
下调会引入本文件明确警告的误召回。

因此这是**度量本身的局限，不是实现缺陷**，修法是换度量（Dice 系数、
去重后计数、按行分块比），不是调阈值。
`internal/fingerprint/similarity_test.go` 里有
`TestJaccardSensitivityToLengthAsymmetry` **故意断言这个漏召回** ——
将来换度量修好了它，测试会失败以提醒回来更新本文件。

### 其他

- 相似召回只覆盖最近 200 条未失败记录，更久远的历史召不回来。
  全量召回需要 SQL 侧索引（`pg_trgm` 之类），属于 v2。
- 不同服务的 `timeout` 会被错误合并（与 04 同一问题）。

当前仍坚持**确定性优先于召回率**：用户看到"没有相关历史"可以接受，
看到"错误的相关历史"才会失去信任。相似路只补召回、并如实标注可信度，
不改变精确路的确定性语义。

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
