package limiter

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestWaiter_ImmediateWhenAvailable 验证有配额时立即放行，不产生额外延迟。
func TestWaiter_ImmediateWhenAvailable(t *testing.T) {
	w := NewWaiter(WaiterOptions{
		Rate:    10, // 10 QPS
		Burst:   10,
		MaxWait: time.Second,
	})

	start := time.Now()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("有配额时应立即放行，实际错误：%v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("有配额时不应有明显延迟，实际 %v", elapsed)
	}
}

// TestWaiter_BlocksWhenExhausted 验证配额耗尽时阻塞等待，而非立即拒绝。
func TestWaiter_BlocksWhenExhausted(t *testing.T) {
	// 2 QPS，突发 1：第 1 次立即通过，第 2 次需等待约 500ms
	w := NewWaiter(WaiterOptions{
		Rate:    2,
		Burst:   1,
		MaxWait: 2 * time.Second,
	})

	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}

	start := time.Now()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 2 次请求应等待后放行：%v", err)
	}
	elapsed := time.Since(start)

	// 2 QPS 即每 500ms 一个令牌，等待时间应在 400ms~700ms 之间
	if elapsed < 400*time.Millisecond {
		t.Fatalf("第 2 次请求应等待约 500ms，实际仅 %v", elapsed)
	}
	if elapsed > 700*time.Millisecond {
		t.Fatalf("第 2 次请求等待过久：%v", elapsed)
	}
}

// TestWaiter_OnePerSecond 验证「1 秒 1 次」的典型场景。
func TestWaiter_OnePerSecond(t *testing.T) {
	w := NewWaiter(WaiterOptions{
		Rate:    1,
		Burst:   1,
		MaxWait: 3 * time.Second,
	})

	// 第 1 次立即通过
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}

	// 第 2 次应等待约 1 秒
	start := time.Now()
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 2 次请求应等待后放行：%v", err)
	}
	elapsed := time.Since(start)

	if elapsed < 900*time.Millisecond {
		t.Fatalf("1 QPS 下第 2 次请求应等待约 1s，实际 %v", elapsed)
	}
	if elapsed > 1200*time.Millisecond {
		t.Fatalf("1 QPS 下第 2 次请求等待过久：%v", elapsed)
	}
}

// TestWaiter_MaxWaitTimeout 验证超过最大等待时间后返回错误。
func TestWaiter_MaxWaitTimeout(t *testing.T) {
	// 1 QPS，最大等待 200ms：第 2 次请求必然超时
	w := NewWaiter(WaiterOptions{
		Rate:    1,
		Burst:   1,
		MaxWait: 200 * time.Millisecond,
	})

	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}

	start := time.Now()
	err := w.Wait(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("超过最大等待时间应返回错误")
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("应在约 200ms 后超时返回，实际 %v", elapsed)
	}
}

// TestWaiter_ContextCancel 验证 context 取消时立即返回错误。
func TestWaiter_ContextCancel(t *testing.T) {
	w := NewWaiter(WaiterOptions{
		Rate:    1,
		Burst:   1,
		MaxWait: 10 * time.Second,
	})

	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := w.Wait(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("context 取消后应返回错误")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("context 取消后应立即返回，实际 %v", elapsed)
	}
}

// TestWaiter_ConcurrentOrdering 验证并发请求按序获得令牌，总耗时符合速率。
func TestWaiter_ConcurrentOrdering(t *testing.T) {
	const n = 5
	// 10 QPS，突发 1：5 个请求需约 400ms
	w := NewWaiter(WaiterOptions{
		Rate:    10,
		Burst:   1,
		MaxWait: 5 * time.Second,
	})

	var wg sync.WaitGroup
	start := time.Now()

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Wait(context.Background())
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 10 QPS 下 5 个请求（突发 1）约需 400ms
	if elapsed < 300*time.Millisecond {
		t.Fatalf("并发请求应受速率限制，实际仅 %v", elapsed)
	}
	if elapsed > 800*time.Millisecond {
		t.Fatalf("并发请求耗时过长：%v", elapsed)
	}
}

// TestWaiter_InvalidParams 验证非法参数被规整。
func TestWaiter_InvalidParams(t *testing.T) {
	w := NewWaiter(WaiterOptions{Rate: 0, Burst: 0, MaxWait: 0})
	if w == nil {
		t.Fatal("非法参数不应返回 nil")
	}
	if w.rate <= 0 {
		t.Fatalf("rate 应被规整为正数，实际 %v", w.rate)
	}
	if w.burst < 1 {
		t.Fatalf("burst 应被规整为 >=1，实际 %d", w.burst)
	}
	if w.maxWait <= 0 {
		t.Fatalf("maxWait 应被规整为正数，实际 %v", w.maxWait)
	}
}

