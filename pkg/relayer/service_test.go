package relayer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

// fakeSigner 是 DigestSigner 的 mock：捕获 SignDigest 入参 + 返回固定 65 byte。
type fakeSigner struct {
	addr      common.Address
	gotDigest [32]byte
	calls     int
	returnSig []byte
	err       error
}

func (f *fakeSigner) Address() common.Address { return f.addr }
func (f *fakeSigner) SignDigest(_ context.Context, d [32]byte) ([]byte, error) {
	f.calls++
	f.gotDigest = d
	if f.err != nil {
		return nil, f.err
	}
	return f.returnSig, nil
}

func newFakeSigner() *fakeSigner {
	sig := make([]byte, 65)
	sig[0] = 0xaa
	sig[64] = 0 // 模拟 ethcrypto.Sign 的 v=0；Service 应自动加 27
	return &fakeSigner{
		addr:      common.HexToAddress("0x9d8A62f656a8d1615C1294fd71e9CFb3E4855A4F"),
		returnSig: sig,
	}
}

// TestNewServiceValidation 校验 NewService 入参检查。
func TestNewServiceValidation(t *testing.T) {
	c, _ := NewClient("http://x")
	signer := newFakeSigner()
	factory := common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041")

	if _, err := NewService(nil, signer, 1, factory); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil client: %v", err)
	}
	if _, err := NewService(c, nil, 1, factory); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil signer: %v", err)
	}
	if _, err := NewService(c, signer, 1, common.Address{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero factory: %v", err)
	}
}

// TestNewPrivateKeySigner 验证 NewPrivateKeySigner 派生地址 + ECDSA 签名生成。
func TestNewPrivateKeySigner(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	sg, err := NewPrivateKeySigner(priv)
	if err != nil {
		t.Fatalf("NewPrivateKeySigner: %v", err)
	}
	if sg.Address() != ethcrypto.PubkeyToAddress(priv.PublicKey) {
		t.Fatalf("address mismatch")
	}
	digest := [32]byte{1, 2, 3}
	sig, err := sg.SignDigest(context.Background(), digest)
	if err != nil || len(sig) != 65 {
		t.Fatalf("sign: len=%d err=%v", len(sig), err)
	}
	pub, err := ethcrypto.SigToPub(digest[:], sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if ethcrypto.PubkeyToAddress(*pub) != sg.Address() {
		t.Fatalf("recovered address mismatch")
	}

	if _, err := NewPrivateKeySigner(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil priv: %v", err)
	}
}

// serviceTestState 收集 mock relayer-service 的请求历史。
type serviceTestState struct {
	deployedCalls   atomic.Int32
	nonceCalls      atomic.Int32
	submitCalls     atomic.Int32
	submitBody      []byte
	transactionPoll atomic.Int32

	deployedResp string
	nonceResp    string
	submitResp   string
	txResponses  []string // 每次 GetTransaction 顺序返回
}

func newServiceMock(t *testing.T, st *serviceTestState) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/deployed"):
			st.deployedCalls.Add(1)
			_, _ = w.Write([]byte(st.deployedResp))
		case strings.HasPrefix(r.URL.Path, "/nonce"):
			st.nonceCalls.Add(1)
			_, _ = w.Write([]byte(st.nonceResp))
		case r.URL.Path == "/submit":
			st.submitCalls.Add(1)
			st.submitBody, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(st.submitResp))
		case strings.HasPrefix(r.URL.Path, "/transaction"):
			i := int(st.transactionPoll.Add(1)) - 1
			if i >= len(st.txResponses) {
				i = len(st.txResponses) - 1
			}
			_, _ = w.Write([]byte(st.txResponses[i]))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestDeploySafe_HappyPath(t *testing.T) {
	st := &serviceTestState{
		deployedResp: `{"deployed":false,"address":"0x2100186071afd66c5d4f5108cF2BB47b13c08946"}`,
		submitResp:   `{"transactionID":"tx-deploy","state":"STATE_NEW"}`,
		txResponses: []string{
			`{"state":"STATE_NEW"}`,
			`{"state":"STATE_CONFIRMED","transactionHash":"0xfinal"}`,
		},
	}
	srv := newServiceMock(t, st)
	defer srv.Close()

	client, _ := NewClient(srv.URL, WithAPIKey("k"))
	signer := newFakeSigner()
	factory := common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041")
	svc, err := NewService(client, signer, 11155420, factory,
		WithPollInterval(5*time.Millisecond), WithPollMaxAttempts(20))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	txID, safeAddr, err := svc.DeploySafe(context.Background(), "0x083ff7c1bc4972eef065542fc562d42e91b706719b313a95bf59eb0338a97fe7",
		common.Address{}, nil, common.Address{})
	if err != nil {
		t.Fatalf("DeploySafe: %v", err)
	}
	if txID != "tx-deploy" {
		t.Fatalf("txID: %s", txID)
	}
	if !strings.EqualFold(safeAddr, "0x2100186071afd66c5d4f5108cF2BB47b13c08946") {
		t.Fatalf("safeAddr: %s", safeAddr)
	}
	if signer.calls != 1 {
		t.Fatalf("signer should have signed once, got %d", signer.calls)
	}

	// 检查 submit body 合法 + signature 已 v→27
	var req SubmitRequest
	if err := json.Unmarshal(st.submitBody, &req); err != nil {
		t.Fatalf("decode submit body: %v", err)
	}
	if req.Type != TxTypeSafeCreate {
		t.Fatalf("type: %s", req.Type)
	}
	if !strings.EqualFold(req.To, factory.Hex()) {
		t.Fatalf("to: %s", req.To)
	}
	if req.From != strings.ToLower(signer.addr.Hex()) {
		t.Fatalf("from must be lowercased EOA, got %s", req.From)
	}
	if !strings.HasSuffix(req.Signature, "1b") { // 0x1b = 27
		t.Fatalf("signature v not normalized to 27/28: %s", req.Signature)
	}
	var params SafeCreateParams
	if err := json.Unmarshal(req.SignatureParams, &params); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if params.ScopeId == "" || params.Payment != "0" {
		t.Fatalf("params: %+v", params)
	}
}

