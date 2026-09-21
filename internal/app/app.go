// Package app 封装 StreamGuard 的服务生命周期管理，供 CLI 与 GUI 共用。
//
// 职责：
//   - 根据配置创建并启动 HTTP 服务
//   - 提供启动 / 停止 / 重启 / 更新配置的统一接口
//   - 暴露运行状态与统计信息
//
// 设计意图：把「服务如何运行」与「如何被调用」解耦，
// 使 CLI（命令行）与 GUI（Wails 绑定）共享同一套核心逻辑。
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/streamguard/streamguard/internal/breaker"
	"github.com/streamguard/streamguard/internal/config"
	"github.com/streamguard/streamguard/internal/limiter"
	"github.com/streamguard/streamguard/internal/proxy"
	"github.com/streamguard/streamguard/internal/retry"
	"github.com/streamguard/streamguard/internal/server"
)

// ErrAlreadyRunning 表示服务已在运行。
var ErrAlreadyRunning = errors.New("server is already running")

// App 是 StreamGuard 应用实例。
type App struct {
	mu sync.RWMutex

	cfg    *config.Config
	logger *log.Logger

	// verboseLogger 是详细日志（请求/响应内容）的输出目标。
	// 与 logger 分离，使 GUI 模式下详细日志只打印到控制台，不进入界面日志面板。
	verboseLogger *log.Logger

	// verboseFile 是详细日志文件句柄，非 nil 时需在停止时关闭。
	verboseFile *os.File

	// requestHook 是逐请求基础信息的回调，供 GUI 日志面板展示。
	// 为 nil 时不向 server 注册钩子（CLI 默认，零开销）。由 mu 保护。
	requestHook RequestLogFunc

	httpServer  *http.Server
	listener    net.Listener
	waiter      *limiter.Waiter
	concurrency *limiter.ConcurrencyLimiter
	proxy       *proxy.Proxy
	breaker     *breaker.Breaker
	retry       *retry.Policy

	running bool
	addr    string
}

// New 创建应用实例。此时服务尚未启动。
func New(cfg *config.Config) *App {
	return &App{
		cfg:           cfg,
		logger:        log.Default(),
		verboseLogger: log.New(os.Stdout, "", log.LstdFlags),
	}
}

// SetLogger 设置日志输出目标。
func (a *App) SetLogger(logger *log.Logger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.logger = logger
}

// SetVerboseLogger 设置详细日志（请求/响应内容）的输出目标。
//
// 详细日志默认输出到标准输出（控制台）。GUI 模式下可传入 nil 关闭，
// 或传入文件 logger 落盘，避免污染界面日志面板。
func (a *App) SetVerboseLogger(logger *log.Logger) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.verboseLogger = logger
}

// RequestLogFunc 是逐请求基础信息的回调签名。
//
// 参数均为基本类型，使调用方（GUI）无需 import server 包。
// elapsed 为请求总耗时，waited 表示是否因限流发生过排队。
// model/bodyBytes 是 chat 请求的元信息（模型名与请求体字节数），
// 非 chat 路径为空/0；不含消息内容，符合面板安全红线。
type RequestLogFunc func(method, path string, status int, elapsed time.Duration, waited bool, model string, bodyBytes int)

// SetRequestLogHook 注册逐请求基础信息回调，用于界面日志面板展示 API 基础信息。
//
// 传入 nil 取消注册。必须在 Start 之前调用；重启（UpdateConfig）时会沿用该回调。
// 注意：回调仅含方法/路径/状态码/耗时/chat 元信息，不含请求/响应内容，
// 符合「详细日志不进面板」红线。
func (a *App) SetRequestLogHook(fn RequestLogFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requestHook = fn
}

// newRequestForwarder 将基本类型回调适配为 server 的 RequestEvent 回调。
//
// hook 为 nil 时返回 nil，使 server 走零开销路径（不包裹 ResponseWriter）。
// 返回的函数类型与 server.Options.OnRequest 底层类型一致，可直接赋值。
func newRequestForwarder(hook RequestLogFunc) func(server.RequestEvent) {
	if hook == nil {
		return nil
	}
	return func(e server.RequestEvent) {
		hook(e.Method, e.Path, e.Status, e.Elapsed, e.Waited, e.Model, e.BodyBytes)
	}
}

