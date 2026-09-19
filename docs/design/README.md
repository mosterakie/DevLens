# DevLens 设计文档

记录 DevLens 的设计决策和理由。每一份回答一个问题，
重点写清楚"选了什么、放弃了什么"。

| 文档 | 内容 |
|---|---|
| [01-product-scope](./01-product-scope.md) | 做什么、不做什么 |
| [02-architecture](./02-architecture.md) | 分层、请求流程、状态机 |
| [03-data-model](./03-data-model.md) | 表结构、索引 |
| [04-fingerprint](./04-fingerprint.md) | 日志归一化与同类判定 |
| [05-related-incidents](./05-related-incidents.md) | 相似问题检索的取舍 |
| [06-api](./06-api.md) | 接口契约 |
| [07-reliability](./07-reliability.md) | 限流、超时、重试、AI 判错 |
| [08-testing](./08-testing.md) | 测试策略 |
| [09-deployment](./09-deployment.md) | 运行与部署 |
| [10-roadmap](./10-roadmap.md) | 里程碑 |

文档是快照。代码和文档不一致时，改其中一边，不要让两者长期偏离。