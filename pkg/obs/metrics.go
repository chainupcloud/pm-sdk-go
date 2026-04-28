package obs

// Metrics 是 SDK 内部使用的轻量指标接口。
//
// 仅暴露两类：
//   - IncCounter(name, labels) — 累加计数器（请求总数、重连次数、签名次数等）
//   - ObserveHistogram(name, value, labels) — 观察直方图（请求耗时、签名耗时等）
//
// labels 用 map[string]string 而非 prometheus 风格的 [...]string，是为了让 SDK
// 调用点保持自描述 + 与具体 collector 解耦。adapter 实现内部把 map 映射成
// prometheus.Labels 即可。
//
// 实现要求：
//   - goroutine-safe
//   - 性能敏感路径建议 adapter 内部用预创建 collector + cached label set
type Metrics interface {
	IncCounter(name string, labels map[string]string)
	ObserveHistogram(name string, value float64, labels map[string]string)
}

// NopMetrics 是默认 Metrics 实现：所有方法 no-op。
type NopMetrics struct{}

// IncCounter 实现 Metrics。
func (NopMetrics) IncCounter(string, map[string]string) {}

// ObserveHistogram 实现 Metrics。
func (NopMetrics) ObserveHistogram(string, float64, map[string]string) {}

// 标准指标名（契约 §9）。SDK 调用挂点统一使用以下常量，避免拼写漂移：
//
//	pmsdk_http_requests_total{path,method,status}
//	pmsdk_http_request_duration_seconds{path}
//	pmsdk_ws_reconnects_total{channel}
//	pmsdk_ws_seq_jumps_total{channel}
//	pmsdk_signer_sign_total{schema}
//	pmsdk_signer_sign_duration_seconds{schema}
const (
	MetricHTTPRequestsTotal      = "pmsdk_http_requests_total"
	MetricHTTPRequestDuration    = "pmsdk_http_request_duration_seconds"
	MetricWSReconnectsTotal      = "pmsdk_ws_reconnects_total"
	MetricWSSeqJumpsTotal        = "pmsdk_ws_seq_jumps_total"
	MetricSignerSignTotal        = "pmsdk_signer_sign_total"
	MetricSignerSignDuration     = "pmsdk_signer_sign_duration_seconds"
)
