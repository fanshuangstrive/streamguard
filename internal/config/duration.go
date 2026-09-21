package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration 是对 time.Duration 的包装，支持在 JSON 中以字符串形式表示。
//
// 背景：Go 标准库的 time.Duration 在 JSON 中默认按纳秒整数解析，
// 无法识别 "2s"、"500ms" 这类人类可读的字符串。本类型通过实现
// json.Unmarshaler / json.Marshaler 解决该问题，同时兼容纯数字输入。
type Duration time.Duration

// UnmarshalJSON 支持两种输入格式：
//   - 字符串："2s"、"500ms"、"1m30s"
//   - 数字：纳秒数（兼容旧格式）
func (d *Duration) UnmarshalJSON(data []byte) error {
	// 尝试按字符串解析
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		if s == "" {
			*d = 0
			return nil
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("failed to parse duration %q: %w", s, err)
		}
		*d = Duration(parsed)
		return nil
	}

	// 回退为数字（纳秒）
	var n int64
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("duration field must be a string or number: %s", string(data))
	}
	*d = Duration(n)
	return nil
}

// MarshalJSON 将时长序列化为字符串形式，便于人类阅读。
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// Duration 返回标准库的 time.Duration。
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}
