package pmsdkgo

import (
	"net/http"
	"time"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/chainupcloud/pm-sdk-go/pkg/gamma"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
	"github.com/chainupcloud/pm-sdk-go/pkg/ws"
)

// Client 是 pm-sdk-go 的顶层门面，聚合各业务子包客户端。
//
// Phase 3：Clob 切换到手写门面 *clob.Facade。
// Phase 4：Gamma 切换到手写门面 *gamma.Facade（GetEvent / ListEvents /
// GetMarket / GetToken）。
// Phase 5：WS 切换到手写门面 *ws.Facade（SubscribeBook / SubscribeOrders +
// 自动重连 + sequence guard）。
type Client struct {
	Clob  *clob.Facade
	Gamma *gamma.Facade
	WS    *ws.Facade

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
	metrics     Metrics
	rateLimit   int // requests per second; 0 表示不限速

	// wsUserAuth 是 ws 用户频道凭证（SubscribeOrders 必需）；nil 时 SubscribeOrders 报 ErrSign。
	wsUserAuth *ws.UserAuth
}

// defaults 返回默认配置。真实默认 endpoint 将在后续 phase 接入 servers 字段推导。
func defaults() *config {
	return &config{
		httpTimeout: 30 * time.Second,
		httpClient:  http.DefaultClient,
		userAgent:   "pm-sdk-go/0.1.0",
		logger:      obs.NopLogger{},
		metrics:     obs.NopMetrics{},
	}
}

// New 构造一个 Client。
//
// Phase 3：Clob 走手写门面 *clob.Facade；可选 signer 透传给 Facade，无 signer
// 时 PlaceOrder 返回 ErrSign。Gamma 仍走 generated 低层 client（Phase 4 替换）。
func New(opts ...Option) (*Client, error) {
	cfg := defaults()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}

	clobOpts := []clob.FacadeOption{
		clob.WithLogger(cfg.logger),
		clob.WithMetrics(cfg.metrics),
	}
	if cfg.signer != nil {
		clobOpts = append(clobOpts, clob.WithSigner(wrapSignerObs(cfg.signer, cfg.logger, cfg.metrics)))
	}
	clobFacade, err := clob.NewFacade(cfg.clobURL, cfg.httpClient, clobOpts...)
	if err != nil {
		return nil, err
	}
	gammaFacade, err := gamma.NewFacade(cfg.gammaURL, cfg.httpClient,
		gamma.WithLogger(cfg.logger),
		gamma.WithMetrics(cfg.metrics),
	)
	if err != nil {
		return nil, err
	}
	wsOpts := []ws.FacadeOption{
		ws.WithLogger(cfg.logger),
		ws.WithMetrics(cfg.metrics),
	}
	if cfg.wsUserAuth != nil {
		wsOpts = append(wsOpts, ws.WithUserAuth(*cfg.wsUserAuth))
	}
	wsFacade, err := ws.NewFacade(cfg.wsURL, wsOpts...)
	if err != nil {
		return nil, err
	}

	return &Client{
		Clob:  clobFacade,
		Gamma: gammaFacade,
		WS:    wsFacade,
		cfg:   cfg,
	}, nil
}
