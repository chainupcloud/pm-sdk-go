package signer

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// ClobAuth EIP-712 类型常量（与后端 services/clob-service/internal/shared/crypto/eip712.go 完全一致）。
//
// 5-field 结构：address / timestamp / nonce / scopeId / message。
// 与上游 Polymarket 的区别：增加 scopeId (bytes32) 用于多租户隔离；零值 = 无作用域。
const (
	clobAuthDomainName    = "ClobAuthDomain"
	clobAuthDomainVersion = "1"
	clobAuthMessage       = "This message attests that I control the given wallet"
)

// 类型哈希在包初始化时算好；与后端的 eip712DomainTypeHash / clobAuthTypeHash 等价。
//
//nolint:gochecknoglobals // 类型哈希为常量字节串，直接固化避免每次签名重算 keccak。
var (
	eip712DomainTypeHashShort = ethcrypto.Keccak256([]byte(
		"EIP712Domain(string name,string version,uint256 chainId)",
	))
	clobAuthTypeHash = ethcrypto.Keccak256([]byte(
		"ClobAuth(address address,string timestamp,uint256 nonce,bytes32 scopeId,string message)",
	))
)

// ClobAuthDomainSeparator 计算 ClobAuthDomain 的 EIP-712 域分隔符（32-byte）。
//
// 与后端 DomainSeparator(chainID) 完全一致：
//
//	keccak256(typeHash || keccak256("ClobAuthDomain") || keccak256("1") || pad32(chainID))
func ClobAuthDomainSeparator(chainID int64) [32]byte {
	nameHash := ethcrypto.Keccak256([]byte(clobAuthDomainName))
	versionHash := ethcrypto.Keccak256([]byte(clobAuthDomainVersion))
	chainIDBig := new(big.Int).SetInt64(chainID)

	out := ethcrypto.Keccak256(
		eip712DomainTypeHashShort,
		nameHash,
		versionHash,
		common.LeftPadBytes(chainIDBig.Bytes(), 32),
	)
	var ds [32]byte
	copy(ds[:], out)
	return ds
}

// ClobAuthStructHash 计算 ClobAuth 5-field 结构体哈希（32-byte）。
//
// 字段顺序（与后端一致）：typeHash || pad32(address) || keccak(timestamp)
// || pad32(nonce) || scopeId[:] || keccak(message)
//
// scopeId 是 bytes32 定长类型，**直接** 32 byte 入 hash（不再 keccak）；
// 零 [32]byte 表示无作用域绑定。
func ClobAuthStructHash(address common.Address, timestamp string, nonce uint64, scopeID [32]byte) [32]byte {
	nonceBig := new(big.Int).SetUint64(nonce)
	timestampHash := ethcrypto.Keccak256([]byte(timestamp))
	messageHash := ethcrypto.Keccak256([]byte(clobAuthMessage))

	out := ethcrypto.Keccak256(
		clobAuthTypeHash,
		common.LeftPadBytes(address.Bytes(), 32),
		timestampHash,
		common.LeftPadBytes(nonceBig.Bytes(), 32),
		scopeID[:],
		messageHash,
	)
	var sh [32]byte
	copy(sh[:], out)
	return sh
}

// BuildClobAuthDigest 是 ClobAuth 一站式入口：返回 keccak256("\x19\x01" ||
// domainSeparator || structHash) 的 32-byte digest，可直接交给 signer.Sign。
func BuildClobAuthDigest(address common.Address, timestamp string, nonce uint64, scopeID [32]byte, chainID int64) [32]byte {
	domainSep := ClobAuthDomainSeparator(chainID)
	structHash := ClobAuthStructHash(address, timestamp, nonce, scopeID)
	d := EIP712Digest(domainSep[:], structHash[:])
	var out [32]byte
	copy(out[:], d)
	return out
}

// ScopeIDFromHex 把 hex 字符串（"0x..." 或 ""）转 [32]byte，与后端 ScopeIDFromHex 行为一致。
//
// 空字符串 → 零值（无作用域）；超过 32 byte 截前 32；不足 32 byte 右对齐左填零。
func ScopeIDFromHex(hex string) [32]byte {
	var id [32]byte
	if hex == "" {
		return id
	}
	b := common.FromHex(hex)
	if len(b) > 32 {
		b = b[:32]
	}
	copy(id[32-len(b):], b)
	return id
}

// ScopeIDToHex 把 [32]byte 转 hex 字符串；零值返回空字符串。
func ScopeIDToHex(id [32]byte) string {
	zero := [32]byte{}
	if id == zero {
		return ""
	}
	return "0x" + common.Bytes2Hex(id[:])
}
