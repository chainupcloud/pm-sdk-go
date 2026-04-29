package relayer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL, WithAPIKey("test-api-key"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c, srv
}

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient(""); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty baseURL: got %v, want ErrInvalidConfig", err)
	}
}

func TestSubmitOK(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transactionID":"tx-123","transactionHash":"","state":"STATE_NEW"}`))
	}))

	req := &SubmitRequest{
		From: "0xabc", To: "0xdef", Type: TxTypeSafe, ScopeID: "0xff",
		Signature: "0xsig", SignatureParams: json.RawMessage(`{"foo":"bar"}`),
	}
	resp, err := c.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if resp.TransactionID != "tx-123" || resp.State != "STATE_NEW" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if captured.Header.Get("RELAYER_API_KEY") != "test-api-key" {
		t.Fatalf("api key header missing: %v", captured.Header)
	}
	if captured.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type header missing")
	}
	if !strings.Contains(string(capturedBody), `"signatureParams":{"foo":"bar"}`) {
		t.Fatalf("signatureParams not raw JSON: %s", capturedBody)
	}
	if captured.URL.Path != "/submit" {
		t.Fatalf("path: %s", captured.URL.Path)
	}
}

func TestSubmit401(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid API key"}`))
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 401 || apiErr.Message != "invalid API key" {
		t.Fatalf("apiErr: %+v", apiErr)
	}
}

func TestSubmit403(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"scopeId does not match authenticated scope"}`))
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("got %v, want ErrAuth", err)
	}
}

func TestSubmit400Precondition(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"missing signature"}`))
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("got %v, want ErrPrecondition", err)
	}
}

func TestSubmit409Conflict(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"safe deployment already in progress"}`))
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v, want ErrConflict", err)
	}
}

func TestSubmit429RateLimit(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"SAFE-CREATE rate limit exceeded"}`))
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	if !errors.Is(err, ErrRateLimit) {
		t.Fatalf("got %v, want ErrRateLimit", err)
	}
}

func TestSubmit5xx(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v, want ErrUpstream", err)
	}
}

func TestSubmitMalformedErrorBody(t *testing.T) {
	// 上游返回 4xx 但 body 不是 JSON——应该用 http.StatusText 兜底。
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<!DOCTYPE html>`))
	}))
	_, err := c.Submit(context.Background(), &SubmitRequest{})
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.Message == "" {
		t.Fatalf("Message should fall back to status text")
	}
}

func TestSubmitContextCancel(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := c.Submit(ctx, &SubmitRequest{})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("got %v, want ErrCancelled", err)
	}
}

func TestGetNonceModeA(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nonce" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("address") != "0xsafe" {
			t.Errorf("address query missing: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"nonce":"42"}`))
	}))
	got, err := c.GetNonce(context.Background(), "0xsafe", "", "")
	if err != nil {
		t.Fatalf("GetNonce: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestGetNonceModeB(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("signer") == "" || q.Get("scopeId") == "" {
			t.Errorf("signer/scopeId missing: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"nonce":"0"}`))
	}))
	n, err := c.GetNonce(context.Background(), "", "0xeoa", "0xff")
	if err != nil || n != 0 {
		t.Fatalf("GetNonce: got n=%d err=%v", n, err)
	}
}

func TestGetNonceMissingArgs(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	_, err := c.GetNonce(context.Background(), "", "", "")
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("got %v, want ErrInvalidConfig", err)
	}
}

func TestGetDeployed(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"deployed":true,"address":"0xabc"}`))
	}))
	dep, addr, err := c.GetDeployed(context.Background(), "0xabc", "", "")
	if err != nil || !dep || addr != "0xabc" {
		t.Fatalf("GetDeployed: dep=%v addr=%s err=%v", dep, addr, err)
	}
}

func TestGetTransaction(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "tx-1" {
			t.Errorf("id missing")
		}
		_, _ = w.Write([]byte(`{"transactionID":"tx-1","state":"STATE_CONFIRMED","transactionHash":"0xhash"}`))
	}))
	tx, err := c.GetTransaction(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.State != StateConfirmed || tx.TransactionHash != "0xhash" {
		t.Fatalf("tx: %+v", tx)
	}
}

func TestGetTransaction404(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"transaction not found"}`))
	}))
	_, err := c.GetTransaction(context.Background(), "tx-x")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestGetTransactions(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("limit: %s", r.URL.Query().Get("limit"))
		}
		_, _ = w.Write([]byte(`[{"transactionID":"a"},{"transactionID":"b"}]`))
	}))
	txs, err := c.GetTransactions(context.Background(), 10, 0)
	if err != nil || len(txs) != 2 {
		t.Fatalf("GetTransactions: len=%d err=%v", len(txs), err)
	}
}

func TestGetRelayPayload(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scopeId") != "0xff" {
			t.Errorf("scopeId missing")
		}
		_, _ = w.Write([]byte(`{"address":"0xrelayer","nonce":99}`))
	}))
	rp, err := c.GetRelayPayload(context.Background(), "0xff")
	if err != nil || rp.Nonce != 99 || rp.Address != "0xrelayer" {
		t.Fatalf("rp=%+v err=%v", rp, err)
	}
}

func TestBearerAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer my-jwt" {
			t.Errorf("Authorization: %s", got)
		}
		if r.Header.Get("RELAYER_API_KEY") != "" {
			t.Errorf("API key set when only bearer was configured")
		}
		_, _ = w.Write([]byte(`{"deployed":false,"address":"0x0"}`))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL, WithBearer("my-jwt"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, _, err := c.GetDeployed(context.Background(), "0xa", "", ""); err != nil {
		t.Fatalf("GetDeployed: %v", err)
	}
}
