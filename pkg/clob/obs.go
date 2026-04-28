package clob

import (
	"net/http"
	"strconv"
	"time"

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

// observe 在 facade HTTP 调用前后挂 logger + metrics。
//
// 使用范式：
//
//	op := f.observe("PlaceOrder", "POST", "/order")
//	resp, err := f.low.PostOrder(ctx, body)
//	op.done(resp, err)
//
// 标签策略：path/method 直接传入；status 在 done 时从 resp 读（transport err
// 时填 "transport_error"）。HTTP 状态被 SDK 视为成功（2xx）也照样 inc，符合
// 标准 RED 指标语义。
func (f *Facade) observe(op, method, path string) *obsCall {
	return &obsCall{
		f:       f,
		op:      op,
		method:  method,
		path:    path,
		started: time.Now(),
	}
}

type obsCall struct {
	f       *Facade
	op      string
	method  string
	path    string
	started time.Time
}

func (c *obsCall) done(resp *http.Response, err error) {
	if c == nil || c.f == nil {
		return
	}
	dur := time.Since(c.started)
	status := "transport_error"
	requestID := ""
	if resp != nil {
		status = strconv.Itoa(resp.StatusCode)
		requestID = resp.Header.Get("X-Request-Id")
	}
	labels := map[string]string{
		"path":   c.path,
		"method": c.method,
		"status": status,
	}
	c.f.metrics.IncCounter(obs.MetricHTTPRequestsTotal, labels)
	c.f.metrics.ObserveHistogram(obs.MetricHTTPRequestDuration,
		dur.Seconds(),
		map[string]string{"path": c.path},
	)
	if err != nil {
		c.f.logger.Warnw("clob http error",
			"op", c.op,
			"method", c.method,
			"path", c.path,
			"duration_ms", dur.Milliseconds(),
			"error", err.Error(),
		)
		return
	}
	if resp != nil && resp.StatusCode >= 400 {
		c.f.logger.Warnw("clob http non-2xx",
			"op", c.op,
			"method", c.method,
			"path", c.path,
			"status", status,
			"request_id", requestID,
			"duration_ms", dur.Milliseconds(),
		)
		return
	}
	c.f.logger.Debugw("clob http ok",
		"op", c.op,
		"method", c.method,
		"path", c.path,
		"status", status,
		"request_id", requestID,
		"duration_ms", dur.Milliseconds(),
	)
}
