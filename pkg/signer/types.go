package signer

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// SignatureType 表示 CTFExchange 订单签名类型枚举（与后端 OpenAPI 一致）：
//
//	0 = EOA          外部账户私钥直接签
//	1 = POLY_PROXY   Polymarket proxy 合约
//	2 = POLY_GNOSIS_SAFE Gnosis Safe
type SignatureType uint8

// SignatureType 取值。
const (
	SignatureTypeEOA            SignatureType = 0
	SignatureTypePolyProxy      SignatureType = 1
	SignatureTypePolyGnosisSafe SignatureType = 2
)

// OrderSide 是 CTFExchange 订单方向（与后端 OrderForVerification.Side 对齐）。
type OrderSide uint8

// OrderSide 取值。
const (
	OrderSideBuy  OrderSide = 0
	OrderSideSell OrderSide = 1
)

// OrderForSigning 是构造 Order EIP-712 digest 所需的 13 个字段。
//
// 字段顺序与后端 `OrderForVerification` 一致；任何字段错位都会导致
// 签名 hash 偏离从而失效。所有 *big.Int 字段非 nil；零值用 big.NewInt(0)。
//
// 与 SDK 上层 OrderReq 的关系：Facade 在 PlaceOrder 内把 OrderReq + signer
// address + chainID + exchangeAddress 组装成 OrderForSigning，再调
// BuildOrderDigest 拿到 32-byte digest 交给 signer.Sign。
type OrderForSigning struct {
	Salt          *big.Int
	Maker         common.Address
	Signer        common.Address
	Taker         common.Address
	TokenID       *big.Int
	MakerAmount   *big.Int
	TakerAmount   *big.Int
	Expiration    uint64
	Nonce         uint64
	FeeRateBps    uint64
	Side          OrderSide
	SignatureType SignatureType
	ScopeID       [32]byte
}