// TestWaiter_Stats 验证统计信息。
func TestWaiter_Stats(t *testing.T) {
	w := NewWaiter(WaiterOptions{Rate: 100, Burst: 10, MaxWait: time.Second})

	_ = w.Wait(context.Background())
	_ = w.Wait(context.Background())

	s := w.Stats()
	if s.Total != 2 {
		t.Fatalf("总请求数应为 2，实际 %d", s.Total)
	}
	if s.Waited != 0 {
		t.Fatalf("有突发配额时不应产生等待，实际等待 %d 次", s.Waited)
	}
}

// TestWaiter_StatsNoDoubleCountUnderContention 验证高并发下统计不重复计数。
//
// 回归：此前令牌被抢占时递归调用 Wait，导致 total/waited 被重复累加
// （20 个请求可能统计出 39 次）。改为循环重试后应精确等于请求数。
func TestWaiter_StatsNoDoubleCountUnderContention(t *testing.T) {
	const n = 20
	w := NewWaiter(WaiterOptions{Rate: 100, Burst: 1, MaxWait: 5 * time.Second})

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = w.Wait(context.Background())
		}()
	}
	wg.Wait()

	s := w.Stats()
	if s.Total != n {
		t.Fatalf("总请求数应精确为 %d，实际 %d（统计重复计数）", n, s.Total)
	}
	if s.Waited > n {
		t.Fatalf("等待数不应超过请求数 %d，实际 %d", n, s.Waited)
	}
}

// TestWaiter_StatsWaitDuration 验证等待耗时统计（avg/max/min）。
func TestWaiter_StatsWaitDuration(t *testing.T) {
	// 2 QPS，突发 1：第 1 次立即通过，第 2 次等待约 500ms
	w := NewWaiter(WaiterOptions{Rate: 2, Burst: 1, MaxWait: 3 * time.Second})

	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 2 次请求应等待后放行：%v", err)
	}

	s := w.Stats()
	if s.Waited != 1 {
		t.Fatalf("应有 1 次等待，实际 %d", s.Waited)
	}
	if s.AvgWait < 400*time.Millisecond || s.AvgWait > 700*time.Millisecond {
		t.Errorf("平均等待应约 500ms，实际 %v", s.AvgWait)
	}
	if s.MaxWait < 400*time.Millisecond {
		t.Errorf("最大等待应约 500ms，实际 %v", s.MaxWait)
	}
	if s.MinWait != s.MaxWait {
		t.Errorf("仅一次等待时 min 应等于 max，实际 min=%v max=%v", s.MinWait, s.MaxWait)
	}
}

// TestWaiter_StatsTimedOut 验证等待超时计数。
func TestWaiter_StatsTimedOut(t *testing.T) {
	// 1 QPS，突发 1，最大等待 100ms：第 2 次请求需等 1s，必然超时
	w := NewWaiter(WaiterOptions{Rate: 1, Burst: 1, MaxWait: 100 * time.Millisecond})

	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}
	if err := w.Wait(context.Background()); err != ErrWaitTimeout {
		t.Fatalf("第 2 次请求应超时，实际 %v", err)
	}

	s := w.Stats()
	if s.TimedOut != 1 {
		t.Errorf("超时计数应为 1，实际 %d", s.TimedOut)
	}
	if s.Canceled != 0 {
		t.Errorf("取消计数应为 0，实际 %d", s.Canceled)
	}
}

// TestWaiter_StatsCanceled 验证等待期间取消计数。
func TestWaiter_StatsCanceled(t *testing.T) {
	// 1 QPS，突发 1：第 2 次请求需等待，期间取消 context
	w := NewWaiter(WaiterOptions{Rate: 1, Burst: 1, MaxWait: 5 * time.Second})

	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("第 1 次请求应放行：%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if err := w.Wait(ctx); err == nil {
		t.Fatal("取消后应返回错误")
	}

	s := w.Stats()
	if s.Canceled != 1 {
		t.Errorf("取消计数应为 1，实际 %d", s.Canceled)
	}
	if s.TimedOut != 0 {
		t.Errorf("超时计数应为 0，实际 %d", s.TimedOut)
	}
}
