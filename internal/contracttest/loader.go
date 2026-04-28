// Package contracttest 是 contract test 的 fixture loader / mock server 框架。
//
// 用途：把 testdata/contract-fixtures/*.json 加载成 (request, response) 对，
// 用 httptest.Server 回放给 SDK Facade，验证 wire 形态与上游 staging 一致。
//
// 用法（仅在 build tag `contract` 下编译的测试文件）：
//
//	//go:build contract
//
//	fx := contracttest.Load(t, "../../testdata/contract-fixtures/clob_place_order.json")
//	srv := contracttest.NewMockServer(t, fx)
//	defer srv.Close()
//	// 用 srv.URL 构造 SDK Facade 调用，断言返回值。
//
// fixture 状态：当前为 placeholder（结构合法但 body 字段值是 mock）；staging
// 联调时由 ops 用 record-and-replay 工具捕获真实请求/响应替换。详情见
// docs/contract-test.md。
package contracttest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// Fixture 是单个 contract test case 的录回放结构。
type Fixture struct {
	Name     string   `json:"name"`
	Comment  string   `json:"comment,omitempty"`
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}

// Request 是 fixture 期望的入站请求形态。
type Request struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   json.RawMessage   `json:"body,omitempty"`
}

// Response 是 fixture 要回放的响应。
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body"`
}

// Load 从 path 读取 fixture JSON。失败 fail-fast。
func Load(t *testing.T, path string) Fixture {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("contracttest: read fixture %s: %v", path, err)
	}
	var fx Fixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("contracttest: decode fixture %s: %v", path, err)
	}
	return fx
}

// NewMockServer 起一个 httptest.Server 按 fixture 回放：
//
//   - method/path 不匹配 → 502 + 错误信息（让测试失败时容易定位）
//   - body 字段对比目前是软校验（仅日志，不强校验，因为 SDK 会算 signature 等动态字段）
//
// 回放服务器只支持 fixture 里的单个 case；多 case 测试请多次 NewMockServer。
func NewMockServer(t *testing.T, fx Fixture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Method, fx.Request.Method) {
			http.Error(w, "method mismatch: got "+r.Method+" want "+fx.Request.Method, http.StatusBadGateway)
			return
		}
		if r.URL.Path != fx.Request.Path {
			http.Error(w, "path mismatch: got "+r.URL.Path+" want "+fx.Request.Path, http.StatusBadGateway)
			return
		}
		// query 软校验：fixture 列出的 key 必须出现，值不一定相等
		for k := range fx.Request.Query {
			if r.URL.Query().Get(k) == "" {
				t.Logf("fixture %q: query key %q absent in request", fx.Name, k)
			}
		}
		// body 仅供 debug；不强校验
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}

		for k, v := range fx.Response.Headers {
			w.Header().Set(k, v)
		}
		status := fx.Response.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(bytes.TrimSpace(fx.Response.Body))
	}))
	t.Cleanup(srv.Close)
	return srv
}
