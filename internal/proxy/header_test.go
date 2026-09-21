package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProxy_ForwardsAllClientHeaders 验证客户端请求头被完整原样转发到上游。
//
// 这是「透明代理」的核心契约：StreamGuard 不修改、不丢弃任何客户端 header，
// 包括认证头、内容协商头、自定义业务头等。
func TestProxy_ForwardsAllClientHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4"}`))
	// 模拟真实客户端会携带的各类请求头。
	req.Header.Set("Authorization", "Bearer client-token")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "openai-python/1.0")
	req.Header.Set("X-Request-Id", "req-abc-123")
	req.Header.Set("X-Custom-Business-Header", "custom-value")
	req.Header.Set("Accept-Language", "zh-CN")

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}

	// 逐个校验关键 header 是否原样到达上游。
	want := map[string]string{
		"Authorization":            "Bearer client-token",
		"Content-Type":             "application/json",
		"Accept":                   "text/event-stream",
		"User-Agent":               "openai-python/1.0",
		"X-Request-Id":             "req-abc-123",
		"X-Custom-Business-Header": "custom-value",
		"Accept-Language":          "zh-CN",
	}
	for k, v := range want {
		if gotV := got.Get(k); gotV != v {
			t.Errorf("header %s 未原样透传：期望 %q，实际 %q", k, v, gotV)
		}
	}
}

// TestProxy_ForwardsQueryString 验证 URL 查询参数被原样转发。
func TestProxy_ForwardsQueryString(t *testing.T) {
	var gotRawQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRawQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models?limit=10&after=abc", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}
	if gotRawQuery != "limit=10&after=abc" {
		t.Fatalf("查询参数未原样透传：%s", gotRawQuery)
	}
}

// TestProxy_ForwardsResponseHeaders 验证上游响应头被原样返回给客户端。
func TestProxy_ForwardsResponseHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Upstream-Trace-Id", "trace-xyz")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type 未透传：%s", got)
	}
	if got := rec.Header().Get("X-Upstream-Trace-Id"); got != "trace-xyz" {
		t.Errorf("自定义响应头未透传：%s", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control 未透传：%s", got)
	}
}

// TestProxy_ForwardsRequestBodyVerbatim 验证请求体被逐字节原样转发，不做任何改写。
func TestProxy_ForwardsRequestBodyVerbatim(t *testing.T) {
	const body = `{"model":"gpt-4","messages":[{"role":"user","content":"你好"}],"stream":true}`

	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求体失败：%v", err)
		}
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}
	if gotBody != body {
		t.Fatalf("请求体被改写：\n期望 %s\n实际 %s", body, gotBody)
	}
}
