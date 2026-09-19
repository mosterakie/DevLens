# Fingerprint：日志归一化与同类判定

目标：两条看似不同的日志，如果本质是同一个错误，应该产生相同的 fingerprint。

## 为什么不能直接 hash 原始日志

```
Error connecting to DB at 10.0.1.4      → a3f1...
Error connecting to DB at 10.0.2.7      → 9c02...
```

每次 IP 变化就是一条新错误，同类问题无法聚合。所以要先归一化。

## 两阶段

```
raw_log --[Normalize]--> normalized --[Compute]--> fingerprint (16B)
            规则表驱动              SHA-256 + 截断
```

分开的理由：归一化规则会频繁调整，hash 算法不会。分开后可以独立演进。

## Normalize 规则表

按顺序应用，顺序有影响。

| # | 规则 | 替换为 |
|---|---|---|
| 1 | ISO8601 时间戳 | `<TS>` |
| 2 | `MM/DD/YYYY HH:MM:SS` | `<TS>` |
| 3 | IPv4 | `<IP>` |
| 4 | IPv6 | `<IP>` |
| 5 | UUID | `<UUID>` |
| 6 | `0x` hex / 内存地址 | `<HEX>` |
| 7 | 长 hex 串（16+） | `<HEX>` |
| 8 | `goroutine N` | `goroutine <N>` |
| 9 | `文件.go:行号` | `文件.go:<L>` |
| 10 | 通用 `文件:行号` | `文件:<L>` |
| 11 | 端口 `:1234` | `:<PORT>` |
| 12 | 带单位耗时 `5s` / `200ms` | `<DURATION>` |
| 13 | 其余数字 | `<N>` |
| 14 | 连续空白 | 单个空格 |
| 15 | 转小写 | - |
| 16 | trim | - |

顺序上要注意：时间戳必须排在数字之前，否则 `2026-09-18`
会先被拆成 `<N>-<N>-<N>`，语义丢失。

### 一个取舍：宁可错合，不可漏分

规则 13 会把 `error code 1042` 和 `error code 1043` 归一成同一个串，
也就是两个不同的错误码会被合并。

v1 选择接受这个错误。理由：把两个不同错误码归为一类，用户看到候选列表
仍能自己判断；而归一化不到位的后果是同类问题完全匹配不上，
"Related Incidents" 功能直接失效。

如果以后场景变成自动告警，这个取舍应该反过来 —— 误报会让工程师失去信任。

### 多行 stack trace

Go 的 panic 日志有几十行且行数会变。v1 简化处理：
只取前 2000 字符参与 fingerprint，`raw_log` 仍完整存储。

## Compute

```go
func Compute(normalized string) []byte {
    sum := sha256.Sum256([]byte(normalized))
    return sum[:16]
}
```

截断到 128 位：碰撞概率对当前数据量可忽略，索引更小。

## 边界情况

| 情况 | 处理 |
|---|---|
| 空日志 / 纯空白 | 拒绝，返回 400 |
| 超长日志 (>64KB) | 截断存储，fingerprint 只用前 2000 字符 |
| 非 UTF-8 字节 | `strings.ToValidUTF8` 替换 |
| 无任何可识别结构 | 仍能归一化，不会 panic |

## 已知的债

**规则改了历史 fingerprint 就失效了。** 彻底的解法是加 `fingerprint_version`
列 + 后台重算任务。v1 不做，但记在这里。

**不同服务的 `timeout` 会被错误合并。** 缓解手段是在 fingerprint 里加入
来源维度（`service_name` + `fingerprint` 复合键）。v1 用户只粘贴日志，
拿不到来源信息，所以先不加。