// Package breaker 实现轻量级熔断器，用于在上游持续失败时快速失败，
// 避免客户端长时间挂起、也避免对已故障的上游持续施压。
//
// 状态机：
//
//	Closed   ──连续失败达阈值──> Open
//	Open     ──冷却时间到──────> HalfOpen
//	HalfOpen ──试探成功────────> Closed
//	HalfOpen ──试探失败────────> Open
//
// 设计要点：
//   - 零第三方依赖，仅用标准库
//   - 并发安全（sync.Mutex 保护全部状态）
//   - 默认关闭（Enabled=false 时 Allow 恒为 true），不改变既有行为
package breaker

import (
	"sync"
	"time"
)

// State 是熔断器状态。
type State int

const (
	// StateClosed 表示正常放行（熔断器关闭）。
	StateClosed State = iota
	// StateOpen 表示熔断中，所有请求快速失败。
	StateOpen
	// StateHalfOpen 表示半开，允许少量试探请求。
	StateHalfOpen
)

// String 返回状态的可读名称。
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Options 是熔断器的构造参数。
type Options struct {
	// Enabled 为 false 时熔断器不生效，Allow 恒返回 true。
	Enabled bool
	// Threshold 是连续失败多少次后熔断，<=0 规整为 5。
	Threshold int
	// Cooldown 是熔断后进入半开的冷却时长，<=0 规整为 30 秒。
	Cooldown time.Duration
}

// Breaker 是熔断器。
type Breaker struct {
	mu sync.Mutex

	enabled   bool
	threshold int
	cooldown  time.Duration

	state       State
	failures    int       // 连续失败计数（Closed 状态下）
	openedAt    time.Time // 进入 Open 的时间
	halfOpenReq int       // 半开状态下已放行的试探请求数

	totalTrips int64 // 累计熔断次数
	rejected   int64 // 累计被快速拒绝的请求数
}

// New 创建熔断器。
func New(opts Options) *Breaker {
	if opts.Threshold <= 0 {
		opts.Threshold = 5
	}
	if opts.Cooldown <= 0 {
		opts.Cooldown = 30 * time.Second
	}
	return &Breaker{
		enabled:   opts.Enabled,
		threshold: opts.Threshold,
		cooldown:  opts.Cooldown,
		state:     StateClosed,
	}
}

// Allow 判断当前请求是否放行。
//
// 返回 false 表示应快速失败（熔断中）。
// 熔断器未启用时恒返回 true。
func (b *Breaker) Allow() bool {
	if !b.enabled {
		return true
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true

	case StateOpen:
		// 冷却时间未到：继续拒绝
		if time.Since(b.openedAt) < b.cooldown {
			b.rejected++
			return false
		}
		// 冷却结束：进入半开，放行一个试探请求
		b.state = StateHalfOpen
		b.halfOpenReq = 1
		return true

	case StateHalfOpen:
		// 半开状态下只放行一个试探请求，其余继续拒绝
		if b.halfOpenReq > 0 {
			b.rejected++
			return false
		}
		b.halfOpenReq = 1
		return true
	}

	return true
}

// Success 记录一次成功，重置失败计数并关闭熔断器。
func (b *Breaker) Success() {
	if !b.enabled {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.failures = 0
	b.halfOpenReq = 0
	b.state = StateClosed
}

// Failure 记录一次失败，达到阈值时熔断。
func (b *Breaker) Failure() {
	if !b.enabled {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateHalfOpen:
		// 试探失败：立即重新熔断
		b.state = StateOpen
		b.openedAt = time.Now()
		b.halfOpenReq = 0
		b.totalTrips++

	case StateClosed:
		b.failures++
		if b.failures >= b.threshold {
			b.state = StateOpen
			b.openedAt = time.Now()
			b.totalTrips++
		}

	case StateOpen:
		// 已在熔断中，刷新冷却起点
		b.openedAt = time.Now()
	}
}

// State 返回当前状态。
func (b *Breaker) State() State {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 冷却已过但尚未有请求触发状态迁移时，对外报告半开。
	if b.state == StateOpen && time.Since(b.openedAt) >= b.cooldown {
		return StateHalfOpen
	}
	return b.state
}

// Stats 返回熔断器统计快照。
func (b *Breaker) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()

	return Stats{
		Enabled:    b.enabled,
		State:      b.state.String(),
		Failures:   b.failures,
		Threshold:  b.threshold,
		Trips:      b.totalTrips,
		Rejected:   b.rejected,
		CooldownMs: b.cooldown.Milliseconds(),
	}
}

// Stats 是熔断器统计快照。
type Stats struct {
	Enabled    bool   `json:"enabled"`     // 是否启用
	State      string `json:"state"`       // 当前状态（closed/open/half-open）
	Failures   int    `json:"failures"`    // 当前连续失败数
	Threshold  int    `json:"threshold"`   // 熔断阈值
	Trips      int64  `json:"trips"`       // 累计熔断次数
	Rejected   int64  `json:"rejected"`    // 累计快速拒绝数
	CooldownMs int64  `json:"cooldown_ms"` // 冷却时长（毫秒）
}
