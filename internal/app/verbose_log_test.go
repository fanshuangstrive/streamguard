package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/config"
)

// newVerboseTestUpstream 创建一个返回固定 JSON 的测试上游。
func newVerboseTestUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-logfile"}`))
	}))
}

// TestApp_VerboseLogFile 验证 log_level=debug + log_file 时详细日志写入文件。
func TestApp_VerboseLogFile(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	logPath := filepath.Join(t.TempDir(), "verbose.log")

	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "debug"
	cfg.LogFile = logPath

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// 发起一个请求，触发详细日志。
	req, err := http.NewRequest(http.MethodPost, "http://"+a.Addr()+"/v1/chat/completions",
		strings.NewReader(`{"model":"test"}`))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("X-Trace", "trace-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	// 文件应存在且包含请求/响应内容。
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log file should exist: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"[req] >>> POST /v1/chat/completions",
		"[req] X-Trace: trace-123",
		`{"model":"test"}`,
		"[resp] <<< 200 OK",
		`{"id":"chatcmpl-logfile"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("日志文件缺少 %q\n实际内容:\n%s", want, out)
		}
	}
}

// TestApp_VerboseLogFileAuto 验证 log_file="auto" 时自动生成按日期命名的日志文件。
func TestApp_VerboseLogFileAuto(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	// 切换到临时目录，使 "auto" 生成的 logs/ 落在可控位置。
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "debug"
	cfg.LogFile = "auto"

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	// 应生成 logs/streamguard-YYYYMMDD.log
	expected := filepath.Join(tmp, "logs", "streamguard-"+time.Now().Format("20060102")+".log")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("auto log file should exist at %s: %v", expected, err)
	}
}

// TestApp_VerboseLogFileRelativePath 验证相对路径基于「命令当前目录」解析到 logs/ 下。
func TestApp_VerboseLogFileRelativePath(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "debug"
	cfg.LogFile = "my.log" // 相对路径

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	// 相对路径应落到 <当前目录>/logs/my.log
	expected := filepath.Join(tmp, "logs", "my.log")
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("relative log file should exist at %s: %v", expected, err)
	}
}

// TestApp_VerboseLogFileAbsolutePath 验证绝对路径不被改写。
func TestApp_VerboseLogFileAbsolutePath(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	absPath := filepath.Join(t.TempDir(), "custom", "abs.log")

	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "debug"
	cfg.LogFile = absPath

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	if _, err := os.Stat(absPath); err != nil {
		t.Fatalf("absolute log file should exist at %s: %v", absPath, err)
	}
}

// TestApp_VerboseLogFileInvalidPath 验证日志文件路径非法时回退到控制台，不影响服务启动。
func TestApp_VerboseLogFileInvalidPath(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	// 构造一个「父路径是文件」的非法路径，MkdirAll 必然失败。
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "debug"
	cfg.LogFile = filepath.Join(blocker, "sub", "x.log")

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("start should succeed even if log file is invalid: %v", err)
	}
	if !a.Running() {
		t.Fatal("app should be running")
	}
	// 应回退到控制台，未持有文件句柄。
	if a.verboseFile != nil {
		t.Error("verboseFile should be nil when falling back to stdout")
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}
}

// TestApp_VerboseLogFileClosedOnStop 验证停止后文件句柄被关闭（可删除/重命名）。
func TestApp_VerboseLogFileClosedOnStop(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	logPath := filepath.Join(t.TempDir(), "verbose.log")
	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "debug"
	cfg.LogFile = logPath

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	// Windows 下若句柄未关闭，删除会失败。
	if err := os.Remove(logPath); err != nil {
		t.Fatalf("log file should be closed after stop, remove failed: %v", err)
	}
}

// TestApp_VerboseLogFileDisabledWhenNotDebug 验证非 debug 级别不创建日志文件。
func TestApp_VerboseLogFileDisabledWhenNotDebug(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	logPath := filepath.Join(t.TempDir(), "verbose.log")
	cfg := testConfig(upstream.URL)
	cfg.LogLevel = "info" // 非 debug
	cfg.LogFile = logPath

	a := New(cfg)
	if err := a.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Errorf("log file should not be created when log_level != debug, stat err: %v", err)
	}
}

// TestConfig_LogFileEnvOverride 验证 STREAMGUARD_LOG_FILE 环境变量可覆盖配置。
func TestConfig_LogFileEnvOverride(t *testing.T) {
	t.Setenv("STREAMGUARD_LOG_FILE", "auto")

	cfg := config.Default()
	cfg.ApplyEnv()

	if cfg.LogFile != "auto" {
		t.Errorf("expected log_file=auto from env, got %q", cfg.LogFile)
	}
}
