package signer

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ErrNoExchangeAddress 表示 SignOrder 调用时未配置 CTFExchange verifyingContract 地址。
var ErrNoExchangeAddress = errors.New("signer: pmcup26 exchange address required for SignOrder")

// PMCup26Signer 是面向 pm-cup2026 业务的高级签名器：在 EIP712Signer 之上内置
// chainID + scopeID + (可选) exchangeAddress 三个上下文参数，向 SDK 业务层提供
//
//   - SignClobAuth(timestamp, nonce) → 65-byte signature
//   - SignOrder(order)               → 65-byte signature
//
// 两个高级方法。低层 Sign(ctx, 32-byte structHash) 仍兼容 Signer 接口，schema 标识
// 为 "pmcup26-v1"。
//
// 与 EIP712Signer 的定位差异：
//
//   - EIP712Signer 通用、域分隔符已固化、不感知业务字段
//   - PMCup26Signer 内置 ClobAuth/Order 两套域 + scopeID + chainID + exchange 地址，
//     SDK Facade 调用层可直接喂业务结构而无需手算 digest
//
// 注意 Sign(ctx, payload) 仍要求 payload 是 32-byte structHash；它会用 ClobAuth
// 域分隔符（默认）签出。如果 caller 想用 Order 域请走 SignOrder 高级方法。
type PMCup26Signer struct {
	privKey         *ecdsa.PrivateKey
	address         common.Address
	scopeID         [32]byte
	chainID         int64
	exchangeAddress common.Address
	hasExchange     bool
}

// PMCup26Option 是 PMCup26Signer 构造选项。
type PMCup26Option func(*PMCup26Signer)

// WithExchangeAddress 注入 CTFExchange verifyingContract 地址；SignOrder 必需。
func WithExchangeAddress(addr common.Address) PMCup26Option {
	return func(s *PMCup26Signer) {
		s.exchangeAddress = addr
		s.hasExchange = true
	}
}

// NewPMCup26Signer 构造 PMCup26Signer。
//
// 入参：
//   - privKey：secp256k1 私钥
//   - scopeID：bytes32 多租户作用域（[32]byte 零值 = 无作用域）
//   - chainID：EIP-712 domain chainId（pm-cup2026 staging/prod 对应配置）
//   - opts：WithExchangeAddress(addr) — SignOrder 路径必需
//
// 若 caller 不调 SignOrder，可不传 exchangeAddress；ClobAuth 路径只用 chainID。
func NewPMCup26Signer(privKey *ecdsa.PrivateKey, scopeID [32]byte, chainID int64, opts ...PMCup26Option) *PMCup26Signer {
	addr := ethcrypto.PubkeyToAddress(privKey.PublicKey)
	s := &PMCup26Signer{
		privKey: privKey,
		address: addr,
		scopeID: scopeID,
		chainID: chainID,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Sign 实现 Signer 接口：对 32-byte structHash 用 ClobAuth 域签名。
//
// 注意：这是兼容路径；推荐用 SignClobAuth / SignOrder 高级方法以避免域错配。
func (s *PMCup26Signer) Sign(_ context.Context, payload []byte) ([]byte, error) {
	if len(payload) != 32 {
		return nil, fmt.Errorf("%w: got %d bytes", ErrInvalidPayload, len(payload))
	}
	domainSep := ClobAuthDomainSeparator(s.chainID)
	digest := EIP712Digest(domainSep[:], payload)
	sig, err := ethcrypto.Sign(digest, s.privKey)
	if err != nil {
		return nil, fmt.Errorf("signer: ecdsa sign: %w", err)
	}
	return sig, nil
}

// Address 返回签名者的 EIP-55 地址。
func (s *PMCup26Signer) Address() string {
	return s.address.Hex()
}

// SchemaVersion 返回 "pmcup26-v1"。
func (s *PMCup26Signer) SchemaVersion() string {
	return "pmcup26-v1"
}

// ScopeID 返回内置 scope ID。
func (s *PMCup26Signer) ScopeID() [32]byte {
	return s.scopeID
}

// ChainID 返回内置 chain ID。
func (s *PMCup26Signer) ChainID() int64 {
	return s.chainID
}

// ExchangeAddress 返回内置 exchange 地址；hasExchange=false 时 addr 是零地址。
func (s *PMCup26Signer) ExchangeAddress() (common.Address, bool) {
	return s.exchangeAddress, s.hasExchange
}

// SignClobAuth 用内置 chainID + scopeID + 签名者地址构造 ClobAuth 5-field digest 并签名。
//
// 返回 65-byte (r||s||v) 签名（v ∈ {0,1}）。
func (s *PMCup26Signer) SignClobAuth(_ context.Context, timestamp string, nonce uint64) ([]byte, error) {
	digest := BuildClobAuthDigest(s.address, timestamp, nonce, s.scopeID, s.chainID)
	sig, err := ethcrypto.Sign(digest[:], s.privKey)
	if err != nil {
		return nil, fmt.Errorf("signer: clob auth sign: %w", err)
	}
	return sig, nil
}

// SignOrder 用内置 chainID + exchange 地址 + scopeID 构造 Order 13-field digest 并签名。
//
// 注意：order.Maker / order.Signer / order.ScopeID 由 caller 显式传入；本函数不会
// 自动覆写这些字段（SDK Facade 在调用前已正确填充）。如果调用方未在 PMCup26Signer
// 构造时注入 ExchangeAddress，本方法返回 ErrNoExchangeAddress。
func (s *PMCup26Signer) SignOrder(_ context.Context, order *OrderForSigning) ([]byte, error) {
	if !s.hasExchange {
		return nil, ErrNoExchangeAddress
	}
	digest := BuildOrderDigest(order, s.exchangeAddress, s.chainID)
	sig, err := ethcrypto.Sign(digest[:], s.privKey)
	if err != nil {
		return nil, fmt.Errorf("signer: order sign: %w", err)
	}
	return sig, nil
}
