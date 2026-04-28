package pmsdkgo

import "errors"

// 哨兵错误（契约 §8）。子包返回的具体错误应通过 errors.Is 可匹配到这些哨兵。
var (
	// ErrSign 签名失败（私钥不可用 / payload 格式不对等）。
	ErrSign = errors.New("pmsdkgo: signer failure")
	// ErrRateLimit 上游 429 / 本地限流触发。
	ErrRateLimit = errors.New("pmsdkgo: upstream rate limit")
	// ErrUpstream 上游 5xx / 不可恢复错误。
	ErrUpstream = errors.New("pmsdkgo: upstream error")
	// ErrPrecondition 业务前置不满足（余额不足 / 价格越界等）。
	ErrPrecondition = errors.New("pmsdkgo: precondition failed")
	// ErrNotFound 资源不存在（404）。
	ErrNotFound = errors.New("pmsdkgo: not found")
	// ErrCancelled context 取消 / 客户端主动 cancel。
	ErrCancelled = errors.New("pmsdkgo: cancelled by ctx")
)

// APIError 是 HTTP 请求失败时的结构化错误。
// 通过 Unwrap 暴露上述哨兵；调用方既可用 errors.Is 也可类型断言取详情。
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Detail     map[string]any

	// sentinel 由构造方根据 StatusCode/Code 选择，供 Unwrap 返回。
	sentinel error
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return "pmsdkgo: api error: " + e.Code + ": " + e.Message
	}
	return "pmsdkgo: api error: " + e.Message
}

// Unwrap 返回对应的哨兵 error，便于 errors.Is 匹配。
func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.sentinel
}
