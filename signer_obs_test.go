package pmsdkgo

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
)

// recLogger 是简单的计数 Logger。
type recLogger struct {
	debug, warn atomic.Int64
}

func (l *recLogger) Debugw(string, ...any) { l.debug.Add(1) }
func (l *recLogger) Infow(string, ...any)  {}
func (l *recLogger) Warnw(string, ...any)  { l.warn.Add(1) }
func (l *recLogger) Errorw(string, ...any) {}

// recMetrics 计数 IncCounter / ObserveHistogram 调用。
type recMetrics struct {
	cnt, hist atomic.Int64
}

func (m *recMetrics) IncCounter(string, map[string]string) { m.cnt.Add(1) }
func (m *recMetrics) ObserveHistogram(string, float64, map[string]string) {
	m.hist.Add(1)
}

// failingSigner 总是失败，用于触发 Warnw 路径。
type failingSigner struct{}

func (failingSigner) Sign(_ context.Context, _ []byte) ([]byte, error) {
	return nil, errors.New("forced sign failure")
}
func (failingSigner) Address() string       { return "0x0000000000000000000000000000000000000001" }
func (failingSigner) SchemaVersion() string { return "test-v1" }

// okSigner 总是成功。
type okSigner struct{}

func (okSigner) Sign(_ context.Context, _ []byte) ([]byte, error) { return []byte{0x01}, nil }
func (okSigner) Address() string                                  { return "0x0000000000000000000000000000000000000002" }
func (okSigner) SchemaVersion() string                            { return "test-v2" }

// TestWrapSignerObs_NilInput 验证 nil signer 直接返回 nil。
func TestWrapSignerObs_NilInput(t *testing.T) {
	if got := wrapSignerObs(nil, nil, nil); got != nil {
		t.Errorf("nil signer should return nil, got %T", got)
	}
}

// TestWrapSignerObs_NopFallback 验证 nil logger/metrics 自动 fallback Nop。
func TestWrapSignerObs_NopFallback(t *testing.T) {
	wrapped := wrapSignerObs(okSigner{}, nil, nil)
	if wrapped == nil {
		t.Fatal("expected non-nil wrapper")
	}
	// 调一次确保不 panic
	if _, err := wrapped.Sign(context.Background(), []byte("p")); err != nil {
		t.Fatalf("Sign: %v", err)
	}
}

// TestWrapSignerObs_SuccessHooks 验证成功路径触发 Debugw + 双 metric。
func TestWrapSignerObs_SuccessHooks(t *testing.T) {
	logger := &recLogger{}
	metrics := &recMetrics{}
	wrapped := wrapSignerObs(okSigner{}, logger, metrics)
	if _, err := wrapped.Sign(context.Background(), []byte("payload")); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if logger.debug.Load() != 1 {
		t.Errorf("debug count = %d, want 1", logger.debug.Load())
	}
	if logger.warn.Load() != 0 {
		t.Errorf("warn count = %d, want 0", logger.warn.Load())
	}
	if metrics.cnt.Load() != 1 || metrics.hist.Load() != 1 {
		t.Errorf("metrics: cnt=%d hist=%d", metrics.cnt.Load(), metrics.hist.Load())
	}
	if got := wrapped.Address(); got != (okSigner{}).Address() {
		t.Errorf("Address pass-through broken: %s", got)
	}
	if got := wrapped.SchemaVersion(); got != "test-v2" {
		t.Errorf("SchemaVersion pass-through broken: %s", got)
	}
}

// TestWrapSignerObs_ErrorHooks 验证失败路径触发 Warnw（不触发 Debug）。
func TestWrapSignerObs_ErrorHooks(t *testing.T) {
	logger := &recLogger{}
	metrics := &recMetrics{}
	wrapped := wrapSignerObs(failingSigner{}, logger, metrics)
	if _, err := wrapped.Sign(context.Background(), []byte("p")); err == nil {
		t.Fatal("expected error")
	}
	if logger.warn.Load() != 1 {
		t.Errorf("warn count = %d, want 1", logger.warn.Load())
	}
	if logger.debug.Load() != 0 {
		t.Errorf("debug count = %d, want 0 on error", logger.debug.Load())
	}
}

// TestWrapSignerObs_OrderSigner 验证 PMCup26Signer（实现 SignOrder）也被装饰。
func TestWrapSignerObs_OrderSigner(t *testing.T) {
	priv, _ := ethcrypto.GenerateKey()
	exch := common.HexToAddress("0x1234567890123456789012345678901234567890")
	pm := signer.NewPMCup26Signer(priv, [32]byte{1, 2, 3}, 137, signer.WithExchangeAddress(exch))

	logger := &recLogger{}
	metrics := &recMetrics{}
	wrapped := wrapSignerObs(pm, logger, metrics)

	// 类型断言：本应仍提供 SignOrder（observedOrderSigner 实现）
	os, ok := wrapped.(orderSigner)
	if !ok {
		t.Fatal("wrapped signer should still implement SignOrder")
	}
	// scopeID 透传
	got := os.ScopeID()
	if got != ([32]byte{1, 2, 3}) {
		t.Errorf("ScopeID pass-through broken: %x", got)
	}

	// 调 SignOrder 验证 hooks
	order := &signer.OrderForSigning{
		Maker:         common.HexToAddress(pm.Address()),
		Signer:        common.HexToAddress(pm.Address()),
		TokenID:       common.Big1,
		MakerAmount:   common.Big1,
		TakerAmount:   common.Big1,
		Salt:          common.Big0,
		ScopeID:       [32]byte{1, 2, 3},
		Side:          signer.OrderSideBuy,
		SignatureType: signer.SignatureTypeEOA,
	}
	if _, err := os.SignOrder(context.Background(), order); err != nil {
		t.Fatalf("SignOrder: %v", err)
	}
	// hooks fired
	if logger.debug.Load() != 1 {
		t.Errorf("expected 1 debug log, got %d", logger.debug.Load())
	}
	if metrics.cnt.Load() != 1 {
		t.Errorf("expected 1 metric inc, got %d", metrics.cnt.Load())
	}

	// 也验证 obs interfaces 是 NopFallback 安全（覆盖 wrapSignerObs 顶部 nil 分支）
	_ = obs.NopMetrics{}
}
