// Package proxy 实现到上游模型服务的反向代理，重点保证 SSE 流式响应逐块透传。
//
// 设计要点：
//   - 使用 httputil.ReverseProxy 转发请求，自动透传 header 与请求体
//   - 请求路径原样拼接到上游地址，不做任何改写
//   - 关闭响应缓冲（FlushInterval = -1），确保 SSE 数据实时到达客户端
//   - 上游不可达时返回标准 502，避免客户端长时间挂起
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/streamguard/streamguard/internal/breaker"
	"github.com/streamguard/streamguard/internal/retry"
)

// Options 是代理的构造参数。
type Options struct {
	// Upstream 是上游模型服务地址，例如 http://127.0.0.1:11434。
	// 请求路径会原样拼接到该地址后转发。
	Upstream string
	// Timeout 是转发到上游的请求超时。SSE 长连接场景需设置较大值。
	Timeout time.Duration
	// PreserveHost 为 true 时保留客户端原始 Host 头，不改写为上游主机名。
	// 部分上游网关（如 Kong）依赖 Host 做路由，此时需要开启。
	PreserveHost bool
	// Breaker 是熔断器，可选。为 nil 时不启用熔断。
	Breaker *breaker.Breaker
	// Retry 是上游限流重试策略，可选。为 nil 时不启用重试。
	Retry *retry.Policy
}

// Proxy 是到上游模型服务的反向代理。
//
// 同时支持 SSE 流式（text/event-stream）与普通 HTTP JSON 响应：
// 通过 FlushInterval = -1 关闭缓冲，SSE 数据实时透传；
// 普通响应则按 Content-Length 正常返回，无需额外配置。
type Proxy struct {
	reverseProxy *httputil.ReverseProxy
	upstream     *url.URL
	breaker      *breaker.Breaker
	retry        *retry.Policy
	timeout      time.Duration
}

// New 创建反向代理。
//
// 上游地址非法时返回错误，避免运行时才暴露配置问题。
func New(opts Options) (*Proxy, error) {
	if opts.Upstream == "" {
		return nil, fmt.Errorf("upstream address must not be empty")
	}

	target, err := url.Parse(opts.Upstream)
	if err != nil {
		return nil, fmt.Errorf("failed to parse upstream address: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("upstream address must use http or https scheme, got: %s", opts.Upstream)
	}
	if target.Host == "" {
		return nil, fmt.Errorf("upstream address is missing host: %s", opts.Upstream)
	}

	if opts.Timeout <= 0 {
		opts.Timeout = 120 * time.Second
	}

	rp := httputil.NewSingleHostReverseProxy(target)

	// 关键：FlushInterval = -1 表示立即刷新，不做任何缓冲。
	// 这是 SSE 流式透传的核心配置，否则数据会被缓冲导致客户端收不到实时输出。
	// 对普通 JSON 响应无副作用。
	rp.FlushInterval = -1

	// 配置上游请求超时与连接池，避免 Windows 下长连接卡死。
	//
	// 注意：此处不设置 ResponseHeaderTimeout，避免长 SSE 流被中断。
	// 整体超时改由 ServeHTTP 按请求设置（非 SSE 请求应用 Timeout，SSE 豁免）。
	rp.Transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// 自定义错误处理：上游不可达时返回标准 502，并记录熔断失败。
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if opts.Breaker != nil {
			opts.Breaker.Failure()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprintf(w,
			`{"error":{"message":"upstream unreachable: %s","type":"upstream_error"}}`,
			err.Error())
	}

	// 记录上游响应结果，用于熔断器状态迁移：
	//   - 5xx 视为上游故障（Failure）
	//   - 其余（含 4xx，属客户端问题）视为上游可用（Success）
	rp.ModifyResponse = func(resp *http.Response) error {
		if opts.Breaker != nil {
			if resp.StatusCode >= 500 {
				opts.Breaker.Failure()
			} else {
				opts.Breaker.Success()
			}
		}
		return nil
	}

	// 自定义 Director：在默认改写基础上补充转发信息。
	//
	// 注意：Director 会被并发调用，因此不能使用闭包外的共享变量保存状态，
	// 所有状态必须从当前请求 r 中读取。
	originalDirector := rp.Director
	rp.Director = func(r *http.Request) {
		// 默认 Director 会把 Host 改写为上游主机名，这里先记下客户端原始 Host。
		clientHost := r.Host
		originalDirector(r)
		// 注意：不手动设置 X-Forwarded-For。
		// 标准库 ReverseProxy.ServeHTTP 会在 Director 之后自动追加客户端 IP，
		// 若此处再设置一次，上游会收到重复值（如 "1.2.3.4:5678, 1.2.3.4"）。
		// Host 头处理：
		//   - 默认（PreserveHost=false）：改写为上游主机名。
		//     注意 NewSingleHostReverseProxy 只设置 r.URL.Host，不会改 r.Host，
		//     而 Go 的 http.Transport 发送时以 r.Host 为准，因此必须显式设置。
		//   - PreserveHost=true：保留客户端原始 Host，供依赖 Host 路由的网关使用。
		if opts.PreserveHost && clientHost != "" {
			r.Host = clientHost
		} else {
			r.Host = target.Host
		}
	}

	p := &Proxy{
		reverseProxy: rp,
		upstream:     target,
		breaker:      opts.Breaker,
		retry:        opts.Retry,
		timeout:      opts.Timeout,
	}

	return p, nil
}

// ServeHTTP 实现 http.Handler，将请求转发到上游。
//
// 熔断器开启时，若上游处于熔断状态则直接返回 503，不发起上游请求。
// 重试开启时，上游返回 429/503 会按策略等待后重试（仅限响应头阶段）。
//
// 超时策略：对非 SSE 请求设置整体超时（Timeout），避免上游挂起导致请求无限等待；
// SSE 流式请求豁免超时，保证长连接不被中断。
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.breaker != nil && !p.breaker.Allow() {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w,
			`{"error":{"message":"upstream circuit breaker is open, please retry later","type":"upstream_unavailable"}}`)
		return
	}

	// 非 SSE 请求应用整体超时；SSE 请求豁免（长连接可能远超 Timeout）。
	if p.timeout > 0 && !isSSERequest(r) {
		ctx, cancel := context.WithTimeout(r.Context(), p.timeout)
		defer cancel()
		r = r.WithContext(ctx)
	}

	if !p.retry.Enabled() {
		p.reverseProxy.ServeHTTP(w, r)
		return
	}

	p.serveWithRetry(w, r)
}

