package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrencyLimiter_SmallRequestImmediate 验证小请求在有空闲槽位时立即放行。
func TestConcurrencyLimiter_SmallRequestImmediate(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 1,
		MaxWait:    time.Second,
	})

	release, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	release()

	st := c.Stats()
	if st.Small != 1 {
		t.Errorf("expected 1 small request, got %d", st.Small)
	}
	if st.SmallActive != 0 {
		t.Errorf("expected 0 active after release, got %d", st.SmallActive)
	}
}

// TestConcurrencyLimiter_ThresholdBoundary 验证阈值边界：等于阈值算小请求，大于算大请求。
func TestConcurrencyLimiter_ThresholdBoundary(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 5,
		LargeLimit: 5,
		MaxWait:    time.Second,
	})

	// 恰好等于阈值 → 小请求
	r1, _ := c.Acquire(context.Background(), 10000)
	defer r1()
	// 阈值 +1 → 大请求
	r2, _ := c.Acquire(context.Background(), 10001)
	defer r2()

	st := c.Stats()
	if st.Small != 1 {
		t.Errorf("expected 1 small (==threshold), got %d", st.Small)
	}
	if st.Large != 1 {
		t.Errorf("expected 1 large (>threshold), got %d", st.Large)
	}
}

// TestConcurrencyLimiter_SmallLimitEnforced 验证小请求并发上限被强制执行。
func TestConcurrencyLimiter_SmallLimitEnforced(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 1,
		MaxWait:    2 * time.Second,
	})

	// 占满 2 个小请求槽位
	r1, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire 1 failed: %v", err)
	}
	r2, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire 2 failed: %v", err)
	}

	// 第 3 个小请求应阻塞
	done := make(chan struct{})
	go func() {
		r3, err := c.Acquire(context.Background(), 100)
		if err != nil {
			t.Errorf("Acquire 3 failed: %v", err)
		} else {
			r3()
		}
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("third small request should have blocked")
	case <-time.After(150 * time.Millisecond):
		// 预期：仍在阻塞
	}

	// 释放一个槽位后，第 3 个应被唤醒
	r1()
	select {
	case <-done:
		// 预期：成功
	case <-time.After(time.Second):
		t.Fatal("third small request should have been released")
	}
	r2()
}

// TestConcurrencyLimiter_LargeLimitEnforced 验证大请求并发上限独立于小请求。
func TestConcurrencyLimiter_LargeLimitEnforced(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 1,
		MaxWait:    2 * time.Second,
	})

	// 占满 1 个大请求槽位
	rl, err := c.Acquire(context.Background(), 50000)
	if err != nil {
		t.Fatalf("Acquire large failed: %v", err)
	}

	// 小请求不受大请求影响，应立即通过
	rs, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("small request should not be blocked by large: %v", err)
	}
	rs()

	// 第 2 个大请求应阻塞
	done := make(chan struct{})
	go func() {
		r2, err := c.Acquire(context.Background(), 50000)
		if err != nil {
			t.Errorf("Acquire large 2 failed: %v", err)
		} else {
			r2()
		}
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("second large request should have blocked")
	case <-time.After(150 * time.Millisecond):
	}

	rl()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second large request should have been released")
	}
}

// TestConcurrencyLimiter_UnlimitedWhenZero 验证上限为 0 时不限制并发。
func TestConcurrencyLimiter_UnlimitedWhenZero(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 0, // 不限
		LargeLimit: 0, // 不限
		MaxWait:    time.Second,
	})

	releases := make([]func(), 0, 50)
	for i := 0; i < 50; i++ {
		r, err := c.Acquire(context.Background(), 999999)
		if err != nil {
			t.Fatalf("Acquire %d should not block when unlimited: %v", i, err)
		}
		releases = append(releases, r)
	}
	for _, r := range releases {
		r()
	}

	st := c.Stats()
	if st.LargeActive != 0 {
		t.Errorf("expected 0 active after all releases, got %d", st.LargeActive)
	}
}

// TestConcurrencyLimiter_MaxWaitTimeout 验证等待超过 maxWait 返回 ErrWaitTimeout。
func TestConcurrencyLimiter_MaxWaitTimeout(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 1,
		LargeLimit: 1,
		MaxWait:    100 * time.Millisecond,
	})

	r1, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire 1 failed: %v", err)
	}
	defer r1()

	start := time.Now()
	_, err = c.Acquire(context.Background(), 100)
	elapsed := time.Since(start)

	if err != ErrWaitTimeout {
		t.Fatalf("expected ErrWaitTimeout, got %v", err)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("expected to wait ~100ms, got %v", elapsed)
	}

	st := c.Stats()
	if st.TimedOut != 1 {
		t.Errorf("expected 1 timeout, got %d", st.TimedOut)
	}
}

// TestConcurrencyLimiter_ContextCancel 验证 ctx 取消时返回 ctx 错误。
func TestConcurrencyLimiter_ContextCancel(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 1,
		LargeLimit: 1,
		MaxWait:    5 * time.Second,
	})

	r1, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire 1 failed: %v", err)
	}
	defer r1()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err = c.Acquire(ctx, 100)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	st := c.Stats()
	if st.Canceled != 1 {
		t.Errorf("expected 1 canceled, got %d", st.Canceled)
	}
}

