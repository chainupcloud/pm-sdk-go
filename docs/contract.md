---
slug: pm-sdk-go-contract
source: design-docs/m10-mirror-making-redesign.md §3.8
created: 2026-04-27
status: Draft
audience: pm-sdk-go independent dev agent
---

# pm-sdk-go 接口契约

> 本文档是 `github.com/chainupcloud/pm-sdk-go` 仓独立开发 agent 的输入。冻结接口形态，确保与 liquidity-service M10 mirror 模块一次性对接成功。
> 风格对标 `github.com/GoPolymarket/polymarket-go-sdk`（DEC-030）。

## 1. 仓库结构

```
pm-sdk-go/
├── client.go             顶层 Client 门面
├── options.go            With* options（HTTP timeout / signer / endpoints / chain id）
├── go.mod                module github.com/chainupcloud/pm-sdk-go
├── pkg/
│   ├── clob/             订单 / 撤单 / 查询；oapi-codegen 底层 + 门面
│   │   ├── client.go
│   │   ├── generated.go      oapi-codegen 输出（CI 生成）
│   │   ├── types.go
│   │   └── examples_test.go
│   ├── gamma/            Event/Market/Token 元数据查询
│   │   ├── client.go
│   │   ├── generated.go
│   │   └── types.go
│   ├── ws/               WebSocket book 订阅
│   │   ├── client.go         手写 reconnect / nonce guard
│   │   └── types.go
│   └── signer/           EIP-712 / pmcup26 5-field auth
│       ├── eip712.go
│       └── pmcup26.go
├── examples/             place_order / subscribe_book / cancel_order
├── docs/
│   ├── README.md
│   └── api.md
└── scripts/
    └── codegen.sh        从 pm-cup2026 仓拉 openapi.yaml + 跑 oapi-codegen
```

## 2. OpenAPI 来源

```
pm-cup2026 仓 (branch: pm-v2):
  services/clob-service/docs/openapi.yaml     → pkg/clob/generated.go
  services/gamma-service/api/openapi.yaml     → pkg/gamma/generated.go  (pm-cup2026#116 落地后)
```

注意：
- clob spec 现在路径是 `services/clob-service/docs/openapi.yaml`（OpenAPI 3.1.0，1420 行 real spec）；本契约修订前曾误写为 `api/`
- gamma 仓内原无 OpenAPI spec，2026-04-27 通过 pm-cup2026#116 新增 `services/gamma-service/api/openapi.yaml`（基于 Polymarket 上游 gamma-openapi.yaml 适配）；PR 未 merge 前 SDK Phase 4 可临时拉 https://docs.polymarket.com/api-spec/gamma-openapi.yaml 兜底，merge 后切回仓内路径

`scripts/codegen.sh` 拉取（git submodule 或 curl）+ `oapi-codegen --config` 生成；CI 校验不漂移。

## 3. 顶层 Client API

```go
package pmsdkgo

type Client struct {
    Clob  *clob.Client
    Gamma *gamma.Client
    WS    *ws.Client
}

func New(opts ...Option) (*Client, error)

type Option func(*config)

func WithEndpoints(clobURL, gammaURL, wsURL string) Option
func WithSigner(signer signer.Signer) Option
func WithHTTPTimeout(d time.Duration) Option
func WithChainID(chainID int64) Option
func WithUserAgent(ua string) Option
func WithLogger(l Logger) Option
func WithRateLimit(rps int) Option
```

## 4. clob 接口

```go
package clob

type Client struct{ /* ... */ }

type OrderReq struct {
    MarketID    string          // varchar(78) downstream market_id
    TokenID     string          // varchar(78) downstream token_id（uint256 字符串）
    Side        Side            // BUY | SELL
    OrderType   OrderType       // LIMIT | MARKET
    Price       decimal.Decimal
    Size        decimal.Decimal
    ClientOrder string          // 幂等 ID
    Expiration  *time.Time
}

type Side string
const ( SideBuy Side = "BUY"; SideSell Side = "SELL" )

type OrderType string
const ( OrderTypeLimit OrderType = "LIMIT"; OrderTypeMarket OrderType = "MARKET" )

func (c *Client) PlaceOrder(ctx context.Context, req OrderReq) (OrderID, error)
func (c *Client) CancelOrder(ctx context.Context, id OrderID) error
func (c *Client) GetOrder(ctx context.Context, id OrderID) (*Order, error)
func (c *Client) ListOrders(ctx context.Context, filter OrderFilter) ([]Order, string /*nextCursor*/, error)
func (c *Client) GetBook(ctx context.Context, tokenID string) (*Book, error)
func (c *Client) GetTrades(ctx context.Context, filter TradeFilter) ([]Trade, string, error)

type OrderID string

type Order struct {
    ID          OrderID
    MarketID    string
    TokenID     string
    Side        Side
    OrderType   OrderType
    Price       decimal.Decimal
    Size        decimal.Decimal
    Filled      decimal.Decimal
    Status      OrderStatus  // OPEN / FILLED / PARTIALLY_FILLED / CANCELLED / REJECTED
    CreatedAt   time.Time
    UpdatedAt   time.Time
    ClientOrder string
}

type Book struct {
    TokenID  string
    Bids     []Level   // 价从高到低
    Asks     []Level   // 价从低到高
    UpdateAt time.Time
}

type Level struct {
    Price decimal.Decimal
    Size  decimal.Decimal
}
```

