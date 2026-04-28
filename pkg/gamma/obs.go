package gamma

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

// observe 与 pkg/clob/obs.go 同形态：返回挂点对象，HTTP 调用前后 .done() 上报。
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
		c.f.logger.Warnw("gamma http error",
			"op", c.op,
			"method", c.method,
			"path", c.path,
			"duration_ms", dur.Milliseconds(),
			"error", err.Error(),
		)
		return
	}
	if resp != nil && resp.StatusCode >= 400 {
		c.f.logger.Warnw("gamma http non-2xx",
			"op", c.op,
			"method", c.method,
			"path", c.path,
			"status", status,
			"request_id", requestID,
			"duration_ms", dur.Milliseconds(),
		)
		return
	}
	c.f.logger.Debugw("gamma http ok",
		"op", c.op,
		"method", c.method,
		"path", c.path,
		"status", status,
		"request_id", requestID,
		"duration_ms", dur.Milliseconds(),
	)
}
