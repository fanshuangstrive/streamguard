// Package server 组装 HTTP 服务：路由分发、限流等待、请求转发。
//
// 请求处理流程：
//
//	客户端请求 /v1/chat/completions
//	  → 限流器 Wait（阻塞等待配额，超时返回 429）
//	  → 反向代理转发到上游模型服务
//	  → SSE 流式响应逐块透传回客户端
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/streamguard/streamguard/internal/limiter"
	"github.com/streamguard/streamguard/internal/proxy"
)

// Options 是服务的构造参数。
type Options struct {
	// Proxy 是反向代理，必填。
	Proxy *proxy.Proxy
	// Waiter 是等待式限流器，可选。为 nil 时不限流。
	Waiter *limiter.Waiter
	// Concurrency 是按请求大小分档的并发限流器，可选。为 nil 时不限并发。
	Concurrency *limiter.ConcurrencyLimiter
	// MaxWait 是限流等待的总时长上限，用于让速率限流与并发限流共享同一 deadline，
	// 避免两者各自等待导致总等待时长叠加。<=0 时不做总时长约束。
	MaxWait time.Duration
	// Logger 是日志输出，可选。为 nil 时使用标准库默认 logger。
	Logger *log.Logger
	// Verbose 为 true 时打印完整的请求/响应内容（含 header 与 body），
	// 用于本地调试。SSE 响应会逐块打印。默认 false，避免日志噪音与敏感信息泄漏。
	Verbose bool
	// VerboseLogger 是详细日志的输出目标，可选。
	// 为 nil 时回退到 Logger。GUI 模式下可单独指向控制台，避免污染界面日志面板。
	VerboseLogger *log.Logger
	// OnRequest 是逐请求基础信息的回调，可选。为 nil 时不产生任何额外开销（CLI 默认）。
	// GUI 模式下用于把方法/路径/状态码/耗时/是否排队上报到界面日志面板。
	OnRequest onRequestFunc
}

// Server 是 StreamGuard 的 HTTP 服务。
type Server struct {
	proxy         *proxy.Proxy
	waiter        *limiter.Waiter
	concurrency   *limiter.ConcurrencyLimiter
	maxWait       time.Duration
	logger        *log.Logger
	verboseLogger *log.Logger
	onRequest     onRequestFunc
	mux           *http.ServeMux
	verbose       bool
}

// New 创建 HTTP 服务。
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}

	// 详细日志默认跟随主 logger，便于 CLI 场景直接输出到控制台。
	verboseLogger := opts.VerboseLogger
	if verboseLogger == nil {
		verboseLogger = logger
	}

	s := &Server{
		proxy:         opts.Proxy,
		waiter:        opts.Waiter,
		concurrency:   opts.Concurrency,
		maxWait:       opts.MaxWait,
		logger:        logger,
		verboseLogger: verboseLogger,
		onRequest:     opts.OnRequest,
		mux:           http.NewServeMux(),
		verbose:       opts.Verbose,
	}
	s.routes()
	return s
}

// routes 注册路由。
func (s *Server) routes() {
	// 代理所有业务路径，路径原样转发到上游。
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/stats", s.handleStats)
	s.mux.HandleFunc("/", s.handleProxy)
}

// ServeHTTP 实现 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleProxy 处理业务请求：限流等待 → 转发到上游。
func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// 请求钩子：用一个只记录状态码、其余全透传的包装器捕获最终响应码。
	// 仅当注册了钩子时启用（CLI 无钩子，零开销）；放在最外层以覆盖限流早退路径。
	var stat *statusRecorder
	if s.onRequest != nil {
		stat = newStatusRecorder(w)
		w = stat
	}

	// 累计限流等待时长（速率限流 + 并发限流），用于面板「排队」标记。
	var waitDur time.Duration

	defer func() {
		if s.onRequest == nil {
			return
		}
		s.onRequest(RequestEvent{
			Method:   r.Method,
			Path:     r.URL.Path,
			Status:   stat.status,
			Elapsed:  time.Since(start),
			Waited:   waitDur >= requestWaitMarkThreshold,
			WaitTime: waitDur,
		})
	}()

	// 详细模式：打印请求行、请求头与请求体。
	if s.verbose {
		s.logRequest(r)
	}

	// 速率限流与并发限流共享同一 deadline，避免两者各自等待导致总时长叠加。
	ctx := r.Context()
	if s.maxWait > 0 && (s.waiter != nil || s.concurrency != nil) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.maxWait)
		defer cancel()
	}

	// 1. 速率限流等待
	if s.waiter != nil {
		t0 := time.Now()
		err := s.waiter.Wait(ctx)
		waitDur += time.Since(t0)
		if err != nil {
			s.writeRateLimitError(w, r, err)
			return
		}
	}

	// 2. 按请求大小分档的并发限流
	if s.concurrency != nil {
		tokens := limiter.EstimateTokens(s.peekBody(r))
		t0 := time.Now()
		release, err := s.concurrency.Acquire(ctx, tokens)
		waitDur += time.Since(t0)
		if err != nil {
			s.writeRateLimitError(w, r, err)
			return
		}
		defer release()
	}

	// 3. 转发到上游
	if s.verbose {
		// 用包装器捕获响应状态码、响应头与响应体（SSE 逐块打印）。
		rec := newVerboseRecorder(w, s.verboseLogger)
		s.proxy.ServeHTTP(rec, r)
		rec.finish()
	} else {
		s.proxy.ServeHTTP(w, r)
	}

	s.logger.Printf("[proxy] %s %s client=%s elapsed=%v",
		r.Method, r.URL.Path, clientIP(r), time.Since(start))
}

