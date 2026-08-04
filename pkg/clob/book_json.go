package clob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// UnmarshalJSON 兼容 Hermes 当前订单簿响应与历史 OpenAPI 生成契约的字段差异。
//
// Hermes /book、/books 当前返回 RFC3339 字符串 timestamp，并将 min_order_size、
// tick_size 编码成字符串；旧契约则把三者声明为 JSON number。兼容必须在同一次响应
// 解码内完成，不能因为 wire 类型不同再次发起 HTTP 请求。
func (o *OrderBookSummary) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	if raw, ok := fields["timestamp"]; ok {
		normalized, err := normalizeBookTimestamp(raw)
		if err != nil {
			return fmt.Errorf("timestamp: %w", err)
		}
		fields["timestamp"] = normalized
	}
	for _, name := range []string{"min_order_size", "tick_size"} {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		normalized, err := normalizeBookFloat(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fields[name] = normalized
	}

	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type orderBookSummaryPlain OrderBookSummary
	var decoded orderBookSummaryPlain
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		return err
	}
	*o = OrderBookSummary(decoded)
	return nil
}

func normalizeBookTimestamp(raw json.RawMessage) (json.RawMessage, error) {
	value, present, err := bookScalarString(raw)
	if err != nil {
		return nil, err
	}
	if !present || value == "" {
		return json.RawMessage("null"), nil
	}

	if unixValue, err := strconv.ParseInt(value, 10, 64); err == nil {
		return bookTimestampNumber(unixValue)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fmt.Errorf("must be RFC3339 or integer Unix seconds/milliseconds: %w", err)
	}
	return bookTimestampNumber(parsed.UnixMilli())
}

func bookTimestampNumber(value int64) (json.RawMessage, error) {
	// generated.OrderBookSummary.Timestamp 当前是 int；显式检查避免 32 位目标静默溢出。
	if int64(int(value)) != value {
		return nil, fmt.Errorf("value %d overflows int", value)
	}
	return json.RawMessage(strconv.FormatInt(value, 10)), nil
}

func normalizeBookFloat(raw json.RawMessage) (json.RawMessage, error) {
	value, present, err := bookScalarString(raw)
	if err != nil {
		return nil, err
	}
	if !present || value == "" {
		return json.RawMessage("null"), nil
	}
	parsed, err := strconv.ParseFloat(value, 32)
	if err != nil {
		return nil, fmt.Errorf("must be a JSON number or numeric string: %w", err)
	}
	return json.RawMessage(strconv.FormatFloat(parsed, 'g', -1, 32)), nil
}

func bookScalarString(raw json.RawMessage) (value string, present bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return "", false, err
		}
		return value, true, nil
	}

	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", false, err
	}
	return number.String(), true, nil
}
