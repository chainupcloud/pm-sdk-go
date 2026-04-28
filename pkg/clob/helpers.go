package clob

import (
	"encoding/hex"
	"encoding/json"
)

// jsonUnmarshal 是包内统一的 JSON 解码入口；预留 hook（如 future 走 sonic / decoder pool）。
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// unmarshalSendOrderResponse 解析 PostOrder 的 200 响应。
func unmarshalSendOrderResponse(data []byte) (*SendOrderResponse, error) {
	var r SendOrderResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// bytesToHex 把签名字节转成 hex（不带 0x 前缀；调用方按需拼）。
func bytesToHex(b []byte) string {
	return hex.EncodeToString(b)
}
