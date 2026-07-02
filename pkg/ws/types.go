package ws

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
)

// UpdateType 是 BookUpdate / OrderUpdate 的事件类型枚举（契约 §6）。
//
// 上游 asyncapi spec 直接发的 event_type 为 `book` / `price_change` /
// `last_trade_price` / `order` / `trade` 等（详见 docs/asyncapi-market.json
// 与 docs/asyncapi-user.json）。SDK 层把它们抽象成三个语义：
//
//   - SNAPSHOT：完整快照（market `book` 事件、user 首次订阅 dump）
//   - DELTA：增量更新（market `price_change`、user `order`/`trade` UPDATE）
//   - RESET：连接重连或 sequence 跳变后下发，要求消费方清缓存重建（SDK 内部生成，
//     不来自上游）
//
// 注意：契约 §6 仅列 SNAPSHOT/DELTA/RESET 三种；上游的 `last_trade_price` /
// `tick_size_change` 等扩展事件目前不通过 BookUpdate 暴露（消费方走 GetTrades
// REST 即可），后续若有需求再加 EventType。
type UpdateType string

// UpdateType 取值。
const (
	// UpdateSnapshot 完整快照：消费方应替换本地缓存。
	UpdateSnapshot UpdateType = "SNAPSHOT"
	// UpdateDelta 增量更新：消费方按 price level 应用差量（size=0 表示删档）。
	UpdateDelta UpdateType = "DELTA"
	// UpdateReset 重置信号：消费方应丢弃本地缓存并等待下一个 SNAPSHOT。
	// SDK 在以下情况推送 RESET：
	//  1. WebSocket 重连成功后（连接断开期间数据缺失，消费方必须重建）
	//  2. 检测到上游 timestamp 倒退或 nonce(hash) 异常重复
	UpdateReset UpdateType = "RESET"
)

// BookUpdate 是市场频道事件（契约 §6 BookUpdate）。
//
// 字段映射（asyncapi-market.json）：
//
//	SNAPSHOT  ← `book`         (asset_id / bids / asks / timestamp)
//	DELTA     ← `price_change` (price_changes[].asset_id / price / size / side)
//	RESET     ← SDK 自身生成（重连或 sequence 跳变）
//
// Sequence 字段说明：上游 spec 没有显式 sequence number，SDK 用单调递增
// timestamp（Unix ms）填入 Sequence，并配合 nonce guard（去重 hash）实现
// 契约 §6 要求的"跳跃检测 → RESET"语义。
type BookUpdate struct {
	// TokenID 即 asset_id（uint256 字符串）。
	TokenID string
	// Type 事件类型；RESET 时其余字段除 Time 外可能为零值。
	Type UpdateType
	// Bids 买盘档位（DELTA 时仅含本次变化档；size=0 表示删档）。
	Bids []clob.Level
	// Asks 卖盘档位（同上）。
	Asks []clob.Level
	// Sequence 单调递增序号（SDK 用上游 timestamp ms 填充，用于跳跃检测）。
	Sequence int64
	// Time 事件时间。
	Time time.Time
}

// OrderUpdate 是用户频道事件（契约 §6 OrderUpdate）。
//
// 字段映射（envelope data 内或平铺，见 wireUserEnvelope）：
//
//	OrderID ← order.id          (订单 hash)
//	Status  ← order.status      (大写 ORDER_STATUS_* 或小写 live/matched/cancelled，
//	                             统一归一为 SDK SdkOrderStatus，见 mapOrderStatus)
//	Filled  ← order.size_matched (envelope 形态无此字段，保持零值)
//	Time    ← order.timestamp    (envelope 形态无此字段，回落 SDK 收帧时间)
//
// 上游 trade 事件不通过 OrderUpdate 暴露，走 [Facade.SubscribeTrades]（TradeUpdate）；
// 逐笔成交明细也可走 GetTrades REST。
type OrderUpdate struct {
	OrderID OrderID
	Status  clob.SdkOrderStatus
	Filled  decimal.Decimal
	Time    time.Time
}

// TradeUpdate 是用户频道 trade 事件（[Facade.SubscribeTrades]）。
//
// 上游同一 event_type="trade" 有两侧 payload（clob-service dispatch）：
//   - match 侧（撮合瞬间）：Status 为短串 "MATCHED"，带 order_id；
//     price / size 为人类可读单位（如 "0.55" / "100"）
//   - settle 侧（结算终态）：Status 为长串枚举 TRADE_STATUS_CONFIRMED /
//     TRADE_STATUS_FAILED 等，无 order_id；size / fee 为最小单位（×10^6）、
//     price 为 DECIMAL 字符串（如 "0.550000"）；tx_hash 仅 CONFIRMED、error 仅 FAILED
//
// SDK 原样透传不做归一：Status 保留原始字符串（消费方按 strings.Contains 匹配
// "CONFIRMED" / "FAILED"），Size / Fee 不做单位换算（两侧单位不同，见上）。
type TradeUpdate struct {
	// TradeID 即 trade id（snowflake decimal 字符串）。
	TradeID string
	// Status 原始状态串：MATCHED（match 侧短串）或 TRADE_STATUS_*（settle 侧长串）。
	Status string
	// AssetID 即 token id（本账号视角一侧）。
	AssetID string
	// Side 本账号成交方向（BUY / SELL）。
	Side clob.SdkSide
	// Price 成交价（两侧均为人类可读概率价）。
	Price decimal.Decimal
	// Size 成交量；match 侧人类可读、settle 侧 ×10^6 最小单位（SDK 不换算）。
	Size decimal.Decimal
	// Fee 手续费（仅 settle 侧，×10^6 最小单位）。
	Fee decimal.Decimal
	// TxHash 链上交易 hash（仅 settle CONFIRMED）。
	TxHash string
	// Error 失败原因（仅 settle FAILED）。
	Error string
	// OrderID 本账号对应订单（仅 match 侧；settle 侧为空）。
	OrderID OrderID
	// Time SDK 收到事件的本地时间（上游 trade 帧无稳定 timestamp 字段）。
	Time time.Time
}