// setupVerboseLogger 根据配置决定详细日志的输出目标。
//
// 优先级：
//  1. log_file 非空 → 写入文件（"auto" 时自动生成 <当前目录>/logs/streamguard-YYYYMMDD.log）
//  2. 否则 → 输出到控制台（标准输出）
//
// 仅在 log_level=debug 时调用。调用方需持有 a.mu 写锁。
func (a *App) setupVerboseLogger() {
	// 先关闭上一次打开的文件，避免重启时句柄泄漏。
	if a.verboseFile != nil {
		_ = a.verboseFile.Close()
		a.verboseFile = nil
	}

	path := a.cfg.LogFile
	if path == "" {
		// 未配置日志文件：保持控制台输出。
		a.verboseLogger = log.New(os.Stdout, "", log.LstdFlags)
		return
	}

	if path == "auto" {
		// 显式基于「命令当前目录」解析，避免依赖进程 cwd 的隐式行为。
		// 相对路径统一挂到当前目录下的 logs/ 子目录。
		path = filepath.Join(logDir(), fmt.Sprintf("streamguard-%s.log", time.Now().Format("20060102")))
	} else if !filepath.IsAbs(path) {
		// 用户配置的相对路径同样基于当前目录解析，保证位置可预期。
		path = filepath.Join(logDir(), path)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		a.logger.Printf("[app] failed to create log dir for %s: %v, falling back to stdout", path, err)
		a.verboseLogger = log.New(os.Stdout, "", log.LstdFlags)
		return
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		a.logger.Printf("[app] failed to open log file %s: %v, falling back to stdout", path, err)
		a.verboseLogger = log.New(os.Stdout, "", log.LstdFlags)
		return
	}

	a.verboseFile = f
	a.verboseLogger = log.New(f, "", log.LstdFlags)
	a.logger.Printf("[app] verbose log file: %s", path)

	// 清理过期日志文件（按配置的保留天数）。
	a.cleanupOldLogs()
}

// cleanupOldLogs 删除 logs/ 目录下超过保留天数的日志文件。
//
// 仅删除形如 streamguard-YYYYMMDD.log 的文件，避免误删用户其他文件。
// 保留天数 <=0 时跳过清理。调用方需持有 a.mu 写锁。
func (a *App) cleanupOldLogs() {
	days := a.cfg.LogRetainDays
	if days <= 0 {
		return
	}

	dir := logDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// 只处理 streamguard-YYYYMMDD.log 形式的文件
		if !strings.HasPrefix(name, "streamguard-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		datePart := strings.TrimSuffix(strings.TrimPrefix(name, "streamguard-"), ".log")
		fileDate, err := time.ParseInLocation("20060102", datePart, time.Local)
		if err != nil {
			continue
		}
		if fileDate.Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, name)); err == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		a.logger.Printf("[app] cleaned up %d expired log file(s) (retain %d days)", removed, days)
	}
}

// logDir 返回日志目录：命令当前目录下的 logs/。
//
// 取不到当前目录时回退到相对路径 "logs"，保证仍可写入。
func logDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "logs"
	}
	return filepath.Join(wd, "logs")
}

// closeVerboseFile 关闭详细日志文件句柄。调用方需持有 a.mu 写锁。
func (a *App) closeVerboseFile() {
	if a.verboseFile != nil {
		_ = a.verboseFile.Close()
		a.verboseFile = nil
	}
}

