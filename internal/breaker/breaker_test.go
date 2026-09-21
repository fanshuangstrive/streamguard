package breaker

import (
	"testing"
	"time"
)

// TestBreaker_DisabledAlwaysAllows 未启用时恒放行。
func TestBreaker_DisabledAlwaysAllows(t *testing.T) {
	b := New(Options{Enabled: false, Threshold: 1, Cooldown: time.Millisecond})

	for i := 0; i < 10; i++ {
		b.Failure()
		if !b.Allow() {
			t.Fatalf("未启用时第 %d 次应放行", i+1)
		}
	}
	if b.State() != StateClosed {
		t.Errorf("未启用时状态应保持 closed，实际 %s", b.State())
	}
}

// TestBreaker_TripsAfterThreshold 连续失败达阈值后熔断。
func TestBreaker_TripsAfterThreshold(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 3, Cooldown: time.Second})

	// 前 2 次失败不熔断
	b.Failure()
	b.Failure()
	if !b.Allow() {
		t.Fatal("未达阈值时不应熔断")
	}

	// 第 3 次失败触发熔断
	b.Failure()
	if b.State() != StateOpen {
		t.Fatalf("达阈值后状态应为 open，实际 %s", b.State())
	}
	if b.Allow() {
		t.Fatal("熔断中应拒绝请求")
	}
}

// TestBreaker_SuccessResetsFailures 成功会重置连续失败计数。
func TestBreaker_SuccessResetsFailures(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 3, Cooldown: time.Second})

	b.Failure()
	b.Failure()
	b.Success() // 重置
	b.Failure()
	b.Failure()

	if b.State() != StateClosed {
		t.Fatalf("成功后失败计数应重置，状态应为 closed，实际 %s", b.State())
	}
	if !b.Allow() {
		t.Fatal("未达阈值时不应熔断")
	}
}

// TestBreaker_HalfOpenAfterCooldown 冷却结束后进入半开并放行试探请求。
func TestBreaker_HalfOpenAfterCooldown(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 1, Cooldown: 50 * time.Millisecond})

	b.Failure()
	if b.State() != StateOpen {
		t.Fatalf("应熔断，实际 %s", b.State())
	}

	// 冷却期内拒绝
	if b.Allow() {
		t.Fatal("冷却期内应拒绝")
	}

	time.Sleep(80 * time.Millisecond)

	// 冷却结束：放行一个试探请求
	if !b.Allow() {
		t.Fatal("冷却结束后应放行试探请求")
	}
	if b.State() != StateHalfOpen {
		t.Fatalf("应进入半开，实际 %s", b.State())
	}
	// 半开状态下第二个请求应被拒绝
	if b.Allow() {
		t.Fatal("半开状态下只应放行一个试探请求")
	}
}

// TestBreaker_HalfOpenSuccessCloses 半开试探成功则关闭熔断器。
func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 1, Cooldown: 20 * time.Millisecond})

	b.Failure()
	time.Sleep(40 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("冷却结束后应放行试探请求")
	}

	b.Success()
	if b.State() != StateClosed {
		t.Fatalf("试探成功后应关闭熔断器，实际 %s", b.State())
	}
	if !b.Allow() {
		t.Fatal("关闭后应放行")
	}
}

// TestBreaker_HalfOpenFailureReopens 半开试探失败则重新熔断。
func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 1, Cooldown: 20 * time.Millisecond})

	b.Failure()
	time.Sleep(40 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("冷却结束后应放行试探请求")
	}

	b.Failure()
	if b.State() != StateOpen {
		t.Fatalf("试探失败后应重新熔断，实际 %s", b.State())
	}
	if b.Allow() {
		t.Fatal("重新熔断后应拒绝")
	}
}

// TestBreaker_Stats 验证统计快照。
func TestBreaker_Stats(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 2, Cooldown: time.Second})

	b.Failure()
	b.Failure() // 触发熔断
	b.Allow()   // 被拒绝
	b.Allow()   // 被拒绝

	s := b.Stats()
	if !s.Enabled {
		t.Error("Enabled 应为 true")
	}
	if s.State != "open" {
		t.Errorf("State 应为 open，实际 %s", s.State)
	}
	if s.Trips != 1 {
		t.Errorf("Trips 应为 1，实际 %d", s.Trips)
	}
	if s.Rejected != 2 {
		t.Errorf("Rejected 应为 2，实际 %d", s.Rejected)
	}
	if s.Threshold != 2 {
		t.Errorf("Threshold 应为 2，实际 %d", s.Threshold)
	}
}

// TestBreaker_Concurrent 并发调用不产生竞态（配合 -count 多次运行）。
func TestBreaker_Concurrent(t *testing.T) {
	b := New(Options{Enabled: true, Threshold: 5, Cooldown: 10 * time.Millisecond})

	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				if b.Allow() {
					if (n+j)%3 == 0 {
						b.Failure()
					} else {
						b.Success()
					}
				}
			}
		}(i)
	}
	for i := 0; i < 20; i++ {
		<-done
	}

	// 只要求不 panic、状态合法
	st := b.State()
	if st != StateClosed && st != StateOpen && st != StateHalfOpen {
		t.Errorf("状态非法：%v", st)
	}
}
