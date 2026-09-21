package proxy

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProxy_SSEStreamingRealtime 验证 SSE 数据块被实时逐块透传，而非等响应结束后一次性返回。
//
// 这是「等待式限流代理」的关键契约：限流只影响请求进入上游的时机，
// 一旦上游开始流式返回，代理必须立即转发每个数据块，不能缓冲。
func TestProxy_SSEStreamingRealtime(t *testing.T) {
	const chunkDelay = 120 * time.Millisecond

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("上游 ResponseWriter 不支持 Flush")
			return
		}
		for _, c := range []string{"你", "好", "世", "界"} {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + c + "\"}}]}\n\n"))
			flusher.Flush()
			time.Sleep(chunkDelay)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	p, err := New(Options{Upstream: upstream.URL, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("创建代理失败：%v", err)
	}

	// 用真实 HTTP 服务器承载代理，才能观察流式时序。
	proxySrv := httptest.NewServer(p)
	defer proxySrv.Close()

	start := time.Now()
	resp, err := http.Post(proxySrv.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatalf("请求代理失败：%v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type 未透传：%s", ct)
	}

	// 逐行读取，记录每个 data 块到达的时间。
	scanner := bufio.NewScanner(resp.Body)
	var arrivals []time.Duration
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") && !strings.Contains(line, "[DONE]") {
			arrivals = append(arrivals, time.Since(start))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("读取流失败：%v", err)
	}

	if len(arrivals) != 4 {
		t.Fatalf("期望收到 4 个数据块，实际 %d", len(arrivals))
	}

	// 若代理做了缓冲，所有块会在同一时刻到达（间隔接近 0）。
	// 实时透传时，相邻块间隔应接近 chunkDelay。
	for i := 1; i < len(arrivals); i++ {
		gap := arrivals[i] - arrivals[i-1]
		if gap < chunkDelay/2 {
			t.Fatalf("第 %d 块与第 %d 块间隔仅 %v，疑似被缓冲（期望约 %v）",
				i, i+1, gap, chunkDelay)
		}
	}
}