## 5. gamma 接口

```go
package gamma

type Client struct{ /* ... */ }

func (c *Client) GetEvent(ctx context.Context, eventID int64) (*Event, error)
func (c *Client) ListEvents(ctx context.Context, filter EventFilter) ([]Event, string, error)
func (c *Client) GetMarket(ctx context.Context, marketID int64) (*Market, error)
func (c *Client) GetToken(ctx context.Context, tokenID string) (*Token, error)

type Event struct { ID int64; Slug string; Title string; ConditionID string; Markets []Market }
type Market struct { ID int64; ConditionID string; OutcomeIndex int; YesTokenID string; NoTokenID string }
type Token struct { ID string; MarketID int64; OutcomeIndex int }
```

## 6. WebSocket 接口

```go
package ws

type Client struct{ /* ... */ }

func (c *Client) SubscribeBook(ctx context.Context, tokenIDs []string) (<-chan BookUpdate, error)
func (c *Client) SubscribeOrders(ctx context.Context, userID string) (<-chan OrderUpdate, error)

type BookUpdate struct {
    TokenID  string
    Type     UpdateType // SNAPSHOT / DELTA / RESET
    Bids     []Level
    Asks     []Level
    Sequence int64      // 单调递增；nonce guard 用
    Time     time.Time
}

type OrderUpdate struct {
    OrderID OrderID
    Status  OrderStatus
    Filled  decimal.Decimal
    Time    time.Time
}
```

**重连语义**：自动重连指数退避（1s → 30s 上限）；onReconnect 后推 `Type=RESET`，消费方应清缓存重订；Sequence 跳跃 → 推 RESET 重订。

## 7. signer 接口

```go
package signer

type Signer interface {
    Sign(ctx context.Context, payload []byte) ([]byte, error)
    Address() string
    SchemaVersion() string  // "polymarket-v1" / "pmcup26-v1"
}

func NewEIP712Signer(privKey *ecdsa.PrivateKey, domainSeparator [32]byte) Signer
func NewPMCup26Signer(privKey *ecdsa.PrivateKey, scopeID string) Signer  // 5-field auth
```

## 8. error model

```go
package pmsdkgo

var (
    ErrSign         = errors.New("signer failure")
    ErrRateLimit    = errors.New("upstream rate limit")
    ErrUpstream     = errors.New("upstream error")
    ErrPrecondition = errors.New("precondition failed")
    ErrNotFound     = errors.New("not found")
    ErrCancelled    = errors.New("cancelled by ctx")
)

type APIError struct {
    StatusCode int
    Code       string
    Message    string
    Detail     map[string]any
}

func (e *APIError) Error() string
func (e *APIError) Unwrap() error
```

## 9. observability

- 内置 zap logger 钩子（默认 nop；通过 `WithLogger` 注入）
- 内置 prometheus metrics（`pmsdkgo_request_total{path,status}` / `pmsdkgo_request_duration_seconds` / `pmsdkgo_ws_disconnect_total`）
- 默认 OFF，通过 `WithMetricsRegistry(registry)` 显式 opt-in

## 10. 版本策略（DEC-035）

- semver `v0.x.0`；M10 release = v0.1.0
- 接口冻结后破坏性变更走 `v0.x → v0.(x+1)`，liquidity-service 同步升 require
- liquidity-service `go.mod` `require github.com/chainupcloud/pm-sdk-go v0.1.0`，不使用 go.work / replace（本地调试除外，commit 前必须切回 tag）

## 11. 测试要求（pm-sdk-go 仓内）

- 每个 pkg 80%+ 单测覆盖
- contract test 录回放（real pm-cup2026 staging 抓取请求/响应作为 fixture）
- examples/ 编译并 `go test ./examples/...` 通过

## 12. 与 liquidity-service M10 对接锚点

- liquidity-service 通过 `internal/adapters/downstream/pmcup26/client.go` 包装 `*pmsdkgo.Client` 并实现 `internal/trading/sdk.go` 的 `trading.Client` interface
- 任何 SDK 行为变更必须先改本契约文档 + 双仓同步 issue，再实施
- SDK 不应感知 liquidity 业务概念（mm_market_enable 等）；只暴露 pm-cup2026 公开 API

## 13. 变更记录

| 版本 | 日期 | 变更 |
|------|------|------|
| v0.1 | 2026-04-27 | 初版（M10 设计冻结） |
