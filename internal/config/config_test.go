package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDefault 验证默认配置的合理性与可用性。
func TestDefault(t *testing.T) {
	cfg := Default()

	if cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("默认监听地址应为 127.0.0.1:8080，实际 %s", cfg.Listen)
	}
	if cfg.Upstream == "" {
		t.Fatal("默认上游地址不应为空")
	}
	if cfg.Rate <= 0 {
		t.Fatal("默认限流速率应大于 0")
	}
	if cfg.Burst <= 0 {
		t.Fatal("默认突发容量应大于 0")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("默认配置应通过校验，实际错误：%v", err)
	}
}

// TestValidate 验证配置校验逻辑。
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "合法配置",
			mutate:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "监听地址为空",
			mutate:  func(c *Config) { c.Listen = "" },
			wantErr: true,
		},
		{
			name:    "上游地址为空",
			mutate:  func(c *Config) { c.Upstream = "" },
			wantErr: true,
		},
		{
			name:    "上游地址非法",
			mutate:  func(c *Config) { c.Upstream = "://bad" },
			wantErr: true,
		},
		{
			name:    "日志级别非法",
			mutate:  func(c *Config) { c.LogLevel = "verbose" },
			wantErr: true,
		},
		{
			name:    "日志级别合法",
			mutate:  func(c *Config) { c.LogLevel = "warn" },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("期望校验失败，但通过了")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("期望校验通过，但失败：%v", err)
			}
		})
	}
}

// TestNormalizeThenValidate 验证「先 Normalize 后 Validate」的约定：
// 负数会被 Normalize 规整为默认值，因此 Validate 不会报错。
func TestNormalizeThenValidate(t *testing.T) {
	cfg := Default()
	cfg.Rate = -1
	cfg.Burst = -1
	cfg.MaxWait = -1
	cfg.Timeout = -1
	cfg.BreakerThreshold = -1
	cfg.BreakerCooldown = -1
	cfg.RetryMaxAttempts = -1
	cfg.RetryInitialWait = -1
	cfg.RetryMaxWait = -1

	cfg.Normalize()

	if cfg.Rate != 1 {
		t.Fatalf("rate 应被规整为 1，实际 %v", cfg.Rate)
	}
	if cfg.Burst != 1 {
		t.Fatalf("burst 应被规整为 1，实际 %v", cfg.Burst)
	}
	if cfg.MaxWait.Duration() != 30*time.Second {
		t.Fatalf("max_wait 应被规整为 30s，实际 %v", cfg.MaxWait.Duration())
	}
	if cfg.Timeout.Duration() != 120*time.Second {
		t.Fatalf("timeout 应被规整为 120s，实际 %v", cfg.Timeout.Duration())
	}
	if cfg.RetryMaxAttempts != 3 {
		t.Fatalf("retry_max_attempts 应被规整为 3，实际 %v", cfg.RetryMaxAttempts)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("规整后应通过校验，实际失败：%v", err)
	}
}

