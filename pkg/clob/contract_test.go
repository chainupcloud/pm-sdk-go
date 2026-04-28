//go:build contract

// Contract test：从 testdata/contract-fixtures/ 加载录回放数据，回放给 Facade
// 验证 wire 形态。默认 `go test ./...` 不跑；CI 走 `go test -tags contract ./...`。
//
// 当前 fixture 是 placeholder（见 testdata/contract-fixtures/*.json 与
// docs/contract-test.md），staging 联调后替换。
package clob

import (
	"context"
	"testing"

	"github.com/chainupcloud/pm-sdk-go/internal/contracttest"
	"github.com/shopspring/decimal"

	pmsigner "github.com/chainupcloud/pm-sdk-go/pkg/signer"
)

// noopSigner 本地最小 signer（避免引 mockSigner 跨文件依赖）。
type noopSigner struct{}

func (noopSigner) Sign(_ context.Context, _ []byte) ([]byte, error) {
	return []byte{0x01, 0x02, 0x03}, nil
}
func (noopSigner) Address() string         { return "0x1111111111111111111111111111111111111111" }
func (noopSigner) SchemaVersion() string   { return "test-v1" }

var _ pmsigner.Signer = noopSigner{}

// TestContract_PlaceOrder 验证 PlaceOrder 在 fixture 形态下能解析 orderID。
func TestContract_PlaceOrder(t *testing.T) {
	fx := contracttest.Load(t, "../../testdata/contract-fixtures/clob_place_order.json")
	srv := contracttest.NewMockServer(t, fx)

	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(noopSigner{}))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	id, err := f.PlaceOrder(context.Background(), OrderReq{
		MarketID:    "0xmarketconditionid",
		TokenID:     "100200300",
		Side:        SideBuy,
		OrderType:   OrderTypeLimit,
		Price:       decimal.RequireFromString("0.55"),
		Size:        decimal.RequireFromString("10"),
		ClientOrder: "fixture-001",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty order id")
	}
}

// TestContract_GetOrder 验证 GetOrder 解析 OpenOrder。
func TestContract_GetOrder(t *testing.T) {
	fx := contracttest.Load(t, "../../testdata/contract-fixtures/clob_get_order.json")
	srv := contracttest.NewMockServer(t, fx)

	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(noopSigner{}))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	o, err := f.GetOrder(context.Background(), OrderID("0xfixtureorderid000000000000000000000000000000000000000000000000"))
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if o == nil || o.ID == "" {
		t.Fatal("expected non-nil order with id")
	}
	if o.Side != SideBuy {
		t.Errorf("side = %q, want BUY", o.Side)
	}
}

// TestContract_GetBook 验证 GetBook 解析 OrderBookSummary。
func TestContract_GetBook(t *testing.T) {
	fx := contracttest.Load(t, "../../testdata/contract-fixtures/clob_get_book.json")
	srv := contracttest.NewMockServer(t, fx)

	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(noopSigner{}))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	book, err := f.GetBook(context.Background(), "100200300")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if book == nil {
		t.Fatal("expected non-nil book")
	}
	if len(book.Bids) == 0 || len(book.Asks) == 0 {
		t.Errorf("empty book bids=%d asks=%d", len(book.Bids), len(book.Asks))
	}
}
