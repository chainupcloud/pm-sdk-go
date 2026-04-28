package gamma

import (
	"strings"
	"time"
)

// jsonTime 兼容上游可能返回 RFC3339 / 不带时区 / 带毫秒等多种 ISO 8601 变体。
//
// 上游 gamma-service 的 *time.Time 字段在 JSON 中默认走 time.Time 的 RFC3339Nano；
// 但部分历史字段可能是 "2026-01-01T00:00:00" 无时区。统一在 SDK 层兜底解析。
type jsonTime struct {
	time.Time
}

func (jt *jsonTime) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" {
		return nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			jt.Time = t.UTC()
			return nil
		}
	}
	// 解析失败，置零；不返回 error 避免因单条字段坏数据拒整个响应。
	return nil
}
