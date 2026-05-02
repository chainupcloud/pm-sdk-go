package clob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/shopspring/decimal"

	pmsigner "github.com/chainupcloud/pm-sdk-go/pkg/signer"
)

// ---------- mock signer ----------

type mockSigner struct {
	addr string
	sig  []byte
	err  error
}

func (m *mockSigner) Sign(_ context.Context, _ []byte) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.sig == nil {
		return []byte{0xde, 0xad, 0xbe, 0xef}, nil
	}
	return m.sig, nil
}
func (m *mockSigner) Address() string {
	if m.addr == "" {
		return "0x1234567890abcdef1234567890abcdef12345678"
	}
	return m.addr
}
func (m *mockSigner) SchemaVersion() string { return "test-v1" }

// ---------- helpers ----------

func newTestServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Facade) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	return srv, f
}

// ---------- happy path ----------

func TestPlaceOrder_Happy(t *testing.T) {
	var receivedBody SendOrder
	var receivedClientOrder string
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/order" {
			t.Errorf("path = %q, want /order", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		receivedClientOrder = r.Header.Get("X-Client-Order-Id")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedBody); err != nil {
			t.Fatalf("decode SendOrder: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"orderID":"12345","status":"live"}`))
	})

	id, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID:    "0xmarket",
		TokenID:     "100200300",
		Side:        SideBuy,
		OrderType:   OrderTypeLimit,
		Price:       decimal.RequireFromString("0.55"),
		Size:        decimal.RequireFromString("10"),
		ClientOrder: "client-001",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if id != "12345" {
		t.Errorf("OrderID = %q, want 12345", id)
	}
	if receivedClientOrder != "client-001" {
		t.Errorf("X-Client-Order-Id = %q", receivedClientOrder)
	}
	if receivedBody.Order.Side != BUY {
		t.Errorf("upstream Side = %q, want BUY", receivedBody.Order.Side)
	}
	if receivedBody.OrderType == nil || *receivedBody.OrderType != GTC {
		t.Errorf("upstream OrderType = %v, want GTC", receivedBody.OrderType)
	}
	if receivedBody.Order.Signature == "" {
		t.Error("Signature should be populated by mock signer")
	}
}

func TestPlaceOrder_NoSigner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500) // 不应被调用
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client()) // 故意不注入 signer
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.PlaceOrder(context.Background(), OrderReq{
		MarketID: "m", TokenID: "t", Side: SideBuy, OrderType: OrderTypeLimit,
		Price: decimal.NewFromInt(1), Size: decimal.NewFromInt(1),
	})
	if !errors.Is(err, ErrSign) {
		t.Errorf("err = %v, want ErrSign", err)
	}
}

func TestPlaceOrder_BadAmounts(t *testing.T) {
	_, f := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called for bad amounts")
	})
	_, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID: "m", TokenID: "t", Side: SideBuy, OrderType: OrderTypeLimit,
		Price: decimal.Zero, Size: decimal.NewFromInt(1),
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

func TestGetOrder_Happy(t *testing.T) {
	createdAt := 1700000000
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/order/") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		idStr := "0xabc"
		market := "0xmarket"
		asset := "100200300"
		price := "0.55"
		size := "10"
		matched := "3"
		side := BUY
		otype := GTC
		status := ORDERSTATUSLIVE
		oo := OpenOrder{
			Id:           &idStr,
			Market:       &market,
			AssetId:      &asset,
			Price:        &price,
			OriginalSize: &size,
			SizeMatched:  &matched,
			Side:         &side,
			OrderType:    &otype,
			Status:       &status,
			CreatedAt:    &createdAt,
		}
		b, _ := json.Marshal(oo)
		_, _ = w.Write(b)
	})
	o, err := f.GetOrder(context.Background(), "0xabc")
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if o.ID != "0xabc" {
		t.Errorf("ID = %q", o.ID)
	}
	if o.Side != SideBuy {
		t.Errorf("Side = %q", o.Side)
	}
	if o.OrderType != OrderTypeLimit {
		t.Errorf("OrderType = %q", o.OrderType)
	}
	if o.Status != OrderStatusPartiallyFilled {
		t.Errorf("Status = %q, want PARTIALLY_FILLED (matched > 0)", o.Status)
	}
	if !o.Price.Equal(decimal.RequireFromString("0.55")) {
		t.Errorf("Price = %s", o.Price)
	}
	if !o.Filled.Equal(decimal.NewFromInt(3)) {
		t.Errorf("Filled = %s", o.Filled)
	}
}

