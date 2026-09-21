package fingerprint

import (
	"reflect"
	"strings"
	"testing"
)

// --- Tokens ---

// TestTokensSplitsNormalizedLog 确认分词建立在归一化结果之上。
func TestTokensSplitsNormalizedLog(t *testing.T) {
	got := Tokens("postgres connection refused while dialing host")
	want := map[string]struct{}{
		"postgres": {}, "connection": {}, "refused": {},
		"while": {}, "dialing": {}, "host": {},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens() = %v, want %v", got, want)
	}
}

// TestTokensIgnoresShortTokens 覆盖长度 < 3 的过滤规则。
//
// 归一化后残留的 `[`、`+`、`a=`、`io` 之类碎片对区分贡献为零，
// 不丢掉它们会把不相关日志的相似度抬到阈值之上。
func TestTokensIgnoresShortTokens(t *testing.T) {
	got := Tokens("a=1 ab abc abcd :x io")

	// 只有长度 < 3 的被丢掉："ab"、":x"、"io"。
	// "a=1" 是 3 个字符，按规则保留（它确实可能携带信息）；
	// 这一点由用例本身锁定，避免有人误以为"短碎片一律丢"。
	want := map[string]struct{}{"a=1": {}, "abc": {}, "abcd": {}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens() = %v, want %v", got, want)
	}
}

// TestTokensKeepsPlaceholdersFromNormalize 确认归一化占位符会进入词集。
//
// 这是刻意的：`<ts>`、`<ip>`、`<port>` 是坐标性质的结构标记，
// 同一类故障的日志会有相同的占位符分布，保留它们能增强同类信号。
func TestTokensKeepsPlaceholdersFromNormalize(t *testing.T) {
	got := Tokens("dial tcp <ip>:<port>: connect: connection refused")
	for _, want := range []string{"<ip>:<port>:", "connection", "refused"} {
		if _, ok := got[want]; !ok {
			t.Errorf("应当保留 token %q, got %v", want, got)
		}
	}
	// 单字符的 ":" 之类的碎片必须被丢掉。
	if _, ok := got[":"]; ok {
		t.Errorf("单字符碎片不该进入词集: %v", got)
	}
}

// TestTokensEmpty 确认空输入得到空集而不是 nil panic。
func TestTokensEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "a b c", "=="} {
		got := Tokens(in)
		if len(got) != 0 {
			t.Errorf("Tokens(%q) 应为空集, got %v", in, got)
		}
	}
}

// TestTokensIsDeterministic 确认同一输入永远得到同一个集合。
//
// map 迭代顺序随机，所以如果实现里混入了顺序依赖，
// 这里会以不稳定失败的形式暴露出来。
func TestTokensIsDeterministic(t *testing.T) {
	const in = "database connection refused database refusals timeout"
	first := Tokens(in)
	for i := 0; i < 50; i++ {
		if !reflect.DeepEqual(first, Tokens(in)) {
			t.Fatalf("第 %d 次调用结果不同: %v vs %v", i, first, Tokens(in))
		}
	}
}

// TestTokensDeduplicates 确认同一条日志里重复的词只算一个。
//
// 不去重的话，啰嗦的日志会单方面拉低相似度（分母虚高）。
func TestTokensDeduplicates(t *testing.T) {
	got := Tokens("timeout timeout timeout other")
	if len(got) != 2 {
		t.Errorf("应当去重成 2 个词, got %d: %v", len(got), got)
	}
}

// --- Jaccard 边界 ---