// Start 启动服务。
//
// 若服务已在运行，返回 ErrAlreadyRunning。
// 监听地址中的端口为 0 时，系统会分配随机端口，可通过 Addr() 获取实际地址。
func (a *App) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.running {
		return ErrAlreadyRunning
	}

	if err := a.cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	// 创建熔断器（默认关闭，不改变既有行为）
	br := breaker.New(breaker.Options{
		Enabled:   a.cfg.BreakerEnabled,
		Threshold: a.cfg.BreakerThreshold,
		Cooldown:  a.cfg.BreakerCooldown.Duration(),
	})

	// 创建上游限流重试策略（默认关闭，不改变既有行为）
	rt := retry.New(retry.Options{
		Enabled:     a.cfg.RetryEnabled,
		MaxAttempts: a.cfg.RetryMaxAttempts,
		InitialWait: a.cfg.RetryInitialWait.Duration(),
		MaxWait:     a.cfg.RetryMaxWait.Duration(),
	})

	// 创建反向代理
	p, err := proxy.New(proxy.Options{
		Upstream:     a.cfg.Upstream,
		Timeout:      a.cfg.Timeout.Duration(),
		PreserveHost: a.cfg.PreserveHost,
		Breaker:      br,
		Retry:        rt,
	})
	if err != nil {
		return fmt.Errorf("failed to create proxy: %w", err)
	}

	// 创建等待式限流器
	waiter := limiter.NewWaiter(limiter.WaiterOptions{
		Rate:    a.cfg.Rate,
		Burst:   a.cfg.Burst,
		MaxWait: a.cfg.MaxWait.Duration(),
	})

	// 创建按请求大小分档的并发限流器（默认关闭，不改变既有行为）
	var concurrency *limiter.ConcurrencyLimiter
	if a.cfg.SizeLimitEnabled {
		concurrency = limiter.NewConcurrencyLimiter(limiter.ConcurrencyOptions{
			Threshold:  a.cfg.SizeLimitThreshold,
			SmallLimit: a.cfg.SizeLimitSmallConcurrent,
			LargeLimit: a.cfg.SizeLimitLargeConcurrent,
			MaxWait:    a.cfg.MaxWait.Duration(),
		})
	}

	// 详细日志：log_level=debug 时按配置决定输出到文件还是控制台。
	if a.cfg.LogLevel == "debug" {
		a.setupVerboseLogger()
	}

	// 创建 HTTP 服务
	srv := server.New(server.Options{
		Proxy:         p,
		Waiter:        waiter,
		Concurrency:   concurrency,
		MaxWait:       a.cfg.MaxWait.Duration(),
		Logger:        a.logger,
		Verbose:       a.cfg.LogLevel == "debug",
		VerboseLogger: a.verboseLogger,
		OnRequest:     newRequestForwarder(a.requestHook),
	})

	// 先监听，以便获取实际端口（支持 :0 随机端口）
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", a.cfg.Listen, err)
	}

	httpServer := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		// 不设置 WriteTimeout，避免长 SSE 流被强制中断。
	}
	a.httpServer = httpServer
	a.listener = ln
	a.waiter = waiter
	a.concurrency = concurrency
	a.proxy = p
	a.breaker = br
	a.retry = rt
	a.addr = ln.Addr().String()
	a.running = true

	// 注意：goroutine 捕获局部变量 httpServer，而非访问 a.httpServer。
	// 否则 Stop() 将其置 nil 后，此处会产生空指针 panic（竞态）。
	go func() {
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.logger.Printf("[app] server exited unexpectedly: %v", err)
		}
	}()

	a.logger.Printf("[app] server started, listening on %s", a.addr)
	return nil
}

// Stop 优雅停止服务。
//
// 未运行时调用不报错，便于调用方无脑清理。
//
// 注意：本服务代理 SSE 长连接，活跃的流式请求不会「空闲」，
// 因此 Shutdown 可能一直等到 ctx 超时。此时会强制关闭残留连接，
// 保证服务确实停止、状态一致，而不是把错误抛给调用方。
func (a *App) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.running {
		return nil
	}

	// 先尝试优雅关闭：等待活跃请求自然结束。
	err := a.httpServer.Shutdown(ctx)
	if err != nil {
		// 超时（通常是 SSE 长连接未结束）：强制关闭，避免服务卡在「半停止」状态。
		a.logger.Printf("[app] graceful shutdown timed out (%v), forcing close", err)
		if closeErr := a.httpServer.Close(); closeErr != nil {
			a.logger.Printf("[app] force close failed: %v", closeErr)
		}
	}

	// 无论优雅还是强制，都视为已停止，保证状态一致。
	a.running = false
	a.httpServer = nil
	a.listener = nil
	// 清空限流器/代理引用，避免 Stats() 在停止后仍返回旧计数（状态残留）。
	a.waiter = nil
	a.concurrency = nil
	a.proxy = nil
	a.breaker = nil
	a.retry = nil
	a.closeVerboseFile()
	a.logger.Printf("[app] server stopped")
	return nil
}

// Restart 重启服务。
func (a *App) Restart(ctx context.Context) error {
	if err := a.Stop(ctx); err != nil {
		return err
	}
	return a.Start()
}

// UpdateConfig 更新配置并重启服务。
//
// 若服务正在运行，会先停止再以新配置启动，实现「保存并重启」语义。
func (a *App) UpdateConfig(ctx context.Context, cfg *config.Config) error {
	a.mu.Lock()
	wasRunning := a.running
	a.cfg = cfg
	a.mu.Unlock()

	if !wasRunning {
		return nil
	}

	if err := a.Stop(ctx); err != nil {
		return err
	}
	return a.Start()
}

