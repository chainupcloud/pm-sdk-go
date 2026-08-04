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

func TestGetBook_StringTimestampSingleRequest(t *testing.T) {
	wantTime := time.Date(2026, time.August, 4, 10, 31, 45, 123000000, time.UTC)
	requests := 0
	_, facade := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/book" {
			t.Fatalf("path = %q, want /book", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"market":"0xmarket",
			"asset_id":"100200300",
			"hash":"",
			"timestamp":"2026-08-04T10:31:45.123Z",
			"bids":[{"price":"0.54","size":"100"}],
			"asks":[{"price":"0.56","size":"50"}],
			"min_order_size":"5",
			"max_order_size":"",
			"tick_size":"0.001",
			"neg_risk":false,
			"last_trade_price":"0.55"
		}`)
	})

	book, err := facade.GetBook(context.Background(), "100200300")
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly 1", requests)
	}
	if !book.UpdateAt.Equal(wantTime) {
		t.Fatalf("UpdateAt = %s, want %s", book.UpdateAt, wantTime)
	}
	if len(book.Bids) != 1 || len(book.Asks) != 1 {
		t.Fatalf("levels = bids:%d asks:%d", len(book.Bids), len(book.Asks))
	}
}

func TestOrderBookSummaryTimestampCompatibility(t *testing.T) {
	want := time.Date(2023, time.November, 14, 22, 13, 20, 123000000, time.UTC)
	tests := []struct {
		name      string
		timestamp string
		want      time.Time
	}{
		{name: "rfc3339_nano", timestamp: `"2023-11-14T22:13:20.123Z"`, want: want},
		{name: "number_seconds", timestamp: `1700000000`, want: time.Unix(1700000000, 0).UTC()},
		{name: "string_seconds", timestamp: `"1700000000"`, want: time.Unix(1700000000, 0).UTC()},
		{name: "number_milliseconds", timestamp: `1700000000123`, want: want},
		{name: "string_milliseconds", timestamp: `"1700000000123"`, want: want},
		{name: "null", timestamp: `null`, want: time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var summary OrderBookSummary
			payload := `{"asset_id":"100","timestamp":` + tc.timestamp + `,"bids":[],"asks":[],"min_order_size":"5","tick_size":"0.001"}`
			if err := jsonUnmarshal([]byte(payload), &summary); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			book := bookSummaryToSDK(&summary, "fallback")
			if !book.UpdateAt.Equal(tc.want) {
				t.Fatalf("UpdateAt = %s, want %s", book.UpdateAt, tc.want)
			}
		})
	}

	var missing OrderBookSummary
	if err := jsonUnmarshal([]byte(`{"asset_id":"100","bids":[],"asks":[],"min_order_size":"5","tick_size":"0.001"}`), &missing); err != nil {
		t.Fatalf("unmarshal missing timestamp: %v", err)
	}
	if missing.Timestamp != nil {
		t.Fatalf("missing Timestamp = %v, want nil", *missing.Timestamp)
	}
	if got := bookSummaryToSDK(&missing, "fallback").UpdateAt; !got.IsZero() {
		t.Fatalf("missing UpdateAt = %s, want zero", got)
	}
}

func TestGetBook_InvalidTimestampReturnsUpstreamWithoutRetry(t *testing.T) {
	requests := 0
	_, facade := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"asset_id":"100","timestamp":"not-a-time","bids":[],"asks":[],"min_order_size":"5","tick_size":"0.001"}`)
	})

	_, err := facade.GetBook(context.Background(), "100")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want exactly 1", requests)
	}
}

func TestParseGetBooksResp_StringAndNumericTimestamp(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`[
			{"asset_id":"yes","timestamp":"2026-08-04T10:31:45Z","bids":[],"asks":[],"min_order_size":"5","tick_size":"0.001"},
			{"asset_id":"no","timestamp":1700000000,"bids":[],"asks":[],"min_order_size":5,"tick_size":0.001}
		]`)),
	}

	parsed, err := ParseGetBooksResp(response)
	if err != nil {
		t.Fatalf("ParseGetBooksResp: %v", err)
	}
	if parsed.JSON200 == nil || len(*parsed.JSON200) != 2 {
		t.Fatalf("JSON200 = %+v, want two books", parsed.JSON200)
	}
	books := *parsed.JSON200
	if books[0].Timestamp == nil || *books[0].Timestamp != int(time.Date(2026, time.August, 4, 10, 31, 45, 0, time.UTC).UnixMilli()) {
		t.Fatalf("books[0].Timestamp = %v", books[0].Timestamp)
	}
	if books[1].Timestamp == nil || *books[1].Timestamp != 1700000000 {
		t.Fatalf("books[1].Timestamp = %v", books[1].Timestamp)
	}
}