// TestLoadFromFile 验证从 JSON 文件加载配置。
func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := `{
		"listen": "127.0.0.1:9090",
		"upstream": "http://127.0.0.1:11434",
		"rate": 5,
		"burst": 3,
		"max_wait": "10s",
		"timeout": "60s"
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置失败：%v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}

	if cfg.Listen != "127.0.0.1:9090" {
		t.Fatalf("listen 解析错误：%s", cfg.Listen)
	}
	if cfg.Upstream != "http://127.0.0.1:11434" {
		t.Fatalf("upstream 解析错误：%s", cfg.Upstream)
	}
	if cfg.Rate != 5 {
		t.Fatalf("rate 解析错误：%v", cfg.Rate)
	}
	if cfg.Burst != 3 {
		t.Fatalf("burst 解析错误：%d", cfg.Burst)
	}
	if cfg.MaxWait != Duration(10*time.Second) {
		t.Fatalf("max_wait 解析错误：%v", cfg.MaxWait)
	}
	if cfg.Timeout != Duration(60*time.Second) {
		t.Fatalf("timeout 解析错误：%v", cfg.Timeout)
	}
}

// TestLoadMissingFile 验证配置文件不存在时返回错误。
func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "not-exist.json"))
	if err == nil {
		t.Fatal("加载不存在的配置文件应返回错误")
	}
}

// TestLoadInvalidJSON 验证非法 JSON 返回错误。
func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{invalid json"), 0o600); err != nil {
		t.Fatalf("写入测试文件失败：%v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}

// TestApplyEnv 验证环境变量覆盖配置。
func TestApplyEnv(t *testing.T) {
	cfg := Default()

	t.Setenv("STREAMGUARD_LISTEN", "127.0.0.1:7777")
	t.Setenv("STREAMGUARD_UPSTREAM", "http://example.com")
	t.Setenv("STREAMGUARD_RATE", "42")
	t.Setenv("STREAMGUARD_BURST", "7")

	cfg.ApplyEnv()

	if cfg.Listen != "127.0.0.1:7777" {
		t.Fatalf("环境变量 listen 未生效：%s", cfg.Listen)
	}
	if cfg.Upstream != "http://example.com" {
		t.Fatalf("环境变量 upstream 未生效：%s", cfg.Upstream)
	}
	if cfg.Rate != 42 {
		t.Fatalf("环境变量 rate 未生效：%v", cfg.Rate)
	}
	if cfg.Burst != 7 {
		t.Fatalf("环境变量 burst 未生效：%d", cfg.Burst)
	}
}

// TestApplyEnvInvalidValue 验证非法环境变量值被忽略，保留原值。
func TestApplyEnvInvalidValue(t *testing.T) {
	cfg := Default()
	original := cfg.Rate

	t.Setenv("STREAMGUARD_RATE", "not-a-number")
	cfg.ApplyEnv()

	if cfg.Rate != original {
		t.Fatalf("非法环境变量应被忽略，期望 %v，实际 %v", original, cfg.Rate)
	}
}

// TestNormalize 验证配置规整逻辑。
func TestNormalize(t *testing.T) {
	cfg := &Config{
		Listen:   "127.0.0.1:8080",
		Upstream: "http://127.0.0.1:11434",
		Rate:     0,
		Burst:    0,
		MaxWait:  0,
		Timeout:  0,
	}
	cfg.Normalize()

	if cfg.Rate != 1 {
		t.Fatalf("rate 应规整为 1，实际 %v", cfg.Rate)
	}
	if cfg.Burst != 1 {
		t.Fatalf("burst 应规整为 1，实际 %d", cfg.Burst)
	}
	if cfg.MaxWait != Duration(30*time.Second) {
		t.Fatalf("max_wait 应规整为 30s，实际 %v", cfg.MaxWait)
	}
	if cfg.Timeout != Duration(120*time.Second) {
		t.Fatalf("timeout 应规整为 120s，实际 %v", cfg.Timeout)
	}
}

// TestDefault_Retry 验证重试相关默认值。
func TestDefault_Retry(t *testing.T) {
	cfg := Default()

	if cfg.RetryEnabled {
		t.Error("retry_enabled 默认应为 false")
	}
	if cfg.RetryMaxAttempts != 3 {
		t.Errorf("retry_max_attempts 默认应为 3，实际 %d", cfg.RetryMaxAttempts)
	}
	if cfg.RetryInitialWait != Duration(time.Second) {
		t.Errorf("retry_initial_wait 默认应为 1s，实际 %v", cfg.RetryInitialWait)
	}
	if cfg.RetryMaxWait != Duration(10*time.Second) {
		t.Errorf("retry_max_wait 默认应为 10s，实际 %v", cfg.RetryMaxWait)
	}
}

// TestLoadFromFile_Retry 验证从 JSON 加载重试配置。
func TestLoadFromFile_Retry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	content := `{
		"upstream": "http://127.0.0.1:11434",
		"retry_enabled": true,
		"retry_max_attempts": 5,
		"retry_initial_wait": "2s",
		"retry_max_wait": "20s"
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置失败：%v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}

	if !cfg.RetryEnabled {
		t.Error("retry_enabled 应为 true")
	}
	if cfg.RetryMaxAttempts != 5 {
		t.Errorf("retry_max_attempts 应为 5，实际 %d", cfg.RetryMaxAttempts)
	}
	if cfg.RetryInitialWait != Duration(2*time.Second) {
		t.Errorf("retry_initial_wait 应为 2s，实际 %v", cfg.RetryInitialWait)
	}
	if cfg.RetryMaxWait != Duration(20*time.Second) {
		t.Errorf("retry_max_wait 应为 20s，实际 %v", cfg.RetryMaxWait)
	}
}

