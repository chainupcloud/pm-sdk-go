package relayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// 哨兵错误。子包内 wrapHTTPError 将 HTTP 响应映射到对应哨兵；
// 调用方既可用 errors.Is 也可类型断言 *APIError 取详情。
//
// 与 pkg/clob 的哨兵语义保持一致（鉴权/限流/上游/前置/未找到/取消），但作为独立
// 变量声明——pkg/relayer 不依赖 pkg/clob，避免循环引用并保留各包独立演化空间。
var (
	// ErrAuth 鉴权失败（401/403：API Key 无效 / Bearer 过期 / scopeId 不匹配等）。
	ErrAuth = errors.New("relayer: auth failure")
	// ErrRateLimit 上游 429（如 SAFE-CREATE per-IP 限流）。
	ErrRateLimit = errors.New("relayer: upstream rate limit")
	// ErrUpstream 上游 5xx / 不可恢复错误。
	ErrUpstream = errors.New("relayer: upstream error")
	// ErrPrecondition 业务前置不满足（400：scopeId 缺失、签名缺失、whitelist 拒绝等）。
	ErrPrecondition = errors.New("relayer: precondition failed")
	// ErrNotFound 资源不存在（404）。
	ErrNotFound = errors.New("relayer: not found")
	// ErrConflict SAFE-CREATE slot 已被占用（409）。
	ErrConflict = errors.New("relayer: conflict")
	// ErrCancelled context 取消 / 客户端主动 cancel。
	ErrCancelled = errors.New("relayer: cancelled by ctx")
	// ErrInvalidConfig 调用方传入参数不合法（如空 baseURL、零私钥）。
	ErrInvalidConfig = errors.New("relayer: invalid config")
	// ErrTxFailed Service 轮询到 STATE_FAILED / STATE_INVALID 终态。
	ErrTxFailed = errors.New("relayer: tx terminal failure")
	// ErrTxTimeout Service 轮询超过预算仍未到终态。
	ErrTxTimeout = errors.New("relayer: tx wait timeout")
)

// APIError 是 HTTP 请求失败时的结构化错误。
type APIError struct {
	StatusCode int
	Message    string
	Body       []byte

	sentinel error
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("relayer: api error %d: %s", e.StatusCode, e.Message)
}

// Unwrap 让 errors.Is 能匹配到对应哨兵。
func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}

const maxStoredBody = 8 * 1024

// wrapHTTPError 把上游非 2xx 响应映射成 *APIError + 哨兵。
//
// relayer-service 的错误 body 形如 `{"error":"..."}`，handler.writeError 出来；
// 解析失败兜底用 http.StatusText。哨兵映射：
//
//	401/403  → ErrAuth
//	404      → ErrNotFound
//	409      → ErrConflict
//	429      → ErrRateLimit
//	400/422  → ErrPrecondition
//	5xx      → ErrUpstream
//	其他     → ErrUpstream
func wrapHTTPError(resp *http.Response, body []byte) error {
	if resp == nil {
		return &APIError{Message: "nil response", sentinel: ErrUpstream}
	}
	apiErr := &APIError{StatusCode: resp.StatusCode}
	if len(body) > 0 {
		if len(body) <= maxStoredBody {
			apiErr.Body = append([]byte(nil), body...)
		} else {
			apiErr.Body = append([]byte(nil), body[:maxStoredBody]...)
		}
		var simple struct {
			Error string `json:"error"`
		}
		if jerr := json.Unmarshal(body, &simple); jerr == nil {
			apiErr.Message = simple.Error
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		apiErr.sentinel = ErrAuth
	case resp.StatusCode == http.StatusNotFound:
		apiErr.sentinel = ErrNotFound
	case resp.StatusCode == http.StatusConflict:
		apiErr.sentinel = ErrConflict
	case resp.StatusCode == http.StatusTooManyRequests:
		apiErr.sentinel = ErrRateLimit
	case resp.StatusCode == http.StatusBadRequest,
		resp.StatusCode == http.StatusUnprocessableEntity:
		apiErr.sentinel = ErrPrecondition
	case resp.StatusCode >= 500:
		apiErr.sentinel = ErrUpstream
	default:
		apiErr.sentinel = ErrUpstream
	}
	return apiErr
}

// wrapTransportError 把传输层错误（连接失败 / ctx 取消）规范化。
func wrapTransportError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrCancelled, err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrCancelled, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}
