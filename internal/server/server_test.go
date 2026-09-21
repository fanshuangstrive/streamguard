package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/breaker"
	"github.com/streamguard/streamguard/internal/limiter"
	"github.com/streamguard/streamguard/internal/proxy"
)

// newTestUpstream 创建一个模拟上游模型服务。
func newTestUpstream(t *testing.T, hits *int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt64(hits, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[]}`))
	}))
}

// TestServer_ProxyChatCompletions 验证 /v1/chat/completions 被正确代理。
func TestServer_ProxyChatCompletions(t *testing.T) {
	var hits int64
	upstream := newTestUpstream(t, &hits)
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	srv := New(Options{Proxy: p})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("上游应被调用 1 次，实际 %d", hits)
	}
}

// TestServer_UnknownPathFallsBackToDefault 验证未匹配路由前缀的路径回退到默认后端。
//
// 设计：代理不限制路径，所有业务路径原样转发到上游。
func TestServer_UnknownPathFallsBackToDefault(t *testing.T) {
	var hits int64
	upstream := newTestUpstream(t, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	srv := New(Options{Proxy: p})

	for _, path := range []string{"/", "/v1/models", "/v1/embeddings"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("路径 %s 应转发到上游并返回 200，实际 %d", path, rec.Code)
		}
	}
	if atomic.LoadInt64(&hits) != 3 {
		t.Fatalf("上游应被调用 3 次，实际 %d", hits)
	}
}

// TestServer_UpstreamDownReturns502 验证上游不可达时返回 502。
func TestServer_UpstreamDownReturns502(t *testing.T) {
	p, err := proxy.New(proxy.Options{
		Upstream: "http://127.0.0.1:1",
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}
	srv := New(Options{Proxy: p})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("上游不可达应返回 502，实际 %d", rec.Code)
	}
}

// TestServer_Healthz 验证健康检查端点。
func TestServer_Healthz(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	srv := New(Options{Proxy: p})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查应返回 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("健康检查响应异常：%s", rec.Body.String())
	}
}

// TestServer_Stats 验证统计端点返回限流信息。
func TestServer_Stats(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	w := limiter.NewWaiter(limiter.WaiterOptions{Rate: 100, Burst: 10, MaxWait: time.Second})
	srv := New(Options{Proxy: p, Waiter: w})

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("统计端点应返回 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate") {
		t.Fatalf("统计响应应包含 rate 字段：%s", rec.Body.String())
	}
}

// TestServer_StatsFieldOrderStable 验证 /stats 字段顺序稳定（结构体序列化）。
//
// 回归：此前用 map 序列化，字段顺序随机，不利于阅读与断言。
func TestServer_StatsFieldOrderStable(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	w := limiter.NewWaiter(limiter.WaiterOptions{Rate: 100, Burst: 10, MaxWait: time.Second})
	srv := New(Options{Proxy: p, Waiter: w})

	body := func() string {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))
		return rec.Body.String()
	}()

	// 连续两次请求应产生完全一致的字段顺序。
	body2 := func() string {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil))
		return rec.Body.String()
	}()

	if body != body2 {
		t.Fatalf("两次 /stats 响应不一致（字段顺序不稳定）：\n%s\n%s", body, body2)
	}
	// upstream 应为首个字段。
	if !strings.HasPrefix(body, `{"upstream":`) {
		t.Fatalf("upstream 应为首个字段：%s", body)
	}
}

// TestServer_StatsIncludesBreaker 验证统计端点包含熔断器字段。
func TestServer_StatsIncludesBreaker(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	br := breaker.New(breaker.Options{Enabled: true, Threshold: 3, Cooldown: time.Minute})
	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second, Breaker: br})
	srv := New(Options{Proxy: p})

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, field := range []string{"breaker_enabled", "breaker_state", "breaker_trips", "breaker_rejected"} {
		if !strings.Contains(body, field) {
			t.Errorf("统计响应应包含 %s 字段：%s", field, body)
		}
	}
	if !strings.Contains(body, `"breaker_state":"closed"`) {
		t.Errorf("初始熔断状态应为 closed：%s", body)
	}
}

// TestServer_RateLimitWaits 验证限流生效：请求被排队等待而非立即拒绝。
func TestServer_RateLimitWaits(t *testing.T) {
	var hits int64
	upstream := newTestUpstream(t, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	// 5 QPS，突发 1：第 2 个请求需等待约 200ms
	w := limiter.NewWaiter(limiter.WaiterOptions{Rate: 5, Burst: 1, MaxWait: 2 * time.Second})
	srv := New(Options{Proxy: p, Waiter: w})

	// 第 1 个请求立即通过
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("第 1 个请求应成功，实际 %d", rec1.Code)
	}

	// 第 2 个请求应等待后成功（而非 429）
	start := time.Now()
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	elapsed := time.Since(start)

	if rec2.Code != http.StatusOK {
		t.Fatalf("第 2 个请求应等待后成功，实际 %d", rec2.Code)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("第 2 个请求应被限流等待，实际仅 %v", elapsed)
	}
	if atomic.LoadInt64(&hits) != 2 {
		t.Fatalf("上游应被调用 2 次，实际 %d", hits)
	}
}

// TestServer_RateLimitTimeout429 验证等待超时返回 429 且为 OpenAI 格式。
func TestServer_RateLimitTimeout429(t *testing.T) {
	var hits int64
	upstream := newTestUpstream(t, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	// 1 QPS，最大等待 100ms：第 2 个请求必然超时
	w := limiter.NewWaiter(limiter.WaiterOptions{Rate: 1, Burst: 1, MaxWait: 100 * time.Millisecond})
	srv := New(Options{Proxy: p, Waiter: w})

	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))

	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))

	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("等待超时应返回 429，实际 %d", rec2.Code)
	}
	body := rec2.Body.String()
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("429 响应应为 OpenAI error 格式：%s", body)
	}
	if !strings.Contains(body, "rate_limit") {
		t.Fatalf("429 响应应包含 rate_limit 类型：%s", body)
	}
	// 被限流的请求不应到达上游
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("被限流请求不应转发，上游应仅被调用 1 次，实际 %d", hits)
	}
}

// TestServer_ClientCancelDuringWait 验证客户端取消时释放等待且不转发到上游。
func TestServer_ClientCancelDuringWait(t *testing.T) {
	var hits int64
	upstream := newTestUpstream(t, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	w := limiter.NewWaiter(limiter.WaiterOptions{Rate: 1, Burst: 1, MaxWait: 10 * time.Second})
	srv := New(Options{Proxy: p, Waiter: w})

	// 消耗掉突发配额
	rec1 := httptest.NewRecorder()
	srv.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)))
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("第 1 个请求应到达上游，实际 %d", got)
	}

	// 第 2 个请求带可取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`)).WithContext(ctx)

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("客户端取消后应立即返回，实际 %v", elapsed)
	}
	// 关键断言：取消的请求不应被转发到上游
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("取消的请求不应转发到上游，上游应仅被调用 1 次，实际 %d", got)
	}
}

