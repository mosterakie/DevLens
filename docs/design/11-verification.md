# 验证记录

这份文档记录**实际跑过的验证**和**发现的问题**，与设计文档分开：
设计文档写"打算怎么做"，这里写"做出来是什么样、哪里和预期不符"。

## 环境

| 组件 | 版本 |
|---|---|
| Go | 1.27.1 |
| PostgreSQL | 18.6（Windows） |
| Redis | 8.10.1 |
| Docker | 29.8.0（Docker Desktop，Linux 容器） |
| 模型 | deepseek-flash |

## 测试规模

```
112 个测试，全绿
├── 单元：93 个
│   ├── fingerprint   幂等性、反向断言、边界
│   ├── domain        状态机、语言解析、置信度降级
│   ├── analyzer      响应解析、围栏剥离、截断、重试
│   ├── service       提交流程、同类判定（假仓储）
│   ├── handler       语言协商
│   └── metrics       指标渲染
└── 集成：19 个（internal/repository，需要真实 PostgreSQL）
```

集成测试用 `devlens_test` 库，跑完约 3.8 秒。`go test -short` 跳过。

## 集成测试覆盖的约束

`internal/repository` 原先零覆盖，而事务和幂等恰是最容易出错的地方。

| 测试 | 验证的约束 |
|---|---|
| `TestCreateRollsBackWhenEventFails` | 事务原子性：第二步失败时第一步必须回滚 |
| `TestCreateWritesInitialEvent` | 建记录时同时写创建事件 |
| `TestSaveAnalysisIsIdempotent` | 重复消费只留一行，且覆盖而非报错 |
| `TestFindRelatedExcludesSelf` | 不把自己算作同类 |
| `TestFindRelatedExcludesFailed` | FAILED 记录不参与同类匹配 |
| `TestCountByFingerprintExcludesFailed` | 同上 |
| `TestListFiltersAndCursor` | 游标分页不重不漏 |
| `TestEmptyListsRoundTripAsEmpty` | JSONB 空数组往返为 `[]` 而非 null |
| `TestAnalysisCascadeDelete` | 级联删除 |

### 一个安全设计

集成测试会 `TRUNCATE` 全部表，所以入口处有库名守卫：库名不以 `_test`
结尾就拒绝运行。本机 PostgreSQL 上有其他项目的真实数据库，配置写错
一次就会造成不可恢复的数据丢失，宁可让测试直接失败。

## 验证中发现并修复的问题

以下都是**只有实际跑起来才会暴露**的问题。

### 1. `confidence` 精度丢失

```
数据库 REAL（4 字节）→ 写入 0.8，读出 0.800000011920929
Go 侧    float64（8 字节）
```

`REAL` 无法精确表示 0.8 这类十进制小数。界面因为 `toFixed(2)` 遮蔽了，
肉眼看不出，但等值比较、排序、聚合都不可靠。

修复：迁移 `0003_confidence_double` 改为 `DOUBLE PRECISION`。

### 2. `repository.Create` 可写入空语言

空字符串会**绕过数据库的 `DEFAULT 'zh-Hans'`**，写进一条语言未定义的
记录。service 层有兜底，但 repository 是公开方法，不该假设调用方先做了校验。

修复：在 repository 内兜底，纵深防御。

### 3. Dockerfile 里的 Go 版本低于 go.mod

```
Dockerfile: golang:1.24-alpine
go.mod:     go 1.27.1
```

构建必然失败。修复：改为 `golang:1.27-alpine`。

### 4. 镜像以 root 运行

Dockerfile 里原先注释写"distroless 默认非 root"——**这个说法是错的**：

| 镜像 | User |
|---|---|
| `static-debian12:latest` | `0`（root） |
| `static-debian12:nonroot` | `65532` |

实测确认后改用 `:nonroot` 变体。

### 5. 列表页 `/index.html` 返回 404

静态文件逐个列出时漏了 `index.html`，只有 `/` 能访问。
表现为复制地址栏 URL 给别人会 404。

修复：改为显式白名单集中维护。

### 6. 输出被 `max_tokens` 截断

`deepseek-flash` 分析一条普通日志约需 2400 tokens，原先设 2048 会截断，
得到断裂的 JSON，且重试无意义（截断是确定性的）。

修复：识别 `finish_reason == "length"` 并立即失败；默认值提到 4096；
提示词要求简洁输出。

## Docker 镜像验证

```
docker build -t devlens:local .      # 成功，编译 45 秒
镜像大小 66.6 MB
User=65532（非 root）
```

容器化端到端验证（api 与 worker 分别作为容器运行，连接宿主机 PG/Redis）：

| 项目 | 结果 |
|---|---|
| `/healthz` `/readyz` | 通过（readyz 说明容器内可达 PG 与 Redis） |
| 前端静态资源 | 全部 200，从镜像内提供 |
| 提交日志 | 202，221ms |
| worker 分析 | 6 秒完成，调用真实 DeepSeek |
| 分析语言 | 中文，行号正确 |
| 同类判定 | 改动时间戳/goroutine/行号后仍识别为同类 |
| `/related` 端点 | 返回正确的同类记录 |
| `/metrics` | 计数器、直方图、队列深度均正常 |
| 日志格式 | 结构化 JSON |

### 两个环境注意点

**容器访问宿主机**：Windows 上 `--network host` 不映射到宿主机 localhost，
需要用 `host.docker.internal`。

**镜像拉取的 IPv6 问题**：本机 DNS 把 `registry-1.docker.io` 解析到 IPv6
地址而该线路不通，导致拉取超时。配置 `registry-mirrors` 后可用。
这是网络环境问题，与项目代码无关。

## 未验证的部分

诚实列出。

| 项目 | 原因 |
|---|---|
| `go test -race` | 需要 gcc，本机未安装。应在 CI 中执行 |
| 真实浏览器中的语言切换交互 | GUI 操作需要人工批准；改用 Node 验证 i18n 纯逻辑 |
| 部署到公网 | 需要托管平台账号与域名 |
| 高并发下的表现 | 未做压测 |

## 复现验证

```bash
# 单元测试
go test -short ./...

# 全部测试（需要 PostgreSQL 与 devlens_test 库）
createdb -O devlens devlens_test
go test ./...

# 前端词条完整性
node scripts/check-i18n.js

# 构建镜像
docker build -t devlens:local .
```