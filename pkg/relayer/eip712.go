package relayer

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// EIP-712 typeHash 字符串与 pm-cup2026/services/clob-service/cmd/market-maker/relayer.go
// 完全一致；任意字符的修改都会让链上 verify 失败，因此本文件应被视为「已 prod 跑过的金本位」，
// 修改前请重新生成 testdata/golden_digests.json 并跑回归测试。
//
//nolint:gochecknoglobals // 类型哈希常量。
var (
	// SafeProxyFactory.createProxy 的 EIP-712 域：3 字段（无 version）。
	createProxyDomainTypeHash = ethcrypto.Keccak256([]byte("EIP712Domain(string name,uint256 chainId,address verifyingContract)"))
	createProxyTypeHash       = ethcrypto.Keccak256([]byte("CreateProxy(address paymentToken,uint256 payment,address paymentReceiver,bytes32 scopeId)"))
	factoryNameHash           = ethcrypto.Keccak256([]byte("Polymarket Contract Proxy Factory"))

	// Safe execTransaction 的 EIP-712 域：2 字段（无 name、无 version）。
	safeDomainTypeHash = ethcrypto.Keccak256([]byte("EIP712Domain(uint256 chainId,address verifyingContract)"))
	safeTxTypeHash     = ethcrypto.Keccak256([]byte("SafeTx(address to,uint256 value,bytes data,uint8 operation,uint256 safeTxGas,uint256 baseGas,uint256 gasPrice,address gasToken,address refundReceiver,uint256 nonce)"))
)

// ParseScopeID 把任意长度的 hex 字符串（带不带 0x 都行）右对齐转成 bytes32。
//
// 与 mm V2 relayer.go 的 parseScopeID 行为一致：超 32 字节截前 32 字节，不足右
// 对齐左 padding 0。SDK 调用方一般传 0x 开头的 32-byte hex。
func ParseScopeID(hexStr string) [32]byte {
	var out [32]byte
	raw := common.FromHex(hexStr)
	if len(raw) > 32 {
		raw = raw[:32]
	}
	copy(out[32-len(raw):], raw)
	return out
}

// SafeCreateInput 是 BuildSafeCreateDigest 的入参——SAFE-CREATE 的 EIP-712 业务字段。
//
// 与 mm V2 的 signSafeCreate 一致：mm V2 实际固定填了 paymentToken=0x0、payment=0、
// paymentReceiver=0x0；这里保持参数化以便未来需要付费 deploy 时直接用，调用方默认
// 三个零值即可。
type SafeCreateInput struct {
	PaymentToken    common.Address
	Payment         *big.Int
	PaymentReceiver common.Address
	// ScopeID 是 bytes32；调用方可用 ParseScopeID 从 hex 字符串构造。
	ScopeID [32]byte
}

// BuildSafeCreateDigest 计算 SAFE-CREATE 提交的 EIP-712 digest（32 字节），
// 调用方对该 digest 直接 ECDSA 签名即可。
//
// 推导路径（与 mm V2 relayer.go signSafeCreate 完全一致）：
//
//   - domainSep = keccak256(domainTypeHash || nameHash || pad32(chainID) || pad32(factory))
//     domainTypeHash = keccak256("EIP712Domain(string name,uint256 chainId,address verifyingContract)")
//     nameHash       = keccak256("Polymarket Contract Proxy Factory")
//
//   - structHash = keccak256(typeHash || pad32(paymentToken) || pad32(payment) || pad32(paymentReceiver) || scopeId)
//     typeHash = keccak256("CreateProxy(address paymentToken,uint256 payment,address paymentReceiver,bytes32 scopeId)")
//
//   - digest    = keccak256("\x19\x01" || domainSep || structHash)
func BuildSafeCreateDigest(input SafeCreateInput, factory common.Address, chainID int64) [32]byte {
	if input.Payment == nil {
		input.Payment = new(big.Int)
	}
	domainSep := ethcrypto.Keccak256(
		createProxyDomainTypeHash,
		factoryNameHash,
		common.LeftPadBytes(big.NewInt(chainID).Bytes(), 32),
		common.LeftPadBytes(factory.Bytes(), 32),
	)
	structHash := ethcrypto.Keccak256(
		createProxyTypeHash,
		common.LeftPadBytes(input.PaymentToken.Bytes(), 32),
		common.LeftPadBytes(input.Payment.Bytes(), 32),
		common.LeftPadBytes(input.PaymentReceiver.Bytes(), 32),
		input.ScopeID[:],
	)
	d := ethcrypto.Keccak256([]byte("\x19\x01"), domainSep, structHash)
	var out [32]byte
	copy(out[:], d)
	return out
}

