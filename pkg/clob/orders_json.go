package clob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const unixMillisecondsThreshold = int64(100_000_000_000)

// decodeListOrdersResponse 在 Facade 层兼容 created_at 的实际 wire 形态，避免修改生成代码。
// data 和 next_cursor 是完整订单快照的必需字段；缺失或 null 不能当作空集合。
func decodeListOrdersResponse(body []byte) ([]SdkOrder, string, error) {
	var response struct {
		Data       *json.RawMessage `json:"data"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, "", err
	}
	if response.Data == nil || response.NextCursor == nil {
		return nil, "", fmt.Errorf("incomplete OrdersResponse: data and next_cursor required")
	}

	var rawOrders []json.RawMessage
	if err := json.Unmarshal(*response.Data, &rawOrders); err != nil {
		return nil, "", fmt.Errorf("decode OrdersResponse data: %w", err)
	}

	orders := make([]SdkOrder, 0, len(rawOrders))
	for i, rawOrder := range rawOrders {
		order, createdAt, err := decodeListOrder(rawOrder)
		if err != nil {
			return nil, "", fmt.Errorf("decode OrdersResponse data[%d]: %w", i, err)
		}
		sdkOrder := openOrderToSDK(&order)
		if !createdAt.IsZero() {
			sdkOrder.CreatedAt = createdAt
			sdkOrder.UpdatedAt = createdAt
		}
		orders = append(orders, *sdkOrder)
	}

	return orders, *response.NextCursor, nil
}

func decodeListOrder(raw json.RawMessage) (OpenOrder, time.Time, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return OpenOrder{}, time.Time{}, err
	}
	if fields == nil {
		return OpenOrder{}, time.Time{}, fmt.Errorf("order must be a JSON object")
	}

	createdAt, err := parseListOrderCreatedAt(fields["created_at"])
	if err != nil {
		return OpenOrder{}, time.Time{}, fmt.Errorf("created_at: %w", err)
	}
	// generated.OpenOrder 把 created_at 固定为 *int。这里清空该字段后复用原有订单映射。
	fields["created_at"] = json.RawMessage("null")
	normalized, err := json.Marshal(fields)
	if err != nil {
		return OpenOrder{}, time.Time{}, err
	}

	var order OpenOrder
	if err := json.Unmarshal(normalized, &order); err != nil {
		return OpenOrder{}, time.Time{}, err
	}
	return order, createdAt, nil
}

func parseListOrderCreatedAt(raw json.RawMessage) (time.Time, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return time.Time{}, nil
	}

	value := string(trimmed)
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return time.Time{}, err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return time.Time{}, nil
		}
	}

	if timestamp, err := strconv.ParseInt(value, 10, 64); err == nil {
		if timestamp >= unixMillisecondsThreshold || timestamp <= -unixMillisecondsThreshold {
			return time.UnixMilli(timestamp).UTC(), nil
		}
		return time.Unix(timestamp, 0).UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be integer Unix seconds/milliseconds or RFC3339Nano: %w", err)
	}
	return parsed.UTC(), nil
}
