package limiter

import (
	"strings"
	"testing"
)

// TestEstimateTokens_EmptyBody 验证空请求体返回 0。
func TestEstimateTokens_EmptyBody(t *testing.T) {
	if n := EstimateTokens(nil); n != 0 {
		t.Errorf("expected 0 for nil body, got %d", n)
	}
	if n := EstimateTokens([]byte{}); n != 0 {
		t.Errorf("expected 0 for empty body, got %d", n)
	}
}

// TestEstimateTokens_ChatBody 验证从 chat/completions 请求体估算 token。
func TestEstimateTokens_ChatBody(t *testing.T) {
	// content 共 30 字节 → 30/3 = 10 token
	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"123456789012345678901234567890"}]}`)
	n := EstimateTokens(body)
	if n != 10 {
		t.Errorf("expected 10 tokens, got %d", n)
	}
}

// TestEstimateTokens_MultipleMessages 验证多条 message 累加。
func TestEstimateTokens_MultipleMessages(t *testing.T) {
	// 两条 content 各 15 字节 → 共 30 字节 → 10 token
	body := []byte(`{"messages":[{"role":"system","content":"123456789012345"},{"role":"user","content":"123456789012345"}]}`)
	n := EstimateTokens(body)
	if n != 10 {
		t.Errorf("expected 10 tokens, got %d", n)
	}
}

// TestEstimateTokens_MultimodalContent 验证多模态 content 数组只统计 text 部分。
func TestEstimateTokens_MultimodalContent(t *testing.T) {
	// text 部分 30 字节 → 10 token（image_url 不计入）
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"123456789012345678901234567890"},{"type":"image_url","image_url":{"url":"http://x/y.png"}}]}]}`)
	n := EstimateTokens(body)
	if n != 10 {
		t.Errorf("expected 10 tokens, got %d", n)
	}
}

// TestEstimateTokens_NonChatBodyFallsBack 验证非 chat 结构退化为整体字节估算。
func TestEstimateTokens_NonChatBodyFallsBack(t *testing.T) {
	// 无 messages 字段 → 退化为整体字节估算
	body := []byte(`{"prompt":"123456789012345678901234567890"}`)
	n := EstimateTokens(body)
	if n <= 0 {
		t.Errorf("expected positive estimate for fallback, got %d", n)
	}
	// 整体 43 字节 → 向上取整 15 token
	if n != 15 {
		t.Errorf("expected 15 tokens (43/3 rounded up), got %d", n)
	}
}

// TestEstimateTokens_InvalidJSONFallsBack 验证非法 JSON 退化为整体字节估算。
func TestEstimateTokens_InvalidJSONFallsBack(t *testing.T) {
	body := []byte("not a json at all")
	n := EstimateTokens(body)
	if n != 6 {
		t.Errorf("expected 6 tokens (18/3), got %d", n)
	}
}

// TestEstimateTokens_ChineseText 验证中文文本估算（UTF-8 3 字节/字）。
func TestEstimateTokens_ChineseText(t *testing.T) {
	// 10 个中文字符 = 30 字节 → 10 token
	text := strings.Repeat("中", 10)
	body := []byte(`{"messages":[{"role":"user","content":"` + text + `"}]}`)
	n := EstimateTokens(body)
	if n != 10 {
		t.Errorf("expected 10 tokens for 10 Chinese chars, got %d", n)
	}
}

// TestEstimateTokens_RoundsUp 验证向上取整，小请求不会被估成 0。
func TestEstimateTokens_RoundsUp(t *testing.T) {
	// content 1 字节 → 向上取整为 1 token
	body := []byte(`{"messages":[{"role":"user","content":"a"}]}`)
	n := EstimateTokens(body)
	if n != 1 {
		t.Errorf("expected 1 token (round up), got %d", n)
	}
}

// TestEstimateTokens_EmptyContentFallsBack 验证 content 为空时退化为整体估算。
func TestEstimateTokens_EmptyContentFallsBack(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":""}]}`)
	n := EstimateTokens(body)
	// content 为空 → 退化为整体字节估算（>0）
	if n <= 0 {
		t.Errorf("expected positive fallback estimate, got %d", n)
	}
}

// TestEstimateTokens_ThresholdScenario 验证用户场景：小请求 vs 大请求。
func TestEstimateTokens_ThresholdScenario(t *testing.T) {
	// 小请求：3000 字节 → 1000 token（< 10000）
	small := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("a", 3000) + `"}]}`)
	if n := EstimateTokens(small); n != 1000 {
		t.Errorf("expected 1000 tokens for small request, got %d", n)
	}

	// 大请求：33000 字节 → 11000 token（> 10000）
	large := []byte(`{"messages":[{"role":"user","content":"` + strings.Repeat("a", 33000) + `"}]}`)
	if n := EstimateTokens(large); n != 11000 {
		t.Errorf("expected 11000 tokens for large request, got %d", n)
	}
}

// TestCountRunes 验证字符计数辅助函数。
func TestCountRunes(t *testing.T) {
	if n := CountRunes("hello"); n != 5 {
		t.Errorf("expected 5, got %d", n)
	}
	if n := CountRunes("中文abc"); n != 5 {
		t.Errorf("expected 5, got %d", n)
	}
}
