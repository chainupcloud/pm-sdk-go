package gamma

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
)

// ---------- helpers ----------

func newTestServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Facade) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	f, err := NewFacade(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	return srv, f
}

const sampleEvent = `{
  "id": "1001",
  "slug": "wc-2026-winner",
  "title": "World Cup 2026 Winner",
  "description": "Who wins the 2026 FIFA World Cup?",
  "category": "Sports",
  "active": true,
  "closed": false,
  "archived": false,
  "startDate": "2026-06-11T18:00:00Z",
  "endDate": "2026-07-19T22:00:00Z",
  "creationDate": "2025-11-01T00:00:00Z",
  "markets": [
    {
      "id": "2001",
      "conditionId": "0xcond1",
      "question": "Will Brazil win the 2026 World Cup?",
      "slug": "brazil-wc26",
      "active": true,
      "closed": false,
      "acceptingOrders": true,
      "endDate": "2026-07-19T22:00:00Z",
      "clobTokenIds": "[\"100200300\",\"400500600\"]"
    }
  ]
}`

// ---------- happy path ----------

func TestGetEvent_Happy(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events/1001" {
			t.Errorf("path = %q, want /events/1001", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleEvent))
	})

	ev, err := f.GetEvent(context.Background(), "1001")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if ev.ID != "1001" {
		t.Errorf("ID = %q", ev.ID)
	}
	if ev.Title != "World Cup 2026 Winner" {
		t.Errorf("Title = %q", ev.Title)
	}
	if !ev.Active || ev.Closed {
		t.Errorf("flags wrong: active=%v closed=%v", ev.Active, ev.Closed)
	}
	if ev.StartDate.IsZero() {
		t.Error("StartDate should be parsed")
	}
	if len(ev.Markets) != 1 {
		t.Fatalf("Markets len = %d, want 1", len(ev.Markets))
	}
	m := ev.Markets[0]
	if m.YesTokenID != "100200300" || m.NoTokenID != "400500600" {
		t.Errorf("clob token ids parse wrong: %+v", m)
	}
}

func TestGetEvent_EmptyID(t *testing.T) {
	f, _ := NewFacade("http://unused.example", http.DefaultClient)
	_, err := f.GetEvent(context.Background(), "")
	if !errors.Is(err, clob.ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

func TestListEvents_Happy(t *testing.T) {
	page := fmt.Sprintf("[%s,%s]", sampleEvent, strings.Replace(sampleEvent, `"id": "1001"`, `"id": "1002"`, 1))
	var receivedQuery string
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Errorf("path = %q", r.URL.Path)
		}
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
	})

	active := true
	events, cursor, err := f.ListEvents(context.Background(), EventFilter{
		Limit:  2,
		Offset: 0,
		Active: &active,
		Slug:   "wc-2026-winner",
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("events len = %d", len(events))
	}
	if cursor != "2" {
		t.Errorf("cursor = %q, want 2 (next offset)", cursor)
	}
	if !strings.Contains(receivedQuery, "limit=2") {
		t.Errorf("query missing limit: %s", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "active=true") {
		t.Errorf("query missing active: %s", receivedQuery)
	}
	if !strings.Contains(receivedQuery, "slug=wc-2026-winner") {
		t.Errorf("query missing slug: %s", receivedQuery)
	}
}

func TestListEvents_LastPage(t *testing.T) {
	// 返回 1 条 < limit=10，cursor 应为空
	_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[" + sampleEvent + "]"))
	})

	events, cursor, err := f.ListEvents(context.Background(), EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("events = %d", len(events))
	}
	if cursor != "" {
		t.Errorf("cursor = %q, want empty (last page)", cursor)
	}
}

func TestGetMarket_Happy(t *testing.T) {
	market := `{
      "id": "2001",
      "conditionId": "0xcond1",
      "question": "Will Brazil win the 2026 World Cup?",
      "slug": "brazil-wc26",
      "active": true,
      "closed": false,
      "acceptingOrders": true,
      "endDate": "2026-07-19T22:00:00Z",
      "clobTokenIds": "[\"100200300\",\"400500600\"]"
    }`
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets/2001" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(market))
	})

	m, err := f.GetMarket(context.Background(), "2001")
	if err != nil {
		t.Fatalf("GetMarket: %v", err)
	}
	if m.ID != "2001" || m.ConditionID != "0xcond1" {
		t.Errorf("ID/ConditionID wrong: %+v", m)
	}
	if m.YesTokenID != "100200300" || m.NoTokenID != "400500600" {
		t.Errorf("token ids wrong: %+v", m)
	}
	if !m.AcceptingOrders {
		t.Error("AcceptingOrders should be true")
	}
}

