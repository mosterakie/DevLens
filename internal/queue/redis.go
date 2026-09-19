// Package queue 用 Redis list 实现分析任务的异步队列。
//
// 选 Redis 而不是 Kafka/RabbitMQ，是因为限流和缓存本来就要用 Redis，
// 不为此再引入一个中间件。代价见 docs/design/07-reliability.md：
// list 没有消费位点，也没有重放能力。
package queue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// AnalyzeQueueName 是待分析任务的主队列。
const AnalyzeQueueName = "analyze:queue"

// Queue 封装入队和出队。
type Queue struct {
	rdb *redis.Client
}

// New 建立客户端并验证连通性。
func New(ctx context.Context, addr, password string, db int) (*Queue, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Queue{rdb: rdb}, nil
}

// Close 关闭连接。
func (q *Queue) Close() error { return q.rdb.Close() }

// Ping 供健康检查使用。
func (q *Queue) Ping(ctx context.Context) error { return q.rdb.Ping(ctx).Err() }

// EnqueueAnalyze 把 Incident 的分析任务入队。
//
// 用 LPUSH + BRPOP 的组合：入队从左侧压入，消费从右侧取出，
// 于是先入队的先被处理。
func (q *Queue) EnqueueAnalyze(ctx context.Context, incidentID int64) error {
	err := q.rdb.LPush(ctx, AnalyzeQueueName, strconv.FormatInt(incidentID, 10)).Err()
	if err != nil {
		return fmt.Errorf("enqueue analyze: %w", err)
	}
	return nil
}

// Depth 返回队列积压长度，用于指标。
func (q *Queue) Depth(ctx context.Context) (int64, error) {
	return q.rdb.LLen(ctx, AnalyzeQueueName).Result()
}

// ErrQueueEmpty 在没有可消费任务时返回。
var ErrQueueEmpty = errors.New("queue empty")

// DequeueAnalyze 阻塞等待一个任务，超时返回 ErrQueueEmpty。
//
// 注意这里用的是 BRPOP 而不是 BRPOPLPUSH：任务被取出后如果 worker
// 崩溃，该任务会丢失。v1 接受这个风险，兜底手段是 worker 启动时
// 扫描长时间停留在 ANALYZING 的记录重新入队（见 07-reliability.md）。
func (q *Queue) DequeueAnalyze(ctx context.Context, timeout time.Duration) (int64, error) {
	res, err := q.rdb.BRPop(ctx, timeout, AnalyzeQueueName).Result()
	if errors.Is(err, redis.Nil) {
		return 0, ErrQueueEmpty
	}
	if err != nil {
		return 0, fmt.Errorf("dequeue analyze: %w", err)
	}
	// BRPOP 返回 [key, value]
	if len(res) != 2 {
		return 0, fmt.Errorf("unexpected BRPOP result length %d", len(res))
	}
	id, err := strconv.ParseInt(res[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse incident id %q: %w", res[1], err)
	}
	return id, nil
}

// RateLimit 实现固定时间窗内的计数，用于限流。
//
// 这里用固定窗口而不是滑动窗口：实现简单，且对当前用途足够。
// 代价是窗口边界处允许两倍突刺，见 07-reliability.md 的取舍说明。
func (q *Queue) RateLimit(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetIn time.Duration, err error) {
	fullKey := "ratelimit:" + key

	pipe := q.rdb.TxPipeline()
	incr := pipe.Incr(ctx, fullKey)
	// 只在第一次计数时设置过期，让窗口从首次请求开始计算。
	pipe.ExpireNX(ctx, fullKey, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, 0, fmt.Errorf("rate limit pipeline: %w", err)
	}

	n := incr.Val()
	if n > int64(limit) {
		ttl, terr := q.rdb.TTL(ctx, fullKey).Result()
		if terr != nil {
			ttl = 0
		}
		return false, 0, ttl, nil
	}
	return true, limit - int(n), window, nil
}
