package server

import (
	"log"
	"net/http"
	"strings"
)

// verboseRecorder 包装 http.ResponseWriter，用于详细日志模式：
//   - 记录响应状态码与响应头
//   - 逐块打印响应体（SSE 场景下每收到一个数据块立即打印）
//
// 关键点：必须实现 http.Flusher 并透传 Flush 调用，
// 否则 SSE 流式响应会被缓冲，破坏实时性。
type verboseRecorder struct {
	http.ResponseWriter
	logger      *log.Logger
	wroteHeader bool
	status      int
	chunks      int
	bytes       int
}

// newVerboseRecorder 创建响应记录器。
func newVerboseRecorder(w http.ResponseWriter, logger *log.Logger) *verboseRecorder {
	return &verboseRecorder{ResponseWriter: w, logger: logger, status: http.StatusOK}
}

// WriteHeader 记录状态码与响应头，然后透传。
func (v *verboseRecorder) WriteHeader(status int) {
	if v.wroteHeader {
		return
	}
	v.wroteHeader = true
	v.status = status

	v.logger.Printf("[resp] <<< %d %s", status, http.StatusText(status))
	for k, vs := range v.Header() {
		for _, val := range vs {
			v.logger.Printf("[resp] %s: %s", k, val)
		}
	}
	v.ResponseWriter.WriteHeader(status)
}

// Write 打印响应体内容，然后透传。
//
// SSE 场景下 ReverseProxy 每收到一个数据块就调用一次 Write，
// 因此这里天然实现「逐块打印」。
func (v *verboseRecorder) Write(b []byte) (int, error) {
	if !v.wroteHeader {
		v.WriteHeader(http.StatusOK)
	}
	v.chunks++
	v.bytes += len(b)

	// 逐块打印，去掉尾部换行避免日志出现空行。
	text := strings.TrimRight(string(b), "\r\n")
	if text != "" {
		v.logger.Printf("[resp] chunk#%d (%d bytes): %s", v.chunks, len(b), text)
	}
	return v.ResponseWriter.Write(b)
}

// Flush 透传 Flush，保证 SSE 实时性不被破坏。
func (v *verboseRecorder) Flush() {
	if f, ok := v.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish 打印响应汇总。
func (v *verboseRecorder) finish() {
	v.logger.Printf("[resp] <<< done: status=%d chunks=%d bytes=%d", v.status, v.chunks, v.bytes)
}
