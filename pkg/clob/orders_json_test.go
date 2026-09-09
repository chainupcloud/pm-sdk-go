package clob

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListOrders_RFC3339NanoCreatedAtSingleResponse(t *testing.T) {
	wantCreatedAt := time.Date(2026, time.September, 9, 11, 16, 17, 123456789, time.FixedZone("+02", 2*60*60))
	requests := 0
	_, facade := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/orders" {
			t.Fatalf("path = %q, want /orders", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"data":[{"id":"order-1","market":"market-1","asset_id":"token-1","price":"0.51","original_size":"10","size_matched":"2","status":"ORDER_STATUS_LIVE","side":"BUY","order_type":"GTC","created_at":"2026-09-09T11:16:17.123456789+02:00"}],
			"next_cursor":"cursor-2"
		}`)
	})

	orders, cursor, err := facade.ListOrders(context.Background(), OrderFilter{})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly 1", requests)
	}
	if cursor != "cursor-2" {
		t.Fatalf("cursor = %q, want cursor-2", cursor)
	}
	if len(orders) != 1 {
		t.Fatalf("len(orders) = %d, want 1", len(orders))
	}
	if orders[0].ID != "order-1" || orders[0].MarketID != "market-1" || orders[0].TokenID != "token-1" {
		t.Fatalf("order identity = %+v", orders[0])
	}
	if orders[0].Status != OrderStatusPartiallyFilled {
		t.Fatalf("Status = %q, want PARTIALLY_FILLED", orders[0].Status)
	}
	if !orders[0].CreatedAt.Equal(wantCreatedAt) || !orders[0].UpdatedAt.Equal(wantCreatedAt) {
		t.Fatalf("timestamps = (%s, %s), want %s", orders[0].CreatedAt, orders[0].UpdatedAt, wantCreatedAt)
	}
	if orders[0].CreatedAt.Location() != time.UTC || orders[0].UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps must use UTC: (%s, %s)", orders[0].CreatedAt.Location(), orders[0].UpdatedAt.Location())
	}
}

func TestListOrders_CreatedAtFormats(t *testing.T) {
	wantMillis := time.Date(2023, time.November, 14, 22, 13, 20, 123000000, time.UTC)
	cases := []struct {
		name      string
		createdAt string
		want      time.Time
	}{
		{name: "number_seconds", createdAt: `1700000000`, want: time.Unix(1700000000, 0).UTC()},
		{name: "string_seconds", createdAt: `"1700000000"`, want: time.Unix(1700000000, 0).UTC()},
		{name: "padded_string_seconds", createdAt: `" 1700000000 "`, want: time.Unix(1700000000, 0).UTC()},
		{name: "number_milliseconds", createdAt: `1700000000123`, want: wantMillis},
		{name: "string_milliseconds", createdAt: `"1700000000123"`, want: wantMillis},
		{name: "rfc3339", createdAt: `"2023-11-14T22:13:20Z"`, want: time.Unix(1700000000, 0).UTC()},
		{name: "padded_rfc3339", createdAt: `" 2023-11-14T22:13:20Z "`, want: time.Unix(1700000000, 0).UTC()},
		{name: "zero_number", createdAt: `0`, want: time.Unix(0, 0).UTC()},
		{name: "zero_string", createdAt: `"0"`, want: time.Unix(0, 0).UTC()},
		{name: "negative_number", createdAt: `-1`, want: time.Unix(-1, 0).UTC()},
		{name: "negative_string", createdAt: `"-1"`, want: time.Unix(-1, 0).UTC()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			_, facade := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = io.WriteString(w, `{"data":[{"id":"order-1","created_at":`+tc.createdAt+`}],"next_cursor":"LTE="}`)
			})

			orders, _, err := facade.ListOrders(context.Background(), OrderFilter{})
			if err != nil {
				t.Fatalf("ListOrders: %v", err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want exactly 1", requests)
			}
			if len(orders) != 1 || !orders[0].CreatedAt.Equal(tc.want) || !orders[0].UpdatedAt.Equal(tc.want) {
				t.Fatalf("timestamps = %+v, want %s", orders, tc.want)
			}
		})
	}
}

func TestListOrders_OptionalCreatedAtIsZero(t *testing.T) {
	for _, createdAt := range []string{"", `null`, `""`, `" \t "`} {
		t.Run(createdAt, func(t *testing.T) {
			payload := `{"data":[{"id":"order-1"`
			if createdAt != "" {
				payload += `,"created_at":` + createdAt
			}
			payload += `}],"next_cursor":"LTE="}`
			_, facade := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, payload)
			})

			orders, _, err := facade.ListOrders(context.Background(), OrderFilter{})
			if err != nil {
				t.Fatalf("ListOrders: %v", err)
			}
			if len(orders) != 1 || !orders[0].CreatedAt.IsZero() || !orders[0].UpdatedAt.IsZero() {
				t.Fatalf("timestamps = %+v, want zero", orders)
			}
		})
	}
}

func TestListOrders_RejectsInvalidResponseWithoutRetry(t *testing.T) {
	cases := []string{
		`{"data":[{"created_at":"not-a-time"}],"next_cursor":"LTE="}`,
		`{"data":[{"created_at":{}}],"next_cursor":"LTE="}`,
		`{"data":[null],"next_cursor":"LTE="}`,
		`{"data":[{"id":"first"},{"created_at":"not-a-time"}],"next_cursor":"LTE="}`,
		`{"data":[],"next_cursor":null}`,
		`{"data":"not-an-array","next_cursor":"LTE="}`,
		`{"data":[],"next_cursor":42}`,
		`{`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			requests := 0
			_, facade := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = io.WriteString(w, body)
			})
			orders, cursor, err := facade.ListOrders(context.Background(), OrderFilter{})
			if !errors.Is(err, ErrUpstream) {
				t.Fatalf("err = %v, want ErrUpstream", err)
			}
			if orders != nil || cursor != "" {
				t.Fatalf("partial result = (%+v, %q), want nil empty", orders, cursor)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want exactly 1", requests)
			}
		})
	}
}

func TestListOrders_PaginatesAndForwardsFilter(t *testing.T) {
	requests := 0
	_, facade := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("id") != "order-1" || r.URL.Query().Get("market") != "market-1" || r.URL.Query().Get("asset_id") != "token-1" {
			t.Fatalf("filters = %s", r.URL.RawQuery)
		}
		switch r.URL.Query().Get("next_cursor") {
		case "":
			_, _ = io.WriteString(w, `{"data":[{"id":"page-1"}],"next_cursor":"cursor-2"}`)
		case "cursor-2":
			_, _ = io.WriteString(w, `{"data":[{"id":"page-2"}],"next_cursor":"LTE="}`)
		default:
			t.Fatalf("next_cursor = %q", r.URL.Query().Get("next_cursor"))
		}
	})

	filter := OrderFilter{OrderID: "order-1", MarketID: "market-1", TokenID: "token-1"}
	first, next, err := facade.ListOrders(context.Background(), filter)
	if err != nil || len(first) != 1 || first[0].ID != "page-1" || next != "cursor-2" {
		t.Fatalf("first page = %+v, next=%q, err=%v", first, next, err)
	}
	filter.NextCursor = next
	second, next, err := facade.ListOrders(context.Background(), filter)
	if err != nil || len(second) != 1 || second[0].ID != "page-2" || next != "LTE=" {
		t.Fatalf("second page = %+v, next=%q, err=%v", second, next, err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want one request per page", requests)
	}
}

func TestListOrders_PropagatesReadContextAndHTTPFailures(t *testing.T) {
	t.Run("cancelled_during_body_read", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		requests := 0
		facade, err := NewFacade("http://clob.invalid", &http.Client{Transport: listOrdersRoundTripper(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: http.StatusOK, Body: listOrdersCancelledBody{cancel: cancel}}, nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		orders, cursor, err := facade.ListOrders(ctx, OrderFilter{})
		if !errors.Is(err, ErrCancelled) || orders != nil || cursor != "" || requests != 1 {
			t.Fatalf("cancelled body: orders=%+v cursor=%q requests=%d error=%v", orders, cursor, requests, err)
		}
	})

	t.Run("read_failure", func(t *testing.T) {
		facade, err := NewFacade("http://clob.invalid", &http.Client{Transport: listOrdersRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: listOrdersReadError{}}, nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		orders, cursor, err := facade.ListOrders(context.Background(), OrderFilter{})
		if !errors.Is(err, ErrUpstream) || !strings.Contains(err.Error(), "read OrdersResponse") {
			t.Fatalf("err = %v, want read ErrUpstream", err)
		}
		if orders != nil || cursor != "" {
			t.Fatalf("read failure returned a usable page: orders=%+v cursor=%q", orders, cursor)
		}
	})

	t.Run("cancelled_context", func(t *testing.T) {
		_, facade := newTestServer(t, func(http.ResponseWriter, *http.Request) {
			t.Fatal("server must not be called for cancelled context")
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := facade.ListOrders(ctx, OrderFilter{})
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("err = %v, want ErrCancelled", err)
		}
	})

	t.Run("http_failure", func(t *testing.T) {
		_, facade := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"error":"upstream failed"}`)
		})
		_, _, err := facade.ListOrders(context.Background(), OrderFilter{})
		if !errors.Is(err, ErrUpstream) {
			t.Fatalf("err = %v, want ErrUpstream", err)
		}
	})
}

type listOrdersRoundTripper func(*http.Request) (*http.Response, error)

func (f listOrdersRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type listOrdersReadError struct{}

type listOrdersCancelledBody struct{ cancel context.CancelFunc }

func (body listOrdersCancelledBody) Read([]byte) (int, error) {
	body.cancel()
	return 0, context.Canceled
}
func (listOrdersCancelledBody) Close() error { return nil }

// 即使读取到的前缀恰好是合法 JSON，非 EOF 读取失败仍不能接受该快照。
func (listOrdersReadError) Read(p []byte) (int, error) {
	return copy(p, `{"data":[],"next_cursor":"LTE="}`), io.ErrUnexpectedEOF
}
func (listOrdersReadError) Close() error { return nil }

var _ http.RoundTripper = listOrdersRoundTripper(nil)
var _ io.ReadCloser = listOrdersReadError{}