func TestGetBook_Happy(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/book" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("token_id") != "100200300" {
			t.Errorf("token_id = %q", r.URL.Query().Get("token_id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"asset_id":"100200300",
			"timestamp":1700000000,
			"bids":[{"price":"0.54","size":"100"},{"price":"0.53","size":"200"}],
			"asks":[{"price":"0.56","size":"50"}]
		}`))
	})
	b, err := f.GetBook(context.Background(), "100200300")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if len(b.Bids) != 2 || len(b.Asks) != 1 {
		t.Fatalf("levels = bids:%d asks:%d", len(b.Bids), len(b.Asks))
	}
	if !b.Bids[0].Price.Equal(decimal.RequireFromString("0.54")) {
		t.Errorf("bid[0].Price = %s", b.Bids[0].Price)
	}
	if !b.Asks[0].Size.Equal(decimal.NewFromInt(50)) {
		t.Errorf("ask[0].Size = %s", b.Asks[0].Size)
	}
}

func TestListOrders_Happy(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("market") != "0xmarket" {
			t.Errorf("market filter = %q", r.URL.Query().Get("market"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data":[{"id":"o1","market":"0xmarket","asset_id":"t1","price":"0.5","original_size":"10","size_matched":"0","status":"ORDER_STATUS_LIVE","side":"BUY","order_type":"GTC"}],
			"next_cursor":"LTE="
		}`))
	})
	orders, cursor, err := f.ListOrders(context.Background(), OrderFilter{MarketID: "0xmarket"})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("len(orders) = %d", len(orders))
	}
	if cursor != "LTE=" {
		t.Errorf("cursor = %q", cursor)
	}
	if orders[0].Status != OrderStatusOpen {
		t.Errorf("Status = %q", orders[0].Status)
	}
}

func TestGetTrades_Happy(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("maker_address") != "0xabc" {
			t.Errorf("maker_address = %q", r.URL.Query().Get("maker_address"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data":[{"id":"t1","market":"0xm","asset_id":"tok","price":"0.5","size":"3","side":"SELL","status":"TRADE_STATUS_MATCHED","trader_side":"TAKER","taker_order_id":"o1","match_time":"2026-04-27T10:00:00Z"}],
			"next_cursor":""
		}`))
	})
	trades, _, err := f.GetTrades(context.Background(), TradeFilter{MakerAddress: "0xabc"})
	if err != nil {
		t.Fatalf("GetTrades: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("len(trades) = %d", len(trades))
	}
	if trades[0].Side != SideSell {
		t.Errorf("Side = %q", trades[0].Side)
	}
	if trades[0].OrderID != "o1" {
		t.Errorf("OrderID = %q", trades[0].OrderID)
	}
}

func TestGetTrades_MissingMaker(t *testing.T) {
	_, f := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called")
	})
	_, _, err := f.GetTrades(context.Background(), TradeFilter{})
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

func TestCancelOrder_Happy(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/order" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"orderID":"order-1"`) {
			t.Errorf("body = %s", body)
		}
		_, _ = w.Write([]byte(`{"canceled":["order-1"]}`))
	})
	if err := f.CancelOrder(context.Background(), "order-1"); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
}

func TestCancelOrder_EmptyID(t *testing.T) {
	_, f := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called")
	})
	err := f.CancelOrder(context.Background(), "")
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

