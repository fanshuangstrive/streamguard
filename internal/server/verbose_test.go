package server

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/proxy"
)

// TestServer_VerboseLogsRequestAndResponse 验证详细模式会打印请求与响应内容。
func TestServer_VerboseLogsRequestAndResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Test", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-verbose"}`))
	}))
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	srv := New(Options{Proxy: p, Logger: logger, Verbose: true})

	body := `{"model":"test-model","stream":false}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-Custom", "custom-value")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	out := buf.String()

	// 请求侧
	for _, want := range []string{
		"[req] >>> POST /v1/chat/completions",
		"[req] Authorization: Bearer test-key",
		"[req] X-Custom: custom-value",
		body,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("日志缺少请求内容 %q\n实际日志:\n%s", want, out)
		}
	}

	// 响应侧
	for _, want := range []string{
		"[resp] <<< 200 OK",
		"[resp] X-Upstream-Test: yes",
		`{"id":"chatcmpl-verbose"}`,
		"[resp] <<< done: status=200",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("日志缺少响应内容 %q\n实际日志:\n%s", want, out)
		}
	}
}

// TestServer_VerboseDisabledByDefault 验证默认不打印请求/响应内容。
func TestServer_VerboseDisabledByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	srv := New(Options{Proxy: p, Logger: logger}) // Verbose 默认 false

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"secret":"value"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	out := buf.String()
	if strings.Contains(out, "[req] >>>") || strings.Contains(out, "[resp] <<<") {
		t.Errorf("默认模式不应打印请求/响应内容，实际日志:\n%s", out)
	}
	if strings.Contains(out, "secret") {
		t.Errorf("默认模式不应泄漏请求体内容，实际日志:\n%s", out)
	}
	// 摘要日志仍应存在
	if !strings.Contains(out, "[proxy] POST /v1/chat/completions") {
		t.Errorf("摘要日志应保留，实际日志:\n%s", out)
	}
}

// TestServer_VerboseSSEStreamsChunks 验证详细模式下 SSE 响应逐块打印且保持实时性。
func TestServer_VerboseSSEStreamsChunks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("上游应支持 Flush")
			return
		}
		for _, chunk := range []string{
			"data: {\"delta\":\"hello\"}\n\n",
			"data: {\"delta\":\"world\"}\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = w.Write([]byte(chunk))
			f.Flush()
			time.Sleep(30 * time.Millisecond)
		}
	}))
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	srv := New(Options{Proxy: p, Logger: logger, Verbose: true})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	out := buf.String()
	for _, want := range []string{
		`data: {"delta":"hello"}`,
		`data: {"delta":"world"}`,
		"data: [DONE]",
		"chunk#1",
		"chunk#2",
		"chunk#3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("SSE 日志缺少 %q\n实际日志:\n%s", want, out)
		}
	}

	// 响应体应完整透传给客户端
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Errorf("客户端应收到完整 SSE 流，实际:\n%s", rec.Body.String())
	}
}

// TestVerboseRecorder_FlushPassthrough 验证 Flush 被正确透传（SSE 实时性关键）。
func TestVerboseRecorder_FlushPassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	var buf bytes.Buffer
	vr := newVerboseRecorder(rec, log.New(&buf, "", 0))

	vr.WriteHeader(http.StatusOK)
	vr.Write([]byte("data: test\n\n"))
	vr.Flush() // 不应 panic，且应透传到底层

	if !rec.Flushed {
		t.Error("Flush 应透传到底层 ResponseWriter")
	}
	vr.finish()

	if !strings.Contains(buf.String(), "chunks=1") {
		t.Errorf("应统计到 1 个 chunk，实际日志:\n%s", buf.String())
	}
}

// TestServer_VerboseLoggerSeparatedFromMainLogger 验证详细日志可独立输出，
// 不污染主日志（GUI 场景：详细日志只进控制台，不进界面日志面板）。
func TestServer_VerboseLoggerSeparatedFromMainLogger(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	p, err := proxy.New(proxy.Options{Upstream: upstream.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	var mainBuf, verboseBuf bytes.Buffer
	srv := New(Options{
		Proxy:         p,
		Logger:        log.New(&mainBuf, "", 0),
		Verbose:       true,
		VerboseLogger: log.New(&verboseBuf, "", 0),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// 详细内容只应出现在 verbose 日志中
	if !strings.Contains(verboseBuf.String(), "[req] >>>") {
		t.Errorf("详细日志应包含请求内容，实际:\n%s", verboseBuf.String())
	}
	if !strings.Contains(verboseBuf.String(), "[resp] <<<") {
		t.Errorf("详细日志应包含响应内容，实际:\n%s", verboseBuf.String())
	}

	// 主日志不应包含详细内容（避免污染界面日志面板）
	if strings.Contains(mainBuf.String(), "[req] >>>") || strings.Contains(mainBuf.String(), "[resp] <<<") {
		t.Errorf("主日志不应包含详细内容，实际:\n%s", mainBuf.String())
	}
	// 主日志仍应有摘要
	if !strings.Contains(mainBuf.String(), "[proxy] POST /v1/chat/completions") {
		t.Errorf("主日志应保留摘要，实际:\n%s", mainBuf.String())
	}
}
