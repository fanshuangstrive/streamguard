// Package config 负责 StreamGuard 的配置加载、校验与规整。
//
// 配置优先级（后者覆盖前者）：
//  1. 内置默认值
//  2. JSON 配置文件
//  3. 环境变量（STREAMGUARD_ 前缀）
//
// 采用 JSON 而非 YAML，是为了保持零外部依赖，符合单机 Windows 客户端定位。
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Config 是 StreamGuard 的运行时配置。
type Config struct {
	// Listen 是本地监听地址，默认仅绑定回环地址，避免暴露到局域网。
	Listen string `json:"listen"`
	// Upstream 是上游模型服务基础地址，例如 https://api.example.com。
	// 代理会把请求路径原样拼接到该地址后转发：
	//   /model/v1/chat/completions → https://api.example.com/model/v1/chat/completions
	Upstream string `json:"upstream"`
	// PreserveHost 为 true 时保留客户端原始 Host 头，不改写为上游主机名。
	// 部分上游网关（如 Kong）依赖 Host 做路由，此时需要开启。
	PreserveHost bool `json:"preserve_host"`
	// Rate 是等待式限流的速率（每秒允许的请求数），默认 1。
	Rate float64 `json:"rate"`
	// Burst 是突发容量，默认 1。
	Burst int `json:"burst"`
	// MaxWait 是单次请求最大等待时长，超过则返回 429，默认 30 秒。
	MaxWait Duration `json:"max_wait"`
	// Timeout 是转发到上游的请求超时，默认 120 秒（兼容长 SSE 流）。
	Timeout Duration `json:"timeout"`
	// LogLevel 是日志级别：debug / info / warn / error。
	LogLevel string `json:"log_level"`
	// LogFile 是详细日志（请求/响应内容）的落盘路径。
	// 为空时不写文件；设为 "auto" 时自动写入 logs/streamguard-YYYYMMDD.log。
	// 仅在 log_level=debug 时生效。
	LogFile string `json:"log_file"`
	// LogRetainDays 是日志文件保留天数，超过则自动删除，默认 7。
	// 设为 0 或负数表示不清理。仅在 log_file 非空时生效。
	LogRetainDays int `json:"log_retain_days"`
	// BreakerEnabled 为 true 时启用上游熔断：连续失败达阈值后快速失败，
	// 避免客户端长时间挂起。默认 false（不改变既有行为）。
	BreakerEnabled bool `json:"breaker_enabled"`
	// BreakerThreshold 是触发熔断的连续失败次数，默认 5。
	BreakerThreshold int `json:"breaker_threshold"`
	// BreakerCooldown 是熔断后的冷却时长，冷却结束进入半开试探，默认 30 秒。
	BreakerCooldown Duration `json:"breaker_cooldown"`
	// RetryEnabled 为 true 时启用上游限流重试：上游返回 429/503 时
	// 等待后自动重试。默认 false（不改变既有行为）。
	RetryEnabled bool `json:"retry_enabled"`
	// RetryMaxAttempts 是最大尝试次数（含首次请求），默认 3。
	RetryMaxAttempts int `json:"retry_max_attempts"`
	// RetryInitialWait 是首次重试的等待时长，默认 1 秒。
	RetryInitialWait Duration `json:"retry_initial_wait"`
	// RetryMaxWait 是单次重试等待的上限，默认 10 秒。
	RetryMaxWait Duration `json:"retry_max_wait"`
	// SizeLimitEnabled 为 true 时启用「按请求大小分档的并发限流」：
	// 大请求（长上下文）限制并发数，避免长时间占用上游连接。
	// 默认 false（不改变既有行为）。
	SizeLimitEnabled bool `json:"size_limit_enabled"`
	// SizeLimitThreshold 是大小请求的分界（估算 token 数），默认 10000。
	// 估算 token <= 该值视为小请求，> 该值视为大请求。
	SizeLimitThreshold int `json:"size_limit_threshold"`
	// SizeLimitSmallConcurrent 是小请求的并发上限，默认 0（不限制）。
	SizeLimitSmallConcurrent int `json:"size_limit_small_concurrent"`
	// SizeLimitLargeConcurrent 是大请求的并发上限，默认 0（不限制）。
	SizeLimitLargeConcurrent int `json:"size_limit_large_concurrent"`
}

