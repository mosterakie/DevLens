package queue

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// 集成测试需要真实 Redis。
//
// 用独立的 DB 索引（默认 15），避免清空开发数据——测试会 FLUSHDB。
// go test -short 跳过。

const testDB = 15

func testAddr() string {
	if v := os.Getenv("TEST_REDIS_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:6379"
}

func setupQueue(t *testing.T) *Queue {
	t.Helper()
	if testing.Short() {
		t.Skip("跳过集成测试（-short）")
	}

	ctx := context.Background()
	q, err := New(ctx, testAddr(), "", testDB)
	if err != nil {
		t.Skipf("连接测试 Redis 失败，跳过: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })

	// 清空测试库。用独立 DB 索引正是为了这一步不影响开发数据。
	if err := q.rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("清空测试库失败: %v", err)
	}
	return q
}

// TestEnqueueDequeueRoundTrip 覆盖最基本的路径。
func TestEnqueueDequeueRoundTrip(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	for _, id := range []int64{11, 22, 33} {
		if err := q.EnqueueAnalyze(ctx, id); err != nil {
			t.Fatalf("入队 %d 失败: %v", id, err)
		}
	}

	depth, err := q.Depth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 3 {
		t.Errorf("队列深度 = %d, want 3", depth)
	}

	// LPUSH 从左边压入、BRPOP 从右边取出，所以先入队的先出。
	for _, want := range []int64{11, 22, 33} {
		got, err := q.DequeueAnalyze(ctx, time.Second)
		if err != nil {
			t.Fatalf("出队失败: %v", err)
		}
		if got != want {
			t.Errorf("出队 = %d, want %d（应当先进先出）", got, want)
		}
	}
}

// TestDequeueEmptyReturnsErrQueueEmpty 确认空队列不会阻塞或返回零值。
//
// worker 依赖这个错误来判断"队列空"并继续等待，而不是把 0
// 当成一个合法的 incident id。
func TestDequeueEmptyReturnsErrQueueEmpty(t *testing.T) {
	q := setupQueue(t)

	start := time.Now()
	_, err := q.DequeueAnalyze(context.Background(), 200*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("空队列应返回 ErrQueueEmpty, got %v", err)
	}
	// 应当是阻塞等待到超时，而不是立即返回。
	if elapsed < 150*time.Millisecond {
		t.Errorf("应当阻塞等待，实际只用了 %v", elapsed)
	}
}

// TestDequeueReturnsAfterTimeout 确认阻塞等待会在超时后返回。
//
// 注意 go-redis 的 BRPOP 不会因 context 取消而立即返回——命令要等
// 服务端超时。worker 因此把单次阻塞时间设得很短（1 秒），
// 用"短阻塞 + 循环检查 ctx"来保证关闭延迟可接受。
//
// 这条测试固定的是那个前提：超时必须生效，且耗时接近设定值。
func TestDequeueReturnsAfterTimeout(t *testing.T) {
	q := setupQueue(t)

	start := time.Now()
	_, err := q.DequeueAnalyze(context.Background(), 500*time.Millisecond)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("超时后应返回 ErrQueueEmpty, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("超时设为 500ms，实际等了 %v", elapsed)
	}
}

// TestDequeueCancellationTakesEffectWithinTimeout 固定一个实际行为：
//
// context 取消不会立即中断 BRPOP，但会在超时后让调用返回。
// 记录这一点是因为它直接影响优雅关闭的最坏延迟——worker 的
// pollTimeout 设为 1 秒正是基于这个约束。
func TestDequeueCancellationTakesEffectWithinTimeout(t *testing.T) {
	q := setupQueue(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := q.DequeueAnalyze(ctx, time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("取消后应当返回错误")
	}
	// 关键：要在设定的超时附近返回，而不是无限阻塞。
	if elapsed > 3*time.Second {
		t.Errorf("取消后应当在超时附近返回，实际 %v", elapsed)
	}
}

func TestDepth(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	if d, err := q.Depth(ctx); err != nil || d != 0 {
		t.Errorf("初始深度 = %d, err = %v", d, err)
	}

	for i := 0; i < 5; i++ {
		if err := q.EnqueueAnalyze(ctx, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	if d, err := q.Depth(ctx); err != nil || d != 5 {
		t.Errorf("深度 = %d, err = %v, want 5", d, err)
	}
}

func TestPing(t *testing.T) {
	q := setupQueue(t)
	if err := q.Ping(context.Background()); err != nil {
		t.Errorf("Ping 失败: %v", err)
	}
}

// TestRateLimitAllowsUnderLimit 确认限额内放行并正确报告剩余。
func TestRateLimitAllowsUnderLimit(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		allowed, remaining, _, err := q.RateLimit(ctx, "test", 10, time.Minute)
		if err != nil {
			t.Fatalf("第 %d 次失败: %v", i+1, err)
		}
		if !allowed {
			t.Fatalf("第 %d 次就被拒绝，限额是 10", i+1)
		}
		wantRemaining := 10 - (i + 1)
		if remaining != wantRemaining {
			t.Errorf("第 %d 次剩余 = %d, want %d", i+1, remaining, wantRemaining)
		}
	}
}

// TestRateLimitBlocksOverLimit 确认超出后拒绝。
func TestRateLimitBlocksOverLimit(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	const limit = 3
	for i := 0; i < limit; i++ {
		allowed, _, _, err := q.RateLimit(ctx, "test", limit, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Fatalf("第 %d 次不该被拒绝", i+1)
		}
	}

	allowed, remaining, resetIn, err := q.RateLimit(ctx, "test", limit, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Error("超出限额应当被拒绝")
	}
	if remaining != 0 {
		t.Errorf("被拒绝时剩余 = %d, want 0", remaining)
	}
	if resetIn <= 0 {
		t.Error("被拒绝时应当报告窗口剩余时间")
	}
}

// TestRateLimitSeparatesKeys 确认不同 key 各自计数。
func TestRateLimitSeparatesKeys(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if _, _, _, err := q.RateLimit(ctx, "key-a", 2, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// key-a 已用满，key-b 应当还是空的。
	allowed, _, _, err := q.RateLimit(ctx, "key-b", 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed {
		t.Error("不同的 key 应当独立计数")
	}
}

// TestRateLimitWindowExpires 确认窗口过期后重新计数。
//
// 窗口至少 1 秒：Redis 的 EXPIRE 低于 1 秒会被截断到 1 秒，
// 用更小的值测不出想要的行为。
func TestRateLimitWindowExpires(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	const window = time.Second

	if allowed, _, _, _ := q.RateLimit(ctx, "expire", 1, window); !allowed {
		t.Fatal("第一次应当放行")
	}
	if allowed, _, _, _ := q.RateLimit(ctx, "expire", 1, window); allowed {
		t.Fatal("第二次应当被拒绝")
	}

	time.Sleep(window + 500*time.Millisecond)

	if allowed, _, _, _ := q.RateLimit(ctx, "expire", 1, window); !allowed {
		t.Error("窗口过期后应当重新放行")
	}
}

// TestRateLimitWindowStartsAtFirstRequest 确认过期时间从首次请求算起。
//
// 如果每次请求都刷新 TTL，持续的低频请求会让窗口永不失效，
// 累计计数最终把正常用户挡在外面。
func TestRateLimitWindowStartsAtFirstRequest(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	// 窗口用 3 秒：Redis 的 EXPIRE 低于 1 秒会被截断，
	// 而我们需要留出足够时间观察 TTL 是否被刷新。
	const window = 3 * time.Second

	if _, _, _, err := q.RateLimit(ctx, "sliding", 100, window); err != nil {
		t.Fatal(err)
	}

	// 在窗口内持续请求，TTL 不应当被刷新。
	for i := 0; i < 4; i++ {
		time.Sleep(300 * time.Millisecond)
		if _, _, _, err := q.RateLimit(ctx, "sliding", 100, window); err != nil {
			t.Fatal(err)
		}
	}

	// 首次请求已过约 1.2 秒。若 TTL 被刷新，它会接近 3 秒；
	// 若从首次请求算起，应当明显小于 2.5 秒。
	ttl, err := q.rdb.TTL(ctx, "ratelimit:sliding").Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl > 2500*time.Millisecond {
		t.Errorf("TTL = %v，看起来被刷新过（应当从首次请求开始计算）", ttl)
	}
}

// TestRateLimitKeysAreNamespaced 确认 key 带上了 ratelimit: 前缀。
//
// 没有前缀的话，限流计数会和业务数据混在同一个键空间里。
func TestRateLimitKeysAreNamespaced(t *testing.T) {
	q := setupQueue(t)
	ctx := context.Background()

	if _, _, _, err := q.RateLimit(ctx, "abc", 5, time.Minute); err != nil {
		t.Fatal(err)
	}

	keys, err := q.rdb.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range keys {
		if k == "ratelimit:abc" {
			found = true
		}
	}
	if !found {
		t.Errorf("应当存在键 ratelimit:abc, 实际键: %v", keys)
	}
}

var _ = redis.Options{}
