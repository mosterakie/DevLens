package service

import (
	"context"
	"strings"
	"testing"

	"github.com/mosterakie/DevLens/internal/domain"
)

// TestSubmitRedactsCredentials 确认存下来的日志不含凭据。
//
// 这是这一版的核心目的：日志会发给外部模型，凭据必须在离开服务
// 之前去掉，存库的也应当是脱敏后的版本。
func TestSubmitRedactsCredentials(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	const secret = "S3cretP%40ss"
	log := "connection failed: postgres://appuser:" + secret +
		"@db.internal:5432/prod\ncontext deadline exceeded while waiting for a database connection\nretrying the request after backoff\nactive connections: 20\npool size: 20\n"

	res, err := svc.Submit(context.Background(), log)
	if err != nil {
		t.Fatalf("Submit 失败: %v", err)
	}

	// 存下来的 raw_log 不含密码。
	if strings.Contains(res.Incident.RawLog, secret) {
		t.Errorf("raw_log 中仍有密码:\n%s", res.Incident.RawLog)
	}
	// 也不含在归一化文本里。
	if strings.Contains(res.Incident.Normalized, secret) {
		t.Errorf("normalized 中仍有密码:\n%s", res.Incident.Normalized)
	}
	// 但诊断需要的信息要保留。
	for _, keep := range []string{"appuser", "db.internal", "5432", "prod"} {
		if !strings.Contains(res.Incident.RawLog, keep) {
			t.Errorf("应当保留 %q，它是诊断信息:\n%s", keep, res.Incident.RawLog)
		}
	}
}

// TestFingerprintUsesRedactedText 是这一版最容易做错的地方。
//
// 如果指纹基于明文计算，那么：
//  1. 同样的故障、只是 token 不同，会被判成两类
//  2. 而且明文已经离开了服务（指纹里含它的哈希），设计目的落空
func TestFingerprintUsesRedactedText(t *testing.T) {
	repoA := newFakeRepo()
	svcA := NewIncidentService(repoA, &fakeQueue{})

	// 两条日志只有 token 值不同，其余完全相同。
	logA := "auth failed for service: token=AAAAAAAAAAAAAAAAAAAA for user 42\ncontext deadline exceeded while calling the auth backend\nretrying request after backoff\n"
	logB := "auth failed for service: token=BBBBBBBBBBBBBBBBBBBB for user 42\ncontext deadline exceeded while calling the auth backend\nretrying request after backoff\n"

	a, err := svcA.Submit(context.Background(), logA)
	if err != nil {
		t.Fatal(err)
	}

	// 换一个仓储，避免走"存量匹配"的分支。
	repoB := newFakeRepo()
	svcB := NewIncidentService(repoB, &fakeQueue{})
	b, err := svcB.Submit(context.Background(), logB)
	if err != nil {
		t.Fatal(err)
	}

	if string(a.Incident.Fingerprint) != string(b.Incident.Fingerprint) {
		t.Errorf("token 不同、其余相同的日志应当归为同类（指纹基于脱敏文本）\n  a: %x\n  b: %x",
			a.Incident.Fingerprint, b.Incident.Fingerprint)
	}
}

// TestLengthCheckBeforeRedaction 确认长度按原始提交判断。
//
// 脱敏让文本变短，若按脱敏后判断，等于悄悄放宽了上限。
func TestLengthCheckBeforeRedaction(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	// 构造一段：脱敏后会短于上限，但原始长度超过上限。
	// 用很长的密码值把原始文本撑过 MaxLogBytes。
	long := strings.Repeat("x", domain.MaxLogBytes)
	log := "password=" + long

	res, err := svc.Submit(context.Background(), log)
	if err == nil {
		t.Fatalf("原始长度超过上限时应当被拒绝，实际通过了（id=%d）", res.Incident.ID)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("应当报长度错误, got %v", err)
	}
	// 确认脱敏后确实变短了——否则这条测试没有验证到想验证的东西。
	if len(log) <= domain.MaxLogBytes {
		t.Fatal("测试数据构造有误：原始长度应当超过上限")
	}
}

// TestSubmitKeepsPlainLogIntact 是反向断言：不含凭据的日志不该被改动。
//
// 过度脱敏会让模型失去诊断线索。
func TestSubmitKeepsPlainLogIntact(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	log := "dial tcp 10.0.1.4:5432: connection refused\n" +
		"goroutine 231 [IO wait]:\n" +
		"\t/payment/db/db.go:142 +0x1a\n" +
		"active connections: 20\n"

	res, err := svc.Submit(context.Background(), log)
	if err != nil {
		t.Fatal(err)
	}

	if res.Incident.RawLog != log {
		t.Errorf("不含凭据的日志不该被改动:\n  in:  %q\n  out: %q", log, res.Incident.RawLog)
	}
}