// ---------- error mapping ----------

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"401_to_ErrSign", http.StatusUnauthorized, ErrSign},
		{"403_to_ErrSign", http.StatusForbidden, ErrSign},
		{"404_to_ErrNotFound", http.StatusNotFound, ErrNotFound},
		{"412_to_ErrPrecondition", http.StatusPreconditionFailed, ErrPrecondition},
		{"422_to_ErrPrecondition", http.StatusUnprocessableEntity, ErrPrecondition},
		{"429_to_ErrRateLimit", http.StatusTooManyRequests, ErrRateLimit},
		{"502_to_ErrUpstream", http.StatusBadGateway, ErrUpstream},
		{"500_to_ErrUpstream", http.StatusInternalServerError, ErrUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Request-Id", "req-xyz")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"upstream said no"}`))
			})
			_, err := f.GetBook(context.Background(), "100")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v (%T), want errors.Is %v", err, err, tc.want)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errors.As(*APIError) failed")
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d", apiErr.StatusCode)
			}
			if apiErr.Message != "upstream said no" {
				t.Errorf("Message = %q", apiErr.Message)
			}
			if apiErr.RequestID != "req-xyz" {
				t.Errorf("RequestID = %q", apiErr.RequestID)
			}
			if apiErr.Error() == "" {
				t.Error("Error() empty")
			}
		})
	}
}

func TestErrorMapping_NoBody(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := f.GetBook(context.Background(), "100")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("not APIError")
	}
	if apiErr.Message == "" {
		t.Error("fallback message should be http.StatusText")
	}
}

// ---------- ctx cancel / timeout ----------

func TestCtxCancel(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 模拟慢响应
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(200)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := f.GetBook(ctx, "100")
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

func TestCtxTimeout(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(200)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.GetBook(ctx, "100")
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

// ---------- enum mapping unit tests ----------

func TestSideMapping(t *testing.T) {
	if mapSide(SideBuy) != BUY || mapSide(SideSell) != SELL {
		t.Error("forward mapping wrong")
	}
	bs := BUY
	ss := SELL
	if mapSideReverse(&bs) != SideBuy || mapSideReverse(&ss) != SideSell {
		t.Error("reverse mapping wrong")
	}
	if mapSideReverse(nil) != "" {
		t.Error("nil reverse should be empty")
	}
}

func TestOrderTypeMapping(t *testing.T) {
	if mapOrderType(OrderTypeLimit) != GTC || mapOrderType(OrderTypeMarket) != FAK {
		t.Error("forward mapping wrong")
	}
	gtc := GTC
	fak := FAK
	if mapOrderTypeReverse(&gtc) != OrderTypeLimit || mapOrderTypeReverse(&fak) != OrderTypeMarket {
		t.Error("reverse mapping wrong")
	}
}

func TestOrderStatusMapping(t *testing.T) {
	live := ORDERSTATUSLIVE
	matched := ORDERSTATUSMATCHED
	cancelled := ORDERSTATUSCANCELED
	invalid := ORDERSTATUSINVALID
	if mapOrderStatusReverse(&live) != OrderStatusOpen {
		t.Error("LIVE → OPEN failed")
	}
	if mapOrderStatusReverse(&matched) != OrderStatusFilled {
		t.Error("MATCHED → FILLED failed")
	}
	if mapOrderStatusReverse(&cancelled) != OrderStatusCancelled {
		t.Error("CANCELED → CANCELLED failed")
	}
	if mapOrderStatusReverse(&invalid) != OrderStatusRejected {
		t.Error("INVALID → REJECTED failed")
	}
	if mapOrderStatusReverse(nil) != "" {
		t.Error("nil reverse not empty")
	}
}

func TestAPIError_NilSafety(t *testing.T) {
	var nilErr *APIError
	if nilErr.Error() != "" {
		t.Error("nil APIError.Error() should be empty")
	}
	if nilErr.Unwrap() != nil {
		t.Error("nil APIError.Unwrap() should be nil")
	}

	withCode := &APIError{StatusCode: 400, Code: "bad_request", Message: "x"}
	if withCode.Error() == "" {
		t.Error("Error() with code should be non-empty")
	}
}

func TestWrapHTTPError_NilResp(t *testing.T) {
	err := wrapHTTPError(nil, nil)
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("nil resp should map to ErrUpstream, got %v", err)
	}
}

func TestWrapHTTPError_LargeBody(t *testing.T) {
	resp := &http.Response{StatusCode: 500, Header: http.Header{}}
	big := make([]byte, maxStoredBody*2)
	for i := range big {
		big[i] = 'A'
	}
	err := wrapHTTPError(resp, big)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("not APIError")
	}
	if len(apiErr.Body) != maxStoredBody {
		t.Errorf("Body trimmed = %d, want %d", len(apiErr.Body), maxStoredBody)
	}
}

