package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/streamguard/streamguard/internal/config"
)

// TestApp_CleanupOldLogs 验证超过保留天数的日志文件被删除，未过期的保留。
func TestApp_CleanupOldLogs(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// 构造：10 天前（应删除）、1 天前（应保留）、今天（应保留）
	oldFile := filepath.Join(logsDir, "streamguard-"+time.Now().AddDate(0, 0, -10).Format("20060102")+".log")
	recentFile := filepath.Join(logsDir, "streamguard-"+time.Now().AddDate(0, 0, -1).Format("20060102")+".log")
	todayFile := filepath.Join(logsDir, "streamguard-"+time.Now().Format("20060102")+".log")
	// 非日志文件（不应被删除）
	otherFile := filepath.Join(logsDir, "notes.txt")

	for _, f := range []string{oldFile, recentFile, todayFile, otherFile} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	cfg := config.Default()
	cfg.LogRetainDays = 7
	a := New(cfg)
	a.cleanupOldLogs()

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("10 天前的日志应被删除")
	}
	if _, err := os.Stat(recentFile); err != nil {
		t.Errorf("1 天前的日志应保留：%v", err)
	}
	if _, err := os.Stat(todayFile); err != nil {
		t.Errorf("今天的日志应保留：%v", err)
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Errorf("非日志文件不应被删除：%v", err)
	}
}

// TestApp_CleanupOldLogs_Disabled 保留天数为 0 时不清理。
func TestApp_CleanupOldLogs_Disabled(t *testing.T) {
	dir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(oldWd)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldFile := filepath.Join(logsDir, "streamguard-"+time.Now().AddDate(0, 0, -100).Format("20060102")+".log")
	if err := os.WriteFile(oldFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := config.Default()
	cfg.LogRetainDays = 0 // 禁用清理
	a := New(cfg)
	a.cleanupOldLogs()

	if _, err := os.Stat(oldFile); err != nil {
		t.Errorf("禁用清理时文件应保留：%v", err)
	}
}

// TestApp_StatsIncludesBreaker 验证 Stats 包含熔断器字段。
func TestApp_StatsIncludesBreaker(t *testing.T) {
	upstream := newVerboseTestUpstream(t)
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	cfg.BreakerEnabled = true
	cfg.BreakerThreshold = 3
	a := New(cfg)

	if err := a.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.Stop(ctx)
	}()

	s := a.Stats()
	if !s.BreakerEnabled {
		t.Error("BreakerEnabled 应为 true")
	}
	if s.BreakerState != "closed" {
		t.Errorf("初始状态应为 closed，实际 %s", s.BreakerState)
	}
}
