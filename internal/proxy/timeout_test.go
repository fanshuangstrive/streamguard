package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProxy_TimeoutAppliesToNonSSE 验证非 SSE 请求受 Timeout 约束。
//
// 回归：此前 Timeout 仅做默认值规整，从未应用到请求，导致上游挂起时无限等待。
func TestProxy_TimeoutAppliesToNonSSE(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	p, err := New(Options{Upstream: up.URL, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	start := time.Now()
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://example.com/x", nil))
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("非 SSE 请求未被超时中断，耗时 %v（期望 < 2s）", elapsed)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("超时应返回 502，实际 %d", rec.Code)
	}
}

// TestProxy_TimeoutExemptsSSE 验证 SSE 请求豁免 Timeout，长连接不被中断。
func TestProxy_TimeoutExemptsSSE(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			_, _ = w.Write([]byte("data: chunk\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(200 * time.Millisecond)
		}
	}))
	defer up.Close()

	p, err := New(Options{Upstream: up.URL, Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/chat/completions",
		strings.NewReader(`{"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("SSE 请求应豁免超时并返回 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "chunk") {
		t.Fatalf("SSE 响应体不完整：%q", rec.Body.String())
	}
}

// TestProxy_TimeoutExemptsSSEByAcceptHeader 验证通过 Accept 头识别 SSE 并豁免超时。
func TestProxy_TimeoutExemptsSSEByAcceptHeader(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	p, err := New(Options{Upstream: up.URL, Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Accept: text/event-stream 应豁免超时，实际 %d", rec.Code)
	}
}

// TestProxy_NoDuplicateXForwardedFor 验证 X-Forwarded-For 不被重复注入。
//
// 回归：此前 Director 手动设置 XFF，标准库又追加一次，上游收到 "ip, ip"。
func TestProxy_NoDuplicateXForwardedFor(t *testing.T) {
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	p, err := New(Options{Upstream: up.URL})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	p.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(got, ",") {
		t.Fatalf("X-Forwarded-For 出现重复值：%q", got)
	}
	if !strings.Contains(got, "1.2.3.4") {
		t.Fatalf("X-Forwarded-For 应包含客户端 IP，实际 %q", got)
	}
}

// TestProxy_PreservesClientXForwardedFor 验证客户端自带的 XFF 被保留并追加。
func TestProxy_PreservesClientXForwardedFor(t *testing.T) {
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer up.Close()

	p, err := New(Options{Upstream: up.URL})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://example.com/x", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	p.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(got, "9.9.9.9") {
		t.Fatalf("客户端 XFF 应被保留，实际 %q", got)
	}
	if !strings.Contains(got, "1.2.3.4") {
		t.Fatalf("应追加客户端 IP，实际 %q", got)
	}
}