func TestGetToken_HappyYes(t *testing.T) {
	resp := `[{
      "id": "2001",
      "conditionId": "0xcond1",
      "clobTokenIds": "[\"100200300\",\"400500600\"]"
    }]`
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets/information" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"100200300"`) {
			t.Errorf("body should carry tokenID: %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	})

	tok, err := f.GetToken(context.Background(), "100200300")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if tok.ID != "100200300" {
		t.Errorf("Token.ID = %q", tok.ID)
	}
	if tok.MarketID != "2001" {
		t.Errorf("Token.MarketID = %q", tok.MarketID)
	}
	if tok.OutcomeIndex != 0 {
		t.Errorf("Token.OutcomeIndex = %d, want 0 (Yes)", tok.OutcomeIndex)
	}
}

func TestGetToken_HappyNo(t *testing.T) {
	resp := `[{
      "id": "2001",
      "conditionId": "0xcond1",
      "clobTokenIds": "[\"100200300\",\"400500600\"]"
    }]`
	_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	})

	tok, err := f.GetToken(context.Background(), "400500600")
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if tok.OutcomeIndex != 1 {
		t.Errorf("Token.OutcomeIndex = %d, want 1 (No)", tok.OutcomeIndex)
	}
}

func TestGetToken_NotFound(t *testing.T) {
	// 上游返回市场，但 tokenID 不在 clobTokenIds 里 → ErrNotFound
	resp := `[{
      "id": "2001",
      "conditionId": "0xcond1",
      "clobTokenIds": "[\"100200300\",\"400500600\"]"
    }]`
	_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(resp))
	})

	_, err := f.GetToken(context.Background(), "999")
	if !errors.Is(err, clob.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestGetToken_EmptyResults(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	_, err := f.GetToken(context.Background(), "999")
	if !errors.Is(err, clob.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// ---------- error mapping ----------

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantSent error
	}{
		{"401 → ErrSign", 401, `{"error":"unauthorized"}`, clob.ErrSign},
		{"403 → ErrSign", 403, `{"error":"forbidden"}`, clob.ErrSign},
		{"404 → ErrNotFound", 404, `{"error":"not found"}`, clob.ErrNotFound},
		{"412 → ErrPrecondition", 412, `{"error":"pre"}`, clob.ErrPrecondition},
		{"422 → ErrPrecondition", 422, `{"error":"unprocessable"}`, clob.ErrPrecondition},
		{"429 → ErrRateLimit", 429, `{"error":"rate limit"}`, clob.ErrRateLimit},
		{"502 → ErrUpstream", 502, `{"error":"bad gw"}`, clob.ErrUpstream},
		{"500 → ErrUpstream", 500, ``, clob.ErrUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := f.GetEvent(context.Background(), "1")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tc.wantSent) {
				t.Errorf("err sentinel mismatch; got %v, want %v", err, tc.wantSent)
			}
			var apiErr *clob.APIError
			if !errors.As(err, &apiErr) {
				t.Errorf("expected *clob.APIError, got %T", err)
				return
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tc.status)
			}
		})
	}
}

func TestCtxCancelled(t *testing.T) {
	// 服务端慢响应；ctx 立刻取消
	_, f := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(200)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.GetEvent(ctx, "1")
	if !errors.Is(err, clob.ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled", err)
	}
}

func TestListEvents_DecodeError(t *testing.T) {
	_, f := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	})
	_, _, err := f.ListEvents(context.Background(), EventFilter{Limit: 5})
	if !errors.Is(err, clob.ErrUpstream) {
		t.Errorf("err = %v, want ErrUpstream wrap", err)
	}
}

func TestGetMarket_EmptyID(t *testing.T) {
	f, _ := NewFacade("http://unused.example", http.DefaultClient)
	_, err := f.GetMarket(context.Background(), "")
	if !errors.Is(err, clob.ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

func TestGetToken_EmptyID(t *testing.T) {
	f, _ := NewFacade("http://unused.example", http.DefaultClient)
	_, err := f.GetToken(context.Background(), "")
	if !errors.Is(err, clob.ErrPrecondition) {
		t.Errorf("err = %v, want ErrPrecondition", err)
	}
}

func TestTransportError_Deadline(t *testing.T) {
	// 服务端永不响应；用 deadline ctx 触发 transport error → ErrCancelled 包装
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	f, _ := NewFacade(srv.URL, srv.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.GetEvent(ctx, "1")
	if !errors.Is(err, clob.ErrCancelled) {
		t.Errorf("err = %v, want ErrCancelled wrap", err)
	}
}

// ---------- error helper unit ----------

func TestWrapHTTPError_NoBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: 500,
		Header:     http.Header{},
	}
	err := wrapHTTPError(resp, nil)
	var apiErr *clob.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *clob.APIError, got %T", err)
	}
	if apiErr.Message == "" {
		t.Error("Message should fall back to http.StatusText")
	}
	if !errors.Is(err, clob.ErrUpstream) {
		t.Errorf("sentinel = %v, want ErrUpstream", err)
	}
}

func TestWrapHTTPError_NilResp(t *testing.T) {
	err := wrapHTTPError(nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
