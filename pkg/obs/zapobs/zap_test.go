package zapobs_test

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs/zapobs"
)

// TestNew_NilSafe 验证 New(nil) 返回 NopLogger 不 panic。
func TestNew_NilSafe(t *testing.T) {
	l := zapobs.New(nil)
	l.Infow("ok", "k", "v")
	if _, ok := l.(obs.NopLogger); !ok {
		t.Fatalf("expected NopLogger fallback, got %T", l)
	}

	l = zapobs.NewSugared(nil)
	l.Infow("ok", "k", "v")
	if _, ok := l.(obs.NopLogger); !ok {
		t.Fatalf("expected NopLogger fallback, got %T", l)
	}
}

// TestZapAdapter_AllLevels 用 zap observer 抓所有级别日志验证 adapter 透传。
func TestZapAdapter_AllLevels(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	zl := zap.New(core)

	l := zapobs.New(zl)
	l.Debugw("debug-msg", "k1", "v1")
	l.Infow("info-msg", "k2", "v2")
	l.Warnw("warn-msg", "k3", "v3")
	l.Errorw("error-msg", "k4", "v4")

	got := recorded.All()
	if len(got) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(got))
	}
	wantMsg := []string{"debug-msg", "info-msg", "warn-msg", "error-msg"}
	for i, e := range got {
		if e.Message != wantMsg[i] {
			t.Errorf("entry %d: msg %q want %q", i, e.Message, wantMsg[i])
		}
	}
}
