package limiter

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrWaitTimeout 表示等待配额超时。
var ErrWaitTimeout = errors.New("rate limit wait timeout exceeded")

// Waiter 是等待式（阻塞排队）限流器。
//
// 与 SlidingWindow 的「拒绝式」不同，Waiter 在配额不足时不会立即拒绝请求，
// 而是阻塞等待直到获得配额，适用于「1 秒 1 次」这类需要平滑排队的场景。
//
// 实现基于令牌桶算法：
//   - 令牌以 rate 的速率匀速补充，桶容量为 burst
//   - 请求消耗 1 个令牌；令牌不足时计算所需等待时间并阻塞
//   - 等待超过 maxWait 或 context 取消时返回错误
//
// 该实现保证并发安全，且多个等待者按先来先服务（FIFO）顺序获得令牌。
type Waiter struct {
	mu sync.Mutex

	rate    float64       // 每秒补充的令牌数
	burst   int           // 桶容量（突发上限）
	maxWait time.Duration // 单次请求最大等待时长

	tokens   float64   // 当前可用令牌数
	lastTime time.Time // 上次补充令牌的时间

	// nextAvailable 记录下一个可用令牌的时间点，用于 FIFO 排队。
	nextAvailable time.Time

	total  int64 // 累计请求数
	waited int64 // 累计发生等待的请求数

	// 等待耗时统计（仅统计实际发生等待的请求，单位纳秒）
	waitSumNs int64 // 等待耗时总和
	waitMaxNs int64 // 单次最大等待耗时
	waitMinNs int64 // 单次最小等待耗时（0 表示尚未记录）
	timedOut  int64 // 等待超时（返回 ErrWaitTimeout）的请求数
	canceled  int64 // 等待期间被取消的请求数
}

// WaiterOptions 是 Waiter 的构造参数。
type WaiterOptions struct {
	Rate    float64       // 每秒允许的请求数（QPS），<=0 规整为 1
	Burst   int           // 突发容量，<=0 规整为 1
	MaxWait time.Duration // 最大等待时长，<=0 规整为 30 秒
}

// NewWaiter 创建等待式限流器。
func NewWaiter(opts WaiterOptions) *Waiter {
	if opts.Rate <= 0 {
		opts.Rate = 1
	}
	if opts.Burst <= 0 {
		opts.Burst = 1
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = 30 * time.Second
	}

	now := time.Now()
	return &Waiter{
		rate:          opts.Rate,
		burst:         opts.Burst,
		maxWait:       opts.MaxWait,
		tokens:        float64(opts.Burst), // 初始满桶，允许首次突发
		lastTime:      now,
		nextAvailable: now,
	}
}

// Wait 阻塞等待直到获得一个配额。
//
// 返回值：
//   - nil：成功获得配额，可以继续处理请求
//   - ErrWaitTimeout：等待超过 maxWait
//   - context 相关错误：ctx 被取消或超时
//
// 实现说明：令牌被并发抢占时使用**循环**重试而非递归，
// 避免 total/waited 被重复计数（递归会让统计虚高）。
func (w *Waiter) Wait(ctx context.Context) error {
	w.mu.Lock()
	w.total++

	now := time.Now()
	w.refill(now)

	// 令牌充足：立即放行
	if w.tokens >= 1 {
		w.tokens--
		w.mu.Unlock()
		return nil
	}

	// 令牌不足：计算需要等待的时间
	// 需要补充的令牌数
	needed := 1 - w.tokens
	waitDuration := time.Duration(needed / w.rate * float64(time.Second))

	// 基于 nextAvailable 实现 FIFO：后到的请求排在已有等待者之后
	base := w.nextAvailable
	if base.Before(now) {
		base = now
	}
	readyAt := base.Add(waitDuration)
	w.nextAvailable = readyAt

	w.waited++
	w.mu.Unlock()

	// 循环等待：令牌被抢占时重新计算等待时间，而非递归调用。
	// 整个等待过程受 maxWait 约束（累计等待时长）。
	deadline := time.Now().Add(w.maxWait)
	for {
		// 计算实际需要等待的时长
		actualWait := time.Until(readyAt)
		if actualWait < 0 {
			actualWait = 0
		}

		// 超过最大等待时长：直接返回超时错误
		if time.Now().Add(actualWait).After(deadline) {
			w.mu.Lock()
			w.timedOut++
			w.mu.Unlock()
			return ErrWaitTimeout
		}

		timer := time.NewTimer(actualWait)
		select {
		case <-timer.C:
			w.mu.Lock()
			w.refill(time.Now())
			if w.tokens >= 1 {
				w.tokens--
				w.recordWait(time.Since(deadline.Add(-w.maxWait)))
				w.mu.Unlock()
				return nil
			}
			// 令牌被其他并发请求抢占：重新排队并继续循环（不递归）。
			needed := 1 - w.tokens
			waitDuration := time.Duration(needed / w.rate * float64(time.Second))
			base := w.nextAvailable
			if base.Before(time.Now()) {
				base = time.Now()
			}
			readyAt = base.Add(waitDuration)
			w.nextAvailable = readyAt
			w.mu.Unlock()

		case <-ctx.Done():
			timer.Stop()
			w.mu.Lock()
			w.canceled++
			w.mu.Unlock()
			return ctx.Err()
		}
	}
}

// recordWait 记录一次等待的耗时统计。调用方必须持有锁。
func (w *Waiter) recordWait(d time.Duration) {
	ns := d.Nanoseconds()
	w.waitSumNs += ns
	if ns > w.waitMaxNs {
		w.waitMaxNs = ns
	}
	if w.waitMinNs == 0 || ns < w.waitMinNs {
		w.waitMinNs = ns
	}
}

// refill 按经过的时间补充令牌。调用方必须持有锁。
func (w *Waiter) refill(now time.Time) {
	elapsed := now.Sub(w.lastTime).Seconds()
	if elapsed <= 0 {
		return
	}
	w.tokens += elapsed * w.rate
	if w.tokens > float64(w.burst) {
		w.tokens = float64(w.burst)
	}
	w.lastTime = now
}

// Stats 返回限流器统计快照。
func (w *Waiter) Stats() WaiterStats {
	w.mu.Lock()
	defer w.mu.Unlock()

	var avgWait time.Duration
	if w.waited > 0 {
		avgWait = time.Duration(w.waitSumNs / w.waited)
	}

	return WaiterStats{
		Total:    w.total,
		Waited:   w.waited,
		Rate:     w.rate,
		Burst:    w.burst,
		TimedOut: w.timedOut,
		Canceled: w.canceled,
		AvgWait:  avgWait,
		MaxWait:  time.Duration(w.waitMaxNs),
		MinWait:  time.Duration(w.waitMinNs),
	}
}

// WaiterStats 是等待式限流器的统计快照。
type WaiterStats struct {
	Total    int64         // 累计请求数
	Waited   int64         // 累计发生等待的请求数
	Rate     float64       // 配置的速率（QPS）
	Burst    int           // 配置的突发容量
	TimedOut int64         // 等待超时（429）的请求数
	Canceled int64         // 等待期间被取消的请求数
	AvgWait  time.Duration // 平均等待耗时（仅统计发生等待的请求）
	MaxWait  time.Duration // 单次最大等待耗时
	MinWait  time.Duration // 单次最小等待耗时
}
