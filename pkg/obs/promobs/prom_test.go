package promobs_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs/promobs"
)

// TestNew_NilRegisterer 验证 nil registerer 退化为 NopMetrics 不 panic。
func TestNew_NilRegisterer(t *testing.T) {
	m := promobs.New(nil)
	m.IncCounter("x", nil)
	m.ObserveHistogram("y", 1.0, nil)
	if _, ok := m.(obs.NopMetrics); !ok {
		t.Fatalf("expected NopMetrics fallback, got %T", m)
	}
}

// TestCounter_Records 验证 IncCounter 真正注册 + 累加 prometheus collector。
func TestCounter_Records(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := promobs.New(reg)

	labels := map[string]string{"path": "/order", "method": "POST", "status": "200"}
	m.IncCounter(obs.MetricHTTPRequestsTotal, labels)
	m.IncCounter(obs.MetricHTTPRequestsTotal, labels)
	m.IncCounter(obs.MetricHTTPRequestsTotal, map[string]string{"path": "/order", "method": "POST", "status": "500"})

	expected := strings.NewReader(`
# HELP pmsdk_http_requests_total Total HTTP requests issued by pm-sdk-go (clob/gamma).
# TYPE pmsdk_http_requests_total counter
pmsdk_http_requests_total{method="POST",path="/order",status="200"} 2
pmsdk_http_requests_total{method="POST",path="/order",status="500"} 1
`)
	if err := testutil.GatherAndCompare(reg, expected, obs.MetricHTTPRequestsTotal); err != nil {
		t.Fatalf("counter compare: %v", err)
	}
}

// TestHistogram_Records 验证 ObserveHistogram 注册 + 写桶。
func TestHistogram_Records(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := promobs.New(reg)

	labels := map[string]string{"path": "/book"}
	m.ObserveHistogram(obs.MetricHTTPRequestDuration, 0.05, labels)
	m.ObserveHistogram(obs.MetricHTTPRequestDuration, 0.5, labels)
	m.ObserveHistogram(obs.MetricHTTPRequestDuration, 1.5, labels)

	count := testutil.CollectAndCount(reg, obs.MetricHTTPRequestDuration)
	if count == 0 {
		t.Fatal("expected histogram collected, got 0")
	}
}

// TestCacheKey_StableAcrossOrder 验证 label keys 顺序无关；同一 (name, keys) 复用 collector。
func TestCacheKey_StableAcrossOrder(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := promobs.New(reg)

	m.IncCounter("test_counter", map[string]string{"a": "x", "b": "y"})
	m.IncCounter("test_counter", map[string]string{"b": "y", "a": "x"}) // 同 keys 不同 map iter
	// 不应 panic / re-register；prometheus 默认拒绝同名不同 desc 注册，能跑通即说明 cache hit。
}

// TestHelpText_AllStandardMetrics 让所有标准指标都被注册一次，保证 helpText 分支被覆盖。
func TestHelpText_AllStandardMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := promobs.New(reg)

	for _, name := range []string{
		obs.MetricHTTPRequestsTotal,
		obs.MetricWSReconnectsTotal,
		obs.MetricWSSeqJumpsTotal,
		obs.MetricSignerSignTotal,
		"unknown_metric_name",
	} {
		m.IncCounter(name, map[string]string{"k": "v"})
	}
	for _, name := range []string{
		obs.MetricHTTPRequestDuration,
		obs.MetricSignerSignDuration,
	} {
		m.ObserveHistogram(name, 0.1, map[string]string{"k": "v"})
	}
}
