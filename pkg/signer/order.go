package signer

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// CTFExchange Order EIP-712 类型常量（与后端 services/clob-service/internal/shared/crypto/order_eip712.go 完全一致）。
//
// 13-field 结构（顺序敏感）：
//
//	salt / maker / signer / taker / tokenId / makerAmount / takerAmount /
//	expiration / nonce / feeRateBps / side / signatureType / scopeId
//
// 与上游 Polymarket 的区别：增加 scopeId (bytes32) 多租户字段。
const (
	ctfDomainNameStr    = "Polymarket CTF Exchange"
	ctfDomainVersionStr = "1"
)

//nolint:gochecknoglobals // 类型哈希常量。
var (
	eip712DomainTypeHashFull = ethcrypto.Keccak256([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
	))
	ctfDomainNameHash    = ethcrypto.Keccak256([]byte(ctfDomainNameStr))
	ctfDomainVersionHash = ethcrypto.Keccak256([]byte(ctfDomainVersionStr))
	orderTypeHash        = ethcrypto.Keccak256([]byte(
		"Order(uint256 salt,address maker,address signer,address taker,uint256 tokenId,uint256 makerAmount,uint256 takerAmount,uint256 expiration,uint256 nonce,uint256 feeRateBps,uint8 side,uint8 signatureType,bytes32 scopeId)",
	))
)

// CTFExchangeDomainSeparator 计算 CTFExchange 的 EIP-712 域分隔符（32-byte）。
//
// 与后端 CTFExchangeDomainSeparator(chainID, exchangeAddress) 完全一致：
//
//	keccak256(typeHash || nameHash || versionHash || pad32(chainID) || pad32(exchangeAddress))
func CTFExchangeDomainSeparator(chainID int64, exchangeAddress common.Address) [32]byte {
	chainIDBig := new(big.Int).SetInt64(chainID)
	out := ethcrypto.Keccak256(
		eip712DomainTypeHashFull,
		ctfDomainNameHash,
		ctfDomainVersionHash,
		common.LeftPadBytes(chainIDBig.Bytes(), 32),
		common.LeftPadBytes(exchangeAddress.Bytes(), 32),
	)
	var ds [32]byte
	copy(ds[:], out)
	return ds
}

// OrderStructHash 计算 Order 13-field 结构体哈希（32-byte）。
//
// 字段顺序与后端 OrderStructHash 一致；任何字段错位 → 链上 verify 失败。
//
// 注意：所有 *big.Int 字段视为 uint256；nil 会被当作 0 处理，避免 panic。
func OrderStructHash(order *OrderForSigning) [32]byte {
	out := ethcrypto.Keccak256(
		orderTypeHash,
		pad32(order.Salt),
		common.LeftPadBytes(order.Maker.Bytes(), 32),
		common.LeftPadBytes(order.Signer.Bytes(), 32),
		common.LeftPadBytes(order.Taker.Bytes(), 32),
		pad32(order.TokenID),
		pad32(order.MakerAmount),
		pad32(order.TakerAmount),
		pad32(new(big.Int).SetUint64(order.Expiration)),
		pad32(new(big.Int).SetUint64(order.Nonce)),
		pad32(new(big.Int).SetUint64(order.FeeRateBps)),
		pad32(new(big.Int).SetUint64(uint64(order.Side))),
		pad32(new(big.Int).SetUint64(uint64(order.SignatureType))),
		order.ScopeID[:],
	)
	var sh [32]byte
	copy(sh[:], out)
	return sh
}

// BuildOrderDigest 一站式入口：返回 32-byte digest 可直接交给 signer.Sign。
func BuildOrderDigest(order *OrderForSigning, exchangeAddress common.Address, chainID int64) [32]byte {
	domainSep := CTFExchangeDomainSeparator(chainID, exchangeAddress)
	structHash := OrderStructHash(order)
	d := EIP712Digest(domainSep[:], structHash[:])
	var out [32]byte
	copy(out[:], d)
	return out
}

// pad32 把 *big.Int 左填零成 32-byte uint256 编码；nil → 32 个零字节。
func pad32(v *big.Int) []byte {
	if v == nil {
		return make([]byte, 32)
	}
	return common.LeftPadBytes(v.Bytes(), 32)
}
