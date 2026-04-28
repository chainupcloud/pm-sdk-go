package obs_test

import (
	"testing"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// TestNopLogger 验证 NopLogger 所有方法都不 panic 且无 side-effect。
func TestNopLogger(t *testing.T) {
	var l obs.Logger = obs.NopLogger{}
	l.Debugw("debug", "k", "v")
	l.Infow("info", "k", "v")
	l.Warnw("warn", "k", "v")
	l.Errorw("error", "k", "v")
	// 奇数 kvs 不应 panic（对实现的容错要求）
	l.Infow("oddkvs", "lonely")
}

// TestNopMetrics 验证 NopMetrics 不 panic。
func TestNopMetrics(t *testing.T) {
	var m obs.Metrics = obs.NopMetrics{}
	m.IncCounter("counter", map[string]string{"k": "v"})
	m.ObserveHistogram("hist", 1.5, nil)
	m.IncCounter("nolabels", nil)
}

// TestMetricNames 是 sanity check，避免 typo 改了常量。
func TestMetricNames(t *testing.T) {
	cases := map[string]string{
		obs.MetricHTTPRequestsTotal:   "pmsdk_http_requests_total",
		obs.MetricHTTPRequestDuration: "pmsdk_http_request_duration_seconds",
		obs.MetricWSReconnectsTotal:   "pmsdk_ws_reconnects_total",
		obs.MetricWSSeqJumpsTotal:     "pmsdk_ws_seq_jumps_total",
		obs.MetricSignerSignTotal:     "pmsdk_signer_sign_total",
		obs.MetricSignerSignDuration:  "pmsdk_signer_sign_duration_seconds",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("metric name mismatch: got %q want %q", got, want)
		}
	}
}
