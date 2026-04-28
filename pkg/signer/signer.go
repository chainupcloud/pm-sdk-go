package signer

import "context"

// Signer 是签名器统一接口。
//
// 实现见同包 eip712.go (NewEIP712Signer) 与 pmcup26.go (NewPMCup26Signer)；
// 均要求 Sign 的 payload 是 32-byte EIP-712 structHash。
type Signer interface {
	// Sign 对 payload 做签名，返回签名字节。
	Sign(ctx context.Context, payload []byte) ([]byte, error)
	// Address 返回签名者的链上地址（EIP-55 校验和形式）。
	Address() string
	// SchemaVersion 返回签名 schema 标识，如 "polymarket-v1" / "pmcup26-v1"。
	SchemaVersion() string
}
