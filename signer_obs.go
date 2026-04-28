package pmsdkgo

import (
	"context"
	"time"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
)

// wrapSignerObs 在 caller 注入的 signer 外层套一层 obs 装饰：
//
//   - logger.Debugw 记录 schema + duration
//   - metrics.IncCounter(MetricSignerSignTotal) + ObserveHistogram(MetricSignerSignDuration)
//
// 当 signer 同时实现 SignOrder（即 *signer.PMCup26Signer）时，装饰器透传该高级
// 接口（clob facade 通过类型断言走完整 EIP-712 路径），并把 obs 也覆盖到 SignOrder 调用。
func wrapSignerObs(s signer.Signer, logger Logger, metrics Metrics) signer.Signer {
	if s == nil {
		return nil
	}
	if logger == nil {
		logger = obs.NopLogger{}
	}
	if metrics == nil {
		metrics = obs.NopMetrics{}
	}
	base := &observedSigner{Signer: s, logger: logger, metrics: metrics}
	if os, ok := s.(orderSigner); ok {
		return &observedOrderSigner{observedSigner: base, inner: os}
	}
	return base
}

// orderSigner 是 *signer.PMCup26Signer 暴露的可选高级接口；与 pkg/clob.orderSigner
// 同形态（pkg/clob 通过类型断言识别），此处复制接口以避免循环依赖。
type orderSigner interface {
	SignOrder(ctx context.Context, order *signer.OrderForSigning) ([]byte, error)
	ScopeID() [32]byte
}

type observedSigner struct {
	signer.Signer
	logger  Logger
	metrics Metrics
}

func (o *observedSigner) Sign(ctx context.Context, payload []byte) ([]byte, error) {
	start := time.Now()
	sig, err := o.Signer.Sign(ctx, payload)
	dur := time.Since(start)
	labels := map[string]string{"schema": o.SchemaVersion()}
	o.metrics.IncCounter(obs.MetricSignerSignTotal, labels)
	o.metrics.ObserveHistogram(obs.MetricSignerSignDuration, dur.Seconds(), labels)
	if err != nil {
		o.logger.Warnw("signer sign failed",
			"schema", o.SchemaVersion(),
			"duration_ms", dur.Milliseconds(),
			"error", err.Error(),
		)
	} else {
		o.logger.Debugw("signer sign ok",
			"schema", o.SchemaVersion(),
			"duration_ms", dur.Milliseconds(),
		)
	}
	return sig, err
}

type observedOrderSigner struct {
	*observedSigner
	inner orderSigner
}

func (o *observedOrderSigner) SignOrder(ctx context.Context, order *signer.OrderForSigning) ([]byte, error) {
	start := time.Now()
	sig, err := o.inner.SignOrder(ctx, order)
	dur := time.Since(start)
	labels := map[string]string{"schema": o.SchemaVersion()}
	o.metrics.IncCounter(obs.MetricSignerSignTotal, labels)
	o.metrics.ObserveHistogram(obs.MetricSignerSignDuration, dur.Seconds(), labels)
	if err != nil {
		o.logger.Warnw("signer sign_order failed",
			"schema", o.SchemaVersion(),
			"duration_ms", dur.Milliseconds(),
			"error", err.Error(),
		)
	} else {
		o.logger.Debugw("signer sign_order ok",
			"schema", o.SchemaVersion(),
			"duration_ms", dur.Milliseconds(),
		)
	}
	return sig, err
}

func (o *observedOrderSigner) ScopeID() [32]byte {
	return o.inner.ScopeID()
}
