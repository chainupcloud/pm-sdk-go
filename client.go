package pmsdkgo

import (
	"errors"
	"net/http"
	"time"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/chainupcloud/pm-sdk-go/pkg/gamma"
	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
	"github.com/chainupcloud/pm-sdk-go/pkg/ws"
)

// Client 是 pm-sdk-go 的顶层门面，聚合各业务子包客户端。
//
// Phase 1 仅暴露字段与构造函数；具体业务方法在 Phase 3+ 实现。
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

// defaults 返回 Phase 1 的默认配置占位值。
// 真实默认 endpoint 将在 Phase 2 codegen 落地后从 OpenAPI servers 字段推导。
func defaults() *config {
	return &config{
		clobURL:     "",
		gammaURL:    "",
		wsURL:       "",
		chainID:     0,
		httpTimeout: 30 * time.Second,
		httpClient:  http.DefaultClient,
		userAgent:   "pm-sdk-go/0.1.0",
		logger:      nopLogger{},
	}
}

// New 构造一个 Client。
//
// Phase 1 仅做配置组装与子客户端占位实例化；返回的 Client 暂不可执行真实请求，
// 调用 Clob/Gamma/WS 方法会得到 ErrNotImplemented。
func New(opts ...Option) (*Client, error) {
	cfg := defaults()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}

	c := &Client{cfg: cfg}

	// 子客户端占位：实际构造在各 pkg 的 Phase 3+ 实现。
	c.Clob = clob.NewStub()
	c.Gamma = gamma.NewStub()
	c.WS = ws.NewStub()

	return c, nil
}

// ErrNotImplemented 在 Phase 1 占位阶段由各子客户端方法返回。
// 进入 Phase 3+ 实现后将逐步消失。
var ErrNotImplemented = errors.New("pmsdkgo: not implemented (phase 1 scaffold)")
