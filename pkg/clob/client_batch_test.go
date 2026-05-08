package clob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// 本文件覆盖 Facade.PlaceOrders / CancelOrders（issue chainupcloud/pm-cup2026-liquidity#389）：
//   - PlaceOrders happy / partial / all-fail / transport
//   - CancelOrders happy / partial / all-fail / transport
//   - Sign-time precondition 失败：单笔 result.Err 填上但其它笔正常发出。

// ---------- helpers ----------

// 为可读性给 PlaceOrders 测试构造 OrderReq 的 helper。
func batchOrderReq(client string, side SdkSide, price, size string) OrderReq {
	return OrderReq{
		MarketID:    "0xmarket",
		TokenID:     "100200300",
		Side:        side,
		OrderType:   OrderTypeLimit,
		Price:       decimal.RequireFromString(price),
		Size:        decimal.RequireFromString(size),
		ClientOrder: client,
	}
}

// ---------- PlaceOrders ----------

func TestPlaceOrders_AllSuccess(t *testing.T) {
	var receivedBody []SendOrder
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders" {
			t.Errorf("path = %q, want /orders", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedBody); err != nil {
			t.Fatalf("decode []SendOrder: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"success":true,"orderID":"o-1"},
			{"success":true,"orderID":"o-2"},
			{"success":true,"orderID":"o-3"}
		]`))
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}

	reqs := []OrderReq{
		batchOrderReq("c-1", SideBuy, "0.50", "10"),
		batchOrderReq("c-2", SideBuy, "0.49", "10"),
		batchOrderReq("c-3", SideSell, "0.51", "10"),
	}
	results, err := f.PlaceOrders(context.Background(), reqs)
	if err != nil {
		t.Fatalf("PlaceOrders err = %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("len(results) = %d", len(results))
	}
	wantIDs := []OrderID{"o-1", "o-2", "o-3"}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("[%d] err = %v, want nil", i, r.Err)
		}
		if r.OrderID != wantIDs[i] {
			t.Errorf("[%d] OrderID = %q, want %q", i, r.OrderID, wantIDs[i])
		}
	}
	if len(receivedBody) != 3 {
		t.Errorf("wire len = %d, want 3", len(receivedBody))
	}
}

func TestPlaceOrders_Partial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 中间一笔 success=false；其余成功。
		_, _ = w.Write([]byte(`[
			{"success":true,"orderID":"o-1"},
			{"success":false,"errorMsg":"INSUFFICIENT_BALANCE"},
			{"success":true,"orderID":"o-3"}
		]`))
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}

	reqs := []OrderReq{
		batchOrderReq("c-1", SideBuy, "0.50", "10"),
		batchOrderReq("c-2", SideBuy, "0.49", "10"),
		batchOrderReq("c-3", SideSell, "0.51", "10"),
	}
	results, err := f.PlaceOrders(context.Background(), reqs)
	if err != nil {
		t.Fatalf("outer err = %v, want nil for partial failure", err)
	}
	if results[0].Err != nil || results[0].OrderID != "o-1" {
		t.Errorf("[0] = %+v", results[0])
	}
	if results[1].Err == nil {
		t.Errorf("[1] should fail")
	} else {
		if !errors.Is(results[1].Err, ErrUpstream) {
			t.Errorf("[1] err not ErrUpstream: %v", results[1].Err)
		}
		if !strings.Contains(results[1].Err.Error(), "INSUFFICIENT_BALANCE") {
			t.Errorf("[1] err missing reason: %v", results[1].Err)
		}
	}
	if results[2].Err != nil || results[2].OrderID != "o-3" {
		t.Errorf("[2] = %+v", results[2])
	}
}

func TestPlaceOrders_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"success":false,"errorMsg":"E1"},
			{"success":false,"errorMsg":"E2"},
			{"success":false,"errorMsg":"E3"}
		]`))
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	reqs := []OrderReq{
		batchOrderReq("c-1", SideBuy, "0.50", "10"),
		batchOrderReq("c-2", SideBuy, "0.49", "10"),
		batchOrderReq("c-3", SideSell, "0.51", "10"),
	}
	results, err := f.PlaceOrders(context.Background(), reqs)
	if err != nil {
		t.Fatalf("outer err = %v, want nil", err)
	}
	for i, r := range results {
		if r.Err == nil {
			t.Errorf("[%d] expected err, got %+v", i, r)
		}
	}
}

