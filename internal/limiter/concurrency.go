package limiter

import (
	"context"
	"sync"
	"time"
)

// ConcurrencyLimiter 是「按请求大小分档」的并发限流器。
//
// 与 Waiter（速率限流，控制「每秒几个」）不同，本限流器控制「同时在途几个」：
// 请求按估算 token 数落入「小请求」或「大请求」档位，各档位有独立的并发上限。
// 典型场景：大请求（长上下文）会长时间占用上游连接，需要限制其并发数，
// 避免拖垮上游；小请求则可以放宽。
//
// 语义：
//   - 估算 token <= threshold → 小请求档，上限 smallLimit
//   - 估算 token >  threshold → 大请求档，上限 largeLimit
//   - 上限 <= 0 表示该档不限制（直接放行）
//
// 并发安全，等待者按先来先服务（FIFO）顺序获得槽位。
type ConcurrencyLimiter struct {
	mu sync.Mutex

	threshold  int           // 大小请求分界（估算 token 数）
	smallLimit int           // 小请求并发上限，<=0 表示不限
	largeLimit int           // 大请求并发上限，<=0 表示不限
	maxWait    time.Duration // 单次请求最大等待时长

	smallActive int // 小请求当前在途数
	largeActive int // 大请求当前在途数

	// 等待队列：FIFO，记录每个等待者的档位与就绪信号。
	queue []*concurrencyWaiter

	// 统计
	total    int64 // 累计请求数
	waited   int64 // 累计发生等待的请求数
	timedOut int64 // 等待超时（429）的请求数
	canceled int64 // 等待期间被取消的请求数
	small    int64 // 累计小请求数
	large    int64 // 累计大请求数
}

// concurrencyWaiter 是等待队列中的一个等待者。
type concurrencyWaiter struct {
	large bool          // 是否为大请求档
	ready chan struct{} // 获得槽位时关闭
}

// ConcurrencyOptions 是 ConcurrencyLimiter 的构造参数。
type ConcurrencyOptions struct {
	Threshold  int           // 大小请求分界（估算 token 数），<=0 规整为 10000
	SmallLimit int           // 小请求并发上限，<=0 表示不限制
	LargeLimit int           // 大请求并发上限，<=0 表示不限制
	MaxWait    time.Duration // 最大等待时长，<=0 规整为 30 秒
}

// NewConcurrencyLimiter 创建按请求大小分档的并发限流器。
func NewConcurrencyLimiter(opts ConcurrencyOptions) *ConcurrencyLimiter {
	if opts.Threshold <= 0 {
		opts.Threshold = 10000
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = 30 * time.Second
	}
	return &ConcurrencyLimiter{
		threshold:  opts.Threshold,
		smallLimit: opts.SmallLimit,
		largeLimit: opts.LargeLimit,
		maxWait:    opts.MaxWait,
	}
}

// Acquire 获取一个并发槽位，返回释放函数。
//
// tokens 是请求的估算 token 数，用于判定档位。
// 返回的 release 函数必须被调用（通常 defer），否则槽位会泄漏。
//
// 返回值：
//   - nil：成功获得槽位
//   - ErrWaitTimeout：等待超过 maxWait
//   - context 相关错误：ctx 被取消或超时
func (c *ConcurrencyLimiter) Acquire(ctx context.Context, tokens int) (func(), error) {
	large := tokens > c.threshold

	c.mu.Lock()
	c.total++
	if large {
		c.large++
	} else {
		c.small++
	}

	// 该档位不限制：直接放行（release 为空操作）。
	if c.limitOf(large) <= 0 {
		c.mu.Unlock()
		return func() {}, nil
	}

	// 有空闲槽位：立即占用。
	if c.activeOf(large) < c.limitOf(large) {
		c.addActive(large, 1)
		c.mu.Unlock()
		return c.releaseFunc(large), nil
	}

	// 槽位已满：进入 FIFO 等待队列。
	w := &concurrencyWaiter{large: large, ready: make(chan struct{})}
	c.queue = append(c.queue, w)
	c.waited++
	c.mu.Unlock()

	timer := time.NewTimer(c.maxWait)
	defer timer.Stop()

	select {
	case <-w.ready:
		// 已由 release 分配槽位（active 已在 release 中加 1）。
		return c.releaseFunc(large), nil
	case <-timer.C:
		c.mu.Lock()
		if c.removeWaiter(w) {
			// 成功从队列摘除：本次等待超时。
			c.timedOut++
			c.mu.Unlock()
			return nil, ErrWaitTimeout
		}
		// 已被 release 分配槽位（竞态）：视为成功，正常释放。
		c.mu.Unlock()
		return c.releaseFunc(large), nil
	case <-ctx.Done():
		c.mu.Lock()
		if c.removeWaiter(w) {
			c.canceled++
			c.mu.Unlock()
			return nil, ctx.Err()
		}
		c.mu.Unlock()
		return c.releaseFunc(large), nil
	}
}

