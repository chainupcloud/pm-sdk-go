package clob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// 哨兵错误（契约 §8）。子包内 wrapHTTPError 将 HTTP 响应映射到对应哨兵；
// 调用方既可用 errors.Is(err, clob.ErrNotFound) 也可类型断言 *APIError 取详情。
//
// 顶层 pmsdkgo 包通过 alias 重导出这些哨兵，保持契约 §8 的 `pmsdkgo.ErrXxx` 可见性。
var (
	// ErrSign 签名失败（私钥不可用 / payload 格式不对 / 401 鉴权失败）。
	ErrSign = errors.New("pmsdkgo: signer failure")
	// ErrRateLimit 上游 429 / 本地限流触发。
	ErrRateLimit = errors.New("pmsdkgo: upstream rate limit")
	// ErrUpstream 上游 5xx / 不可恢复错误。
	ErrUpstream = errors.New("pmsdkgo: upstream error")
	// ErrPrecondition 业务前置不满足（412 / 422 余额不足、价格越界等）。
	ErrPrecondition = errors.New("pmsdkgo: precondition failed")
	// ErrNotFound 资源不存在（404）。
	ErrNotFound = errors.New("pmsdkgo: not found")
	// ErrCancelled context 取消 / 客户端主动 cancel。
	ErrCancelled = errors.New("pmsdkgo: cancelled by ctx")
)

// APIError 是 HTTP 请求失败时的结构化错误（契约 §8）。
//
// 字段：
//   - StatusCode：HTTP 响应码
//   - Code：上游 ErrorResponse 的业务错误码（若 body 解析出 code 字段）
//   - Message：人类可读消息
//   - RequestID：上游响应 X-Request-Id header（若有）
//   - Body：原始响应 body（最多 8KiB，调试用）
//   - Detail：解析出的额外字段（map[string]any）
//
// Unwrap 返回根据 StatusCode 选择的哨兵 error，便于 errors.Is 匹配。
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	Body       []byte
	Detail     map[string]any

	// sentinel 由 wrapHTTPError 根据 StatusCode/Code 选择，供 Unwrap 返回。
	sentinel error
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("pmsdkgo: api error %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("pmsdkgo: api error %d: %s", e.StatusCode, e.Message)
}

// Unwrap 返回对应的哨兵 error，便于 errors.Is 匹配。
func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}

// SetSentinel 设置 Unwrap 返回的哨兵 error；供同 module 内其他子包（如 gamma）
// 在自己的 wrapHTTPError 中复用同一份 *APIError 类型时挂钩 sentinel。
func (e *APIError) SetSentinel(s error) {
	if e == nil {
		return
	}
	e.sentinel = s
}

// 限制原始 body 留存大小，避免内存膨胀。
const maxStoredBody = 8 * 1024

// wrapHTTPError 把上游非 2xx 响应映射成 *APIError + 哨兵。
//
// 入参：
//   - resp：HTTP response（StatusCode / Header 由本函数读取，body 由 caller 提供）
//   - body：已 ReadAll 的响应 body 字节
//
// 解析策略：
//  1. 优先按上游 ErrorResponse `{"error": "..."}` schema 解析 message
//  2. 兜底按 generic map[string]any 解析以填 Detail / Code（若上游加了 code 字段）
//  3. body 解析失败也照样返回 APIError，message 用 http.StatusText
//
// 哨兵映射（基于 StatusCode）：
//
//	401     → ErrSign           // 鉴权失败 = 签名问题
//	403     → ErrSign           // 同上
//	404     → ErrNotFound
//	412     → ErrPrecondition
//	422     → ErrPrecondition   // 业务校验失败（余额不足 / 价格越界）
//	429     → ErrRateLimit
//	5xx     → ErrUpstream
//	其他    → ErrUpstream（默认）
func wrapHTTPError(resp *http.Response, body []byte) error {
	if resp == nil {
		return &APIError{Message: "nil response", sentinel: ErrUpstream}
	}

	apiErr := &APIError{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("X-Request-Id"),
	}
	if len(body) > 0 {
		// trim 存储 body，避免内存膨胀
		if len(body) <= maxStoredBody {
			apiErr.Body = append([]byte(nil), body...)
		} else {
			apiErr.Body = append([]byte(nil), body[:maxStoredBody]...)
		}

		// 上游 ErrorResponse schema：{"error": "..."}
		var simple struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if jerr := json.Unmarshal(body, &simple); jerr == nil {
			apiErr.Message = simple.Error
			apiErr.Code = simple.Code
		}

		// 兜底 generic map（保留全部字段供 Detail 调试）
		var generic map[string]any
		if jerr := json.Unmarshal(body, &generic); jerr == nil {
			apiErr.Detail = generic
			if apiErr.Message == "" {
				if v, ok := generic["message"].(string); ok {
					apiErr.Message = v
				}
			}
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		apiErr.sentinel = ErrSign
	case resp.StatusCode == http.StatusNotFound:
		apiErr.sentinel = ErrNotFound
	case resp.StatusCode == http.StatusPreconditionFailed,
		resp.StatusCode == http.StatusUnprocessableEntity:
		apiErr.sentinel = ErrPrecondition
	case resp.StatusCode == http.StatusTooManyRequests:
		apiErr.sentinel = ErrRateLimit
	case resp.StatusCode >= 500:
		apiErr.sentinel = ErrUpstream
	default:
		apiErr.sentinel = ErrUpstream
	}
	return apiErr
}

// wrapTransportError 把传输层错误（连接失败 / ctx 取消）规范化。
// 主要职责：把 ctx.Err() 暴露的取消信号映射到 ErrCancelled。
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
