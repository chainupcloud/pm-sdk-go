package signer

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ErrInvalidPayload 表示 Sign payload 长度不符合预期（EIP-712 要求 32 byte structHash）。
var ErrInvalidPayload = errors.New("signer: payload must be 32-byte EIP-712 struct hash")

// eip712Signer 实现 Signer，对外通过 NewEIP712Signer 构造。
//
// 工作模式：
//  1. caller 在 SDK 业务层（clob/auth）按 EIP-712 规范算好 structHash（32 byte）
//  2. 把该 structHash 作为 Sign 的 payload 入参
//  3. 内部用预先固化的 domainSeparator 拼接 "\x19\x01" || domain || structHash 算 digest
//  4. 用 secp256k1 私钥对 digest 签名，返回 65-byte (r||s||v) 签名（v ∈ {0,1}）
//
// v 值约定：与后端 ethcrypto.Sign 一致 — 输出 0/1；钱包侧若期待 27/28 由 caller 加 27。
type eip712Signer struct {
	privKey         *ecdsa.PrivateKey
	address         common.Address
	domainSeparator [32]byte
	schema          string
}

// NewEIP712Signer 构造一个通用 EIP-712 签名器。
//
// 入参：
//   - privKey：secp256k1 私钥；签出的地址 = ethcrypto.PubkeyToAddress(privKey.PublicKey)
//   - domainSeparator：预先按 EIP-712 §3 算好的域分隔符（32 byte），caller 自行决定
//     用 ClobAuth domain（DomainSeparator）还是 CTFExchange domain
//     （CTFExchangeDomainSeparator）
//
// Sign 的 payload 必须是 32-byte structHash；非 32 byte 一律拒绝。
func NewEIP712Signer(privKey *ecdsa.PrivateKey, domainSeparator [32]byte) Signer {
	addr := ethcrypto.PubkeyToAddress(privKey.PublicKey)
	return &eip712Signer{
		privKey:         privKey,
		address:         addr,
		domainSeparator: domainSeparator,
		schema:          "polymarket-v1",
	}
}

// Sign 对 32-byte EIP-712 structHash 做签名，返回 65-byte (r||s||v) 字节串。
func (s *eip712Signer) Sign(_ context.Context, payload []byte) ([]byte, error) {
	if len(payload) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidPayload, len(payload))
	}
	digest := EIP712Digest(s.domainSeparator[:], payload)
	sig, err := ethcrypto.Sign(digest, s.privKey)
	if err != nil {
		return nil, fmt.Errorf("signer: ecdsa sign: %w", err)
	}
	return sig, nil
}

// Address 返回签名者的 EIP-55 地址字符串。
func (s *eip712Signer) Address() string {
	return s.address.Hex()
}

// SchemaVersion 返回 schema 标识。
func (s *eip712Signer) SchemaVersion() string {
	return s.schema
}

// EIP712Digest 计算 keccak256("\x19\x01" || domainSeparator || structHash) 的 32-byte digest。
//
// caller 持有 domainSeparator + structHash 双段 32-byte 值时直接调本函数即可拿到
// EIP-712 §4 规定的最终 digest。
func EIP712Digest(domainSeparator, structHash []byte) []byte {
	return ethcrypto.Keccak256(
		[]byte("\x19\x01"),
		domainSeparator,
		structHash,
	)
}
