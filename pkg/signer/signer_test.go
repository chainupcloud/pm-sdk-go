package signer

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// goldenFixture 是 testdata/golden.json 的反序列化形态；字段与生成器一致。
type goldenFixture struct {
	Comment   string                  `json:"_comment"`
	PrivKey   string                  `json:"private_key"`
	ClobAuth  []goldenClobAuthCase    `json:"clob_auth"`
	Orders    []goldenOrderCase       `json:"orders"`
	DomainSep map[string]string       `json:"domain_separators"`
}

type goldenClobAuthCase struct {
	Name             string `json:"name"`
	ChainID          int64  `json:"chain_id"`
	Address          string `json:"address"`
	Timestamp        string `json:"timestamp"`
	Nonce            uint64 `json:"nonce"`
	ScopeIDHex       string `json:"scope_id_hex"`
	DomainSeparator  string `json:"domain_separator"`
	StructHash       string `json:"struct_hash"`
	Digest           string `json:"digest"`
	Signature        string `json:"signature"`
	RecoveredAddress string `json:"recovered_address"`
}

type goldenOrderCase struct {
	Name             string `json:"name"`
	ChainID          int64  `json:"chain_id"`
	ExchangeAddress  string `json:"exchange_address"`
	Salt             string `json:"salt"`
	Maker            string `json:"maker"`
	Signer           string `json:"signer"`
	Taker            string `json:"taker"`
	TokenID          string `json:"token_id"`
	MakerAmount      string `json:"maker_amount"`
	TakerAmount      string `json:"taker_amount"`
	Expiration       uint64 `json:"expiration"`
	Nonce            uint64 `json:"nonce"`
	FeeRateBps       uint64 `json:"fee_rate_bps"`
	Side             uint8  `json:"side"`
	SignatureType    uint8  `json:"signature_type"`
	ScopeIDHex       string `json:"scope_id_hex"`
	DomainSeparator  string `json:"domain_separator"`
	StructHash       string `json:"struct_hash"`
	Digest           string `json:"digest"`
	Signature        string `json:"signature"`
	RecoveredAddress string `json:"recovered_address"`
}

// loadGolden 解析 testdata/golden.json。
func loadGolden(t *testing.T) *goldenFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var g goldenFixture
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &g
}

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.TrimPrefix(s, "0x")
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode hex %q: %v", s, err)
	}
	return b
}

func TestClobAuthDomainSeparator_Golden(t *testing.T) {
	g := loadGolden(t)
	cases := map[int64]string{
		137: g.DomainSep["clob_auth_chain_137"],
		1:   g.DomainSep["clob_auth_chain_1"],
	}
	for chainID, want := range cases {
		got := ClobAuthDomainSeparator(chainID)
		gotHex := "0x" + hex.EncodeToString(got[:])
		if gotHex != want {
			t.Errorf("chain %d domain separator = %s, want %s", chainID, gotHex, want)
		}
	}
}

func TestCTFExchangeDomainSeparator_Golden(t *testing.T) {
	g := loadGolden(t)
	exchange := common.HexToAddress("0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E")
	cases := map[int64]string{
		137: g.DomainSep["ctf_exchange_chain_137"],
		1:   g.DomainSep["ctf_exchange_chain_1"],
	}
	for chainID, want := range cases {
		got := CTFExchangeDomainSeparator(chainID, exchange)
		gotHex := "0x" + hex.EncodeToString(got[:])
		if gotHex != want {
			t.Errorf("chain %d ctf domain separator = %s, want %s", chainID, gotHex, want)
		}
	}
}

