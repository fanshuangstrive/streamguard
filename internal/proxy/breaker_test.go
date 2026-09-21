package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/breaker"
)

// TestProxy_BreakerOpenReturns503 熔断开启时直接返回 503，不发起上游请求。
func TestProxy_BreakerOpenReturns503(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	br := breaker.New(breaker.Options{Enabled: true, Threshold: 1, Cooldown: time.Minute})
	br.Failure() // 立即熔断

	p, err := New(Options{Upstream: upstream.URL, Breaker: br})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("熔断时应返回 503，实际 %d", rec.Code)
	}
	if upstreamHits != 0 {
		t.Errorf("熔断时不应请求上游，实际请求 %d 次", upstreamHits)
	}
}

// TestProxy_BreakerClosedForwards 熔断器关闭时正常转发。
func TestProxy_BreakerClosedForwards(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	br := breaker.New(breaker.Options{Enabled: true, Threshold: 5, Cooldown: time.Minute})
	p, err := New(Options{Upstream: upstream.URL, Breaker: br})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("正常时应返回 200，实际 %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("响应体应为 ok，实际 %q", rec.Body.String())
	}
}

// TestProxy_BreakerTripsOnUpstreamDown 上游不可达时累计失败并最终熔断。
func TestProxy_BreakerTripsOnUpstreamDown(t *testing.T) {
	// 指向一个已关闭的端口，保证连接失败
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := upstream.URL
	upstream.Close()

	br := breaker.New(breaker.Options{Enabled: true, Threshold: 2, Cooldown: time.Minute})
	p, err := New(Options{Upstream: deadURL, Breaker: br})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 前两次请求触发失败计数
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("第 %d 次应返回 502，实际 %d", i+1, rec.Code)
		}
	}

	if br.State() != breaker.StateOpen {
		t.Fatalf("连续失败后应熔断，实际 %s", br.State())
	}

	// 第三次请求应被快速拒绝（503）
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("熔断后应返回 503，实际 %d", rec.Code)
	}
}

// TestProxy_BreakerSuccessOn2xx 上游 2xx 响应会重置熔断器失败计数。
func TestProxy_BreakerSuccessOn2xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	br := breaker.New(breaker.Options{Enabled: true, Threshold: 3, Cooldown: time.Minute})
	br.Failure()
	br.Failure()

	p, err := New(Options{Upstream: upstream.URL, Breaker: br})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	if br.State() != breaker.StateClosed {
		t.Errorf("成功后应保持 closed，实际 %s", br.State())
	}
}

// TestProxy_BreakerFailureOn5xx 上游 5xx 视为故障，累计失败。
func TestProxy_BreakerFailureOn5xx(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	br := breaker.New(breaker.Options{Enabled: true, Threshold: 2, Cooldown: time.Minute})
	p, err := New(Options{Upstream: upstream.URL, Breaker: br})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		p.ServeHTTP(rec, req)
	}

	if br.State() != breaker.StateOpen {
		t.Errorf("连续 5xx 后应熔断，实际 %s", br.State())
	}
}

// TestProxy_NoBreakerUnaffected 未配置熔断器时行为不变。
func TestProxy_NoBreakerUnaffected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Breaker() != nil {
		t.Error("未配置时 Breaker() 应返回 nil")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("应返回 200，实际 %d", rec.Code)
	}
}
