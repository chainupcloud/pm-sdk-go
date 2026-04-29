package relayer

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// DigestSigner 是本包内部的签名抽象：对一个 32-byte EIP-712 digest 签名并返回
// 65-byte (r||s||v) 签名（v ∈ {0,1}）。
//
// 为什么不直接复用 pkg/signer.Signer？因为：
//  1. Safe 的两套域分隔符（SafeProxyFactory + 各个 Safe 地址）每次调用动态变化，
//     而 pkg/signer.Signer 在构造期把 domainSeparator 固化（见 NewEIP712Signer）。
//  2. PMCup26Signer.Sign 内部用 ClobAuth 域，对 Safe 路径是错误域——会签出无效签名。
//
// 实际选择：让 pkg/relayer 自己保留计算 digest 的逻辑（BuildSafeCreateDigest /
// BuildSafeTxDigest），DigestSigner 只负责 ECDSA 那一步。这样：
//   - 不污染 pkg/signer 的语义（payload 必须是 structHash）
//   - 单测可以传 mock signer
//   - NewPrivateKeySigner 是 thin shim，调用方常见用法一行注入私钥即可
type DigestSigner interface {
	// Address 返回签名者 EOA 地址（小写或 EIP-55 大小写均可，调用 Service 时
	// 内部会按 SubmitRequest 要求统一小写化）。
	Address() common.Address
	// SignDigest 对 32-byte digest 做 secp256k1 签名，返回 65-byte (r||s||v)。
	// v 应为 0/1（Safe 合约和 SafeProxyFactory 期望 v ∈ {27,28}，本包 Service
	// 在写入 SubmitRequest 前统一加 27）。
	SignDigest(ctx context.Context, digest [32]byte) ([]byte, error)
}

// privateKeySigner 是 DigestSigner 的开箱即用实现，包装 *ecdsa.PrivateKey。
type privateKeySigner struct {
	priv *ecdsa.PrivateKey
	addr common.Address
}

// NewPrivateKeySigner 用 *ecdsa.PrivateKey 构造 DigestSigner。priv == nil 时
// 返回 ErrInvalidConfig。
func NewPrivateKeySigner(priv *ecdsa.PrivateKey) (DigestSigner, error) {
	if priv == nil {
		return nil, fmt.Errorf("%w: nil private key", ErrInvalidConfig)
	}
	return &privateKeySigner{
		priv: priv,
		addr: ethcrypto.PubkeyToAddress(priv.PublicKey),
	}, nil
}

func (s *privateKeySigner) Address() common.Address {
	return s.addr
}

func (s *privateKeySigner) SignDigest(_ context.Context, digest [32]byte) ([]byte, error) {
	sig, err := ethcrypto.Sign(digest[:], s.priv)
	if err != nil {
		return nil, fmt.Errorf("relayer: ecdsa sign: %w", err)
	}
	return sig, nil
}

// normalizeSafeSig 把 ethcrypto.Sign 输出的 v ∈ {0,1} 加 27 得到 Safe 期望的
// {27,28}。返回新 slice 不修改入参。
func normalizeSafeSig(sig []byte) ([]byte, error) {
	if len(sig) != 65 {
		return nil, errors.New("relayer: signature must be 65 bytes")
	}
	out := make([]byte, 65)
	copy(out, sig)
	if out[64] < 27 {
		out[64] += 27
	}
	return out, nil
}