// releaseFunc 返回释放槽位的闭包。
func (c *ConcurrencyLimiter) releaseFunc(large bool) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.addActive(large, -1)
			c.dispatch()
		})
	}
}

// dispatch 把空闲槽位分配给队首等待者。调用方必须持有锁。
//
// 只唤醒队首中「该档位仍有空闲槽位」的等待者，保证 FIFO 且不超发。
func (c *ConcurrencyLimiter) dispatch() {
	for len(c.queue) > 0 {
		w := c.queue[0]
		if c.activeOf(w.large) >= c.limitOf(w.large) {
			// 队首档位已满：停止派发，保持 FIFO 公平性。
			return
		}
		c.queue = c.queue[1:]
		c.addActive(w.large, 1)
		close(w.ready)
	}
}

// removeWaiter 从等待队列中摘除指定等待者，成功返回 true。调用方必须持有锁。
func (c *ConcurrencyLimiter) removeWaiter(target *concurrencyWaiter) bool {
	for i, w := range c.queue {
		if w == target {
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			return true
		}
	}
	return false
}

// limitOf 返回指定档位的并发上限。调用方必须持有锁。
func (c *ConcurrencyLimiter) limitOf(large bool) int {
	if large {
		return c.largeLimit
	}
	return c.smallLimit
}

// activeOf 返回指定档位当前在途数。调用方必须持有锁。
func (c *ConcurrencyLimiter) activeOf(large bool) int {
	if large {
		return c.largeActive
	}
	return c.smallActive
}

// addActive 调整指定档位的在途计数。调用方必须持有锁。
func (c *ConcurrencyLimiter) addActive(large bool, delta int) {
	if large {
		c.largeActive += delta
	} else {
		c.smallActive += delta
	}
}

// Stats 返回并发限流器统计快照。
func (c *ConcurrencyLimiter) Stats() ConcurrencyStats {
	c.mu.Lock()
	defer c.mu.Unlock()

	return ConcurrencyStats{
		Threshold:   c.threshold,
		SmallLimit:  c.smallLimit,
		LargeLimit:  c.largeLimit,
		SmallActive: c.smallActive,
		LargeActive: c.largeActive,
		Waiting:     len(c.queue),
		Total:       c.total,
		Small:       c.small,
		Large:       c.large,
		Waited:      c.waited,
		TimedOut:    c.timedOut,
		Canceled:    c.canceled,
	}
}

// ConcurrencyStats 是并发限流器的统计快照。
type ConcurrencyStats struct {
	Threshold   int   // 大小请求分界（估算 token 数）
	SmallLimit  int   // 小请求并发上限（<=0 表示不限）
	LargeLimit  int   // 大请求并发上限（<=0 表示不限）
	SmallActive int   // 小请求当前在途数
	LargeActive int   // 大请求当前在途数
	Waiting     int   // 当前排队等待数
	Total       int64 // 累计请求数
	Small       int64 // 累计小请求数
	Large       int64 // 累计大请求数
	Waited      int64 // 累计发生等待的请求数
	TimedOut    int64 // 等待超时（429）的请求数
	Canceled    int64 // 等待期间被取消的请求数
}
