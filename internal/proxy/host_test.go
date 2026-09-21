package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestProxy_DefaultRewritesHost 验证默认情况下 Host 被改写为上游主机名。
//
// 这是 httputil.ReverseProxy 的标准行为，适合大多数上游服务。
// 注意：NewSingleHostReverseProxy 只设置 r.URL.Host，不会改 r.Host，
// 而 Go 的 http.Transport 发送时以 r.Host 为准，因此代理必须显式改写。
func TestProxy_DefaultRewritesHost(t *testing.T) {
	var gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	// 模拟真实 HTTP 服务端场景：客户端请求的 Host 为本地代理地址。
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}

	wantHost, _ := url.Parse(upstream.URL)
	if gotHost != wantHost.Host {
		t.Fatalf("默认应改写 Host 为上游主机：期望 %s，实际 %s", wantHost.Host, gotHost)
	}
}

// TestProxy_PreserveHost 验证开启 PreserveHost 后保留客户端原始 Host。
//
// 部分上游网关（如 Kong）依赖 Host 头做路由，此时必须保留原始值。
func TestProxy_PreserveHost(t *testing.T) {
	var gotHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{
		Upstream:     upstream.URL,
		Timeout:      5 * time.Second,
		PreserveHost: true,
	})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Host = "client.example.com"
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}
	if gotHost != "client.example.com" {
		t.Fatalf("开启 PreserveHost 后应保留原始 Host：期望 client.example.com，实际 %s", gotHost)
	}
}

// TestProxy_PreserveHostConcurrent 验证并发请求下每个请求的 Host 互不干扰。
//
// 回归测试：Director 曾被并发调用时共享闭包变量，导致后续请求复用首个请求的 Host。
func TestProxy_PreserveHostConcurrent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 回显收到的 Host，便于客户端校验。
		w.Header().Set("X-Echo-Host", r.Host)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{
		Upstream:     upstream.URL,
		Timeout:      5 * time.Second,
		PreserveHost: true,
	})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			wantHost := fmt.Sprintf("client-%d.example.com", i)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
			req.Host = wantHost
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if got := rec.Header().Get("X-Echo-Host"); got != wantHost {
				errs <- fmt.Sprintf("请求 %d：期望 Host %s，实际 %s", i, wantHost, got)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for msg := range errs {
		t.Error(msg)
	}
}
