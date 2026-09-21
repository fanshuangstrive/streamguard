package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestApp_RequestLogHook_Forwards 验证注册的回调能收到经过代理的请求基础信息。
func TestApp_RequestLogHook_Forwards(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))

	var mu sync.Mutex
	var got struct {
		method    string
		path      string
		status    int
		model     string
		bodyBytes int
		called    bool
	}
	a.SetRequestLogHook(func(method, path string, status int, _ time.Duration, _ bool, model string, bodyBytes int) {
		mu.Lock()
		got.method, got.path, got.status, got.called = method, path, status, true
		got.model, got.bodyBytes = model, bodyBytes
		mu.Unlock()
	})

	if err := a.Start(); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	defer a.Stop(context.Background())

	body := `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post("http://"+a.Addr()+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("请求代理失败：%v", err)
	}
	defer resp.Body.Close()

	// 钩子在响应写回后由 defer 触发，给一点时间确保已回调。
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		called := got.called
		mu.Unlock()
		if called {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !got.called {
		t.Fatal("请求钩子未被调用")
	}
	if got.method != http.MethodPost || got.path != "/v1/chat/completions" {
		t.Fatalf("方法/路径不符：%s %s", got.method, got.path)
	}
	if got.status != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d", got.status)
	}
	if got.model != "test-model" {
		t.Fatalf("应上报模型名 test-model，实际 %q", got.model)
	}
	if got.bodyBytes != len(body) {
		t.Fatalf("应上报请求体大小 %d，实际 %d", len(body), got.bodyBytes)
	}
}

// TestApp_RequestLogHook_NilSafe 验证未注册回调时服务正常工作。
func TestApp_RequestLogHook_NilSafe(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL)) // 未 SetRequestLogHook
	if err := a.Start(); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	defer a.Stop(context.Background())

	resp, err := http.Get("http://" + a.Addr() + "/v1/chat")
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("无回调时正常请求应 200，实际 %d", resp.StatusCode)
	}
}