// peekBody 读取请求体用于 token 估算，并回填 r.Body 保证后续代理仍能读到完整内容。
//
// 优先使用 r.GetBody（httptest 与部分客户端会设置），避免重复读取。
// 读取失败或请求体为空时返回 nil（估算为 0 token）。
func (s *Server) peekBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		// 读取失败：回填空 body，避免后续代理读到半截数据。
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

// logRequest 打印请求行、请求头与请求体（详细模式）。
func (s *Server) logRequest(r *http.Request) {
	l := s.verboseLogger
	l.Printf("[req] >>> %s %s %s", r.Method, r.URL.RequestURI(), r.Proto)
	l.Printf("[req] Host: %s", r.Host)
	for k, vs := range r.Header {
		for _, v := range vs {
			l.Printf("[req] %s: %s", k, v)
		}
	}

	// 读取并回填请求体，保证后续代理仍能读到完整内容。
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			l.Printf("[req] <failed to read body: %v>", err)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if len(body) > 0 {
			l.Printf("[req] body (%d bytes): %s", len(body), string(body))
		}
	}
}

// writeRateLimitError 返回 OpenAI 兼容的 429 错误响应。
func (s *Server) writeRateLimitError(w http.ResponseWriter, r *http.Request, err error) {
	// Client canceled: do not write a response (connection already closed).
	if errors.Is(err, r.Context().Err()) && r.Context().Err() != nil {
		s.logger.Printf("[rate-limit] client canceled client=%s", clientIP(r))
		return
	}

	s.logger.Printf("[rate-limit] request rejected client=%s reason=%v", clientIP(r), err)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)

	resp := map[string]any{
		"error": map[string]any{
			"message": "rate limit wait timeout exceeded, please retry later",
			"type":    "rate_limit_exceeded",
			"code":    "rate_limit_exceeded",
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// handleHealthz 健康检查端点。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// statsResponse 是 /stats 的响应结构。
//
// 使用结构体而非 map，保证 JSON 字段顺序稳定（便于阅读与测试断言）。
type statsResponse struct {
	Upstream        string  `json:"upstream"`
	Rate            float64 `json:"rate"`
	Burst           int     `json:"burst"`
	Total           int64   `json:"total"`
	Waited          int64   `json:"waited"`
	TimedOut        int64   `json:"timed_out"`
	Canceled        int64   `json:"canceled"`
	AvgWaitMs       int64   `json:"avg_wait_ms"`
	MaxWaitMs       int64   `json:"max_wait_ms"`
	MinWaitMs       int64   `json:"min_wait_ms"`
	BreakerEnabled  bool    `json:"breaker_enabled"`
	BreakerState    string  `json:"breaker_state"`
	BreakerTrips    int64   `json:"breaker_trips"`
	BreakerRejected int64   `json:"breaker_rejected"`
	RetryEnabled    bool    `json:"retry_enabled"`

	SizeLimitEnabled     bool  `json:"size_limit_enabled"`
	SizeLimitThreshold   int   `json:"size_limit_threshold"`
	SizeLimitSmallLimit  int   `json:"size_limit_small_limit"`
	SizeLimitLargeLimit  int   `json:"size_limit_large_limit"`
	SizeLimitSmallActive int   `json:"size_limit_small_active"`
	SizeLimitLargeActive int   `json:"size_limit_large_active"`
	SizeLimitWaiting     int   `json:"size_limit_waiting"`
	SizeLimitTimedOut    int64 `json:"size_limit_timed_out"`
}

// handleStats 返回限流与代理统计信息。
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	resp := statsResponse{
		Upstream: s.proxy.Upstream(),
	}
	if s.waiter != nil {
		ws := s.waiter.Stats()
		resp.Rate = ws.Rate
		resp.Burst = ws.Burst
		resp.Total = ws.Total
		resp.Waited = ws.Waited
		resp.TimedOut = ws.TimedOut
		resp.Canceled = ws.Canceled
		resp.AvgWaitMs = ws.AvgWait.Milliseconds()
		resp.MaxWaitMs = ws.MaxWait.Milliseconds()
		resp.MinWaitMs = ws.MinWait.Milliseconds()
	}
	if br := s.proxy.Breaker(); br != nil {
		bs := br.Stats()
		resp.BreakerEnabled = bs.Enabled
		resp.BreakerState = bs.State
		resp.BreakerTrips = bs.Trips
		resp.BreakerRejected = bs.Rejected
	}
	if rt := s.proxy.Retry(); rt != nil {
		resp.RetryEnabled = rt.Enabled()
	}
	if s.concurrency != nil {
		cs := s.concurrency.Stats()
		resp.SizeLimitEnabled = true
		resp.SizeLimitThreshold = cs.Threshold
		resp.SizeLimitSmallLimit = cs.SmallLimit
		resp.SizeLimitLargeLimit = cs.LargeLimit
		resp.SizeLimitSmallActive = cs.SmallActive
		resp.SizeLimitLargeActive = cs.LargeActive
		resp.SizeLimitWaiting = cs.Waiting
		resp.SizeLimitTimedOut = cs.TimedOut
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// clientIP 提取客户端 IP，优先使用 X-Forwarded-For。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