// TestJaccardEmptySets 覆盖空集这个最重要的边界。
//
// 数学上两个空集是 0/0 未定义。返回 1 会把两条都归一化成空串的
// 日志判为"完全一致"—— 这是最危险的误召回；所以约定返回 0，
// 表示"没有相似度证据"。
func TestJaccardEmptySets(t *testing.T) {
	empty := map[string]struct{}{}
	other := map[string]struct{}{"a": {}}

	cases := []struct {
		name string
		a, b map[string]struct{}
		want float64
	}{
		{"两个空集", empty, empty, 0},
		{"nil 与空集", nil, empty, 0},
		{"空集与非空", empty, other, 0},
		{"非空与空集", other, empty, 0},
		{"nil 与非空", nil, other, 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := Jaccard(tt.a, tt.b); got != tt.want {
				t.Errorf("Jaccard = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestJaccardIdentical 覆盖完全相同：结果为 1。
func TestJaccardIdentical(t *testing.T) {
	a := map[string]struct{}{"database": {}, "timeout": {}, "pool": {}}
	b := map[string]struct{}{"pool": {}, "timeout": {}, "database": {}}

	if got := Jaccard(a, b); got != 1 {
		t.Errorf("相同集合的 Jaccard = %v, want 1", got)
	}
}

// TestJaccardDisjoint 覆盖完全不同：结果为 0。
func TestJaccardDisjoint(t *testing.T) {
	a := map[string]struct{}{"alpha": {}, "beta": {}}
	b := map[string]struct{}{"gamma": {}, "delta": {}}

	if got := Jaccard(a, b); got != 0 {
		t.Errorf("不相交集合的 Jaccard = %v, want 0", got)
	}
}

// TestJaccardPartialOverlap 覆盖部分重合的计算。
//
// |A∩B| = 2, |A∪B| = 4 -> 0.5
func TestJaccardPartialOverlap(t *testing.T) {
	a := map[string]struct{}{"alpha": {}, "beta": {}, "gamma": {}}
	b := map[string]struct{}{"beta": {}, "gamma": {}, "delta": {}}

	got := Jaccard(a, b)
	if got != 0.5 {
		t.Errorf("Jaccard = %v, want 0.5", got)
	}
}

// TestJaccardIsSymmetric 确认交换入参不改变结果。
func TestJaccardIsSymmetric(t *testing.T) {
	a := map[string]struct{}{"alpha": {}, "beta": {}, "gamma": {}}
	b := map[string]struct{}{"beta": {}, "delta": {}}

	if Jaccard(a, b) != Jaccard(b, a) {
		t.Errorf("不对称: Jaccard(a,b)=%v Jaccard(b,a)=%v", Jaccard(a, b), Jaccard(b, a))
	}
}

// TestJaccardDoesNotMutate 确认计算是只读的。
//
// 相似度会被多路复用，如果它偷偷修改了入参，
// 后续计算会基于被污染的数据，症状极难定位。
func TestJaccardDoesNotMutate(t *testing.T) {
	a := map[string]struct{}{"alpha": {}, "beta": {}}
	b := map[string]struct{}{"beta": {}, "gamma": {}}
	aCopy := map[string]struct{}{"alpha": {}, "beta": {}}
	bCopy := map[string]struct{}{"beta": {}, "gamma": {}}

	_ = Jaccard(a, b)

	if !reflect.DeepEqual(a, aCopy) || !reflect.DeepEqual(b, bCopy) {
		t.Errorf("Jaccard 修改了入参: a=%v b=%v", a, b)
	}
}

// TestJaccardSensitivityToLengthAsymmetry 记录一个已知局限，而不是掩盖它。
//
// Jaccard 的分母是并集大小，所以当同一种故障的一条日志比另一条长得多
// （多出十几行重复的噪声行）时，分数会被稀释到阈值以下而漏召回。
//
// 这条测试**故意断言"漏召回"**，作用是：
//  1. 把已知局限写成可执行的文档，而不是散落在注释里；
//  2. 如果有人以后换了度量（Dice/去重/分块）修好了它，
//     这个测试会失败，提醒他回来更新注释与设计说明。
//
// 不要把阈值调低来让它通过 —— 全库实测显示 0.3-0.5 区间混着
// 仅共享 panic/goroutine 字样的记录，下调会引入误召回。
func TestJaccardSensitivityToLengthAsymmetry(t *testing.T) {
	// 短的一条：4 行核心信息。
	short := Normalize(`2026-10-01 10:00:00.100 [erro]  pubsub: pubsub disconnected from postgres  error=EOF
2026-10-01 10:00:00.200 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-nonprod.svc.cluster.local:5432  timeout_ms=0
2026-10-01 10:00:00.300 [erro]  pubsub: pubsub failed to connect to postgres  error="dial tcp 10.1.2.3:5432: connect: connection refused"
2026-10-01 10:00:01.400 [erro]  coderd: failed to ping database, retrying in 1s`)

	// 长的一条：同一种故障，但多出十几行重复的噪声行。
	long := Normalize(`2026-10-01 11:30:00.100 [erro]  pubsub: pubsub disconnected from postgres  error=unexpected EOF
2026-10-01 11:30:00.200 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-prod.svc.cluster.local:5432  timeout_ms=0
2026-10-01 11:30:00.300 [erro]  pubsub: pubsub failed to connect to postgres  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:00.400 [erro]  coderd.chatd.processor: failed to acquire chats  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:00.500 [erro]  coderd.chatd.processor: failed to acquire chats  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:00.600 [warn]  coderd.inmem-provisionerd: heartbeat failed  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:00.700 [warn]  coderd.gitsync: acquire stale chat diff statuses  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:00.800 [warn]  coderd: run replica update loop failed while syncing replicas from the enterprise manager
2026-10-01 11:30:00.900 [warn]  coderd.inmem-provisionerd: heartbeat failed  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:01.000 [erro]  coderd.chatd.processor: failed to acquire chats  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:01.100 [warn]  coderd.gitsync: acquire stale chat diff statuses  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:01.200 [warn]  coderd.inmem-provisionerd: heartbeat failed  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:01.300 [warn]  coderd.gitsync: acquire stale chat diff statuses  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-10-01 11:30:01.400 [erro]  coderd: database ping failed, backing off`)

	shortTok, longTok := Tokens(short), Tokens(long)

	// 前提：两条确实都归一化出了内容，否则测试没有意义。
	if len(shortTok) == 0 || len(longTok) == 0 {
		t.Fatal("前置条件不成立：应当都有 token")
	}
	// 长的一条词数明显更多（这就是长度不对称）。
	if len(longTok) <= len(shortTok) {
		t.Fatalf("测试数据构造有误：长日志词数 %d 应多于短日志 %d", len(longTok), len(shortTok))
	}

	got := Jaccard(shortTok, longTok)
	if got >= SimilarityThreshold {
		t.Errorf("长度不对称的同类日志 Jaccard = %v, 本次记录为漏召回（< %v）；"+
			"如果你改了度量让它命中，请同步更新 SimilarityThreshold 的注释"+
			"与 05-related-incidents.md", got, SimilarityThreshold)
	}

	// 记录真实数值，便于以后对比度量改动的影响。
	if got < 0.30 || got > 0.40 {
		t.Logf("注意：长度不对称场景的 Jaccard = %.4f，与记录的 ~0.35 有偏差", got)
	}
}

// TestSimilarityThresholdDiscriminates 是阈值选值的回归护栏。
//
// 用真实日志形态验证"同类命中、异类不命中"。阈值一旦被改动，
// 这里会立刻失败——这正是我们要的：阈值是实测出来的，不是拍的。
func TestSimilarityThresholdDiscriminates(t *testing.T) {
	// 同类：同一故障，换了 IP / 环境名 / 措辞（error=eof vs unexpected eof，
	// retrying in 1s vs backing off）。
	sameA := Normalize(`2026-09-08 01:19:09.357 [erro]  pubsub: pubsub disconnected from postgres  error=EOF
2026-09-08 01:19:09.383 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-nonprod.svc.cluster.local:5432  timeout_ms=0
2026-09-08 01:19:09.383 [erro]  pubsub: pubsub failed to connect to postgres  error="dial tcp 10.1.2.3:5432: connect: connection refused"
2026-09-08 01:19:10.413 [erro]  coderd: failed to ping database, retrying in 1s`)

	sameB := Normalize(`2026-09-08 03:44:11.001 [erro]  pubsub: pubsub disconnected from postgres  error=unexpected EOF
2026-09-08 03:44:11.020 [erro]  pubsub: pubsub failed to dial postgres  network=tcp  address=postgresql.coder-prod.svc.cluster.local:5432  timeout_ms=0
2026-09-08 03:44:11.020 [erro]  pubsub: pubsub failed to connect to postgres  error="dial tcp 10.9.8.7:5432: connect: connection refused"
2026-09-08 03:44:11.100 [erro]  coderd: database ping failed, backing off`)

	// 异类：Go panic，与上面的数据库连接故障无关。
	unrelated := Normalize(`panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x638 pc=0x190717c]

goroutine 15 [running]:
github.com/databricks/terraform-provider-databricks/exporter.(*importContext).emitRfaAccessRequestDestinations(...)
      exporter/impl_uc.go:813 +0x7c`)

	// 前提：这两条确实是"指纹不同但同类"，否则本测试没有意义。
	if ComputeHex(sameA) == ComputeHex(sameB) {
		t.Fatal("前置条件不成立：两条日志的指纹应当不同（否则走精确匹配就命中了）")
	}

	same := Jaccard(Tokens(sameA), Tokens(sameB))
	if same < SimilarityThreshold {
		t.Errorf("同类日志的 Jaccard = %v, 应 >= 阈值 %v", same, SimilarityThreshold)
	}

	diff := Jaccard(Tokens(sameA), Tokens(unrelated))
	if diff >= SimilarityThreshold {
		t.Errorf("异类日志的 Jaccard = %v, 应 < 阈值 %v", diff, SimilarityThreshold)
	}

	// 分离度必须明显，否则阈值只是碰巧落在中间。
	if same-diff < 0.4 {
		t.Errorf("同类与异类的分离度不足: %v - %v = %v", same, diff, same-diff)
	}
}

// TestNormalizeUnchangedBySimilarity 是"不得放宽归一化规则"的护栏。
//
// 设计约束明确要求改动不能碰 Normalize 的正则。指纹的确定性语义
// 依赖它，一旦改了，库里所有历史指纹立刻失效。
//
// 这里锁定几个关键输入的归一化结果：任何正则调整都会让本测试失败，
// 逼改动者先想清楚指纹迁移的问题。
func TestNormalizeUnchangedBySimilarity(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// 自由文本差异必须原样保留，这是相似度路存在的理由。
			name: "自由文本不归一",
			in:   "error=eof",
			want: "error=eof",
		},
		{
			name: "不同的措辞保持不同",
			in:   "coderd: failed to ping database, retrying in 1s",
			want: "coderd: failed to ping database, retrying in <duration>",
		},
		{
			// 机器生成的差异仍然归一。
			name: "时间戳归一",
			in:   "2026-09-08 01:19:09.357 [erro] x",
			want: "<ts> [erro] x",
		},
		{
			name: "IPv4 归一",
			in:   "dial tcp 10.1.2.3:5432: connect",
			want: "dial tcp <ip>:<port>: connect",
		},
		{
			name: "行号归一",
			in:   "exporter/impl_uc.go:813 +0x7c",
			want: "exporter/impl_uc.go:<l> +<hex>",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q\n"+
					"（改动 Normalize 规则会破坏历史指纹的确定性语义，"+
					"见 04-fingerprint.md 与本次改造的设计约束）",
					tt.in, got, tt.want)
			}
		})
	}
}

// TestTokensDeterministicOnRealisticLog 确认去重词集不依赖 map 顺序。
//
// 用带重复词的长文本反复计算，任何顺序依赖都会表现为不稳定。
func TestTokensDeterministicOnRealisticLog(t *testing.T) {
	log := strings.Repeat("database connection refused database timeout ", 20)
	want := Tokens(log)

	for i := 0; i < 100; i++ {
		got := Tokens(log)
		if len(got) != len(want) {
			t.Fatalf("第 %d 次 token 数不同: %d vs %d", i, len(got), len(want))
		}
		for k := range want {
			if _, ok := got[k]; !ok {
				t.Fatalf("第 %d 次缺少 token %q", i, k)
			}
		}
	}
}
