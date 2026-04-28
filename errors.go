package pmsdkgo

import (
	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
)

// 契约 §8 哨兵错误与 APIError 的真实定义在 pkg/clob/errors.go；
// 顶层这里通过 alias 重导出，使 `pmsdkgo.ErrSign` 等保持契约可见性。
//
// 子包返回的具体错误（含 *clob.APIError 包装）通过 errors.Is 与这些哨兵匹配。
var (
	// ErrSign 签名失败（私钥不可用 / payload 格式不对 / 401-403 鉴权失败）。
	ErrSign = clob.ErrSign
	// ErrRateLimit 上游 429 / 本地限流触发。
	ErrRateLimit = clob.ErrRateLimit
	// ErrUpstream 上游 5xx / 不可恢复错误。
	ErrUpstream = clob.ErrUpstream
	// ErrPrecondition 业务前置不满足（412 / 422）。
	ErrPrecondition = clob.ErrPrecondition
	// ErrNotFound 资源不存在（404）。
	ErrNotFound = clob.ErrNotFound
	// ErrCancelled context 取消 / 客户端主动 cancel。
	ErrCancelled = clob.ErrCancelled
)

// APIError 是 HTTP 请求失败时的结构化错误（契约 §8）。
// 真正的字段定义见 pkg/clob/errors.go；此处只做 alias 暴露。
type APIError = clob.APIError