// isSSERequest 判断请求是否为 SSE 流式请求。
//
// 判定依据（任一满足即视为 SSE）：
//   - 请求头 Accept 含 text/event-stream
//   - 请求体 JSON 中 stream=true（OpenAI 兼容接口的流式开关）
//
// 请求体判定需要读取 body，因此仅在必要时（Accept 未命中）才读取，
// 读取后立即还原 body，不影响后续转发。
func isSSERequest(r *http.Request) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	// 仅对可能携带 JSON body 的方法做进一步判定。
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch {
		return false
	}
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	// 优先用 GetBody 读取，避免消耗原始 body。
	if r.GetBody != nil {
		body, err := r.GetBody()
		if err != nil {
			return false
		}
		defer body.Close()
		return bodyHasStreamTrue(body)
	}
	// 无 GetBody：读取后还原。
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	r.ContentLength = int64(len(bodyBytes))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}
	return bodyHasStreamTrue(bytes.NewReader(bodyBytes))
}

// bodyHasStreamTrue 判断 JSON 请求体中 stream 字段是否为 true。
//
// 采用宽松解析：只关心顶层 stream 布尔字段，解析失败一律视为非流式。
func bodyHasStreamTrue(r io.Reader) bool {
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.NewDecoder(r).Decode(&payload); err != nil {
		return false
	}
	return payload.Stream
}

// serveWithRetry 在响应头阶段对可重试状态码进行等待重试。
//
// 关键约束：
//   - 只在响应头到达、body 尚未写入客户端时重试，避免破坏 SSE 流
//   - 请求体需可重放：优先用 GetBody，否则提前缓冲到内存
//   - 重试耗尽后透传最后一次响应
func (p *Proxy) serveWithRetry(w http.ResponseWriter, r *http.Request) {
	// 准备可重放的请求体。
	// 优先使用 GetBody（标准库 server 会自动设置）；缺失时手动缓冲。
	if r.Body != nil && r.Body != http.NoBody && r.GetBody == nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			// 读取失败则无法重试，直接转发。
			p.reverseProxy.ServeHTTP(w, r)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		r.ContentLength = int64(len(bodyBytes))
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	maxAttempts := p.retry.MaxAttempts()
	for attempt := 1; ; attempt++ {
		// 每次尝试前重置请求体（首次无需重置）。
		if attempt > 1 && r.GetBody != nil {
			body, err := r.GetBody()
			if err != nil {
				p.reverseProxy.ServeHTTP(w, r)
				return
			}
			r.Body = body
		}

		// 用缓冲响应器捕获上游响应头，决定是否重试。
		bw := newBufferedResponseWriter()
		p.reverseProxy.ServeHTTP(bw, r)

		status := bw.statusCode()
		canRetry := attempt < maxAttempts && p.retry.ShouldRetry(status)
		if !canRetry {
			bw.flushTo(w)
			return
		}

		// 计算等待时长（优先 Retry-After，其次指数退避）。
		wait := p.retry.Wait(attempt, bw.response())
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-r.Context().Done():
				// 客户端已取消，透传最后一次响应后返回。
				bw.flushTo(w)
				return
			}
		}
	}
}

// Breaker 返回熔断器，未启用时返回 nil。
func (p *Proxy) Breaker() *breaker.Breaker {
	return p.breaker
}

// Retry 返回重试策略，未启用时返回 nil。
func (p *Proxy) Retry() *retry.Policy {
	return p.retry
}

// Upstream 返回上游服务地址。
func (p *Proxy) Upstream() string {
	return p.upstream.String()
}