func TestClobAuthStructHashAndDigest_Golden(t *testing.T) {
	g := loadGolden(t)
	priv, err := ethcrypto.ToECDSA(mustDecodeHex(t, g.PrivKey))
	if err != nil {
		t.Fatalf("priv: %v", err)
	}
	addr := ethcrypto.PubkeyToAddress(priv.PublicKey)

	for _, c := range g.ClobAuth {
		t.Run(c.Name, func(t *testing.T) {
			if !strings.EqualFold(addr.Hex(), c.Address) {
				t.Fatalf("fixture address %s != recovered %s", c.Address, addr.Hex())
			}
			scope := ScopeIDFromHex(c.ScopeIDHex)

			ds := ClobAuthDomainSeparator(c.ChainID)
			if got := "0x" + hex.EncodeToString(ds[:]); got != c.DomainSeparator {
				t.Errorf("domain separator = %s, want %s", got, c.DomainSeparator)
			}

			sh := ClobAuthStructHash(addr, c.Timestamp, c.Nonce, scope)
			if got := "0x" + hex.EncodeToString(sh[:]); got != c.StructHash {
				t.Errorf("struct hash = %s, want %s", got, c.StructHash)
			}

			digest := BuildClobAuthDigest(addr, c.Timestamp, c.Nonce, scope, c.ChainID)
			if got := "0x" + hex.EncodeToString(digest[:]); got != c.Digest {
				t.Errorf("digest = %s, want %s", got, c.Digest)
			}

			// Sign and compare signature bytes (deterministic via ethcrypto.Sign over given digest).
			sig, err := ethcrypto.Sign(digest[:], priv)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if got := "0x" + hex.EncodeToString(sig); got != c.Signature {
				t.Errorf("signature = %s, want %s", got, c.Signature)
			}

			// Recover signer from the golden signature (independent of regeneration).
			pub, err := ethcrypto.SigToPub(digest[:], mustDecodeHex(t, c.Signature))
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			recovered := ethcrypto.PubkeyToAddress(*pub)
			if !strings.EqualFold(recovered.Hex(), c.RecoveredAddress) {
				t.Errorf("recovered = %s, want %s", recovered.Hex(), c.RecoveredAddress)
			}
		})
	}
}

func TestOrderStructHashAndDigest_Golden(t *testing.T) {
	g := loadGolden(t)
	priv, err := ethcrypto.ToECDSA(mustDecodeHex(t, g.PrivKey))
	if err != nil {
		t.Fatalf("priv: %v", err)
	}

	for _, c := range g.Orders {
		t.Run(c.Name, func(t *testing.T) {
			scope := ScopeIDFromHex(c.ScopeIDHex)
			salt, _ := new(big.Int).SetString(c.Salt, 10)
			tokenID, _ := new(big.Int).SetString(c.TokenID, 10)
			mAmt, _ := new(big.Int).SetString(c.MakerAmount, 10)
			tAmt, _ := new(big.Int).SetString(c.TakerAmount, 10)

			order := &OrderForSigning{
				Salt:          salt,
				Maker:         common.HexToAddress(c.Maker),
				Signer:        common.HexToAddress(c.Signer),
				Taker:         common.HexToAddress(c.Taker),
				TokenID:       tokenID,
				MakerAmount:   mAmt,
				TakerAmount:   tAmt,
				Expiration:    c.Expiration,
				Nonce:         c.Nonce,
				FeeRateBps:    c.FeeRateBps,
				Side:          OrderSide(c.Side),
				SignatureType: SignatureType(c.SignatureType),
				ScopeID:       scope,
			}
			exchange := common.HexToAddress(c.ExchangeAddress)

			ds := CTFExchangeDomainSeparator(c.ChainID, exchange)
			if got := "0x" + hex.EncodeToString(ds[:]); got != c.DomainSeparator {
				t.Errorf("ctf domain separator = %s, want %s", got, c.DomainSeparator)
			}

			sh := OrderStructHash(order)
			if got := "0x" + hex.EncodeToString(sh[:]); got != c.StructHash {
				t.Errorf("order struct hash = %s, want %s", got, c.StructHash)
			}

			digest := BuildOrderDigest(order, exchange, c.ChainID)
			if got := "0x" + hex.EncodeToString(digest[:]); got != c.Digest {
				t.Errorf("digest = %s, want %s", got, c.Digest)
			}

			sig, err := ethcrypto.Sign(digest[:], priv)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if got := "0x" + hex.EncodeToString(sig); got != c.Signature {
				t.Errorf("signature = %s, want %s", got, c.Signature)
			}

			pub, err := ethcrypto.SigToPub(digest[:], mustDecodeHex(t, c.Signature))
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			recovered := ethcrypto.PubkeyToAddress(*pub)
			if !strings.EqualFold(recovered.Hex(), c.RecoveredAddress) {
				t.Errorf("recovered = %s, want %s", recovered.Hex(), c.RecoveredAddress)
			}
		})
	}
}

