// Package retry 实现上游限流（429/503）时的自动等待重试策略。
//
// 设计要点：
//   - 默认关闭，不改变既有行为
//   - 优先尊重上游返回的 Retry-After 头，其次使用指数退避
//   - 只在响应头阶段重试（body 未开始传输），避免破坏 SSE 流
//   - 与熔断器联动：重试耗尽仍失败时计入熔断失败
package retry

import (
	"math"
	"net/http"
	"strconv"
	"time"
)

// Options 是重试策略的构造参数。
type Options struct {
	// Enabled 为 true 时启用重试。
	Enabled bool
	// MaxAttempts 是最大尝试次数（含首次请求）。<=0 时使用默认值 3。
	MaxAttempts int
	// InitialWait 是首次重试的等待时长。<=0 时使用默认值 1s。
	InitialWait time.Duration
	// MaxWait 是单次等待的上限。<=0 时使用默认值 10s。
	MaxWait time.Duration
	// Statuses 是触发重试的 HTTP 状态码集合。为空时使用默认 {429, 503}。
	Statuses []int
}

// 默认参数。
const (
	defaultMaxAttempts = 3
	defaultInitialWait = time.Second
	defaultMaxWait     = 10 * time.Second
)

// Policy 是重试策略。
type Policy struct {
	enabled     bool
	maxAttempts int
	initialWait time.Duration
	maxWait     time.Duration
	statuses    map[int]bool
}

// New 创建重试策略。
func New(opts Options) *Policy {
	p := &Policy{
		enabled:     opts.Enabled,
		maxAttempts: opts.MaxAttempts,
		initialWait: opts.InitialWait,
		maxWait:     opts.MaxWait,
		statuses:    make(map[int]bool),
	}
	if p.maxAttempts <= 0 {
		p.maxAttempts = defaultMaxAttempts
	}
	if p.initialWait <= 0 {
		p.initialWait = defaultInitialWait
	}
	if p.maxWait <= 0 {
		p.maxWait = defaultMaxWait
	}
	if len(opts.Statuses) == 0 {
		p.statuses[http.StatusTooManyRequests] = true
		p.statuses[http.StatusServiceUnavailable] = true
	} else {
		for _, s := range opts.Statuses {
			p.statuses[s] = true
		}
	}
	return p
}

// Enabled 返回是否启用重试。
func (p *Policy) Enabled() bool {
	return p != nil && p.enabled
}

// MaxAttempts 返回最大尝试次数（含首次）。
func (p *Policy) MaxAttempts() int {
	if p == nil {
		return 1
	}
	return p.maxAttempts
}

// ShouldRetry 判断给定状态码是否应触发重试。
func (p *Policy) ShouldRetry(status int) bool {
	if !p.Enabled() {
		return false
	}
	return p.statuses[status]
}

// Wait 计算第 attempt 次重试（attempt 从 1 开始）应等待的时长。
//
// 优先使用上游返回的 Retry-After 头（秒数或 HTTP 日期），
// 否则使用指数退避：initialWait * 2^(attempt-1)，并受 maxWait 约束。
func (p *Policy) Wait(attempt int, resp *http.Response) time.Duration {
	if p == nil {
		return 0
	}

	// 优先尊重上游的 Retry-After。
	if resp != nil {
		if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
			return clamp(d, p.maxWait)
		}
	}

	if attempt < 1 {
		attempt = 1
	}
	// 指数退避：1s, 2s, 4s, 8s ...
	backoff := float64(p.initialWait) * math.Pow(2, float64(attempt-1))
	d := time.Duration(backoff)
	return clamp(d, p.maxWait)
}

// clamp 将时长限制在 [0, max] 范围内。
func clamp(d, max time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if max > 0 && d > max {
		return max
	}
	return d
}

// parseRetryAfter 解析 Retry-After 头，支持秒数与 HTTP 日期两种格式。
func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	// 格式一：秒数
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	// 格式二：HTTP 日期
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}
