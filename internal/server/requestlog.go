package server

import (
	"bufio"
	"net"
	"net/http"
	"time"
)

// RequestEvent 是一次代理请求的基础信息，用于向界面日志面板上报。
//
// 只包含方法、路径、最终响应状态码、总耗时与是否发生限流排队，
// **不含请求/响应头与 body**，符合「详细日志不进面板」的敏感信息红线。
type RequestEvent struct {
	Method   string        // 请求方法
	Path     string        // 请求路径（原样，不含 query）
	Status   int           // 客户端最终收到的响应状态码
	Elapsed  time.Duration // 从进入处理到响应写出的总耗时
	Waited   bool          // 是否因限流发生过排队（等待时长超过阈值）
	WaitTime time.Duration // 限流等待累计时长
}

// requestWaitMarkThreshold 是判定「本请求发生过排队」的最小等待时长。
//
// Waiter.Wait 在令牌充足时立即返回（微秒级），仅当真正排队才会阻塞；
// 用 1ms 阈值区分「瞬时放行」与「实际排队」，避免正常低负载时误标。
const requestWaitMarkThreshold = time.Millisecond

// onRequestFunc 是逐请求基础信息的回调签名。
type onRequestFunc func(RequestEvent)

// statusRecorder 是最轻量的 ResponseWriter 包装器：仅记录最终状态码，
// 其余（Write / Flush / 连接相关方法）全部透传给底层 writer。
//
// 关键约束（R6 SSE 实时性）：必须实现 http.Flusher 并透传 Flush，
// 同时提供 Unwrap / Hijack / CloseNotify 等透传，
// 使 ReverseProxy 与 http.NewResponseController 能拿到底层能力，
// 否则流式响应会被缓冲或降级。
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// newStatusRecorder 创建状态记录器，默认状态码 200（未显式 WriteHeader 时隐式为 200）。
func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	return &statusRecorder{ResponseWriter: w, status: http.StatusOK}
}

// WriteHeader 记录状态码后透传。仅首次生效（与 net/http 语义一致）。
func (s *statusRecorder) WriteHeader(status int) {
	if s.wroteHeader {
		return
	}
	s.wroteHeader = true
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// Write 在未显式写头时隐式记为 200，随后透传。
func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.wroteHeader = true
		// status 保持默认 200
	}
	return s.ResponseWriter.Write(b)
}

// Flush 透传底层 Flusher，保证 SSE 实时性不被破坏。
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 供 http.NewResponseController 穿透到底层 writer，
// 使 SetWriteDeadline / EnableFullDuplex 等能力不因包装而丢失。
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// Hijack 透传底层 Hijacker（WebSocket 等协议升级场景），不支持时返回错误。
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
