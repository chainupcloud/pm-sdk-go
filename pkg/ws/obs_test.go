package ws

import (
	"testing"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

type recLogger struct{ count int }

func (r *recLogger) Debugw(string, ...any) { r.count++ }
func (r *recLogger) Infow(string, ...any)  { r.count++ }
func (r *recLogger) Warnw(string, ...any)  { r.count++ }
func (r *recLogger) Errorw(string, ...any) { r.count++ }

type recMetrics struct{ inc int }

func (r *recMetrics) IncCounter(string, map[string]string)                   { r.inc++ }
func (r *recMetrics) ObserveHistogram(string, float64, map[string]string)    {}

// TestWS_WithLoggerMetrics 验证 option 注入 + 默认 Nop。
func TestWS_WithLoggerMetrics(t *testing.T) {
	logger := &recLogger{}
	metrics := &recMetrics{}
	f, err := NewFacade("wss://example.com", WithLogger(logger), WithMetrics(metrics))
	if err != nil {
		t.Fatalf("NewFacade: %v", err)
	}
	if f.logger == nil || f.metrics == nil {
		t.Fatal("logger/metrics should be set")
	}
	if _, ok := f.logger.(*recLogger); !ok {
		t.Errorf("logger = %T, want *recLogger", f.logger)
	}

	// 默认 Nop 兜底
	f2, _ := NewFacade("wss://example.com")
	if _, ok := f2.logger.(obs.NopLogger); !ok {
		t.Errorf("default logger = %T, want NopLogger", f2.logger)
	}
	if _, ok := f2.metrics.(obs.NopMetrics); !ok {
		t.Errorf("default metrics = %T, want NopMetrics", f2.metrics)
	}
}
