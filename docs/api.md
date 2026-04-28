# pm-sdk-go API Reference

> 完整 API 参考，对应 v0.1.0。设计契约权威源：
> [pm-sdk-go-contract.md](https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/design-docs/pm-sdk-go-contract.md)。

目录
- [顶层 Client](#顶层-client)
- [pkg/clob](#pkgclob)
- [pkg/gamma](#pkggamma)
- [pkg/ws](#pkgws)
- [pkg/signer](#pkgsigner)
- [pkg/obs](#pkgobs)
- [错误模型](#错误模型)

---

## 顶层 Client

```go
import pmsdkgo "github.com/chainupcloud/pm-sdk-go"

cli, err := pmsdkgo.New(opts...)
```

### `type Client`

| 字段 | 类型 | 说明 |
|------|------|------|
| `Clob` | `*clob.Facade` | clob 业务门面 |
| `Gamma` | `*gamma.Facade` | gamma 业务门面 |
| `WS` | `*ws.Facade` | WebSocket 频道门面 |

### `func New(opts ...Option) (*Client, error)`

按顺序应用 Option，构造 Client。返回的 Client 可被多 goroutine 并发复用。

### Options

| Option | 说明 |
|--------|------|
| `WithEndpoints(clobURL, gammaURL, wsURL string)` | 设置三个 endpoint；空串沿用默认 |
| `WithSigner(signer.Signer)` | 注入签名器；缺失时 `PlaceOrder` 返回 `ErrSign` |
| `WithHTTPTimeout(time.Duration)` | 单次 HTTP 请求超时（默认 30s） |
| `WithHTTPClient(*http.Client)` | 自带 transport / proxy / mTLS |
| `WithChainID(int64)` | EIP-712 domain chainId |
| `WithUserAgent(string)` | 覆盖默认 User-Agent |
| `WithLogger(Logger)` | observability：日志钩子（默认 Nop） |
| `WithMetrics(Metrics)` | observability：指标钩子（默认 Nop） |
| `WithRateLimit(rps int)` | 客户端限流；0 = 不限速 |
| `WithWSUserAuth(ws.UserAuth)` | WS user channel 凭证（`SubscribeOrders` 必需） |

### Mini quickstart

```go
cli, err := pmsdkgo.New(
    pmsdkgo.WithEndpoints("https://clob.x", "https://gamma.x", "wss://ws.x"),
    pmsdkgo.WithChainID(137),
    pmsdkgo.WithSigner(mySigner),
)
if err != nil { log.Fatal(err) }
```

---

## pkg/clob

下单 / 撤单 / 查询订单与订单簿。

### Methods on `*clob.Facade`

| 方法 | 说明 |
|------|------|
| `PlaceOrder(ctx, OrderReq) (OrderID, error)` | 下单；signer 必需 |
| `CancelOrder(ctx, OrderID) error` | 撤单 |
| `GetOrder(ctx, OrderID) (*SdkOrder, error)` | 查单个订单 |
| `ListOrders(ctx, OrderFilter) ([]SdkOrder, nextCursor string, error)` | 列出订单 |
| `GetBook(ctx, tokenID string) (*Book, error)` | 取订单簿快照 |
| `GetTrades(ctx, TradeFilter) ([]SdkTrade, nextCursor string, error)` | 查成交 |

### `type OrderReq`

| 字段 | 类型 | 说明 |
|------|------|------|
| `MarketID` | `string` | condition_id |
| `TokenID` | `string` | uint256 字符串 |
| `Side` | `SdkSide` | `SideBuy` / `SideSell` |
| `OrderType` | `SdkOrderType` | `OrderTypeLimit` / `OrderTypeMarket` |
| `Price` | `decimal.Decimal` | 限价（USDC 计价） |
| `Size` | `decimal.Decimal` | 数量（token 计） |
| `ClientOrder` | `string` | 调用方幂等 ID |
| `Expiration` | `*time.Time` | GTD 单的过期时间 |

### `type SdkOrder`

| 字段 | 类型 |
|------|------|
| `ID` | `OrderID` |
| `MarketID` | `string` |
| `TokenID` | `string` |
| `Side` | `SdkSide` |
| `OrderType` | `SdkOrderType` |
| `Price` | `decimal.Decimal` |
| `Size` | `decimal.Decimal` |
| `Filled` | `decimal.Decimal` |
| `Status` | `SdkOrderStatus` |
| `CreatedAt` / `UpdatedAt` | `time.Time` |
| `ClientOrder` | `string` |

### `type Book` / `type Level`

```go
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

### `type SdkTrade`

| 字段 | 类型 |
|------|------|
| `ID` | `string` |
| `MarketID` / `TokenID` | `string` |
| `Side` | `SdkSide` |
| `Price` / `Size` | `decimal.Decimal` |
| `MatchTime` | `time.Time` |
| `OrderID` | `OrderID` |
| `TraderSide` | `string` (`MAKER`/`TAKER` 透传) |
| `Status` | `string` (上游 trade status) |

### Filters

```go
type OrderFilter struct {
    OrderID    string  // order_hash / id
    MarketID   string  // condition_id
    TokenID    string
    NextCursor string  // 分页游标，"LTE=" = last page
}

type TradeFilter struct {
    MakerAddress string   // 必填
    TradeID      string
    MarketID     string
    TokenID      string
    Before       *time.Time
    After        *time.Time
    NextCursor   string
}
```

### 枚举

```go
const (
    SideBuy  SdkSide = "BUY"
    SideSell SdkSide = "SELL"
)
const (
    OrderTypeLimit  SdkOrderType = "LIMIT"
    OrderTypeMarket SdkOrderType = "MARKET"
)
const (
    OrderStatusOpen            SdkOrderStatus = "OPEN"
    OrderStatusFilled          SdkOrderStatus = "FILLED"
    OrderStatusPartiallyFilled SdkOrderStatus = "PARTIALLY_FILLED"
    OrderStatusCancelled       SdkOrderStatus = "CANCELLED"
    OrderStatusRejected        SdkOrderStatus = "REJECTED"
)
```

### Mini example

```go
id, err := cli.Clob.PlaceOrder(ctx, clob.OrderReq{
    MarketID: "0xcondition", TokenID: "100200300",
    Side: clob.SideBuy, OrderType: clob.OrderTypeLimit,
    Price: decimal.RequireFromString("0.55"),
    Size:  decimal.RequireFromString("10"),
    ClientOrder: "demo-001",
})
```

---

## pkg/gamma

事件 / 市场 / token 元数据查询。

### Methods on `*gamma.Facade`

| 方法 | 说明 |
|------|------|
| `GetEvent(ctx, eventID string) (*Event, error)` | 单个事件 |
| `ListEvents(ctx, EventFilter) ([]Event, nextCursor string, error)` | 列出事件 |
| `GetMarket(ctx, marketID string) (*Market, error)` | 单个市场 |
| `GetToken(ctx, tokenID string) (*Token, error)` | 反查 token 所属 Market（无独立端点，走 `POST /markets/information`） |

### `type Event` / `type Market` / `type Token`

```go
type Event struct {
    ID, Slug, Title, Description, Category string
    Active, Closed, Archived               bool
    StartDate, EndDate, CreationDate       time.Time
    Markets                                []Market
}

type Market struct {
    ID, ConditionID, Question, Slug   string
    YesTokenID, NoTokenID             string  // 由 ClobTokenIDs JSON 数组 [0]/[1] 解析
    Active, Closed, AcceptingOrders   bool
    EndDate                           time.Time
}

type Token struct {
    ID           string
    MarketID     string
    OutcomeIndex int  // 0=Yes, 1=No
}
```

### `type EventFilter`

| 字段 | 类型 | 说明 |
|------|------|------|
| `Limit`, `Offset` | `int` | 分页 |
| `Order` | `string` | 排序字段 |
| `Ascending` | `bool` | |
| `Slug`, `TagID` | `string`/`int` | 过滤 |
| `Active`, `Closed`, `Archived` | `*bool` | tri-state |
| `StartDateMin`/`Max`, `EndDateMin`/`Max` | `*time.Time` | 时间范围 |

### 注意

- `GetToken` 找不到时返回 `ErrNotFound`
- ID 字段类型是 `string`（上游真实 wire 形态；DEC-038）
- `nextCursor` 编码下一页 offset；空串表示已到末页

---

## pkg/ws

WebSocket book / order channel 订阅，自动重连 + sequence guard。

### Methods on `*ws.Facade`

| 方法 | 说明 |
|------|------|
| `SubscribeBook(ctx, tokenIDs []string) (<-chan BookUpdate, error)` | 订阅市场频道 |
| `SubscribeOrders(ctx, userID string) (<-chan OrderUpdate, error)` | 订阅用户频道；`WithUserAuth` 必需 |

### Reconnect 语义

- 指数退避 1s → 30s（封顶），叠加 0–500ms jitter
- 重连成功后推 `BookUpdate{Type: UpdateReset}` 通知消费方清缓存
- Sequence 倒退或 hash 重复 → 推 `Type: UpdateReset` 后续费正常 DELTA 流

### `type BookUpdate`

| 字段 | 类型 | 说明 |
|------|------|------|
| `TokenID` | `string` | asset_id |
| `Type` | `UpdateType` | `SNAPSHOT` / `DELTA` / `RESET` |
| `Bids`, `Asks` | `[]clob.Level` | 档位数据 |
| `Sequence` | `int64` | 单调递增（用上游 timestamp ms 填充） |
| `Time` | `time.Time` | 事件时间 |

### `type OrderUpdate`

| 字段 | 类型 |
|------|------|
| `OrderID` | `clob.OrderID` |
| `Status` | `clob.SdkOrderStatus` |
| `Filled` | `decimal.Decimal` |
| `Time` | `time.Time` |

### `type UserAuth`

```go
type UserAuth struct {
    APIKey     string
    Secret     string  // 当前 WS auth 不校验，可留空
    Passphrase string
}
```

### Facade Options

| Option | 说明 |
|--------|------|
| `WithUserAuth(UserAuth)` | user channel 凭证 |
| `WithPingInterval(time.Duration)` | PING 间隔（默认 10s） |
| `WithMaxBackoff(time.Duration)` | 最大重连退避（默认 30s） |
| `WithLogger(obs.Logger)` | 日志钩子 |
| `WithMetrics(obs.Metrics)` | 指标钩子 |

---

## pkg/signer

EIP-712 / pmcup26 5-field 签名器。

### `type Signer interface`

```go
type Signer interface {
    Sign(ctx context.Context, payload []byte) ([]byte, error)  // payload = 32-byte structHash
    Address() string
    SchemaVersion() string  // "polymarket-v1" / "pmcup26-v1"
}
```

### 构造器

| 函数 | 说明 |
|------|------|
| `NewEIP712Signer(privKey, domainSeparator [32]byte) Signer` | 通用 EIP-712；caller 自带 domainSeparator |
| `NewPMCup26Signer(privKey, scopeID [32]byte, chainID int64, opts ...PMCup26Option) *PMCup26Signer` | pm-cup2026 业务签名器；内置 ClobAuth + Order 双域 |

### `*PMCup26Signer` 高级方法

| 方法 | 说明 |
|------|------|
| `SignClobAuth(ctx, timestamp string, nonce uint64) ([]byte, error)` | 5-field auth |
| `SignOrder(ctx, *OrderForSigning) ([]byte, error)` | 13-field Order；需 `WithExchangeAddress` |
| `ScopeID() [32]byte` | 内置 scope id |
| `ChainID() int64` | 内置 chain id |
| `ExchangeAddress() (common.Address, bool)` | hasExchange flag |

### Helpers

| 函数 | 说明 |
|------|------|
| `ClobAuthDomainSeparator(chainID) [32]byte` | ClobAuth 域分隔符 |
| `ClobAuthStructHash(addr, ts, nonce, scopeID) [32]byte` | 5-field structHash |
| `BuildClobAuthDigest(...)` | 一站式 digest |
| `BuildOrderDigest(*OrderForSigning, exchange, chainID)` | 13-field Order digest |
| `EIP712Digest(domainSep, structHash) []byte` | 通用 EIP-712 §4 digest |
| `ScopeIDFromHex(hex) [32]byte` / `ScopeIDToHex([32]byte) string` | scope id 编解码 |

### Mini example

```go
priv, _ := ethcrypto.GenerateKey()
signer := pmsigner.NewPMCup26Signer(priv, [32]byte{}, 137,
    pmsigner.WithExchangeAddress(common.HexToAddress("0xExchange")),
)
sig, err := signer.SignClobAuth(ctx, "2026-04-27T12:00:00Z", 0)
```

---

## pkg/obs

Observability 钩子（契约 §9）。

### `type Logger interface`

```go
type Logger interface {
    Debugw(msg string, kvs ...any)
    Infow(msg string, kvs ...any)
    Warnw(msg string, kvs ...any)
    Errorw(msg string, kvs ...any)
}
```

`obs.NopLogger` 默认实现。

### `type Metrics interface`

```go
type Metrics interface {
    IncCounter(name string, labels map[string]string)
    ObserveHistogram(name string, value float64, labels map[string]string)
}
```

`obs.NopMetrics` 默认实现。

### 标准指标名

| 常量 | 名 | labels |
|------|-----|--------|
| `MetricHTTPRequestsTotal` | `pmsdk_http_requests_total` | `path`, `method`, `status` |
| `MetricHTTPRequestDuration` | `pmsdk_http_request_duration_seconds` | `path` |
| `MetricWSReconnectsTotal` | `pmsdk_ws_reconnects_total` | `channel` |
| `MetricWSSeqJumpsTotal` | `pmsdk_ws_seq_jumps_total` | `channel` |
| `MetricSignerSignTotal` | `pmsdk_signer_sign_total` | `schema` |
| `MetricSignerSignDuration` | `pmsdk_signer_sign_duration_seconds` | `schema` |

### Adapter 子包

| 包 | 说明 |
|----|------|
| `pkg/obs/zapobs.New(*zap.Logger) Logger` | zap adapter |
| `pkg/obs/zapobs.NewSugared(*zap.SugaredLogger) Logger` | 接受已 Sugar 实例 |
| `pkg/obs/promobs.New(prometheus.Registerer) Metrics` | prometheus adapter |

不引这些子包就不会把 zap / prometheus 打进二进制。

### 标准 log 字段

挂点统一带：`op`, `method`, `path`, `status`, `duration_ms`, `request_id`（HTTP X-Request-Id 透传，若 server 设置）。

---

## 错误模型

哨兵 + 结构化 APIError 双层。

### 哨兵

```go
var (
    ErrSign         = errors.New("pmsdkgo: signer failure")        // 401/403/签名失败
    ErrRateLimit    = errors.New("pmsdkgo: upstream rate limit")   // 429
    ErrUpstream     = errors.New("pmsdkgo: upstream error")        // 5xx / 不可恢复
    ErrPrecondition = errors.New("pmsdkgo: precondition failed")   // 412/422 / 业务校验
    ErrNotFound     = errors.New("pmsdkgo: not found")             // 404
    ErrCancelled    = errors.New("pmsdkgo: cancelled by ctx")      // ctx 取消
)
```

顶层 `pmsdkgo.ErrXxx` 是 `clob.ErrXxx` 的 alias。

### `type APIError`

```go
type APIError struct {
    StatusCode int
    Code       string
    Message    string
    RequestID  string         // 上游 X-Request-Id
    Body       []byte         // 原始 body（≤8KiB）
    Detail     map[string]any // 解析出的 generic map
}
func (e *APIError) Error() string
func (e *APIError) Unwrap() error  // 返回对应哨兵，便于 errors.Is
```

### 用法

```go
id, err := cli.Clob.PlaceOrder(ctx, req)
switch {
case errors.Is(err, pmsdkgo.ErrSign):
    // 401/403 — 重新拿 API key
case errors.Is(err, pmsdkgo.ErrRateLimit):
    // 429 — 退避重试
case errors.Is(err, pmsdkgo.ErrPrecondition):
    var ae *pmsdkgo.APIError
    if errors.As(err, &ae) {
        log.Printf("422: %s detail=%+v", ae.Message, ae.Detail)
    }
case errors.Is(err, pmsdkgo.ErrUpstream):
    // 5xx — 长退避或熔断
case err != nil:
    log.Print(err)
}
```

---

## 版本与兼容

- semver `v0.x.0`；M10 release = `v0.1.0`
- 接口冻结后破坏性变更走 `v0.x → v0.(x+1)`
- 详情见 [contract §10](https://github.com/chainupcloud/pm-cup2026-liquidity/blob/main/docs/design-docs/pm-sdk-go-contract.md#10-版本策略dec-035)
