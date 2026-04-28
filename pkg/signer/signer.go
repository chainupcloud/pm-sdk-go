package signer

import "context"

// Signer 是签名器统一接口。
//
// TODO Phase 6: 在 eip712.go / pmcup26.go 中提供实际实现：
//   - NewEIP712Signer(privKey *ecdsa.PrivateKey, domainSeparator [32]byte) Signer
//   - NewPMCup26Signer(privKey *ecdsa.PrivateKey, scopeID string) Signer
type Signer interface {
	// Sign 对 payload 做签名，返回签名字节。
	Sign(ctx context.Context, payload []byte) ([]byte, error)
	// Address 返回签名者的链上地址（EIP-55 校验和形式）。
	Address() string
	// SchemaVersion 返回签名 schema 标识，如 "polymarket-v1" / "pmcup26-v1"。
	SchemaVersion() string
}
