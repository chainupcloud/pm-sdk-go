//go:build contract

// Contract test：默认 `go test ./...` 不跑；CI 走 `go test -tags contract ./...`。
package gamma

import (
	"context"
	"testing"

	"github.com/chainupcloud/pm-sdk-go/internal/contracttest"
)

// TestContract_ListEvents 验证 ListEvents 解析 fixture 数组。
func TestContract_ListEvents(t *testing.T) {
	fx := contracttest.Load(t, "../../testdata/contract-fixtures/gamma_list_events.json")
	srv := contracttest.NewMockServer(t, fx)

	f, err := NewFacade(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	active := true
	events, _, err := f.ListEvents(context.Background(), EventFilter{
		Limit:  20,
		Active: &active,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	if events[0].ID == "" {
		t.Error("event[0].ID empty")
	}
	if len(events[0].Markets) == 0 {
		t.Error("event[0].Markets empty")
	}
}

// TestContract_GetMarket 验证 GetMarket 解析 fixture 单 Market。
func TestContract_GetMarket(t *testing.T) {
	fx := contracttest.Load(t, "../../testdata/contract-fixtures/gamma_get_market.json")
	srv := contracttest.NewMockServer(t, fx)

	f, err := NewFacade(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	m, err := f.GetMarket(context.Background(), "mk-1")
	if err != nil {
		t.Fatalf("GetMarket: %v", err)
	}
	if m == nil || m.ID == "" {
		t.Fatal("expected non-nil market")
	}
	if m.YesTokenID == "" || m.NoTokenID == "" {
		t.Errorf("expected yes/no token ids parsed; got yes=%q no=%q", m.YesTokenID, m.NoTokenID)
	}
}
