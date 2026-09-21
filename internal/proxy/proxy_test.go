package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProxy_ForwardBasic 验证基础请求转发与响应透传。
func TestProxy_ForwardBasic(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("上游收到的路径错误：%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization 头未透传：%s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"content":"hi"}}]}`))
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[]}`))
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "chatcmpl-1") {
		t.Fatalf("响应体未正确透传：%s", rec.Body.String())
	}
}

// TestProxy_SSEStreaming 验证 SSE 流式响应逐块透传，不被缓冲。
func TestProxy_SSEStreaming(t *testing.T) {
	chunks := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\"好\"}}]}\n\n",
		"data: [DONE]\n\n",
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("上游 ResponseWriter 不支持 Flush")
			return
		}
		for _, c := range chunks {
			_, _ = io.WriteString(w, c)
			flusher.Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"stream":true}`))
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream，实际 %s", ct)
	}

	body := rec.Body.String()
	for _, c := range chunks {
		if !strings.Contains(body, strings.TrimSpace(c)) {
			t.Fatalf("SSE 分块丢失：%q\n完整响应：%s", c, body)
		}
	}
}

// TestProxy_SSEIncrementalDelivery 验证 SSE 数据是增量到达而非一次性缓冲。
func TestProxy_SSEIncrementalDelivery(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		for i := 0; i < 3; i++ {
			_, _ = io.WriteString(w, "data: chunk\n\n")
			flusher.Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	// 使用真实 HTTP 服务端到端验证增量传输
	front := httptest.NewServer(p)
	defer front.Close()

	resp, err := http.Post(front.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	start := time.Now()
	firstChunkAt := time.Duration(0)

	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("读取首个分块失败：%v", err)
	}
	firstChunkAt = time.Since(start)

	if !strings.Contains(line, "chunk") {
		t.Fatalf("首个分块内容异常：%q", line)
	}
	// 若被整体缓冲，首个分块会在全部数据生成后才到达（>90ms）
	if firstChunkAt > 80*time.Millisecond {
		t.Fatalf("SSE 疑似被缓冲，首块延迟 %v", firstChunkAt)
	}
}

// TestProxy_UpstreamError 验证上游错误状态码被透传。
func TestProxy_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("期望透传 401，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid api key") {
		t.Fatalf("错误响应体未透传：%s", rec.Body.String())
	}
}

// TestProxy_UpstreamUnreachable 验证上游不可达时返回 502。
func TestProxy_UpstreamUnreachable(t *testing.T) {
	// 使用一个已关闭的端口
	p, err := New(Options{Upstream: "http://127.0.0.1:1", Timeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("上游不可达应返回 502，实际 %d", rec.Code)
	}
}

// TestProxy_RequestBodyPreserved 验证请求体被完整转发。
func TestProxy_RequestBodyPreserved(t *testing.T) {
	const payload = `{"model":"gpt-4","messages":[{"role":"user","content":"你好"}]}`

	var received string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if received != payload {
		t.Fatalf("请求体未完整转发\n期望：%s\n实际：%s", payload, received)
	}
}

// TestNew_InvalidUpstream 验证非法上游地址返回错误。
func TestNew_InvalidUpstream(t *testing.T) {
	if _, err := New(Options{Upstream: "://bad"}); err == nil {
		t.Fatal("非法上游地址应返回错误")
	}
	if _, err := New(Options{Upstream: ""}); err == nil {
		t.Fatal("空上游地址应返回错误")
	}
}