// TestApplyEnv_Retry 验证重试相关环境变量覆盖。
func TestApplyEnv_Retry(t *testing.T) {
	cfg := Default()

	t.Setenv("STREAMGUARD_RETRY_ENABLED", "true")
	t.Setenv("STREAMGUARD_RETRY_MAX_ATTEMPTS", "7")
	t.Setenv("STREAMGUARD_RETRY_INITIAL_WAIT", "3s")
	t.Setenv("STREAMGUARD_RETRY_MAX_WAIT", "30s")

	cfg.ApplyEnv()

	if !cfg.RetryEnabled {
		t.Error("环境变量 retry_enabled 未生效")
	}
	if cfg.RetryMaxAttempts != 7 {
		t.Errorf("环境变量 retry_max_attempts 未生效：%d", cfg.RetryMaxAttempts)
	}
	if cfg.RetryInitialWait != Duration(3*time.Second) {
		t.Errorf("环境变量 retry_initial_wait 未生效：%v", cfg.RetryInitialWait)
	}
	if cfg.RetryMaxWait != Duration(30*time.Second) {
		t.Errorf("环境变量 retry_max_wait 未生效：%v", cfg.RetryMaxWait)
	}
}

// TestNormalize_Retry 验证重试配置规整。
func TestNormalize_Retry(t *testing.T) {
	cfg := &Config{
		Listen:           "127.0.0.1:8080",
		Upstream:         "http://127.0.0.1:11434",
		RetryMaxAttempts: 0,
		RetryInitialWait: 0,
		RetryMaxWait:     0,
	}
	cfg.Normalize()

	if cfg.RetryMaxAttempts != 3 {
		t.Errorf("retry_max_attempts 应规整为 3，实际 %d", cfg.RetryMaxAttempts)
	}
	if cfg.RetryInitialWait != Duration(time.Second) {
		t.Errorf("retry_initial_wait 应规整为 1s，实际 %v", cfg.RetryInitialWait)
	}
	if cfg.RetryMaxWait != Duration(10*time.Second) {
		t.Errorf("retry_max_wait 应规整为 10s，实际 %v", cfg.RetryMaxWait)
	}
}

// TestValidate_Retry 验证重试配置校验。
func TestValidate_Retry(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name:    "合法重试配置",
			mutate:  func(c *Config) { c.RetryEnabled = true; c.RetryMaxAttempts = 3 },
			wantErr: false,
		},
		{
			name:    "重试参数为负（Normalize 后规整，Validate 通过）",
			mutate:  func(c *Config) { c.RetryMaxAttempts = -1; c.Normalize() },
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("期望校验失败，但通过了")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("期望校验通过，但失败：%v", err)
			}
		})
	}
}

// TestDefault_SizeLimit 验证大小限流的默认值。
func TestDefault_SizeLimit(t *testing.T) {
	cfg := Default()
	if cfg.SizeLimitEnabled {
		t.Error("size_limit_enabled 默认应为 false")
	}
	if cfg.SizeLimitThreshold != 10000 {
		t.Errorf("size_limit_threshold 默认应为 10000，实际 %d", cfg.SizeLimitThreshold)
	}
	if cfg.SizeLimitSmallConcurrent != 0 {
		t.Errorf("size_limit_small_concurrent 默认应为 0，实际 %d", cfg.SizeLimitSmallConcurrent)
	}
	if cfg.SizeLimitLargeConcurrent != 0 {
		t.Errorf("size_limit_large_concurrent 默认应为 0，实际 %d", cfg.SizeLimitLargeConcurrent)
	}
}

