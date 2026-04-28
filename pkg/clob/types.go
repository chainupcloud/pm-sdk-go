package clob

import (
	"time"

	"github.com/shopspring/decimal"
)

// SDK 层手写类型（契约 §4）。
//
// 命名约定：与 generated.go 不冲突的类型沿用契约名（OrderReq / Book / Level 等）；
// 与 oapi-codegen 输出冲突的类型用 Sdk 前缀（SdkOrder / SdkTrade / SdkSide /
// SdkOrderType / SdkOrderStatus），语义为面向 SDK 调用方的稳定抽象，由 Facade
// 在内部 map 到 generated 的 wire 类型。

// OrderID 是订单全局唯一 ID（来自 clob-service 的 snowflake string）。
type OrderID string

// SdkSide 是订单方向（契约 §4 Side）。
type SdkSide string

// SdkSide 取值。
const (
	SideBuy  SdkSide = "BUY"
	SideSell SdkSide = "SELL"
)

// SdkOrderType 是订单类型（契约 §4 OrderType）。
//
// 注意：上游 clob-service 使用 GTC/GTD/FAK/FOK 表示挂单时效；契约层抽象成
// LIMIT/MARKET 二选一，Facade 层做 map（LIMIT→GTC、MARKET→FAK）。
type SdkOrderType string

// SdkOrderType 取值。
const (
	OrderTypeLimit  SdkOrderType = "LIMIT"
	OrderTypeMarket SdkOrderType = "MARKET"
)

// SdkOrderStatus 是订单生命周期状态（契约 §4 OrderStatus）。
//
// 上游 clob-service 用 ORDER_STATUS_LIVE / ORDER_STATUS_MATCHED / ... 等枚举；
// SDK 层抽象成 OPEN / FILLED / PARTIALLY_FILLED / CANCELLED / REJECTED。
type SdkOrderStatus string

// SdkOrderStatus 取值。
const (
	OrderStatusOpen            SdkOrderStatus = "OPEN"
	OrderStatusFilled          SdkOrderStatus = "FILLED"
	OrderStatusPartiallyFilled SdkOrderStatus = "PARTIALLY_FILLED"
	OrderStatusCancelled       SdkOrderStatus = "CANCELLED"
	OrderStatusRejected        SdkOrderStatus = "REJECTED"
)

// OrderReq 是 PlaceOrder 入参（契约 §4）。
type OrderReq struct {
	// MarketID 下游 market_id（condition_id）。
	MarketID string
	// TokenID 下游 token_id（uint256 字符串）。
	TokenID string
	// Side 买卖方向。
	Side SdkSide
	// OrderType 订单类型。
	OrderType SdkOrderType
	// Price 限价（市价单填上限价；以 USDC 计价）。
	Price decimal.Decimal
	// Size 数量（以 token 计）。
	Size decimal.Decimal
	// ClientOrder 调用方幂等 ID（透传）。
	ClientOrder string
	// Expiration GTD 单的过期时间（nil = 不过期）。
	Expiration *time.Time
}

// SdkOrder 是订单详情响应（契约 §4 Order）。
type SdkOrder struct {
	ID          OrderID         `json:"id"`
	MarketID    string          `json:"market_id"`
	TokenID     string          `json:"token_id"`
	Side        SdkSide         `json:"side"`
	OrderType   SdkOrderType    `json:"order_type"`
	Price       decimal.Decimal `json:"price"`
	Size        decimal.Decimal `json:"size"`
	Filled      decimal.Decimal `json:"filled"`
	Status      SdkOrderStatus  `json:"status"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ClientOrder string          `json:"client_order"`
}

// Book 是订单簿快照（契约 §4 Book）。
type Book struct {
	TokenID  string    `json:"token_id"`
	Bids     []Level   `json:"bids"`     // 价从高到低
	Asks     []Level   `json:"asks"`     // 价从低到高
	UpdateAt time.Time `json:"update_at"`
}

// Level 是订单簿单档（契约 §4 Level）。
type Level struct {
	Price decimal.Decimal `json:"price"`
	Size  decimal.Decimal `json:"size"`
}

// SdkTrade 是成交记录（契约 §4 Trade，由 GetTrades 返回）。
type SdkTrade struct {
	ID         string          `json:"id"`
	MarketID   string          `json:"market_id"`
	TokenID    string          `json:"token_id"`
	Side       SdkSide         `json:"side"`
	Price      decimal.Decimal `json:"price"`
	Size       decimal.Decimal `json:"size"`
	MatchTime  time.Time       `json:"match_time"`
	OrderID    OrderID         `json:"order_id"`
	TraderSide string          `json:"trader_side"` // MAKER / TAKER（透传）
	Status     string          `json:"status"`      // 上游 trade status 透传
}

// OrderFilter 是 ListOrders 查询条件（契约 §4）。
type OrderFilter struct {
	OrderID    string // 按 order_hash / id 过滤
	MarketID   string // 按 condition_id 过滤
	TokenID    string // 按 token_id 过滤
	NextCursor string // 分页游标（Base64，"LTE=" = last page）
}

// TradeFilter 是 GetTrades 查询条件（契约 §4）。
type TradeFilter struct {
	MakerAddress string     // 必填：trader 地址（0x + 40 hex）
	TradeID      string     // 可选：按 trade id 过滤
	MarketID     string     // 可选：condition_id
	TokenID      string     // 可选：token_id
	Before       *time.Time // 可选：上界（trades 在此之前）
	After        *time.Time // 可选：下界
	NextCursor   string     // 分页游标
}