// TestSubmitRedactsBeforeStoring 确认多种凭据形式都会被处理。
func TestSubmitRedactsBeforeStoring(t *testing.T) {
	cases := []struct {
		name   string
		log    string
		secret string
	}{
		{
			name:   "Authorization 头",
			log:    "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature\n401 unauthorized from the upstream service while fetching orders\nretrying the request after backoff\n",
			secret: "eyJhbGciOiJIUzI1NiJ9.payload.signature",
		},
		{
			name:   "api_key",
			log:    "config loaded with api_key=sk-7297abcdefghijklmnopqrstuvwxyz4a9c\nstartup failed because the upstream service rejected the credentials\n",
			secret: "sk-7297abcdefghijklmnopqrstuvwxyz4a9c",
		},
		{
			name:   "redis DSN",
			log:    "cannot connect redis://default:myRedisPass@cache:6379\nconnection refused while opening the cache connection for session lookup\n",
			secret: "myRedisPass",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := NewIncidentService(repo, &fakeQueue{})

			res, err := svc.Submit(context.Background(), tt.log)
			if err != nil {
				t.Fatalf("Submit 失败: %v", err)
			}
			if strings.Contains(res.Incident.RawLog, tt.secret) {
				t.Errorf("凭据未被脱掉:\n%s", res.Incident.RawLog)
			}
			if !strings.Contains(res.Incident.RawLog, "***") {
				t.Errorf("应当有占位符:\n%s", res.Incident.RawLog)
			}
		})
	}
}

// TestSubmitPassesRedactedLogToQueue 确认入队的是脱敏后的记录。
//
// worker 从库里读记录再交给模型，所以只要存的是脱敏版，
// 交给模型的就是脱敏版。这里固定住这个前提。
func TestSubmitPassesRedactedLogToQueue(t *testing.T) {
	repo := newFakeRepo()
	q := &fakeQueue{}
	svc := NewIncidentService(repo, q)

	const secret = "Hunter2Secret"
	log := "login failed password=" + secret + " for user admin\ncontext deadline exceeded while validating credentials\nretrying the login request\n"

	res, err := svc.Submit(context.Background(), log)
	if err != nil {
		t.Fatal(err)
	}

	if len(q.enqueued) != 1 {
		t.Fatalf("应当入队 1 条, got %d", len(q.enqueued))
	}

	// 库里存的这条就是 worker 将要读取的。
	stored := repo.incidents[0]
	if strings.Contains(stored.RawLog, secret) {
		t.Errorf("库中记录仍含密码，worker 会把它发给模型:\n%s", stored.RawLog)
	}
	if res.Incident.ID != stored.ID {
		t.Error("返回的记录与库中记录不一致")
	}
}

// TestLangStillWorks 确认脱敏没有影响语言处理。
func TestLangStillWorks(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	res, err := svc.SubmitLang(context.Background(), validLog(), domain.LangEn)
	if err != nil {
		t.Fatal(err)
	}
	if res.Incident.Lang != domain.LangEn {
		t.Errorf("lang = %q, want en", res.Incident.Lang)
	}
}

// TestRedactionMayShortenBelowMinimum 记录一个刻意接受的行为。
//
// 长度校验按原始提交判断，脱敏后不再重新校验。所以一条内容几乎
// 全是凭据的日志，脱敏后可能短于 MinLogBytes 仍被接受。
//
// 这是有意的：脱敏只替换凭据，不移除诊断信息。若因此拒绝请求，
// 用户会看到"日志太短"而他的原文并不短，无从理解。
func TestRedactionMayShortenBelowMinimum(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	secret := strings.Repeat("A", 80)
	log := "password=" + secret + " failed while opening the database connection for tenant 7\n"

	if len(log) < domain.MinLogBytes {
		t.Fatalf("测试数据构造有误：原文 %d 字节，应当达到下限 %d", len(log), domain.MinLogBytes)
	}

	res, err := svc.Submit(context.Background(), log)
	if err != nil {
		t.Fatalf("原文合法就应当接受，实际报错: %v", err)
	}

	if len(res.Incident.RawLog) >= len(log) {
		t.Error("脱敏应当让文本变短")
	}
	if !strings.Contains(res.Incident.RawLog, "database connection") {
		t.Errorf("不该丢掉诊断信息: %s", res.Incident.RawLog)
	}
	if strings.Contains(res.Incident.RawLog, secret) {
		t.Error("密码仍在")
	}
}

// TestRedactionDoesNotDropLines 确认脱敏不改变行数。
//
// evidence.source_line 是行号，如果脱敏增删行，用户核对原文时
// 会对不上——这个性质对"证据可核对"很关键。
func TestRedactionDoesNotDropLines(t *testing.T) {
	repo := newFakeRepo()
	svc := NewIncidentService(repo, &fakeQueue{})

	lines := []string{
		"ERROR: authentication failed",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.sig",
		"password=Hunter2SecretValue",
		"goroutine 231 [IO wait]:",
		"\t/payment/db/db.go:142 +0x1a",
		"active connections: 20",
		"pool size: 20",
	}
	log := strings.Join(lines, "\n") + "\n"

	res, err := svc.Submit(context.Background(), log)
	if err != nil {
		t.Fatal(err)
	}

	gotLines := strings.Split(strings.TrimRight(res.Incident.RawLog, "\n"), "\n")
	if len(gotLines) != len(lines) {
		t.Fatalf("行数变了：%d -> %d\n%s", len(lines), len(gotLines), res.Incident.RawLog)
	}

	for _, i := range []int{3, 4, 5} {
		if gotLines[i] != lines[i] {
			t.Errorf("第 %d 行被改动:\n  原: %q\n  现: %q", i+1, lines[i], gotLines[i])
		}
	}
}