func TestPlaceOrders_TransportErr(t *testing.T) {
	// 关掉 server 让 client 拿到 connection refused。
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()
	f, err := NewFacade(url, http.DefaultClient, WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	reqs := []OrderReq{batchOrderReq("c-1", SideBuy, "0.5", "10")}
	results, err := f.PlaceOrders(context.Background(), reqs)
	if err == nil {
		t.Fatalf("expected transport err")
	}
	if len(results) != 1 {
		t.Errorf("results len = %d", len(results))
	}
}

func TestPlaceOrders_NoSigner(t *testing.T) {
	f, err := NewFacade("http://localhost:0", http.DefaultClient) // no signer
	if err != nil {
		t.Fatal(err)
	}
	reqs := []OrderReq{batchOrderReq("c-1", SideBuy, "0.5", "10")}
	results, err := f.PlaceOrders(context.Background(), reqs)
	if err != nil {
		t.Fatalf("outer err should be nil when only sign fails; got %v", err)
	}
	if len(results) != 1 || !errors.Is(results[0].Err, ErrSign) {
		t.Errorf("results = %+v", results)
	}
}

func TestPlaceOrders_Empty(t *testing.T) {
	f, err := NewFacade("http://localhost:0", http.DefaultClient, WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := f.PlaceOrders(context.Background(), nil)
	if err != nil || len(results) != 0 {
		t.Errorf("results = %+v err = %v", results, err)
	}
}

// ---------- CancelOrders ----------

func TestCancelOrders_AllSuccess(t *testing.T) {
	var receivedIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orders" {
			t.Errorf("path = %q, want /orders", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &receivedIDs); err != nil {
			t.Fatalf("body should be []string, got %s err %v", body, err)
		}
		_, _ = w.Write([]byte(`{"canceled":["a","b","c"]}`))
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := f.CancelOrders(context.Background(), []OrderID{"a", "b", "c"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(receivedIDs) != 3 || receivedIDs[0] != "a" {
		t.Errorf("wire ids = %v", receivedIDs)
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("[%d] err = %v", i, r.Err)
		}
	}
}

func TestCancelOrders_Partial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"canceled":["a","c"],
			"not_canceled":{"b":"NOT_FOUND"}
		}`))
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := f.CancelOrders(context.Background(), []OrderID{"a", "b", "c"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if results[0].Err != nil {
		t.Errorf("a should be ok: %v", results[0].Err)
	}
	if results[1].Err == nil || !errors.Is(results[1].Err, ErrUpstream) {
		t.Errorf("b should have ErrUpstream: %v", results[1].Err)
	}
	if !strings.Contains(results[1].Err.Error(), "NOT_FOUND") {
		t.Errorf("b err missing reason: %v", results[1].Err)
	}
	if results[2].Err != nil {
		t.Errorf("c should be ok: %v", results[2].Err)
	}
}

func TestCancelOrders_AllFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"canceled":[],
			"not_canceled":{"a":"X","b":"Y","c":"Z"}
		}`))
	}))
	defer srv.Close()
	f, err := NewFacade(srv.URL, srv.Client(), WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := f.CancelOrders(context.Background(), []OrderID{"a", "b", "c"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for i, r := range results {
		if r.Err == nil {
			t.Errorf("[%d] expected err", i)
		}
	}
}

func TestCancelOrders_TransportErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()
	f, err := NewFacade(url, http.DefaultClient, WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	results, err := f.CancelOrders(context.Background(), []OrderID{"a"})
	if err == nil {
		t.Fatalf("expected transport err")
	}
	if len(results) != 1 {
		t.Errorf("results len = %d", len(results))
	}
}

func TestCancelOrders_EmptyAndZeroID(t *testing.T) {
	f, err := NewFacade("http://localhost:0", http.DefaultClient, WithSigner(&mockSigner{}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.CancelOrders(context.Background(), nil)
	if err != nil || len(res) != 0 {
		t.Errorf("empty: results = %+v err = %v", res, err)
	}

	// 全空字符串：不发 RPC，全部 per-id ErrPrecondition。
	res, err = f.CancelOrders(context.Background(), []OrderID{"", ""})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	for i, r := range res {
		if !errors.Is(r.Err, ErrPrecondition) {
			t.Errorf("[%d] err = %v", i, r.Err)
		}
	}
}
