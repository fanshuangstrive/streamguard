package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/config"
)

// testConfig 返回一份指向测试上游的配置。
func testConfig(upstream string) *config.Config {
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0" // 随机端口，避免测试冲突
	cfg.Upstream = upstream
	cfg.Rate = 100
	cfg.Burst = 10
	cfg.MaxWait = config.Duration(time.Second)
	cfg.Timeout = config.Duration(5 * time.Second)
	return cfg
}

// TestApp_StartStop 验证应用可正常启动与停止。
func TestApp_StartStop(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))

	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	if !a.Running() {
		t.Fatal("Running should be true after start")
	}

	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}
	if a.Running() {
		t.Fatal("Running should be false after stop")
	}
}

// TestApp_DoubleStart 验证重复启动返回错误。
func TestApp_DoubleStart(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))
	if err := a.Start(); err != nil {
		t.Fatalf("首次failed to start: %v", err)
	}
	defer a.Stop(context.Background())

	if err := a.Start(); err == nil {
		t.Fatal("double start should return an error")
	}
}

// TestApp_StopWhenNotRunning 验证未运行时停止不报错。
func TestApp_StopWhenNotRunning(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("stop when not running should not error: %v", err)
	}
}

// TestApp_StopWithActiveLongConnection 验证存在活跃长连接（SSE）时，
// Stop 在 ctx 超时后仍能强制关闭并返回 nil，且状态一致。
//
// 回归背景：SSE 长连接不会「空闲」，Shutdown 会一直等到 ctx 超时，
// 旧实现直接把 context deadline exceeded 抛给调用方，导致 GUI 报
// "failed to apply config: graceful shutdown failed: context deadline exceeded"。
func TestApp_StopWithActiveLongConnection(t *testing.T) {
	// 上游保持连接不结束，模拟 SSE 长流。
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // 阻塞，直到测试结束
	}))
	defer upstream.Close()
	defer close(release)

	a := New(testConfig(upstream.URL))
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// 发起一个会长时间挂起的请求，使服务存在活跃连接。
	req, err := http.NewRequest(http.MethodGet, "http://"+a.Addr()+"/v1/chat/completions", nil)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// 给请求一点时间建立连接。
	time.Sleep(100 * time.Millisecond)

	// 使用极短超时，模拟 GUI 场景下 Shutdown 无法在期限内完成。
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := a.Stop(ctx); err != nil {
		t.Fatalf("Stop should force-close and return nil, got: %v", err)
	}
	if a.Running() {
		t.Fatal("Running should be false after forced stop")
	}

	// 强制关闭后，挂起的请求应被中断。
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pending request should be aborted after force close")
	}
}

// TestApp_Addr 验证启动后可获取实际监听地址。
func TestApp_Addr(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer a.Stop(context.Background())

	addr := a.Addr()
	if addr == "" {
		t.Fatal("should get listen address after start")
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("unexpected listen address: %s", addr)
	}
}

// TestApp_ProxiesRequest 验证应用启动后能正常代理请求。
func TestApp_ProxiesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test-ok"}`))
	}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer a.Stop(context.Background())

	resp, err := http.Post("http://"+a.Addr()+"/v1/chat/completions",
		"application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestApp_Stats 验证统计信息可获取。
func TestApp_Stats(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer a.Stop(context.Background())

	stats := a.Stats()
	if stats.Upstream != upstream.URL {
		t.Fatalf("wrong upstream in stats: %s", stats.Upstream)
	}
	if stats.Rate != 100 {
		t.Fatalf("wrong rate in stats: %v", stats.Rate)
	}
}

// TestApp_UpdateConfig 验证运行时更新配置（重启服务）。
func TestApp_UpdateConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	a := New(testConfig(upstream.URL))
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer a.Stop(context.Background())

	newCfg := testConfig(upstream.URL)
	newCfg.Rate = 5
	newCfg.Burst = 2

	if err := a.UpdateConfig(context.Background(), newCfg); err != nil {
		t.Fatalf("failed to update config: %v", err)
	}

	stats := a.Stats()
	if stats.Rate != 5 {
		t.Fatalf("rate should be 5 after update, got %v", stats.Rate)
	}
	if !a.Running() {
		t.Fatal("server should still be running after config update")
	}
}

// TestApp_Config 验证可获取当前配置副本。
func TestApp_Config(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	a := New(cfg)

	got := a.Config()
	if got.Upstream != cfg.Upstream {
		t.Fatalf("wrong upstream in config copy: %s", got.Upstream)
	}

	// 修改副本不应影响原配置
	got.Upstream = "http://changed"
	if a.Config().Upstream == "http://changed" {
		t.Fatal("Config() should return a copy; mutation must not affect internal state")
	}
}
