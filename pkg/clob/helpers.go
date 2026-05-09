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

// normalizeECDSAv 把 ethcrypto.Sign 输出的 v ∈ {0,1} 加 27 得到
// OpenZeppelin ECDSA / Safe.checkSignatures 期望的 {27,28}。
//
// 链上 CTFExchange.matchOrders 走 OZ ECDSA.recover；signature_type=2
// (POLY_GNOSIS_SAFE) 经 Safe.isValidSignature → checkSignatures →
// 仍走 ECDSA.recover。两路径都要 v ∈ {27,28}，否则 revert
// "ECDSA: invalid signature 'v' value"。
//
// 返回新 slice 不修改入参；非 65 字节直接原样返回。
func normalizeECDSAv(sig []byte) []byte {
	if len(sig) != 65 {
		return sig
	}
	out := make([]byte, 65)
	copy(out, sig)
	if out[64] < 27 {
		out[64] += 27
	}
	return out
}