// Running 返回服务是否正在运行。
func (a *App) Running() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

// Addr 返回实际监听地址。未启动时返回空字符串。
func (a *App) Addr() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.addr
}

// VerboseLogPath 返回详细日志的实际落盘路径。
//
// 规则与 setupVerboseLogger 一致：
//   - log_file 为空 → ""（输出到控制台，无文件）
//   - "auto" → <当前目录>/logs/streamguard-YYYYMMDD.log
//   - 相对路径 → <当前目录>/logs/<path>
//   - 绝对路径 → 原样
//
// 供 GUI 展示日志位置，方便用户查看或清理。
func (a *App) VerboseLogPath() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	path := a.cfg.LogFile
	if path == "" {
		return ""
	}
	if path == "auto" {
		return filepath.Join(logDir(), fmt.Sprintf("streamguard-%s.log", time.Now().Format("20060102")))
	}
	if !filepath.IsAbs(path) {
		return filepath.Join(logDir(), path)
	}
	return path
}

// Config 返回当前配置的副本，避免调用方修改内部状态。
func (a *App) Config() *config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()

	cp := *a.cfg
	return &cp
}

// Stats 返回运行状态与统计快照。
func (a *App) Stats() Stats {
	a.mu.RLock()
	defer a.mu.RUnlock()

	s := Stats{
		Running:  a.running,
		Addr:     a.addr,
		Upstream: a.cfg.Upstream,
		Rate:     a.cfg.Rate,
		Burst:    a.cfg.Burst,
	}
	if a.waiter != nil {
		ws := a.waiter.Stats()
		s.Total = ws.Total
		s.Waited = ws.Waited
		s.TimedOut = ws.TimedOut
		s.Canceled = ws.Canceled
		s.AvgWaitMs = ws.AvgWait.Milliseconds()
		s.MaxWaitMs = ws.MaxWait.Milliseconds()
	}
	if a.breaker != nil {
		bs := a.breaker.Stats()
		s.BreakerEnabled = bs.Enabled
		s.BreakerState = bs.State
		s.BreakerTrips = bs.Trips
		s.BreakerRejected = bs.Rejected
	}
	s.RetryEnabled = a.cfg.RetryEnabled
	s.SizeLimitEnabled = a.cfg.SizeLimitEnabled
	if a.concurrency != nil {
		cs := a.concurrency.Stats()
		s.SizeLimitSmallActive = cs.SmallActive
		s.SizeLimitLargeActive = cs.LargeActive
		s.SizeLimitWaiting = cs.Waiting
		s.SizeLimitTimedOut = cs.TimedOut
	}
	return s
}

// Stats 是应用运行状态与统计快照。
type Stats struct {
	Running         bool    `json:"running"`          // 是否运行中
	Addr            string  `json:"addr"`             // 实际监听地址
	Upstream        string  `json:"upstream"`         // 上游地址
	Rate            float64 `json:"rate"`             // 限流速率
	Burst           int     `json:"burst"`            // 突发容量
	Total           int64   `json:"total"`            // 累计请求数
	Waited          int64   `json:"waited"`           // 累计等待数
	TimedOut        int64   `json:"timed_out"`        // 等待超时（429）数
	Canceled        int64   `json:"canceled"`         // 等待期间取消数
	AvgWaitMs       int64   `json:"avg_wait_ms"`      // 平均等待耗时（毫秒）
	MaxWaitMs       int64   `json:"max_wait_ms"`      // 单次最大等待耗时（毫秒）
	BreakerEnabled  bool    `json:"breaker_enabled"`  // 熔断器是否启用
	BreakerState    string  `json:"breaker_state"`    // 熔断器状态
	BreakerTrips    int64   `json:"breaker_trips"`    // 累计熔断次数
	BreakerRejected int64   `json:"breaker_rejected"` // 累计快速拒绝数
	RetryEnabled    bool    `json:"retry_enabled"`    // 上游限流重试是否启用

	SizeLimitEnabled     bool  `json:"size_limit_enabled"`      // 大小限流是否启用
	SizeLimitSmallActive int   `json:"size_limit_small_active"` // 小请求当前在途数
	SizeLimitLargeActive int   `json:"size_limit_large_active"` // 大请求当前在途数
	SizeLimitWaiting     int   `json:"size_limit_waiting"`      // 当前排队等待数
	SizeLimitTimedOut    int64 `json:"size_limit_timed_out"`    // 大小限流等待超时数
}
