package gamma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
)

// 哨兵 + APIError 复用 pkg/clob 定义（契约 §8 顶层 pmsdkgo.ErrXxx / APIError 由
// clob 包真正实现；gamma 只在错误映射函数里 sentinel switch 处复用 clob 哨兵
// 常量）。
//
// 选择：复制 wrapHTTPError / wrapTransportError 而不抽 internal/httpx，理由：
//   1. 共享 50 行函数 vs 引入新内部包，前者更轻
//   2. 保留 Phase 3 已绿测试代码不动（Surgical Changes）
//   3. Phase 5/6 ws/signer 加入时再评估是否抽内部包

var (
	errSign         = clob.ErrSign
	errRateLimit    = clob.ErrRateLimit
	errUpstream     = clob.ErrUpstream
	errPrecondition = clob.ErrPrecondition
	errNotFound     = clob.ErrNotFound
	errCancelled    = clob.ErrCancelled
)

// 限制原始 body 留存大小，避免内存膨胀。
const maxStoredBody = 8 * 1024

// wrapHTTPError 把上游非 2xx 响应映射成 *clob.APIError + 哨兵。
//
// 哨兵映射：
//
//	401/403  → ErrSign（gamma 大多 read-only / 公开，但 /auth/login 等仍可能 401）
//	404      → ErrNotFound
//	412/422  → ErrPrecondition
//	429      → ErrRateLimit
//	5xx      → ErrUpstream
//	其他     → ErrUpstream
func wrapHTTPError(resp *http.Response, body []byte) error {
	if resp == nil {
		return &clob.APIError{Message: "nil response"}
	}

	apiErr := &clob.APIError{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("X-Request-Id"),
	}
	if len(body) > 0 {
		if len(body) <= maxStoredBody {
			apiErr.Body = append([]byte(nil), body...)
		} else {
			apiErr.Body = append([]byte(nil), body[:maxStoredBody]...)
		}

		var simple struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if jerr := json.Unmarshal(body, &simple); jerr == nil {
			apiErr.Message = simple.Error
			apiErr.Code = simple.Code
		}

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
		apiErr.SetSentinel(errSign)
	case resp.StatusCode == http.StatusNotFound:
		apiErr.SetSentinel(errNotFound)
	case resp.StatusCode == http.StatusPreconditionFailed,
		resp.StatusCode == http.StatusUnprocessableEntity:
		apiErr.SetSentinel(errPrecondition)
	case resp.StatusCode == http.StatusTooManyRequests:
		apiErr.SetSentinel(errRateLimit)
	case resp.StatusCode >= 500:
		apiErr.SetSentinel(errUpstream)
	default:
		apiErr.SetSentinel(errUpstream)
	}
	return apiErr
}

// wrapTransportError 把传输层错误（连接失败 / ctx 取消）规范化。
func wrapTransportError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", errCancelled, err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", errCancelled, err)
	}
	return fmt.Errorf("%w: %v", errUpstream, err)
}
