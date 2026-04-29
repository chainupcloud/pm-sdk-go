//go:build ignore

// gen_golden.go — 一次性脚本：用确定输入算 SafeCreate / SafeTx digest，
// 把结果写到 testdata/golden_digests.json，给 eip712_test.go 做回归基线。
//
// 运行：cd pkg/relayer/testdata && go run gen_golden.go
//
// 输入是写死的 fixture 私钥 + 0x... 地址常量，确保任何机器/任何时间运行都
// 输出相同 digest；任何 typeHash 字符串改动会让回归测试 diff 报错。
package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/chainupcloud/pm-sdk-go/pkg/relayer"
)

type fixture struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Signature string `json:"signature"`
	Signer    string `json:"signer"`
	Notes     string `json:"notes"`
}

func main() {
	// 固定私钥（以"a"x64 padding，0x4646...）。
	privHex := "4646464646464646464646464646464646464646464646464646464646464646"
	priv, err := ethcrypto.HexToECDSA(privHex)
	if err != nil {
		panic(err)
	}
	signerAddr := ethcrypto.PubkeyToAddress(priv.PublicKey)

	// OP Sepolia 的链 + 合约（与 clob-service CLAUDE 中的常量一致）。
	chainID := int64(11155420)
	factory := common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041")
	scopeID := relayer.ParseScopeID("0x083ff7c1bc4972eef065542fc562d42e91b706719b313a95bf59eb0338a97fe7")

	out := []fixture{}

	// SafeCreate：mm V2 prod 场景的零值参数。
	d1 := relayer.BuildSafeCreateDigest(relayer.SafeCreateInput{
		PaymentToken:    common.Address{},
		Payment:         big.NewInt(0),
		PaymentReceiver: common.Address{},
		ScopeID:         scopeID,
	}, factory, chainID)
	sig1, _ := ethcrypto.Sign(d1[:], priv)
	out = append(out, fixture{
		Name:      "safe_create_zero_payment",
		Digest:    "0x" + hex.EncodeToString(d1[:]),
		Signature: "0x" + hex.EncodeToString(sig1),
		Signer:    signerAddr.Hex(),
		Notes:     "Zero paymentToken/payment/paymentReceiver — matches mm V2 prod call site.",
	})

	// SafeTx：USDC.approve(exchange, MaxUint256) 场景。
	usdc := common.HexToAddress("0x508A62Bd6A37b03dB215c6aAb82Fc1683e95Abf4")
	exchange := common.HexToAddress("0xC6e9081EcaD84AfB3a772933Fb865AB8A9C317d9")
	safeAddr := common.HexToAddress("0x2100186071afd66c5d4f5108cF2BB47b13c08946")

	// approve(address,uint256) selector = 0x095ea7b3
	maxUint := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	calldata := append([]byte{}, common.FromHex("0x095ea7b3")...)
	calldata = append(calldata, common.LeftPadBytes(exchange.Bytes(), 32)...)
	calldata = append(calldata, common.LeftPadBytes(maxUint.Bytes(), 32)...)

	d2 := relayer.BuildSafeTxDigest(relayer.SafeTxInput{
		To:    usdc,
		Data:  calldata,
		Nonce: big.NewInt(0),
	}, safeAddr, chainID)
	sig2, _ := ethcrypto.Sign(d2[:], priv)
	out = append(out, fixture{
		Name:      "safe_tx_usdc_approve_nonce0",
		Digest:    "0x" + hex.EncodeToString(d2[:]),
		Signature: "0x" + hex.EncodeToString(sig2),
		Signer:    signerAddr.Hex(),
		Notes:     "USDC.approve(exchange, MaxUint256) Safe tx, nonce 0.",
	})

	// SafeTx：mint 场景，nonce > 0 验证 nonce padding。
	mintCalldata := append([]byte{}, common.FromHex("0x40c10f19")...) // mint(address,uint256)
	mintAmt := new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(1_000_000))
	mintCalldata = append(mintCalldata, common.LeftPadBytes(safeAddr.Bytes(), 32)...)
	mintCalldata = append(mintCalldata, common.LeftPadBytes(mintAmt.Bytes(), 32)...)

	d3 := relayer.BuildSafeTxDigest(relayer.SafeTxInput{
		To:    usdc,
		Data:  mintCalldata,
		Nonce: big.NewInt(7),
	}, safeAddr, chainID)
	sig3, _ := ethcrypto.Sign(d3[:], priv)
	out = append(out, fixture{
		Name:      "safe_tx_usdc_mint_nonce7",
		Digest:    "0x" + hex.EncodeToString(d3[:]),
		Signature: "0x" + hex.EncodeToString(sig3),
		Signer:    signerAddr.Hex(),
		Notes:     "USDC.mint(safe, 1_000_000_000_000) Safe tx, nonce 7.",
	})

	js, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile("golden_digests.json", js, 0o644); err != nil {
		panic(err)
	}
	fmt.Println(string(js))
}
