package ws

import (
	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// WithLogger 注入 obs.Logger；默认 NopLogger。
func WithLogger(l obs.Logger) FacadeOption {
	return func(f *Facade) {
		if l != nil {
			f.logger = l
		}
	}
}

// WithMetrics 注入 obs.Metrics；默认 NopMetrics。
func WithMetrics(m obs.Metrics) FacadeOption {
	return func(f *Facade) {
		if m != nil {
			f.metrics = m
		}
	}
}