// OrderID 是订单 ID（契约 §6 与 §4 共用类型；alias 到 clob.OrderID 避免循环依赖）。
type OrderID = clob.OrderID

// ---------- wire types（内部反序列化用，不 export 给消费方）----------

// wireBookSnapshot 对应上游 `book` event。
type wireBookSnapshot struct {
	EventType string          `json:"event_type"`
	AssetID   string          `json:"asset_id"`
	Market    string          `json:"market"`
	Bids      []wireOrderLvl  `json:"bids"`
	Asks      []wireOrderLvl  `json:"asks"`
	Timestamp wireTimestampMs `json:"timestamp"`
	Hash      string          `json:"hash"`
}

// wirePriceChange 对应上游 `price_change` event。
type wirePriceChange struct {
	EventType    string             `json:"event_type"`
	Market       string             `json:"market"`
	PriceChanges []wirePriceChangeE `json:"price_changes"`
	Timestamp    wireTimestampMs    `json:"timestamp"`
}

// wirePriceChangeE 是 price_changes 数组中的单条变化。
type wirePriceChangeE struct {
	AssetID string `json:"asset_id"`
	Price   string `json:"price"`
	Size    string `json:"size"`
	Side    string `json:"side"` // BUY / SELL
	Hash    string `json:"hash"`
	BestBid string `json:"best_bid"`
	BestAsk string `json:"best_ask"`
}

// wireOrderLvl 对应 OrderLevel schema。
type wireOrderLvl struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// wireOrderEvent 对应上游用户频道 `order` 事件 payload（envelope data 内或平铺）。
// envelope 形态（clob-service 实际推送）仅带 id/status/asset_id/side/price/type 等
// 子集（撤单帧只有 id/status），其余字段按可缺省处理。
type wireOrderEvent struct {
	EventType    string          `json:"event_type"`
	Type         string          `json:"type"` // PLACEMENT / UPDATE / CANCELLATION
	ID           string          `json:"id"`
	Owner        string          `json:"owner"`
	Market       string          `json:"market"`
	AssetID      string          `json:"asset_id"`
	Side         string          `json:"side"`
	OriginalSize string          `json:"original_size"`
	SizeMatched  string          `json:"size_matched"`
	Price        string          `json:"price"`
	Status       string          `json:"status"` // ORDER_STATUS_*
	OrderType    string          `json:"order_type"`
	Timestamp    wireTimestampMs `json:"timestamp"`
}

// wireUserEnvelope 用于嗅探用户频道帧的事件类型与 data 包裹层。
//
// clob-service 实际下发 envelope 形态（wsservice.UserEvent）：
//
//	{"event_type":"order"|"trade","owner":"...","condition_id":"...","data":{...}}
//
// 而 asyncapi-user.json 文档（对齐 Polymarket）描述的是平铺字段形态；
// order / trade 解析两种都兼容（data 为 JSON object 时取 data，否则整帧平铺解析）。
type wireUserEnvelope struct {
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
}

// wireTradeEvent 对应用户频道 `trade` 事件 payload（envelope data 内或平铺）。
// match 侧与 settle 侧字段集不同（见 TradeUpdate 注释），全部按可缺省处理。
type wireTradeEvent struct {
	ID      string      `json:"id"`
	Status  string      `json:"status"`
	AssetID string      `json:"asset_id"`
	Side    string      `json:"side"`
	Price   wireDecimal `json:"price"`
	Size    wireDecimal `json:"size"`
	Fee     wireDecimal `json:"fee"`
	TxHash  string      `json:"tx_hash"`
	Error   string      `json:"error"`
	OrderID string      `json:"order_id"`
}

// wireDecimal 容忍 number 与 quoted string 两种 wire 形态，保存原始字面量
// （envelope 形态数值全是 string，平铺文档形态可能是 number）。
type wireDecimal string

// UnmarshalJSON 容忍 number 或 quoted string 两种 wire 形态。
func (d *wireDecimal) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		*d = wireDecimal(b[1 : len(b)-1])
		return nil
	}
	*d = wireDecimal(b)
	return nil
}

// wireTimestampMs 容忍上游 int 与 string 两种 timestamp 形态（asyncapi 标 integer，
// 实际后端可能 marshal 成 string）。
type wireTimestampMs int64

// UnmarshalJSON 容忍 number 或 quoted string 两种 wire 形态。
func (t *wireTimestampMs) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		// quoted string
		s := string(b[1 : len(b)-1])
		if s == "" {
			return nil
		}
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		*t = wireTimestampMs(v)
		return nil
	}
	v, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return err
	}
	*t = wireTimestampMs(v)
	return nil
}

// 订阅请求 envelope。
type wireMarketSubscribe struct {
	AssetsIDs    []string `json:"assets_ids"`
	Type         string   `json:"type"` // "market"
	InitialDump  bool     `json:"initial_dump"`
	Level        int      `json:"level,omitempty"`
	CustomEnable bool     `json:"custom_feature_enabled,omitempty"`
}

type wireUserAuth struct {
	APIKey     string `json:"apiKey"`
	Secret     string `json:"secret,omitempty"`
	Passphrase string `json:"passphrase"`
}

type wireUserSubscribe struct {
	Auth    wireUserAuth `json:"auth"`
	Type    string       `json:"type"` // "user"
	Markets []string     `json:"markets"`
}
