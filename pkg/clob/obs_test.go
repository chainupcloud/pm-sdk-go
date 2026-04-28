package clob

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// captureLogger 是测试用 obs.Logger，仅记录调用次数 + 最近一条 msg。
type captureLogger struct {
	debug, info, warn, errCount atomic.Int64
	lastMsg                     atomic.Value // string
}

func (l *captureLogger) Debugw(msg string, _ ...any) { l.debug.Add(1); l.lastMsg.Store(msg) }
func (l *captureLogger) Infow(msg string, _ ...any)  { l.info.Add(1); l.lastMsg.Store(msg) }
func (l *captureLogger) Warnw(msg string, _ ...any)  { l.warn.Add(1); l.lastMsg.Store(msg) }
func (l *captureLogger) Errorw(msg string, _ ...any) { l.errCount.Add(1); l.lastMsg.Store(msg) }

// captureMetrics 记录 IncCounter / ObserveHistogram 调用 count + 最近一次 labels。
type captureMetrics struct {
	counters   atomic.Int64
	histograms atomic.Int64
}

func (m *captureMetrics) IncCounter(string, map[string]string) { m.counters.Add(1) }
func (m *captureMetrics) ObserveHistogram(string, float64, map[string]string) {
	m.histograms.Add(1)
}

// TestObs_HookFires_OnSuccess 验证 2xx 响应触发 metrics + Debugw。
func TestObs_HookFires_OnSuccess(t *testing.T) {
	logger := &captureLogger{}
	metrics := &captureMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-abc")
		_, _ = w.Write([]byte(`{"market":"asset-1","bids":[],"asks":[]}`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client(),
		WithLogger(logger), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}

	if _, err := f.GetBook(context.Background(), "asset-1"); err != nil {
		t.Fatalf("GetBook: %v", err)
	}

	if got := metrics.counters.Load(); got != 1 {
		t.Errorf("counters = %d, want 1", got)
	}
	if got := metrics.histograms.Load(); got != 1 {
		t.Errorf("histograms = %d, want 1", got)
	}
	if got := logger.debug.Load(); got != 1 {
		t.Errorf("debug logs = %d, want 1", got)
	}
	if got := logger.warn.Load(); got != 0 {
		t.Errorf("warn logs = %d, want 0 on 2xx", got)
	}
}

// TestObs_HookFires_OnError 验证 5xx 响应触发 Warnw。
func TestObs_HookFires_OnError(t *testing.T) {
	logger := &captureLogger{}
	metrics := &captureMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"oops"}`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client(),
		WithLogger(logger), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}

	_, _ = f.GetBook(context.Background(), "asset-1")

	if got := metrics.counters.Load(); got != 1 {
		t.Errorf("counters = %d, want 1", got)
	}
	if got := logger.warn.Load(); got != 1 {
		t.Errorf("warn logs = %d, want 1 on 5xx", got)
	}
	if got := logger.debug.Load(); got != 0 {
		t.Errorf("debug logs = %d, want 0 on 5xx", got)
	}
}

// TestObs_NopByDefault 验证未注入 obs 时挂点用 NopLogger / NopMetrics 不 panic。
func TestObs_NopByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	if _, ok := f.logger.(obs.NopLogger); !ok {
		t.Errorf("default logger = %T, want NopLogger", f.logger)
	}
	if _, ok := f.metrics.(obs.NopMetrics); !ok {
		t.Errorf("default metrics = %T, want NopMetrics", f.metrics)
	}
	_, _ = f.GetBook(context.Background(), "asset-1")
}