// SafeTxInput 是 BuildSafeTxDigest 的入参——SAFE 提交的 EIP-712 业务字段。
//
// 与 mm V2 的 computeSafeTxHash 一致：value/operation/safeTxGas/baseGas/gasPrice/
// gasToken/refundReceiver 默认全部零值（CALL 类型、relayer 全部代付）。Data 是
// Safe 在 to 上要执行的 calldata（如 ERC-20 approve / mint / setApprovalForAll）。
type SafeTxInput struct {
	To             common.Address
	Value          *big.Int // 默认 0
	Data           []byte   // calldata；keccak256(data) 进 structHash
	Operation      uint8    // 0=CALL, 1=DELEGATECALL；默认 0
	SafeTxGas      *big.Int // 默认 0
	BaseGas        *big.Int // 默认 0
	GasPrice       *big.Int // 默认 0
	GasToken       common.Address
	RefundReceiver common.Address
	Nonce          *big.Int // 来自 GET /nonce
}

// BuildSafeTxDigest 计算 SAFE execTransaction 的 EIP-712 digest（32 字节）。
//
// 推导路径（与 mm V2 relayer.go computeSafeTxHash 完全一致）：
//
//   - domainSep = keccak256(domainTypeHash || pad32(chainID) || pad32(safeAddr))
//     domainTypeHash = keccak256("EIP712Domain(uint256 chainId,address verifyingContract)")
//
//   - structHash = keccak256(typeHash || pad32(to) || pad32(value) || keccak256(data) ||
//     pad32(operation) || pad32(safeTxGas) || pad32(baseGas) ||
//     pad32(gasPrice) || pad32(gasToken) || pad32(refundReceiver) ||
//     pad32(nonce))
//     typeHash = keccak256("SafeTx(...)")
//
//   - digest    = keccak256("\x19\x01" || domainSep || structHash)
func BuildSafeTxDigest(input SafeTxInput, safe common.Address, chainID int64) [32]byte {
	bigOrZero := func(v *big.Int) *big.Int {
		if v == nil {
			return new(big.Int)
		}
		return v
	}
	value := bigOrZero(input.Value)
	safeTxGas := bigOrZero(input.SafeTxGas)
	baseGas := bigOrZero(input.BaseGas)
	gasPrice := bigOrZero(input.GasPrice)
	nonce := bigOrZero(input.Nonce)

	domainSep := ethcrypto.Keccak256(
		safeDomainTypeHash,
		common.LeftPadBytes(big.NewInt(chainID).Bytes(), 32),
		common.LeftPadBytes(safe.Bytes(), 32),
	)

	dataHash := ethcrypto.Keccak256(input.Data)
	structHash := ethcrypto.Keccak256(
		safeTxTypeHash,
		common.LeftPadBytes(input.To.Bytes(), 32),
		common.LeftPadBytes(value.Bytes(), 32),
		dataHash,
		common.LeftPadBytes(big.NewInt(int64(input.Operation)).Bytes(), 32),
		common.LeftPadBytes(safeTxGas.Bytes(), 32),
		common.LeftPadBytes(baseGas.Bytes(), 32),
		common.LeftPadBytes(gasPrice.Bytes(), 32),
		common.LeftPadBytes(input.GasToken.Bytes(), 32),
		common.LeftPadBytes(input.RefundReceiver.Bytes(), 32),
		common.LeftPadBytes(nonce.Bytes(), 32),
	)

	d := ethcrypto.Keccak256([]byte("\x19\x01"), domainSep, structHash)
	var out [32]byte
	copy(out[:], d)
	return out
}