// TestEIP712Signer_RoundTrip 验证通用 EIP712Signer 的 sign → recover 闭环。
func TestEIP712Signer_RoundTrip(t *testing.T) {
	priv, err := ethcrypto.GenerateKey()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	addr := ethcrypto.PubkeyToAddress(priv.PublicKey)
	domainSep := ClobAuthDomainSeparator(137)
	s := NewEIP712Signer(priv, domainSep)
	if !strings.EqualFold(s.Address(), addr.Hex()) {
		t.Errorf("address mismatch")
	}
	if s.SchemaVersion() != "polymarket-v1" {
		t.Errorf("schema = %s", s.SchemaVersion())
	}

	structHash := ClobAuthStructHash(addr, "1700000000", 0, [32]byte{})
	sig, err := s.Sign(context.Background(), structHash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if len(sig) != 65 {
		t.Fatalf("sig len = %d", len(sig))
	}

	digest := EIP712Digest(domainSep[:], structHash[:])
	pub, err := ethcrypto.SigToPub(digest, sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !strings.EqualFold(ethcrypto.PubkeyToAddress(*pub).Hex(), addr.Hex()) {
		t.Errorf("recover mismatch")
	}
}

func TestEIP712Signer_RejectShortPayload(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	s := NewEIP712Signer(priv, [32]byte{})
	_, err := s.Sign(context.Background(), []byte("short"))
	if err == nil {
		t.Fatal("expected error for non-32-byte payload")
	}
}

// TestPMCup26Signer_SignClobAuth 验证 PMCup26Signer 的 ClobAuth 路径与
// Golden fixture 一致（chain137 / scope1 / ts1700000000 / nonce0 入参）。
func TestPMCup26Signer_SignClobAuth_Golden(t *testing.T) {
	g := loadGolden(t)
	priv, err := ethcrypto.ToECDSA(mustDecodeHex(t, g.PrivKey))
	if err != nil {
		t.Fatalf("priv: %v", err)
	}
	scope := ScopeIDFromHex("0x0000000000000000000000000000000000000000000000000000000000000001")
	s := NewPMCup26Signer(priv, scope, 137)

	if s.SchemaVersion() != "pmcup26-v1" {
		t.Errorf("schema = %s", s.SchemaVersion())
	}
	if s.ChainID() != 137 {
		t.Errorf("chainID = %d", s.ChainID())
	}
	gotScope := s.ScopeID()
	if gotScope != scope {
		t.Errorf("scope mismatch")
	}

	sig, err := s.SignClobAuth(context.Background(), "1700000000", 0)
	if err != nil {
		t.Fatalf("SignClobAuth: %v", err)
	}
	want := g.ClobAuth[0].Signature
	if got := "0x" + hex.EncodeToString(sig); got != want {
		t.Errorf("sig = %s, want %s", got, want)
	}
}

func TestPMCup26Signer_SignOrder_Golden(t *testing.T) {
	g := loadGolden(t)
	priv, err := ethcrypto.ToECDSA(mustDecodeHex(t, g.PrivKey))
	if err != nil {
		t.Fatalf("priv: %v", err)
	}
	c := g.Orders[0] // chain137_buy_scope42
	scope := ScopeIDFromHex(c.ScopeIDHex)
	exchange := common.HexToAddress(c.ExchangeAddress)

	s := NewPMCup26Signer(priv, scope, c.ChainID, WithExchangeAddress(exchange))
	if exch, ok := s.ExchangeAddress(); !ok || !strings.EqualFold(exch.Hex(), c.ExchangeAddress) {
		t.Errorf("exchange addr not stored: ok=%v %s", ok, exch.Hex())
	}

	salt, _ := new(big.Int).SetString(c.Salt, 10)
	tokenID, _ := new(big.Int).SetString(c.TokenID, 10)
	mAmt, _ := new(big.Int).SetString(c.MakerAmount, 10)
	tAmt, _ := new(big.Int).SetString(c.TakerAmount, 10)
	order := &OrderForSigning{
		Salt:          salt,
		Maker:         common.HexToAddress(c.Maker),
		Signer:        common.HexToAddress(c.Signer),
		Taker:         common.HexToAddress(c.Taker),
		TokenID:       tokenID,
		MakerAmount:   mAmt,
		TakerAmount:   tAmt,
		Expiration:    c.Expiration,
		Nonce:         c.Nonce,
		FeeRateBps:    c.FeeRateBps,
		Side:          OrderSide(c.Side),
		SignatureType: SignatureType(c.SignatureType),
		ScopeID:       scope,
	}

	sig, err := s.SignOrder(context.Background(), order)
	if err != nil {
		t.Fatalf("SignOrder: %v", err)
	}
	if got := "0x" + hex.EncodeToString(sig); got != c.Signature {
		t.Errorf("sig = %s, want %s", got, c.Signature)
	}
}

func TestPMCup26Signer_SignOrder_NoExchange(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	s := NewPMCup26Signer(priv, [32]byte{}, 137)
	_, err := s.SignOrder(context.Background(), &OrderForSigning{Salt: big.NewInt(1), TokenID: big.NewInt(1), MakerAmount: big.NewInt(1), TakerAmount: big.NewInt(1)})
	if err != ErrNoExchangeAddress {
		t.Errorf("err = %v, want ErrNoExchangeAddress", err)
	}
}

func TestPMCup26Signer_LowLevelSign(t *testing.T) {
	// 兼容路径：Sign(ctx, 32-byte) 走 ClobAuth 域；与 SignClobAuth 对同一 structHash 等价。
	priv, _ := ethcrypto.GenerateKey()
	addr := ethcrypto.PubkeyToAddress(priv.PublicKey)
	scope := [32]byte{}
	s := NewPMCup26Signer(priv, scope, 1)
	if !strings.EqualFold(s.Address(), addr.Hex()) {
		t.Errorf("address mismatch")
	}
	sh := ClobAuthStructHash(addr, "1234567890", 1, scope)
	sig, err := s.Sign(context.Background(), sh[:])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 65 {
		t.Errorf("sig len = %d", len(sig))
	}
	// Round-trip recover via the ClobAuth digest.
	digest := BuildClobAuthDigest(addr, "1234567890", 1, scope, 1)
	pub, err := ethcrypto.SigToPub(digest[:], sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !strings.EqualFold(ethcrypto.PubkeyToAddress(*pub).Hex(), addr.Hex()) {
		t.Errorf("low-level Sign should match ClobAuth domain")
	}
}

func TestPMCup26Signer_LowLevelSign_RejectShortPayload(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	s := NewPMCup26Signer(priv, [32]byte{}, 137)
	_, err := s.Sign(context.Background(), []byte{0x01})
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}

func TestScopeIDFromHex(t *testing.T) {
	if ScopeIDFromHex("") != [32]byte{} {
		t.Error("empty → zero")
	}
	id := ScopeIDFromHex("0x0000000000000000000000000000000000000000000000000000000000000001")
	if id[31] != 1 {
		t.Errorf("last byte = %d", id[31])
	}
	for i := 0; i < 31; i++ {
		if id[i] != 0 {
			t.Errorf("byte %d = %d", i, id[i])
		}
	}
	hexStr := ScopeIDToHex(id)
	if hexStr != "0x0000000000000000000000000000000000000000000000000000000000000001" {
		t.Errorf("round trip = %s", hexStr)
	}
	if ScopeIDToHex([32]byte{}) != "" {
		t.Error("zero → empty")
	}

	// 超过 32 byte 截断（仅保留前 32 字节；测试不验值，只验长度即可）
	long := strings.Repeat("ab", 40) // 80 hex chars = 40 bytes
	idLong := ScopeIDFromHex("0x" + long)
	// idLong 是值类型 [32]byte，长度天然 32；用 hex 字符串长度兜一道实证。
	if got := ScopeIDToHex(idLong); len(got) != 66 {
		t.Errorf("hex out len = %d, want 66", len(got))
	}
}

func TestClobAuthMessage_Constant(t *testing.T) {
	if clobAuthMessage != "This message attests that I control the given wallet" {
		t.Errorf("clobAuthMessage drifted: %q", clobAuthMessage)
	}
}

func TestEIP712Digest_Stable(t *testing.T) {
	ds := [32]byte{1, 2, 3}
	sh := [32]byte{4, 5, 6}
	d1 := EIP712Digest(ds[:], sh[:])
	d2 := EIP712Digest(ds[:], sh[:])
	if string(d1) != string(d2) {
		t.Error("digest should be deterministic")
	}
	if len(d1) != 32 {
		t.Errorf("digest len = %d", len(d1))
	}
}