// TestPlaceOrder_PMCup26Signer 验证 Phase 6 的 EIP-712 签名 wiring：当 Facade 注入
// *PMCup26Signer 时，PlaceOrder 会走 SignOrder 完整路径（13-field EIP-712）并把
// scopeId / signatureType 等字段正确填入 generated.Order；signature 应能被 ecrecover
// 反推出 signer 地址。
func TestPlaceOrder_PMCup26Signer(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	signerAddr := ethcrypto.PubkeyToAddress(priv.PublicKey)

	exchange := common.HexToAddress("0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E")
	scope := pmsigner.ScopeIDFromHex("0x0000000000000000000000000000000000000000000000000000000000000042")
	chainID := int64(137)

	pms := pmsigner.NewPMCup26Signer(priv, scope, chainID, pmsigner.WithExchangeAddress(exchange))

	var captured SendOrder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderID":"o-eip712-1"}`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(pms))
	if err != nil {
		t.Fatal(err)
	}

	id, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID:    "0xmarket",
		TokenID:     "100200300",
		Side:        SideBuy,
		OrderType:   OrderTypeLimit,
		Price:       decimal.RequireFromString("0.55"),
		Size:        decimal.RequireFromString("10"),
		ClientOrder: "client-eip712",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if id != "o-eip712-1" {
		t.Errorf("OrderID = %q", id)
	}

	// scopeId hex 必须出现在 wire 上
	if captured.Order.ScopeId == nil || *captured.Order.ScopeId != "0x0000000000000000000000000000000000000000000000000000000000000042" {
		t.Errorf("ScopeId = %v", captured.Order.ScopeId)
	}
	if captured.Order.SignatureType == nil || *captured.Order.SignatureType != N0 {
		t.Errorf("SignatureType = %v", captured.Order.SignatureType)
	}
	if captured.Order.Maker != signerAddr.Hex() {
		t.Errorf("Maker = %s, want %s", captured.Order.Maker, signerAddr.Hex())
	}

	// 反向验证签名：用相同 OrderForSigning 算 digest，ecrecover 应等于 signer 地址。
	// 金额按 6-decimal raw integer（issue #9 toBaseUnits）：
	// makerAmount = 0.55 * 10 * 10^6 = 5_500_000；takerAmount = 10 * 10^6 = 10_000_000。
	tokenIDBig := new(big.Int).SetUint64(100200300)
	mAmt := big.NewInt(5_500_000)
	tAmt := big.NewInt(10_000_000)

	order := &pmsigner.OrderForSigning{
		Salt:          big.NewInt(0),
		Maker:         signerAddr,
		Signer:        signerAddr,
		Taker:         common.Address{},
		TokenID:       tokenIDBig,
		MakerAmount:   mAmt,
		TakerAmount:   tAmt,
		Expiration:    0,
		Nonce:         0,
		FeeRateBps:    0,
		Side:          pmsigner.OrderSideBuy,
		SignatureType: pmsigner.SignatureTypeEOA,
		ScopeID:       scope,
	}
	digest := pmsigner.BuildOrderDigest(order, exchange, chainID)
	sigHex := captured.Order.Signature
	if len(sigHex) < 4 || sigHex[:2] != "0x" {
		t.Fatalf("signature not hex: %q", sigHex)
	}
	sigBytes, err := hexDecode(sigHex[2:])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	pub, err := ethcrypto.SigToPub(digest[:], sigBytes)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	got := ethcrypto.PubkeyToAddress(*pub)
	if got != signerAddr {
		t.Errorf("recovered = %s, want %s", got.Hex(), signerAddr.Hex())
	}
}