// TestServer_SSEPassthrough 验证服务层不破坏 SSE 流式透传。
func TestServer_SSEPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(w, "data: {\"delta\":\"x\"}\n\n")
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	srv := New(Options{Proxy: p})

	front := httptest.NewServer(srv)
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream，实际 %s", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "[DONE]") {
		t.Fatalf("SSE 响应不完整：%s", string(body))
	}
}

// newSlowUpstream 创建一个会阻塞指定时长的上游，用于并发限流测试。
func newSlowUpstream(t *testing.T, delay time.Duration, hits *int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt64(hits, 1)
		}
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[]}`))
	}))
}

// TestServer_SizeLimitSmallConcurrent 验证小请求并发上限生效。
func TestServer_SizeLimitSmallConcurrent(t *testing.T) {
	var hits int64
	upstream := newSlowUpstream(t, 300*time.Millisecond, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	cl := limiter.NewConcurrencyLimiter(limiter.ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 1,
		LargeLimit: 1,
		MaxWait:    5 * time.Second,
	})
	srv := New(Options{Proxy: p, Concurrency: cl, MaxWait: 5 * time.Second})

	front := httptest.NewServer(srv)
	defer front.Close()

	// 小请求体（估算 token 远小于 10000）
	smallBody := `{"messages":[{"role":"user","content":"hi"}]}`

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(smallBody))
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 并发上限 1 + 上游耗时 300ms → 两个请求串行，总耗时 >= 600ms
	if elapsed < 550*time.Millisecond {
		t.Errorf("小请求并发上限 1 应导致串行执行（>=600ms），实际 %v", elapsed)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("上游应被调用 2 次，实际 %d", got)
	}
}

// TestServer_SizeLimitLargeRequestBlocked 验证大请求并发上限独立生效。
func TestServer_SizeLimitLargeRequestBlocked(t *testing.T) {
	var hits int64
	upstream := newSlowUpstream(t, 300*time.Millisecond, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	cl := limiter.NewConcurrencyLimiter(limiter.ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 5,
		LargeLimit: 1,
		MaxWait:    5 * time.Second,
	})
	srv := New(Options{Proxy: p, Concurrency: cl, MaxWait: 5 * time.Second})

	front := httptest.NewServer(srv)
	defer front.Close()

	// 大请求体：content 33000 字节 → 11000 token > 10000
	largeBody := `{"messages":[{"role":"user","content":"` + strings.Repeat("a", 33000) + `"}]}`

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(largeBody))
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if elapsed < 550*time.Millisecond {
		t.Errorf("大请求并发上限 1 应导致串行执行（>=600ms），实际 %v", elapsed)
	}
}

// TestServer_SizeLimitTimeout429 验证并发等待超时返回 429。
func TestServer_SizeLimitTimeout429(t *testing.T) {
	var hits int64
	upstream := newSlowUpstream(t, 800*time.Millisecond, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	cl := limiter.NewConcurrencyLimiter(limiter.ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 1,
		LargeLimit: 1,
		MaxWait:    200 * time.Millisecond,
	})
	srv := New(Options{Proxy: p, Concurrency: cl, MaxWait: 200 * time.Millisecond})

	front := httptest.NewServer(srv)
	defer front.Close()

	smallBody := `{"messages":[{"role":"user","content":"hi"}]}`

	// 第一个请求占满槽位
	go func() {
		resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(smallBody))
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(100 * time.Millisecond)

	// 第二个请求应等待超时 → 429
	resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(smallBody))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", resp.StatusCode)
	}
}

// TestServer_SizeLimitDisabledByDefault 验证未配置并发限流时行为不变。
func TestServer_SizeLimitDisabledByDefault(t *testing.T) {
	var hits int64
	upstream := newSlowUpstream(t, 200*time.Millisecond, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	// 不传 Concurrency → 不限并发
	srv := New(Options{Proxy: p})

	front := httptest.NewServer(srv)
	defer front.Close()

	smallBody := `{"messages":[{"role":"user","content":"hi"}]}`

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json", strings.NewReader(smallBody))
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 不限并发 → 3 个请求并行，总耗时约 200ms（远小于串行的 600ms）
	if elapsed > 500*time.Millisecond {
		t.Errorf("未启用并发限流时应并行执行（~200ms），实际 %v", elapsed)
	}
}

// TestServer_SizeLimitStats 验证 /stats 输出大小限流统计。
func TestServer_SizeLimitStats(t *testing.T) {
	var hits int64
	upstream := newTestUpstream(t, &hits)
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	cl := limiter.NewConcurrencyLimiter(limiter.ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 1,
		MaxWait:    time.Second,
	})
	srv := New(Options{Proxy: p, Concurrency: cl, MaxWait: time.Second})

	// 发一个小请求
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// 查询 /stats
	req2 := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)

	body := rec2.Body.String()
	for _, want := range []string{
		`"size_limit_enabled":true`,
		`"size_limit_threshold":10000`,
		`"size_limit_small_limit":2`,
		`"size_limit_large_limit":1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/stats 应包含 %s，实际：%s", want, body)
		}
	}
}

// TestServer_SizeLimitPreservesBody 验证 token 估算读取请求体后，代理仍能读到完整 body。
func TestServer_SizeLimitPreservesBody(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	p, _ := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	cl := limiter.NewConcurrencyLimiter(limiter.ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 1,
		MaxWait:    time.Second,
	})
	srv := New(Options{Proxy: p, Concurrency: cl, MaxWait: time.Second})

	payload := `{"messages":[{"role":"user","content":"hello world"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotBody != payload {
		t.Errorf("上游收到的 body 应完整保留\n期望：%s\n实际：%s", payload, gotBody)
	}
}
