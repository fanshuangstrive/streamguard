package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/limiter"
	"github.com/streamguard/streamguard/internal/proxy"
)

// TestStatusRecorder_CapturesStatus 验证包装器记录状态码且透传写入与 Flush。
func TestStatusRecorder_CapturesStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := newStatusRecorder(rec)

	sr.WriteHeader(http.StatusTeapot)
	_, _ = sr.Write([]byte("hi"))

	if sr.status != http.StatusTeapot {
		t.Fatalf("期望记录 418，实际 %d", sr.status)
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("底层 writer 应收到 418，实际 %d", rec.Code)
	}
	if rec.Body.String() != "hi" {
		t.Fatalf("响应体未透传：%q", rec.Body.String())
	}
	// Flush 不应 panic（底层 httptest.ResponseRecorder 支持 Flusher）。
	sr.Flush()
}

// TestStatusRecorder_DefaultStatus200 验证未显式 WriteHeader 时隐式记为 200。
func TestStatusRecorder_DefaultStatus200(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := newStatusRecorder(rec)
	_, _ = sr.Write([]byte("ok"))
	if sr.status != http.StatusOK {
		t.Fatalf("默认状态码应为 200，实际 %d", sr.status)
	}
}

// TestServer_OnRequestHook_BasicInfo 验证成功请求上报方法/路径/状态码 200。
func TestServer_OnRequestHook_BasicInfo(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	var mu sync.Mutex
	var got []RequestEvent
	srv := New(Options{
		Proxy: p,
		OnRequest: func(e RequestEvent) {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	srv.ServeHTTP(httptest.NewRecorder(), req)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("钩子应被调用 1 次，实际 %d", len(got))
	}
	e := got[0]
	if e.Method != http.MethodPost || e.Path != "/v1/chat/completions" {
		t.Fatalf("方法/路径不符：%s %s", e.Method, e.Path)
	}
	if e.Status != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d", e.Status)
	}
	if e.Elapsed <= 0 {
		t.Fatalf("耗时应大于 0，实际 %v", e.Elapsed)
	}
}

// TestServer_OnRequestHook_RateLimit429 验证限流拒绝时上报最终状态码 429。
func TestServer_OnRequestHook_RateLimit429(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	// burst=1 且速率极低：第二个请求排队超时 → 429。
	waiter := limiter.NewWaiter(limiter.WaiterOptions{Rate: 0.001, Burst: 1, MaxWait: 20 * time.Millisecond})

	var mu sync.Mutex
	var codes []int
	srv := New(Options{
		Proxy:   p,
		Waiter:  waiter,
		MaxWait: 20 * time.Millisecond,
		OnRequest: func(e RequestEvent) {
			mu.Lock()
			codes = append(codes, e.Status)
			mu.Unlock()
		},
	})

	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{}`)))
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat", strings.NewReader(`{}`)))

	mu.Lock()
	defer mu.Unlock()
	if len(codes) != 2 {
		t.Fatalf("钩子应被调用 2 次，实际 %d", len(codes))
	}
	if codes[0] != http.StatusOK {
		t.Fatalf("首个请求应 200，实际 %d", codes[0])
	}
	if codes[1] != http.StatusTooManyRequests {
		t.Fatalf("第二个请求应 429，实际 %d", codes[1])
	}
}

// TestServer_OnRequestHook_SSEPassthrough 验证启用钩子后 SSE 仍逐块实时透传（R6）。
//
// 这是本功能的关键回归：statusRecorder 若缓冲响应体，块间隔会塌缩到接近 0。
func TestServer_OnRequestHook_SSEPassthrough(t *testing.T) {
	const chunkDelay = 120 * time.Millisecond

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("上游不支持 Flush")
			return
		}
		for _, c := range []string{"a", "b", "c", "d"} {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			f.Flush()
			time.Sleep(chunkDelay)
		}
	}))
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}
	srv := New(Options{Proxy: p, OnRequest: func(RequestEvent) {}})

	proxySrv := httptest.NewServer(srv)
	defer proxySrv.Close()

	start := time.Now()
	resp, err := http.Post(proxySrv.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("请求代理失败：%v", err)
	}
	defer resp.Body.Close()

	var arrivals []time.Duration
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			arrivals = append(arrivals, time.Since(start))
		}
	}
	if len(arrivals) != 4 {
		t.Fatalf("期望 4 个数据块，实际 %d", len(arrivals))
	}
	for i := 1; i < len(arrivals); i++ {
		if gap := arrivals[i] - arrivals[i-1]; gap < chunkDelay/2 {
			t.Fatalf("第 %d/%d 块间隔仅 %v，疑似被缓冲（期望约 %v）", i, i+1, gap, chunkDelay)
		}
	}
}

// TestServer_NoHookNoOverhead 验证未注册钩子时行为不变（不包裹 writer）。
func TestServer_NoHookNoOverhead(t *testing.T) {
	upstream := newTestUpstream(t, nil)
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}
	srv := New(Options{Proxy: p}) // 无 OnRequest

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chat", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("无钩子时正常请求应 200，实际 %d", rec.Code)
	}
}