// hexDecode 是测试本地 hex helper，避免引入额外依赖。
func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		var hi, lo byte
		var err error
		hi, err = hexDigit(s[2*i])
		if err != nil {
			return nil, err
		}
		lo, err = hexDigit(s[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexDigit(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, fmt.Errorf("bad hex char %q", c)
}

// TestPlaceOrder_PolyGnosisSafe_Maker 验证 issue #15：当传入 SignatureType=POLY_GNOSIS_SAFE
// 且 Maker=Safe 地址时，wire payload Order.Maker 是 Safe（不是 Signer EOA），Order.Signer 仍是
// EOA hex，Order.SignatureType="2"；签名仍是 EOA 私钥对 EIP-712 digest 的输出，ecrecover
// 反推应得 Signer EOA 地址。Maker / SignatureType 同步进 EIP-712 OrderForSigning。
func TestPlaceOrder_PolyGnosisSafe_Maker(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	signerAddr := ethcrypto.PubkeyToAddress(priv.PublicKey)
	safeAddr := common.HexToAddress("0xAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAaAa")

	exchange := common.HexToAddress("0x4bFb41d5B3570DeFd03C39a9A4D8dE6Bd8B8982E")
	scope := pmsigner.ScopeIDFromHex("0x0000000000000000000000000000000000000000000000000000000000000042")
	chainID := int64(137)

	pms := pmsigner.NewPMCup26Signer(priv, scope, chainID, pmsigner.WithExchangeAddress(exchange))

	var captured SendOrder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orderID":"o-safe-1"}`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(pms))
	if err != nil {
		t.Fatal(err)
	}

	id, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID:      "0xmarket",
		TokenID:       "100200300",
		Side:          SideBuy,
		OrderType:     OrderTypeLimit,
		Price:         decimal.RequireFromString("0.55"),
		Size:          decimal.RequireFromString("10"),
		ClientOrder:   "client-safe",
		SignatureType: pmsigner.SignatureTypePolyGnosisSafe,
		Maker:         safeAddr,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if id != "o-safe-1" {
		t.Errorf("OrderID = %q", id)
	}
	if captured.Order.Maker != safeAddr.Hex() {
		t.Errorf("wire Maker = %s, want Safe %s", captured.Order.Maker, safeAddr.Hex())
	}
	if captured.Order.Signer != signerAddr.Hex() {
		t.Errorf("wire Signer = %s, want EOA %s", captured.Order.Signer, signerAddr.Hex())
	}
	if captured.Order.SignatureType == nil || *captured.Order.SignatureType != N2 {
		t.Errorf("wire SignatureType = %v, want N2", captured.Order.SignatureType)
	}

	// 反向验证签名：Maker 必须用 Safe 进 digest（与 client.go 同步），ecrecover 应得 EOA。
	tokenIDBig := new(big.Int).SetUint64(100200300)
	mAmt := big.NewInt(5_500_000)
	tAmt := big.NewInt(10_000_000)
	order := &pmsigner.OrderForSigning{
		Salt:          big.NewInt(0),
		Maker:         safeAddr,
		Signer:        signerAddr,
		Taker:         common.Address{},
		TokenID:       tokenIDBig,
		MakerAmount:   mAmt,
		TakerAmount:   tAmt,
		Expiration:    0,
		Nonce:         0,
		FeeRateBps:    0,
		Side:          pmsigner.OrderSideBuy,
		SignatureType: pmsigner.SignatureTypePolyGnosisSafe,
		ScopeID:       scope,
	}
	digest := pmsigner.BuildOrderDigest(order, exchange, chainID)
	sigBytes, err := hexDecode(captured.Order.Signature[2:])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	pub, err := ethcrypto.SigToPub(digest[:], sigBytes)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	got := ethcrypto.PubkeyToAddress(*pub)
	if got != signerAddr {
		t.Errorf("recovered = %s, want signer EOA %s", got.Hex(), signerAddr.Hex())
	}
}

// TestPlaceOrder_PolyGnosisSafe_MissingMaker_Errors 锁定 issue #15 校验：SignatureType=
// POLY_GNOSIS_SAFE 但 Maker 零值 → 必须返 ErrPrecondition（避免 wallet-subgraph 把 Maker
// 当 EOA 而拒签 INSUFFICIENT_BALANCE）。
func TestPlaceOrder_PolyGnosisSafe_MissingMaker_Errors(t *testing.T) {
	_, f := newTestServer(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when Maker missing")
	})
	_, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID:      "m",
		TokenID:       "t",
		Side:          SideBuy,
		OrderType:     OrderTypeLimit,
		Price:         decimal.NewFromInt(1),
		Size:          decimal.NewFromInt(1),
		SignatureType: pmsigner.SignatureTypePolyGnosisSafe,
		// Maker 留空 → 必须 ErrPrecondition
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

// TestPlaceOrder_PostOnlyPropagated 锁定 OrderReq.PostOnly 三态透传到 wire SendOrder.PostOnly：
//   nil   → wire 缺省（omitempty，后端默认行为）
//   true  → wire postOnly=true（后端拒填 maker-taker 即时成交）
//   false → wire postOnly=false（显式允许立即成交）
func TestPlaceOrder_PostOnlyPropagated(t *testing.T) {
	tru := true
	fls := false
	cases := []struct {
		name string
		in   *bool
		want *bool
	}{
		{"nil", nil, nil},
		{"true", &tru, &tru},
		{"false", &fls, &fls},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured SendOrder
			_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &captured)
				_, _ = w.Write([]byte(`{"orderID":"o-postonly"}`))
			})
			_, err := f.PlaceOrder(context.Background(), OrderReq{
				MarketID: "m", TokenID: "t",
				Side: SideBuy, OrderType: OrderTypeLimit,
				Price: decimal.NewFromInt(1), Size: decimal.NewFromInt(1),
				PostOnly: tc.in,
			})
			if err != nil {
				t.Fatalf("PlaceOrder: %v", err)
			}
			switch {
			case tc.want == nil && captured.PostOnly != nil:
				t.Errorf("PostOnly = %v, want nil", *captured.PostOnly)
			case tc.want != nil && captured.PostOnly == nil:
				t.Errorf("PostOnly = nil, want %v", *tc.want)
			case tc.want != nil && captured.PostOnly != nil && *tc.want != *captured.PostOnly:
				t.Errorf("PostOnly = %v, want %v", *captured.PostOnly, *tc.want)
			}
		})
	}
}

