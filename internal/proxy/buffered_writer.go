package proxy

import (
	"bytes"
	"net/http"
)

// bufferedResponseWriter 缓冲上游响应，用于在响应头阶段决定是否重试。
//
// 设计要点：
//   - 捕获状态码、响应头与响应体，不立即写入客户端
//   - 实现 http.Flusher，避免 ReverseProxy 因缺少 Flusher 而报错
//   - 重试决策完成后通过 flushTo 一次性写入真实 ResponseWriter
//
// 注意：仅在启用重试时使用。由于需要完整缓冲响应体，
// 对超大响应会占用内存；但重试场景下响应通常较小（429/503 错误体）。
type bufferedResponseWriter struct {
	header    http.Header
	body      bytes.Buffer
	status    int
	wroteHead bool
}

// newBufferedResponseWriter 创建缓冲响应器。
func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header: make(http.Header),
		status: http.StatusOK,
	}
}

// Header 返回响应头集合。
func (b *bufferedResponseWriter) Header() http.Header {
	return b.header
}

// WriteHeader 记录状态码，不立即写出。
func (b *bufferedResponseWriter) WriteHeader(status int) {
	if b.wroteHead {
		return
	}
	b.status = status
	b.wroteHead = true
}

// Write 缓冲响应体。
func (b *bufferedResponseWriter) Write(p []byte) (int, error) {
	if !b.wroteHead {
		b.wroteHead = true
	}
	return b.body.Write(p)
}

// Flush 实现 http.Flusher。
//
// 缓冲阶段无需真正刷新，仅保证 ReverseProxy 认为底层支持 Flush，
// 避免其因类型断言失败而走降级路径。
func (b *bufferedResponseWriter) Flush() {}

// statusCode 返回已记录的状态码。
func (b *bufferedResponseWriter) statusCode() int {
	return b.status
}

// response 构造一个仅含响应头的 http.Response，供重试策略读取 Retry-After。
func (b *bufferedResponseWriter) response() *http.Response {
	return &http.Response{
		StatusCode: b.status,
		Header:     b.header,
	}
}

// flushTo 将缓冲的状态码、响应头与响应体写入真实 ResponseWriter。
func (b *bufferedResponseWriter) flushTo(w http.ResponseWriter) {
	dst := w.Header()
	for k, vs := range b.header {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	w.WriteHeader(b.status)
	if b.body.Len() > 0 {
		_, _ = w.Write(b.body.Bytes())
	}
}