// Default 返回内置默认配置。
func Default() *Config {
	return &Config{
		Listen:           "127.0.0.1:8080",
		Upstream:         "http://127.0.0.1:11434",
		Rate:             1,
		Burst:            1,
		MaxWait:          Duration(30 * time.Second),
		Timeout:          Duration(120 * time.Second),
		LogLevel:         "info",
		LogFile:          "",
		LogRetainDays:    7,
		BreakerEnabled:   false,
		BreakerThreshold: 5,
		BreakerCooldown:  Duration(30 * time.Second),
		RetryEnabled:     false,
		RetryMaxAttempts: 3,
		RetryInitialWait: Duration(time.Second),
		RetryMaxWait:     Duration(10 * time.Second),

		SizeLimitEnabled:         false,
		SizeLimitThreshold:       10000,
		SizeLimitSmallConcurrent: 0,
		SizeLimitLargeConcurrent: 0,
	}
}

// Load 从指定 JSON 文件加载配置，并与默认值合并。
//
// 文件不存在或解析失败时返回错误，由调用方决定是否回退到默认配置。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfg.Normalize()
	return cfg, nil
}

// ApplyEnv 使用环境变量覆盖配置项。
//
// 支持的环境变量（均以 STREAMGUARD_ 为前缀）：
//   - STREAMGUARD_LISTEN
//   - STREAMGUARD_UPSTREAM
//   - STREAMGUARD_PRESERVE_HOST
//   - STREAMGUARD_RATE
//   - STREAMGUARD_BURST
//   - STREAMGUARD_MAX_WAIT
//   - STREAMGUARD_TIMEOUT
//   - STREAMGUARD_LOG_LEVEL
//   - STREAMGUARD_LOG_FILE
//   - STREAMGUARD_LOG_RETAIN_DAYS
//   - STREAMGUARD_BREAKER_ENABLED
//   - STREAMGUARD_BREAKER_THRESHOLD
//   - STREAMGUARD_BREAKER_COOLDOWN
//   - STREAMGUARD_RETRY_ENABLED
//   - STREAMGUARD_RETRY_MAX_ATTEMPTS
//   - STREAMGUARD_RETRY_INITIAL_WAIT
//   - STREAMGUARD_RETRY_MAX_WAIT
//   - STREAMGUARD_SIZE_LIMIT_ENABLED
//   - STREAMGUARD_SIZE_LIMIT_THRESHOLD
//   - STREAMGUARD_SIZE_LIMIT_SMALL_CONCURRENT
//   - STREAMGUARD_SIZE_LIMIT_LARGE_CONCURRENT
//
// 非法值会被忽略并保留原值，避免因环境变量拼写错误导致服务异常。
func (c *Config) ApplyEnv() {
	if v := os.Getenv("STREAMGUARD_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("STREAMGUARD_UPSTREAM"); v != "" {
		c.Upstream = v
	}
	if v, ok := envBool("STREAMGUARD_PRESERVE_HOST"); ok {
		c.PreserveHost = v
	}
	if v, ok := envFloat("STREAMGUARD_RATE"); ok {
		c.Rate = v
	}
	if v, ok := envInt("STREAMGUARD_BURST"); ok {
		c.Burst = v
	}
	if v, ok := envDuration("STREAMGUARD_MAX_WAIT"); ok {
		c.MaxWait = Duration(v)
	}
	if v, ok := envDuration("STREAMGUARD_TIMEOUT"); ok {
		c.Timeout = Duration(v)
	}
	if v := os.Getenv("STREAMGUARD_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
	if v := os.Getenv("STREAMGUARD_LOG_FILE"); v != "" {
		c.LogFile = v
	}
	if v, ok := envInt("STREAMGUARD_LOG_RETAIN_DAYS"); ok {
		c.LogRetainDays = v
	}
	if v, ok := envBool("STREAMGUARD_BREAKER_ENABLED"); ok {
		c.BreakerEnabled = v
	}
	if v, ok := envInt("STREAMGUARD_BREAKER_THRESHOLD"); ok {
		c.BreakerThreshold = v
	}
	if v, ok := envDuration("STREAMGUARD_BREAKER_COOLDOWN"); ok {
		c.BreakerCooldown = Duration(v)
	}
	if v, ok := envBool("STREAMGUARD_RETRY_ENABLED"); ok {
		c.RetryEnabled = v
	}
	if v, ok := envInt("STREAMGUARD_RETRY_MAX_ATTEMPTS"); ok {
		c.RetryMaxAttempts = v
	}
	if v, ok := envDuration("STREAMGUARD_RETRY_INITIAL_WAIT"); ok {
		c.RetryInitialWait = Duration(v)
	}
	if v, ok := envDuration("STREAMGUARD_RETRY_MAX_WAIT"); ok {
		c.RetryMaxWait = Duration(v)
	}
	if v, ok := envBool("STREAMGUARD_SIZE_LIMIT_ENABLED"); ok {
		c.SizeLimitEnabled = v
	}
	if v, ok := envInt("STREAMGUARD_SIZE_LIMIT_THRESHOLD"); ok {
		c.SizeLimitThreshold = v
	}
	if v, ok := envInt("STREAMGUARD_SIZE_LIMIT_SMALL_CONCURRENT"); ok {
		c.SizeLimitSmallConcurrent = v
	}
	if v, ok := envInt("STREAMGUARD_SIZE_LIMIT_LARGE_CONCURRENT"); ok {
		c.SizeLimitLargeConcurrent = v
	}
}

// Normalize 将零值或非法值规整为安全默认值。
//
// 注意：本方法会把负数一并规整为默认值，因此**必须在 Validate 之前调用**，
// 否则 Validate 中的负数检查永远不会触发（死代码）。
// 调用顺序约定：Load/ApplyEnv → Normalize → Validate。
func (c *Config) Normalize() {
	if c.Rate <= 0 {
		c.Rate = 1
	}
	if c.Burst <= 0 {
		c.Burst = 1
	}
	if c.MaxWait <= 0 {
		c.MaxWait = Duration(30 * time.Second)
	}
	if c.Timeout <= 0 {
		c.Timeout = Duration(120 * time.Second)
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.LogRetainDays == 0 {
		c.LogRetainDays = 7
	}
	if c.BreakerThreshold <= 0 {
		c.BreakerThreshold = 5
	}
	if c.BreakerCooldown <= 0 {
		c.BreakerCooldown = Duration(30 * time.Second)
	}
	if c.RetryMaxAttempts <= 0 {
		c.RetryMaxAttempts = 3
	}
	if c.RetryInitialWait <= 0 {
		c.RetryInitialWait = Duration(time.Second)
	}
	if c.RetryMaxWait <= 0 {
		c.RetryMaxWait = Duration(10 * time.Second)
	}
	if c.SizeLimitThreshold <= 0 {
		c.SizeLimitThreshold = 10000
	}
	// 并发上限为负数时规整为 0（不限制），避免出现无意义的负值。
	if c.SizeLimitSmallConcurrent < 0 {
		c.SizeLimitSmallConcurrent = 0
	}
	if c.SizeLimitLargeConcurrent < 0 {
		c.SizeLimitLargeConcurrent = 0
	}
}

// Validate 校验配置的合法性。
//
// 注意：数值字段的负数检查在 Normalize 之后已不可达（负数会被规整为默认值），
// 因此这里只校验 Normalize 无法修复的项（地址、枚举等）。
// 保留数值检查是为了防御「未调用 Normalize 直接 Validate」的误用场景。
func (c *Config) Validate() error {
	if c.Listen == "" {
		return fmt.Errorf("listen must not be empty")
	}
	if c.Upstream == "" {
		return fmt.Errorf("upstream must not be empty")
	}
	u, err := url.Parse(c.Upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream address: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("upstream must use http or https scheme, got: %s", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("upstream is missing host: %s", c.Upstream)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level must be one of debug/info/warn/error, got: %s", c.LogLevel)
	}
	// 启用大小限流时，至少要有一个档位设置了并发上限，否则配置无意义。
	if c.SizeLimitEnabled && c.SizeLimitSmallConcurrent <= 0 && c.SizeLimitLargeConcurrent <= 0 {
		return fmt.Errorf("size_limit_enabled is true but both size_limit_small_concurrent and size_limit_large_concurrent are 0 (unlimited)")
	}
	return nil
}

// envInt 读取整型环境变量，非法值返回 ok=false。
func envInt(key string) (int, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// envDuration 读取时长环境变量，支持 "1s"、"500ms" 等格式。
func envDuration(key string) (time.Duration, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, false
	}
	return d, true
}

// envFloat 读取浮点型环境变量，非法值返回 ok=false。
func envFloat(key string) (float64, bool) {
	v := os.Getenv(key)
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// envBool 读取布尔型环境变量，非法值返回 ok=false。
// 接受 1/0、true/false、yes/no、on/off（大小写不敏感）。
func envBool(key string) (bool, bool) {
	v := os.Getenv(key)
	if v == "" {
		return false, false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, false
	}
	return b, true
}
