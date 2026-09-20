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
145 个测试，全绿
├── 单元：114 个
│   ├── fingerprint   幂等性、反向断言、边界
│   ├── domain        状态机、语言解析、置信度降级
│   ├── analyzer      响应解析、围栏剥离、截断、重试
│   ├── service       提交流程、同类判定（假仓储）
│   ├── handler       语言协商
│   ├── metrics       指标渲染
│   ├── config        .env 解析与优先级
│   ├── migrate       迁移文件加载与校验
│   └── usecases      真实日志样本上的归一化
└── 集成：31 个（需要真实 PostgreSQL）
    ├── repository    事务回滚、幂等、游标分页、级联删除
    └── migrate       迁移幂等、失败回滚、校验和、baseline
```

集成测试用 `devlens_test` 库，跑完约 4 秒。`go test -short` 跳过。

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

### 7. 迁移加载器把 down 脚本当成命名错误

`internal/migrate` 的 `Load` 只判断了"是否以 `.sql` 结尾"，没排除
`.down.sql`，于是合法的 down 脚本被当成"命名不合规范"直接报错。

这个错误在仓库里必然触发——`migrations/` 下就有三个 down 文件，
意味着服务启动时就会挂。是测试抓出来的，见 `TestLoadIgnoresDownFiles`
和 `TestRepoMigrationsAreValid`。

修复：先排除 `.down.sql`，其余的 `.sql` 才视为命名不规范。

### 8. 升级路径缺失：手动建库的环境启动失败

迁移系统实现后第一次在已有库上启动，直接崩了：

```
fatal: 执行 0001_init.up.sql 失败: 类型 "severity_t" 已经存在
```

这不是 bug，是**升级路径没有设计**。任何用户如果先用旧版本手动执行过
`psql -f migrations/*.up.sql`，再升级到带自动迁移的版本，都会遇到。

修复：新增 baseline 机制，把已有库标记为已应用某个版本。写入前先校验
必须存在的表——空库上执行会被拒绝，因为把空库标成"已迁移"会让后续
迁移建立在错误前提上，而且报错会出现在很久以后。

实测在保留 17 条数据的库上完成 baseline，之后 api 启动日志显示
"数据库结构已是最新"，检查耗时 4ms。

### 9. devcheck 的环境变量优先级与主程序相反

`cmd/devcheck` 只读 `.env` 而忽略进程环境变量，而 `config.Load` 的
优先级是"真实环境变量 > `.env`"。后果是用环境变量覆盖配置时，
devcheck 检查的是**错误的库**。

修复：抽一个 `lookup` 函数，与 `config.Load` 使用相同的优先级。

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

## CI 实际运行结果

`.github/workflows/ci.yml`，三个 job 在 GitHub Actions 上跑通（2 分 7 秒）：

| Job | 耗时 | 结果 |
|---|---|---|
| 单元测试与竞态检测 | 2m4s | ok |
| 集成测试 | 54s | ok |
| 镜像构建 | 1m47s | ok |

### 竞态检测

这是本地做不了、只能靠 CI 的检查。日志确认 `go test -race -short ./...`
真的以 race 模式编译执行（该步骤耗时 51 秒，是插桩编译的典型开销）：

```
ok  internal/analyzer     12.207s
ok  internal/config        1.017s
ok  internal/domain        1.017s
ok  internal/fingerprint   1.071s
ok  internal/handler       1.015s
ok  internal/metrics       1.009s
ok  internal/repository    1.010s
ok  internal/service       1.016s
```

**未发现竞态。**

### 镜像 job 的两条断言

CI 日志里可以看到断言确实执行了：

```
镜像用户: 65532
{"level":"ERROR","error":"DATABASE_URL is required"}
```

第一条断言镜像不是 root。distroless 的默认 tag 是 root，改错了
不会导致构建失败，只会静默地以 root 运行，所以值得显式断言。

第二条断言缺少必填配置时进程以非零状态退出，验证的是 fail-fast
行为——配置错误应该在启动时暴露，而不是等到第一个请求。

### 为什么本机跑不了 -race

原记录只写了"未安装 gcc"，实际原因更具体：

本机装了 Visual Studio 18 BuildTools（MSVC 的 `cl.exe`，版本 19.51），
所以 C++ 可以编译。但 **Go 的 cgo 在 Windows 上只支持 gcc，不支持 MSVC**：
cgo 会向 C 编译器传 `-Werror`，MSVC 对应的参数是 `/WX`，收到 `-Werror`
直接报 `D8021: 无效的数值参数`。

用最小 cgo 程序验证过，同样失败，说明是工具链不兼容而非项目代码问题。

## 一个工具选择的记录

原本想用 PowerShell 脚本做环境检查（`scripts/dev.ps1`），失败了，改用
Go 写成 `cmd/devcheck`。

失败原因是三层问题叠加，且都不在脚本逻辑本身：

1. **编码**：Windows PowerShell 5.1 读取没有 BOM 的 `.ps1` 时，用系统
   ANSI 代码页解码，而不是 UTF-8。中文变成乱码（`执行` → `鎵ц`），
   乱码改变字节长度后引号配对被打乱，报出的是"字符串未终止""缺少右
   花括号"这类与真实原因无关的错误。
2. **转义**：PowerShell 的引号、反引号续行、`$var:` 会被解析成驱动器
   引用等规则，与生成文件的工具链层层冲突。
3. **反馈慢**：每次改动要经过 shell → 文件 → 解析三道关才能确认，
   定位成本远高于问题本身。

Go 版本没有这些问题：源文件统一 UTF-8，字符串处理无歧义，而且能像
其他代码一样写单元测试（覆盖了 `probe` 的连通与关闭、`.env` 解析的
注释与含 `#` 的值、打码函数）。

**结论**：在 Windows 上用 shell 脚本做跨环境工具，编码与转义的成本
容易被低估。这个项目已经依赖 Go 工具链，用它更省事。

## 未验证的部分

诚实列出。

| 项目 | 原因 |
|---|---|
| 真实浏览器中的语言切换交互 | GUI 操作需要人工批准；改用 Node 验证 i18n 纯逻辑 |
| 部署到公网 | 需要托管平台账号与域名 |
| 高并发下的表现 | 未做压测 |
| CI 在 PR 流程下的行为 | 目前只有 main 上的 push 触发过 |

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