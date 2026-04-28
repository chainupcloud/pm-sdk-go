package gamma

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

type captureLogger struct {
	debug, warn atomic.Int64
}

func (l *captureLogger) Debugw(string, ...any) { l.debug.Add(1) }
func (l *captureLogger) Infow(string, ...any)  {}
func (l *captureLogger) Warnw(string, ...any)  { l.warn.Add(1) }
func (l *captureLogger) Errorw(string, ...any) {}

type captureMetrics struct {
	c, h atomic.Int64
}

func (m *captureMetrics) IncCounter(string, map[string]string)              { m.c.Add(1) }
func (m *captureMetrics) ObserveHistogram(string, float64, map[string]string) { m.h.Add(1) }

// TestObs_GammaListEvents_Success 验证 gamma facade 在 2xx 触发 obs 钩子。
func TestObs_GammaListEvents_Success(t *testing.T) {
	logger := &captureLogger{}
	metrics := &captureMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client(), WithLogger(logger), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	if _, _, err := f.ListEvents(context.Background(), EventFilter{}); err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if metrics.c.Load() != 1 || metrics.h.Load() != 1 {
		t.Errorf("metrics: c=%d h=%d", metrics.c.Load(), metrics.h.Load())
	}
	if logger.debug.Load() != 1 {
		t.Errorf("debug=%d, want 1", logger.debug.Load())
	}
}

// TestObs_GammaGetEvent_Error 验证 4xx 触发 Warnw。
func TestObs_GammaGetEvent_Error(t *testing.T) {
	logger := &captureLogger{}
	metrics := &captureMetrics{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"missing"}`))
	}))
	defer srv.Close()

	f, err := NewFacade(srv.URL, srv.Client(), WithLogger(logger), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	_, _ = f.GetEvent(context.Background(), "ev-x")
	if logger.warn.Load() != 1 {
		t.Errorf("warn=%d, want 1", logger.warn.Load())
	}
}

// TestObs_GammaNopByDefault 验证默认 Nop。
func TestObs_GammaNopByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
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
}
