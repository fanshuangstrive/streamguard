package limiter

import (
	"encoding/json"
	"unicode/utf8"
)

// tokenBytesPerToken 是估算 token 数时使用的「每 token 字节数」。
//
// 说明：精确 token 数需要引入 tokenizer（如 tiktoken），会破坏本项目
// 「运行时零第三方依赖」的约束。因此这里采用保守的启发式估算：
//
//   - 英文约 4 字节/token，中文约 3 字节/token（UTF-8）
//   - 取 3 作为除数，对英文偏保守（高估），对中文接近准确
//
// 高估是安全方向：宁可把请求判为大请求（更严格限流），
// 也不要低估导致大请求挤占上游。
const tokenBytesPerToken = 3

// EstimateTokens 估算请求体的输入 token 数。
//
// 优先解析 OpenAI 兼容的 chat/completions 请求体，累加 messages 中
// 各条 content 的字节数；解析失败或结构不符时，退化为对整个请求体
// 做字节数估算。
//
// 返回值恒 >= 0。空请求体返回 0。
func EstimateTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}

	if n, ok := estimateFromChatBody(body); ok {
		return n
	}
	return estimateFromBytes(len(body))
}

// estimateFromChatBody 尝试从 chat/completions 请求体中提取文本长度。
//
// 支持 content 为字符串或数组（多模态）两种形态。
// 返回 ok=false 表示结构不符，调用方应退化为整体字节估算。
func estimateFromChatBody(body []byte) (int, bool) {
	var req struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return 0, false
	}
	if len(req.Messages) == 0 {
		return 0, false
	}

	total := 0
	for _, m := range req.Messages {
		total += contentBytes(m.Content)
	}
	if total == 0 {
		return 0, false
	}
	return estimateFromBytes(total), true
}

// contentBytes 返回单条 message 的 content 字节数。
//
// content 可能是：
//   - 字符串："hello"
//   - 数组（多模态）：[{"type":"text","text":"..."},{"type":"image_url",...}]
//   - null / 缺失
func contentBytes(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}

	// 形态一：字符串
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return len(s)
	}

	// 形态二：数组（多模态），只统计 text 部分。
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		n := 0
		for _, p := range parts {
			n += len(p.Text)
		}
		return n
	}

	return 0
}

// estimateFromBytes 按字节数估算 token 数（向上取整）。
func estimateFromBytes(n int) int {
	if n <= 0 {
		return 0
	}
	// 向上取整，避免小请求被估成 0。
	tokens := (n + tokenBytesPerToken - 1) / tokenBytesPerToken
	// 防御：极端情况下（如非法 UTF-8）保证至少 1。
	if tokens <= 0 {
		tokens = 1
	}
	return tokens
}

// CountRunes 返回字符串的字符数（用于调试与测试）。
func CountRunes(s string) int {
	return utf8.RuneCountInString(s)
}