// TestLoadFromFile_SizeLimit 验证从文件加载大小限流配置。
func TestLoadFromFile_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{
		"listen": "127.0.0.1:8080",
		"upstream": "http://127.0.0.1:11434",
		"size_limit_enabled": true,
		"size_limit_threshold": 8000,
		"size_limit_small_concurrent": 3,
		"size_limit_large_concurrent": 1
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入配置文件失败：%v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("加载配置失败：%v", err)
	}
	if !cfg.SizeLimitEnabled {
		t.Error("size_limit_enabled 应为 true")
	}
	if cfg.SizeLimitThreshold != 8000 {
		t.Errorf("size_limit_threshold 应为 8000，实际 %d", cfg.SizeLimitThreshold)
	}
	if cfg.SizeLimitSmallConcurrent != 3 {
		t.Errorf("size_limit_small_concurrent 应为 3，实际 %d", cfg.SizeLimitSmallConcurrent)
	}
	if cfg.SizeLimitLargeConcurrent != 1 {
		t.Errorf("size_limit_large_concurrent 应为 1，实际 %d", cfg.SizeLimitLargeConcurrent)
	}
}

// TestApplyEnv_SizeLimit 验证环境变量覆盖大小限流配置。
func TestApplyEnv_SizeLimit(t *testing.T) {
	t.Setenv("STREAMGUARD_SIZE_LIMIT_ENABLED", "true")
	t.Setenv("STREAMGUARD_SIZE_LIMIT_THRESHOLD", "5000")
	t.Setenv("STREAMGUARD_SIZE_LIMIT_SMALL_CONCURRENT", "4")
	t.Setenv("STREAMGUARD_SIZE_LIMIT_LARGE_CONCURRENT", "2")

	cfg := Default()
	cfg.ApplyEnv()

	if !cfg.SizeLimitEnabled {
		t.Error("size_limit_enabled 应为 true")
	}
	if cfg.SizeLimitThreshold != 5000 {
		t.Errorf("size_limit_threshold 应为 5000，实际 %d", cfg.SizeLimitThreshold)
	}
	if cfg.SizeLimitSmallConcurrent != 4 {
		t.Errorf("size_limit_small_concurrent 应为 4，实际 %d", cfg.SizeLimitSmallConcurrent)
	}
	if cfg.SizeLimitLargeConcurrent != 2 {
		t.Errorf("size_limit_large_concurrent 应为 2，实际 %d", cfg.SizeLimitLargeConcurrent)
	}
}

// TestNormalize_SizeLimit 验证大小限流配置的规整。
func TestNormalize_SizeLimit(t *testing.T) {
	cfg := &Config{
		Listen:                   "127.0.0.1:8080",
		Upstream:                 "http://127.0.0.1:11434",
		SizeLimitThreshold:       0,
		SizeLimitSmallConcurrent: -1,
		SizeLimitLargeConcurrent: -5,
	}
	cfg.Normalize()

	if cfg.SizeLimitThreshold != 10000 {
		t.Errorf("size_limit_threshold 应规整为 10000，实际 %d", cfg.SizeLimitThreshold)
	}
	if cfg.SizeLimitSmallConcurrent != 0 {
		t.Errorf("size_limit_small_concurrent 负数应规整为 0，实际 %d", cfg.SizeLimitSmallConcurrent)
	}
	if cfg.SizeLimitLargeConcurrent != 0 {
		t.Errorf("size_limit_large_concurrent 负数应规整为 0，实际 %d", cfg.SizeLimitLargeConcurrent)
	}
}

// TestValidate_SizeLimit 验证大小限流配置校验。
func TestValidate_SizeLimit(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{
			name: "启用且设置了小请求上限",
			mutate: func(c *Config) {
				c.SizeLimitEnabled = true
				c.SizeLimitSmallConcurrent = 2
			},
			wantErr: false,
		},
		{
			name: "启用且设置了大请求上限",
			mutate: func(c *Config) {
				c.SizeLimitEnabled = true
				c.SizeLimitLargeConcurrent = 1
			},
			wantErr: false,
		},
		{
			name: "启用但两档都不限（无意义配置）",
			mutate: func(c *Config) {
				c.SizeLimitEnabled = true
				c.SizeLimitSmallConcurrent = 0
				c.SizeLimitLargeConcurrent = 0
			},
			wantErr: true,
		},
		{
			name: "未启用时两档为 0 合法",
			mutate: func(c *Config) {
				c.SizeLimitEnabled = false
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.mutate(cfg)
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("期望校验失败，但通过了")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("期望校验通过，但失败：%v", err)
			}
		})
	}
}
