package retry

import (
	"net/http"
	"testing"
	"time"
)

// TestPolicy_DisabledNeverRetries 未启用时任何状态码都不重试。
func TestPolicy_DisabledNeverRetries(t *testing.T) {
	p := New(Options{Enabled: false})
	if p.Enabled() {
		t.Error("Enabled 应为 false")
	}
	for _, s := range []int{429, 503, 500, 502} {
		if p.ShouldRetry(s) {
			t.Errorf("未启用时状态码 %d 不应重试", s)
		}
	}
}

// TestPolicy_DefaultStatuses 默认只对 429 与 503 重试。
func TestPolicy_DefaultStatuses(t *testing.T) {
	p := New(Options{Enabled: true})
	if !p.ShouldRetry(http.StatusTooManyRequests) {
		t.Error("429 应触发重试")
	}
	if !p.ShouldRetry(http.StatusServiceUnavailable) {
		t.Error("503 应触发重试")
	}
	if p.ShouldRetry(http.StatusInternalServerError) {
		t.Error("500 默认不应触发重试")
	}
	if p.ShouldRetry(http.StatusOK) {
		t.Error("200 不应触发重试")
	}
}

// TestPolicy_CustomStatuses 自定义状态码集合。
func TestPolicy_CustomStatuses(t *testing.T) {
	p := New(Options{Enabled: true, Statuses: []int{500, 502}})
	if !p.ShouldRetry(500) || !p.ShouldRetry(502) {
		t.Error("自定义状态码应触发重试")
	}
	if p.ShouldRetry(429) {
		t.Error("未列入自定义集合的 429 不应重试")
	}
}

// TestPolicy_Defaults 默认参数填充。
func TestPolicy_Defaults(t *testing.T) {
	p := New(Options{Enabled: true})
	if p.MaxAttempts() != defaultMaxAttempts {
		t.Errorf("默认最大尝试次数应为 %d，实际 %d", defaultMaxAttempts, p.MaxAttempts())
	}
}

// TestPolicy_ExponentialBackoff 无 Retry-After 时使用指数退避。
func TestPolicy_ExponentialBackoff(t *testing.T) {
	p := New(Options{Enabled: true, InitialWait: time.Second, MaxWait: time.Minute})
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
	}
	for _, c := range cases {
		got := p.Wait(c.attempt, nil)
		if got != c.want {
			t.Errorf("第 %d 次重试等待应为 %v，实际 %v", c.attempt, c.want, got)
		}
	}
}

// TestPolicy_BackoffClampedByMaxWait 退避时长受 MaxWait 约束。
func TestPolicy_BackoffClampedByMaxWait(t *testing.T) {
	p := New(Options{Enabled: true, InitialWait: time.Second, MaxWait: 3 * time.Second})
	if got := p.Wait(5, nil); got != 3*time.Second {
		t.Errorf("退避应被限制为 3s，实际 %v", got)
	}
}

// TestPolicy_RespectsRetryAfterSeconds 优先使用 Retry-After 秒数。
func TestPolicy_RespectsRetryAfterSeconds(t *testing.T) {
	p := New(Options{Enabled: true, InitialWait: time.Second, MaxWait: time.Minute})
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "7")

	if got := p.Wait(1, resp); got != 7*time.Second {
		t.Errorf("应使用 Retry-After=7s，实际 %v", got)
	}
}

// TestPolicy_RetryAfterClampedByMaxWait Retry-After 也受 MaxWait 约束。
func TestPolicy_RetryAfterClampedByMaxWait(t *testing.T) {
	p := New(Options{Enabled: true, MaxWait: 5 * time.Second})
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "120")

	if got := p.Wait(1, resp); got != 5*time.Second {
		t.Errorf("Retry-After 应被限制为 5s，实际 %v", got)
	}
}

// TestPolicy_RespectsRetryAfterHTTPDate 支持 HTTP 日期格式的 Retry-After。
func TestPolicy_RespectsRetryAfterHTTPDate(t *testing.T) {
	p := New(Options{Enabled: true, MaxWait: time.Minute})
	resp := &http.Response{Header: http.Header{}}
	future := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	resp.Header.Set("Retry-After", future)

	got := p.Wait(1, resp)
	// 允许一定误差（解析与执行耗时）
	if got < 2*time.Second || got > 4*time.Second {
		t.Errorf("HTTP 日期格式 Retry-After 应约 3s，实际 %v", got)
	}
}

// TestPolicy_InvalidRetryAfterFallsBackToBackoff 非法 Retry-After 回退到退避。
func TestPolicy_InvalidRetryAfterFallsBackToBackoff(t *testing.T) {
	p := New(Options{Enabled: true, InitialWait: time.Second, MaxWait: time.Minute})
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("Retry-After", "not-a-valid-value")

	if got := p.Wait(1, resp); got != time.Second {
		t.Errorf("非法 Retry-After 应回退到退避 1s，实际 %v", got)
	}
}

// TestPolicy_NilSafe nil 策略安全。
func TestPolicy_NilSafe(t *testing.T) {
	var p *Policy
	if p.Enabled() {
		t.Error("nil 策略 Enabled 应为 false")
	}
	if p.ShouldRetry(429) {
		t.Error("nil 策略不应重试")
	}
	if p.MaxAttempts() != 1 {
		t.Error("nil 策略最大尝试次数应为 1")
	}
	if p.Wait(1, nil) != 0 {
		t.Error("nil 策略等待应为 0")
	}
}
