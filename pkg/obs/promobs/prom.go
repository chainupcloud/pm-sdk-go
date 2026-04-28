// Package promobs 提供 obs.Metrics 的 prometheus 适配器。
//
// 用户在自家代码里构造 *prometheus.Registry 并把它注入 SDK：
//
//	reg := prometheus.NewRegistry()
//	cli, _ := pmsdkgo.New(pmsdkgo.WithMetrics(promobs.New(reg)))
//
// 适配器在首次调用 IncCounter / ObserveHistogram 时按 (name, label keys) 自动
// 注册 collector；同名指标的 label keys 必须保持稳定（pmsdk 的标准指标遵守此约定）。
//
// SDK 不直接依赖 prometheus：用户不引本子包就不会把 client_golang 打进二进制。
package promobs

import (
	"sort"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// New 用给定 registry 构造 obs.Metrics 适配器。
//
// reg=nil 时返回 NopMetrics（避免误用导致 panic）。
func New(reg prometheus.Registerer) obs.Metrics {
	if reg == nil {
		return obs.NopMetrics{}
	}
	return &promMetrics{
		reg:        reg,
		counters:   make(map[string]*prometheus.CounterVec),
		histograms: make(map[string]*prometheus.HistogramVec),
	}
}

type promMetrics struct {
	reg prometheus.Registerer

	mu         sync.Mutex
	counters   map[string]*prometheus.CounterVec
	histograms map[string]*prometheus.HistogramVec
}

// IncCounter 实现 obs.Metrics。首次调用时按 labels keys 注册 CounterVec；
// 之后同 name 的不同 labels values 通过 With 复用同一 collector。
func (p *promMetrics) IncCounter(name string, labels map[string]string) {
	keys, vals := splitLabels(labels)
	cacheKey := name + "|" + strings.Join(keys, ",")

	p.mu.Lock()
	cv, ok := p.counters[cacheKey]
	if !ok {
		cv = prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: name, Help: helpText(name)},
			keys,
		)
		// 注册失败（同名重复注册）时回退到已注册实例；忽略其他 err。
		if err := p.reg.Register(cv); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				cv = are.ExistingCollector.(*prometheus.CounterVec)
			}
		}
		p.counters[cacheKey] = cv
	}
	p.mu.Unlock()

	cv.WithLabelValues(vals...).Inc()
}

// ObserveHistogram 实现 obs.Metrics。首次调用时注册 HistogramVec（默认桶）。
func (p *promMetrics) ObserveHistogram(name string, value float64, labels map[string]string) {
	keys, vals := splitLabels(labels)
	cacheKey := name + "|" + strings.Join(keys, ",")

	p.mu.Lock()
	hv, ok := p.histograms[cacheKey]
	if !ok {
		hv = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    name,
				Help:    helpText(name),
				Buckets: prometheus.DefBuckets,
			},
			keys,
		)
		if err := p.reg.Register(hv); err != nil {
			if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
				hv = are.ExistingCollector.(*prometheus.HistogramVec)
			}
		}
		p.histograms[cacheKey] = hv
	}
	p.mu.Unlock()

	hv.WithLabelValues(vals...).Observe(value)
}

// splitLabels 把 map 转成稳定排序的 (keys, values) 双切片，保证同一组 label keys
// 总是产生相同的 cacheKey。
func splitLabels(m map[string]string) ([]string, []string) {
	if len(m) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]string, len(keys))
	for i, k := range keys {
		vals[i] = m[k]
	}
	return keys, vals
}

// helpText 提供已知 SDK 标准指标的 Help 文本；其他指标返回 generic 描述。
func helpText(name string) string {
	switch name {
	case obs.MetricHTTPRequestsTotal:
		return "Total HTTP requests issued by pm-sdk-go (clob/gamma)."
	case obs.MetricHTTPRequestDuration:
		return "HTTP request duration in seconds."
	case obs.MetricWSReconnectsTotal:
		return "WebSocket reconnect attempts."
	case obs.MetricWSSeqJumpsTotal:
		return "WebSocket sequence jumps that triggered RESET."
	case obs.MetricSignerSignTotal:
		return "Total signer.Sign invocations."
	case obs.MetricSignerSignDuration:
		return "Signer.Sign duration in seconds."
	default:
		return "pm-sdk-go metric"
	}
}