func TestDeploySafe_AlreadyDeployed(t *testing.T) {
	st := &serviceTestState{
		deployedResp: `{"deployed":true,"address":"0xexisting"}`,
	}
	srv := newServiceMock(t, st)
	defer srv.Close()

	client, _ := NewClient(srv.URL, WithAPIKey("k"))
	signer := newFakeSigner()
	svc, _ := NewService(client, signer, 1, common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041"),
		WithPollInterval(time.Millisecond))

	txID, safeAddr, err := svc.DeploySafe(context.Background(), "0xff", common.Address{}, nil, common.Address{})
	if err != nil {
		t.Fatalf("DeploySafe: %v", err)
	}
	if txID != "" || safeAddr != "0xexisting" {
		t.Fatalf("expected empty txID + existing addr, got txID=%s addr=%s", txID, safeAddr)
	}
	if st.submitCalls.Load() != 0 {
		t.Fatalf("submit should not be called when already deployed")
	}
	if signer.calls != 0 {
		t.Fatalf("signer should not be invoked when already deployed")
	}
}

func TestDeploySafe_MissingScope(t *testing.T) {
	client, _ := NewClient("http://x", WithAPIKey("k"))
	signer := newFakeSigner()
	svc, _ := NewService(client, signer, 1, common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041"))
	_, _, err := svc.DeploySafe(context.Background(), "", common.Address{}, nil, common.Address{})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("got %v, want ErrInvalidConfig", err)
	}
}

func TestExecuteSafeTx_HappyPath(t *testing.T) {
	st := &serviceTestState{
		nonceResp:  `{"nonce":"5"}`,
		submitResp: `{"transactionID":"tx-exec","state":"STATE_NEW"}`,
		txResponses: []string{
			`{"state":"STATE_EXECUTED","transactionHash":"0xtemp"}`,
			`{"state":"STATE_CONFIRMED","transactionHash":"0xfinalhash"}`,
		},
	}
	srv := newServiceMock(t, st)
	defer srv.Close()

	client, _ := NewClient(srv.URL, WithAPIKey("k"))
	signer := newFakeSigner()
	factory := common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041")
	svc, _ := NewService(client, signer, 11155420, factory,
		WithPollInterval(5*time.Millisecond), WithPollMaxAttempts(20))

	safeAddr := common.HexToAddress("0x2100186071afd66c5d4f5108cF2BB47b13c08946")
	to := common.HexToAddress("0x508A62Bd6A37b03dB215c6aAb82Fc1683e95Abf4")
	calldata := []byte{0xde, 0xad, 0xbe, 0xef}

	txID, hash, err := svc.ExecuteSafeTx(context.Background(), safeAddr, to, calldata, "0xff")
	if err != nil {
		t.Fatalf("ExecuteSafeTx: %v", err)
	}
	if txID != "tx-exec" {
		t.Fatalf("txID: %s", txID)
	}
	if hash != "0xfinalhash" {
		t.Fatalf("hash should be from confirmed state, got %s", hash)
	}
	if st.nonceCalls.Load() != 1 {
		t.Fatalf("nonce should be queried once, got %d", st.nonceCalls.Load())
	}
	if signer.calls != 1 {
		t.Fatalf("signer should have signed once")
	}

	var req SubmitRequest
	if err := json.Unmarshal(st.submitBody, &req); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	if req.Type != TxTypeSafe || req.Nonce != "5" {
		t.Fatalf("submit body: %+v", req)
	}
	if req.Data != "0xdeadbeef" {
		t.Fatalf("calldata: %s", req.Data)
	}
}

func TestExecuteSafeTx_TerminalFailure(t *testing.T) {
	st := &serviceTestState{
		nonceResp:  `{"nonce":"0"}`,
		submitResp: `{"transactionID":"tx-fail","state":"STATE_NEW"}`,
		txResponses: []string{
			`{"state":"STATE_FAILED","errorMessage":"reverted: bad calldata"}`,
		},
	}
	srv := newServiceMock(t, st)
	defer srv.Close()

	client, _ := NewClient(srv.URL, WithAPIKey("k"))
	signer := newFakeSigner()
	svc, _ := NewService(client, signer, 1, common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041"),
		WithPollInterval(time.Millisecond))

	_, _, err := svc.ExecuteSafeTx(context.Background(),
		common.HexToAddress("0x1"), common.HexToAddress("0x2"), nil, "0xff")
	if !errors.Is(err, ErrTxFailed) {
		t.Fatalf("got %v, want ErrTxFailed", err)
	}
}

func TestWaitTerminal_Timeout(t *testing.T) {
	st := &serviceTestState{
		txResponses: []string{`{"state":"STATE_NEW"}`},
	}
	srv := newServiceMock(t, st)
	defer srv.Close()

	client, _ := NewClient(srv.URL, WithAPIKey("k"))
	signer := newFakeSigner()
	svc, _ := NewService(client, signer, 1, common.HexToAddress("0x4BEb566a2bBb875b203D11192D04bB2EEF8d9041"),
		WithPollInterval(time.Millisecond), WithPollMaxAttempts(3))

	err := svc.waitTerminal(context.Background(), "tx-x")
	if !errors.Is(err, ErrTxTimeout) {
		t.Fatalf("got %v, want ErrTxTimeout", err)
	}
}
