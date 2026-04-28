package pmsdkgo

import (
	"net/http"
	"time"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/chainupcloud/pm-sdk-go/pkg/gamma"
	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
	"github.com/chainupcloud/pm-sdk-go/pkg/ws"
)

// Client 是 pm-sdk-go 的顶层门面，聚合各业务子包客户端。
//
// Phase 2：Clob/Gamma 直接持有 oapi-codegen 生成的低层 *clob.Client / *gamma.Client；
// WS 仍是 Phase 1 占位。Phase 3+ 会引入手写门面替换字段类型。
type Client struct {
	Clob  *clob.Client
	Gamma *gamma.Client
	WS    *ws.Client

	cfg *config
}

// config 是 Client 的内部配置容器，由 Option 写入。
type config struct {
	clobURL     string
	gammaURL    string
	wsURL       string
	chainID     int64
	httpTimeout time.Duration
	httpClient  *http.Client
	userAgent   string
	signer      signer.Signer
	logger      Logger
	rateLimit   int // requests per second; 0 表示不限速
}

// defaults 返回默认配置。真实默认 endpoint 将在后续 phase 接入 servers 字段推导。
func defaults() *config {
	return &config{
		httpTimeout: 30 * time.Second,
		httpClient:  http.DefaultClient,
		userAgent:   "pm-sdk-go/0.1.0",
		logger:      nopLogger{},
	}
}

// New 构造一个 Client。
//
// Phase 2 仅做配置组装并实例化 oapi-codegen 生成的低层 *clob.Client / *gamma.Client；
// 业务方法（PlaceOrder / GetEvent 等）在 Phase 3+ 落地。
func New(opts ...Option) (*Client, error) {
	cfg := defaults()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}

	clobCli, err := clob.NewClient(cfg.clobURL)
	if err != nil {
		return nil, err
	}
	gammaCli, err := gamma.NewClient(cfg.gammaURL)
	if err != nil {
		return nil, err
	}

	return &Client{
		Clob:  clobCli,
		Gamma: gammaCli,
		WS:    ws.NewStub(),
		cfg:   cfg,
	}, nil
}