func TestExpirationPropagated(t *testing.T) {
	exp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var captured SendOrder
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		_, _ = w.Write([]byte(`{"orderID":"o1"}`))
	})
	_, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID: "m", TokenID: "t",
		Side: SideSell, OrderType: OrderTypeLimit,
		Price: decimal.NewFromInt(1), Size: decimal.NewFromInt(1),
		Expiration: &exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if captured.Order.Expiration == nil ||
		*captured.Order.Expiration != "1767225600" {
		t.Errorf("Expiration upstream = %v", captured.Order.Expiration)
	}
}

// TestComputeAmounts_BaseUnits 锁定 issue #9 的 6-decimal scale 行为：
// BUY 时 makerAmount = price*size*10^6 (USDC raw)，takerAmount = size*10^6 (token raw)；
// SELL 反之。fractional bits 超过 6 位的 floor truncate。
func TestComputeAmounts_BaseUnits(t *testing.T) {
	cases := []struct {
		name              string
		side              SdkSide
		price, size       string
		wantMaker, wantTk string
	}{
		{"BUY_simple", SideBuy, "0.55", "10", "5500000", "10000000"},
		{"SELL_simple", SideSell, "0.55", "10", "10000000", "5500000"},
		{"BUY_fraction_truncates", SideBuy, "0.49875", "100", "49875000", "100000000"},
		{"BUY_seven_decimal_floor", SideBuy, "0.1234567", "1", "123456", "1000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := OrderReq{
				Side:  tc.side,
				Price: decimal.RequireFromString(tc.price),
				Size:  decimal.RequireFromString(tc.size),
			}
			gotMaker, gotTk, err := computeAmounts(req)
			if err != nil {
				t.Fatalf("computeAmounts: %v", err)
			}
			if gotMaker != tc.wantMaker {
				t.Errorf("makerAmount = %q, want %q", gotMaker, tc.wantMaker)
			}
			if gotTk != tc.wantTk {
				t.Errorf("takerAmount = %q, want %q", gotTk, tc.wantTk)
			}
		})
	}
}
