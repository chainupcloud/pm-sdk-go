package pmsdkgo

import (
	"net/http"
	"time"

	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
	"github.com/chainupcloud/pm-sdk-go/pkg/ws"
)

// Option 是 New 的配置入参。所有 With* 函数返回 Option。
type Option func(*config)

// WithEndpoints 设置 clob / gamma / ws 三个 endpoint URL。
// 任一参数为空字符串表示沿用默认值。
func WithEndpoints(clobURL, gammaURL, wsURL string) Option {
	return func(c *config) {
		if clobURL != "" {
			c.clobURL = clobURL
		}
		if gammaURL != "" {
			c.gammaURL = gammaURL
		}
		if wsURL != "" {
			c.wsURL = wsURL
		}
	}
}

// WithSigner 注入签名器，用于下单等需要 EIP-712 / pmcup26 5-field auth 的请求。
func WithSigner(s signer.Signer) Option {
	return func(c *config) {
		c.signer = s
	}
}

// WithHTTPTimeout 设置单次 HTTP 请求超时。默认 30s。
func WithHTTPTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.httpTimeout = d
		}
	}
}

// WithHTTPClient 注入自定义 *http.Client（如需自带 transport / proxy / mTLS）。
// 注入后 WithHTTPTimeout 仍生效，但作用于 client 内部 ctx，不覆盖外层 transport。
func WithHTTPClient(hc *http.Client) Option {
	return func(c *config) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithChainID 指定 EIP-712 domain chainId；与 signer 必须一致。
func WithChainID(chainID int64) Option {
	return func(c *config) {
		c.chainID = chainID
	}
}

// WithUserAgent 覆盖默认 User-Agent header。
func WithUserAgent(ua string) Option {
	return func(c *config) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// WithLogger 注入 zap 风格的轻量 Logger 钩子。默认 nop。
//
// 可用 pkg/obs/zapobs.New(*zap.Logger) 构造 zap adapter。
func WithLogger(l Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithMetrics 注入 prometheus 风格的轻量 Metrics 钩子。默认 nop。
//
// 可用 pkg/obs/promobs.New(prometheus.Registerer) 构造 prometheus adapter。
// SDK 暴露的标准指标见 pkg/obs/metrics.go 的 Metric* 常量。
func WithMetrics(m Metrics) Option {
	return func(c *config) {
		if m != nil {
			c.metrics = m
		}
	}
}

// WithRateLimit 设置每秒最多请求数；0 表示不限速。
// Phase 1 仅记录配置值，限流实现见 Phase 7 observability。
func WithRateLimit(rps int) Option {
	return func(c *config) {
		if rps >= 0 {
			c.rateLimit = rps
		}
	}
}

// WithWSUserAuth 注入 WebSocket 用户频道凭证（apiKey + passphrase）。
// 缺失时 Client.WS.SubscribeOrders 返回 ErrSign。
func WithWSUserAuth(auth ws.UserAuth) Option {
	return func(c *config) {
		a := auth
		c.wsUserAuth = &a
	}
}