// TestConcurrencyLimiter_FIFOOrdering 验证等待者按 FIFO 顺序获得槽位。
func TestConcurrencyLimiter_FIFOOrdering(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 1,
		LargeLimit: 1,
		MaxWait:    5 * time.Second,
	})

	// 占满唯一槽位
	r0, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire 0 failed: %v", err)
	}

	const n = 5
	var order []int
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r, err := c.Acquire(context.Background(), 100)
			if err != nil {
				t.Errorf("Acquire %d failed: %v", idx, err)
				return
			}
			mu.Lock()
			order = append(order, idx)
			mu.Unlock()
			r()
		}(i)
		// 保证 goroutine 按顺序进入等待队列
		time.Sleep(20 * time.Millisecond)
	}

	r0()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != n {
		t.Fatalf("expected %d completions, got %d", n, len(order))
	}
	for i := 0; i < n; i++ {
		if order[i] != i {
			t.Errorf("FIFO violated: expected order[%d]=%d, got %d (full: %v)", i, i, order[i], order)
			break
		}
	}
}

// TestConcurrencyLimiter_ReleaseIdempotent 验证重复调用 release 不会导致计数错误。
func TestConcurrencyLimiter_ReleaseIdempotent(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 1,
		MaxWait:    time.Second,
	})

	r, err := c.Acquire(context.Background(), 100)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	r()
	r() // 重复释放应被忽略
	r()

	st := c.Stats()
	if st.SmallActive != 0 {
		t.Errorf("expected 0 active after idempotent release, got %d", st.SmallActive)
	}
}

// TestConcurrencyLimiter_NoSlotLeakUnderContention 验证高并发下无槽位泄漏。
func TestConcurrencyLimiter_NoSlotLeakUnderContention(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 3,
		LargeLimit: 2,
		MaxWait:    10 * time.Second,
	})

	const goroutines = 40
	var wg sync.WaitGroup
	var maxSmall, maxLarge int64

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tokens := 100
			if idx%2 == 0 {
				tokens = 50000
			}
			r, err := c.Acquire(context.Background(), tokens)
			if err != nil {
				t.Errorf("Acquire %d failed: %v", idx, err)
				return
			}
			// 记录峰值在途数，验证未超限
			st := c.Stats()
			if int64(st.SmallActive) > atomic.LoadInt64(&maxSmall) {
				atomic.StoreInt64(&maxSmall, int64(st.SmallActive))
			}
			if int64(st.LargeActive) > atomic.LoadInt64(&maxLarge) {
				atomic.StoreInt64(&maxLarge, int64(st.LargeActive))
			}
			time.Sleep(time.Millisecond)
			r()
		}(i)
	}
	wg.Wait()

	st := c.Stats()
	if st.SmallActive != 0 || st.LargeActive != 0 {
		t.Errorf("slot leak: small=%d large=%d", st.SmallActive, st.LargeActive)
	}
	if st.Total != goroutines {
		t.Errorf("expected total=%d, got %d", goroutines, st.Total)
	}
	if maxSmall > 3 {
		t.Errorf("small concurrency exceeded limit: peak=%d limit=3", maxSmall)
	}
	if maxLarge > 2 {
		t.Errorf("large concurrency exceeded limit: peak=%d limit=2", maxLarge)
	}
}

// TestConcurrencyLimiter_StatsCounts 验证统计计数正确。
func TestConcurrencyLimiter_StatsCounts(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  10000,
		SmallLimit: 2,
		LargeLimit: 2,
		MaxWait:    time.Second,
	})

	r1, _ := c.Acquire(context.Background(), 100)
	r1()
	r2, _ := c.Acquire(context.Background(), 200)
	r2()
	r3, _ := c.Acquire(context.Background(), 50000)
	r3()

	st := c.Stats()
	if st.Total != 3 {
		t.Errorf("expected total=3, got %d", st.Total)
	}
	if st.Small != 2 {
		t.Errorf("expected small=2, got %d", st.Small)
	}
	if st.Large != 1 {
		t.Errorf("expected large=1, got %d", st.Large)
	}
	if st.Threshold != 10000 {
		t.Errorf("expected threshold=10000, got %d", st.Threshold)
	}
	if st.SmallLimit != 2 || st.LargeLimit != 2 {
		t.Errorf("expected limits 2/2, got %d/%d", st.SmallLimit, st.LargeLimit)
	}
}

// TestConcurrencyLimiter_DefaultThreshold 验证阈值 <=0 时规整为 10000。
func TestConcurrencyLimiter_DefaultThreshold(t *testing.T) {
	c := NewConcurrencyLimiter(ConcurrencyOptions{
		Threshold:  0,
		SmallLimit: 1,
		LargeLimit: 1,
		MaxWait:    0,
	})
	st := c.Stats()
	if st.Threshold != 10000 {
		t.Errorf("expected default threshold 10000, got %d", st.Threshold)
	}
}
