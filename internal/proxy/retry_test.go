package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/retry"
)

// TestProxy_RetryDisabledByDefault 未配置重试时 429 直接透传。
func TestProxy_RetryDisabledByDefault(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Retry() != nil {
		t.Error("未配置时 Retry() 应返回 nil")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("应透传 429，实际 %d", rec.Code)
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Errorf("不应重试，上游应被调用 1 次，实际 %d", hits)
	}
}

// TestProxy_RetryOn429ThenSuccess 上游先 429 后成功，代理自动重试。
func TestProxy_RetryOn429ThenSuccess(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&hits, 1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	pol := retry.New(retry.Options{
		Enabled:     true,
		MaxAttempts: 3,
		InitialWait: 10 * time.Millisecond,
		MaxWait:     50 * time.Millisecond,
	})
	p, err := New(Options{Upstream: upstream.URL, Retry: pol})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("重试后应返回 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("响应体应为 ok，实际 %q", rec.Body.String())
	}
	if atomic.LoadInt64(&hits) != 2 {
		t.Errorf("上游应被调用 2 次，实际 %d", hits)
	}
}

// TestProxy_RetryExhaustedPassesThrough 重试耗尽后透传最后一次响应。
func TestProxy_RetryExhaustedPassesThrough(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	pol := retry.New(retry.Options{
		Enabled:     true,
		MaxAttempts: 3,
		InitialWait: 5 * time.Millisecond,
		MaxWait:     10 * time.Millisecond,
	})
	p, err := New(Options{Upstream: upstream.URL, Retry: pol})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("重试耗尽应透传 429，实际 %d", rec.Code)
	}
	if atomic.LoadInt64(&hits) != 3 {
		t.Errorf("应尝试 3 次，实际 %d", hits)
	}
	// 响应体应完整透传
	if !strings.Contains(rec.Body.String(), "rate limited") {
		t.Errorf("响应体应透传，实际 %q", rec.Body.String())
	}
	// Retry-After 头应透传
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("Retry-After 头应透传，实际 %q", rec.Header().Get("Retry-After"))
	}
}

// TestProxy_RetryNotTriggeredOn500 默认不对 500 重试。
func TestProxy_RetryNotTriggeredOn500(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	pol := retry.New(retry.Options{Enabled: true, MaxAttempts: 3, InitialWait: time.Millisecond})
	p, _ := New(Options{Upstream: upstream.URL, Retry: pol})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("应返回 500，实际 %d", rec.Code)
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Errorf("500 不应重试，上游应被调用 1 次，实际 %d", hits)
	}
}

// TestProxy_RetryPreservesRequestBody 重试时请求体被正确重放。
func TestProxy_RetryPreservesRequestBody(t *testing.T) {
	const payload = `{"model":"test","messages":[]}`
	var bodies []string
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&hits, 1)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		bodies = append(bodies, string(buf))
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	pol := retry.New(retry.Options{Enabled: true, MaxAttempts: 3, InitialWait: 5 * time.Millisecond})
	p, _ := New(Options{Upstream: upstream.URL, Retry: pol})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	if len(bodies) != 2 {
		t.Fatalf("上游应收到 2 次请求，实际 %d", len(bodies))
	}
	for i, b := range bodies {
		if b != payload {
			t.Errorf("第 %d 次请求体应完整重放，期望 %q，实际 %q", i+1, payload, b)
		}
	}
}

// TestProxy_RetryRespectsRetryAfterHeader 重试等待尊重上游 Retry-After。
func TestProxy_RetryRespectsRetryAfterHeader(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&hits, 1)
		if n == 1 {
			// 上游要求等待 1 秒（但策略 MaxWait 会限制为 200ms）
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	pol := retry.New(retry.Options{
		Enabled:     true,
		MaxAttempts: 2,
		InitialWait: time.Millisecond,
		MaxWait:     200 * time.Millisecond, // 限制等待上限
	})
	p, _ := New(Options{Upstream: upstream.URL, Retry: pol})

	start := time.Now()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	// 等待应被 MaxWait 限制在 200ms 附近（允许调度误差）
	if elapsed < 150*time.Millisecond {
		t.Errorf("应等待约 200ms，实际 %v", elapsed)
	}
	if elapsed > 800*time.Millisecond {
		t.Errorf("等待不应超过 MaxWait 太多，实际 %v", elapsed)
	}
}

// TestProxy_RetryWithBreaker 重试与熔断器共存时行为正确。
func TestProxy_RetryWithBreaker(t *testing.T) {
	var hits int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	pol := retry.New(retry.Options{Enabled: true, MaxAttempts: 2, InitialWait: time.Millisecond})
	p, _ := New(Options{Upstream: upstream.URL, Retry: pol})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("应透传 429，实际 %d", rec.Code)
	}
	if atomic.LoadInt64(&hits) != 2 {
		t.Errorf("应尝试 2 次，实际 %d", hits)
	}
}
