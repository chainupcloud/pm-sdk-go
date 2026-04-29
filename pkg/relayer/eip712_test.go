package relayer

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// 黄金 fixture：testdata/golden_digests.json，由 testdata/gen_golden.go 生成。
// 任何 EIP-712 typeHash / 字段顺序 / padding 漂移都会让以下三组对比失败。
type goldenFixture struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Signature string `json:"signature"`
	Signer    string `json:"signer"`
	Notes     string `json:"notes"`
}

func loadGolden(t *testing.T) map[string]goldenFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/golden_digests.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var fixtures []goldenFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	out := make(map[string]goldenFixture, len(fixtures))
	for _, f := range fixtures {
		out[f.Name] = f
	}
	return out
}

func TestParseScopeID(t *testing.T) {
	hexStr := "0x083ff7c1bc4972eef065542fc562d42e91b706719b313a95bf59eb0338a97fe7"
	got := ParseScopeID(hexStr)
	if "0x"+hex.EncodeToString(got[:]) != hexStr {
		t.Fatalf("ParseScopeID round-trip mismatch: %x", got[:])
	}
	// 短 hex 应右对齐左 padding
	short := ParseScopeID("0xabcd")
	want := [32]byte{}
	want[30] = 0xab
	want[31] = 0xcd
	if short != want {
		t.Fatalf("ParseScopeID short padding wrong: got %x", short[:])
	}
}

// TestSafeCreateDigestGolden 验证 SafeCreate digest 与黄金 fixture 一致。
func TestSafeCreateDigestGolden(t *testing.T) {
	g := loadGolden(t)["safe_create_zero_payment"]
	chainID := int64(11155420)
	factory := common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041")
	scopeID := ParseScopeID("0x083ff7c1bc4972eef065542fc562d42e91b706719b313a95bf59eb0338a97fe7")

	got := BuildSafeCreateDigest(SafeCreateInput{
		PaymentToken:    common.Address{},
		Payment:         big.NewInt(0),
		PaymentReceiver: common.Address{},
		ScopeID:         scopeID,
	}, factory, chainID)

	want := strings.ToLower(g.Digest)
	if "0x"+hex.EncodeToString(got[:]) != want {
		t.Fatalf("digest mismatch:\n  got  %x\n  want %s", got[:], want)
	}
}

// TestSafeTxDigestGolden 验证 SafeTx digest（USDC.approve 与 USDC.mint 两组场景）。
func TestSafeTxDigestGolden(t *testing.T) {
	golden := loadGolden(t)
	chainID := int64(11155420)
	usdc := common.HexToAddress("0x508A62Bd6A37b03dB215c6aAb82Fc1683e95Abf4")
	exchange := common.HexToAddress("0xC6e9081EcaD84AfB3a772933Fb865AB8A9C317d9")
	safeAddr := common.HexToAddress("0x2100186071afd66c5d4f5108cF2BB47b13c08946")

	t.Run("approve_nonce0", func(t *testing.T) {
		maxUint := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
		calldata := append([]byte{}, common.FromHex("0x095ea7b3")...)
		calldata = append(calldata, common.LeftPadBytes(exchange.Bytes(), 32)...)
		calldata = append(calldata, common.LeftPadBytes(maxUint.Bytes(), 32)...)

		got := BuildSafeTxDigest(SafeTxInput{
			To:    usdc,
			Data:  calldata,
			Nonce: big.NewInt(0),
		}, safeAddr, chainID)
		want := strings.ToLower(golden["safe_tx_usdc_approve_nonce0"].Digest)
		if "0x"+hex.EncodeToString(got[:]) != want {
			t.Fatalf("digest mismatch:\n  got  %x\n  want %s", got[:], want)
		}
	})

	t.Run("mint_nonce7", func(t *testing.T) {
		amt := new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(1_000_000))
		calldata := append([]byte{}, common.FromHex("0x40c10f19")...)
		calldata = append(calldata, common.LeftPadBytes(safeAddr.Bytes(), 32)...)
		calldata = append(calldata, common.LeftPadBytes(amt.Bytes(), 32)...)

		got := BuildSafeTxDigest(SafeTxInput{
			To:    usdc,
			Data:  calldata,
			Nonce: big.NewInt(7),
		}, safeAddr, chainID)
		want := strings.ToLower(golden["safe_tx_usdc_mint_nonce7"].Digest)
		if "0x"+hex.EncodeToString(got[:]) != want {
			t.Fatalf("digest mismatch:\n  got  %x\n  want %s", got[:], want)
		}
	})
}

// TestSafeTxDigestNilDefaults 验证 SafeTxInput 的 *big.Int 字段为 nil 时与零值
// 等价（默认 0），避免调用方因疏忽传 nil 触发 panic。
func TestSafeTxDigestNilDefaults(t *testing.T) {
	chainID := int64(11155420)
	safeAddr := common.HexToAddress("0x2100186071afd66c5d4f5108cF2BB47b13c08946")
	to := common.HexToAddress("0x508A62Bd6A37b03dB215c6aAb82Fc1683e95Abf4")
	data := common.FromHex("0xdeadbeef")

	withNils := BuildSafeTxDigest(SafeTxInput{To: to, Data: data}, safeAddr, chainID)
	withZeros := BuildSafeTxDigest(SafeTxInput{
		To:    to,
		Data:  data,
		Value: big.NewInt(0), SafeTxGas: big.NewInt(0), BaseGas: big.NewInt(0),
		GasPrice: big.NewInt(0), Nonce: big.NewInt(0),
	}, safeAddr, chainID)
	if withNils != withZeros {
		t.Fatalf("nil ≠ zero defaults")
	}
}

// TestSafeCreateSignatureRecovery 用黄金 signature 对 ecrecover 反推签名地址，
// 验证 digest + 签名生成路径完整正确（typeHash、padding、digest 全部对就能 recover 出预期地址）。
func TestSafeCreateSignatureRecovery(t *testing.T) {
	g := loadGolden(t)["safe_create_zero_payment"]
	digest := common.FromHex(g.Digest)
	sig := common.FromHex(g.Signature)
	if len(sig) != 65 {
		t.Fatalf("expected 65-byte sig, got %d", len(sig))
	}
	pub, err := ethcrypto.SigToPub(digest, sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	gotAddr := ethcrypto.PubkeyToAddress(*pub)
	if !strings.EqualFold(gotAddr.Hex(), g.Signer) {
		t.Fatalf("recovered %s, want %s", gotAddr.Hex(), g.Signer)
	}
}
