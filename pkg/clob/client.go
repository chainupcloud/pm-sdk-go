package clob

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/chainupcloud/pm-sdk-go/pkg/signer"
)

// Facade 是 clob 业务门面（契约 §4）。
//
// 内部组合 oapi-codegen 生成的低层 *Client（同 package 名 `clob`）+ signer，
// 对外暴露 PlaceOrder / CancelOrder / GetOrder / ListOrders / GetBook / GetTrades。
//
// 注意类型命名：generated.go 已 export `Client` / `Order` / `Trade` / `Side` /
// `OrderType` / `OrderStatus`，本门面命名为 Facade，其余 SDK 类型用 Sdk 前缀
// 避免冲突；顶层 pmsdkgo 包 `Client.Clob *clob.Facade`。
type Facade struct {
	low    *Client
	signer signer.Signer
}

// FacadeOption 是 Facade 构造选项。
type FacadeOption func(*Facade)

// WithSigner 注入签名器。无 signer 时 PlaceOrder 返回 ErrSign。
func WithSigner(s signer.Signer) FacadeOption {
	return func(f *Facade) {
		f.signer = s
	}
}

// NewFacade 构造一个 Facade。server 为 clob-service base URL（例如
// https://clob.polymarket.com）；httpDoer 为 *http.Client 兼容实例（nil = http.DefaultClient）。
func NewFacade(server string, httpDoer HttpRequestDoer, opts ...FacadeOption) (*Facade, error) {
	clientOpts := []ClientOption{}
	if httpDoer != nil {
		clientOpts = append(clientOpts, WithHTTPClient(httpDoer))
	}
	low, err := NewClient(server, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("clob: new client: %w", err)
	}
	f := &Facade{low: low}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	return f, nil
}

// PlaceOrder 下单（契约 §4）。
//
// 流程：
//  1. signer 必填；缺失返回 %w ErrSign
//  2. 构造 generated SendOrder（EIP-712 字段尚由 Phase 6 完整签名；Phase 3
//     仅 stub 签名/字段，确保 wire 形态对齐）
//  3. 调用 generated PostOrder
//  4. 非 2xx → wrapHTTPError；2xx → 解析 SendOrderResponse.OrderID
func (f *Facade) PlaceOrder(ctx context.Context, req OrderReq) (OrderID, error) {
	if f.signer == nil {
		return "", fmt.Errorf("%w: PlaceOrder requires signer (use clob.WithSigner)", ErrSign)
	}

	makerAmount, takerAmount, err := computeAmounts(req)
	if err != nil {
		return "", err
	}

	// Phase 3 stub：实际 EIP-712 hash 由 Phase 6 在 signer 内部完成；
	// 这里直接把 ClientOrder + payload 简单串作为 sig 输入，签名结果填入 Order.Signature。
	payload := []byte(req.MarketID + "|" + req.TokenID + "|" + string(req.Side) + "|" +
		req.Price.String() + "|" + req.Size.String() + "|" + req.ClientOrder)
	sig, err := f.signer.Sign(ctx, payload)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSign, err)
	}

	side := mapSide(req.Side)
	otype := mapOrderType(req.OrderType)

	expiration := "0"
	if req.Expiration != nil {
		expiration = strconv.FormatInt(req.Expiration.Unix(), 10)
	}

	order := Order{
		Maker:       f.signer.Address(),
		MakerAmount: makerAmount,
		TakerAmount: takerAmount,
		Side:        side,
		Signature:   "0x" + bytesToHex(sig),
		Signer:      f.signer.Address(),
		Taker:       "0x0000000000000000000000000000000000000000",
		TokenID:     req.TokenID,
		Expiration:  &expiration,
	}
	body := SendOrder{
		Order:     order,
		OrderType: &otype,
	}

	resp, err := f.low.PostOrder(ctx, body, withClientOrderHeader(req.ClientOrder))
	if err != nil {
		return "", wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", wrapHTTPError(resp, respBody)
	}

	parsed, err := unmarshalSendOrderResponse(respBody)
	if err != nil {
		return "", fmt.Errorf("%w: decode SendOrderResponse: %v", ErrUpstream, err)
	}
	if parsed.OrderID == nil || *parsed.OrderID == "" {
		return "", fmt.Errorf("%w: empty order id in response", ErrUpstream)
	}
	return OrderID(*parsed.OrderID), nil
}

// CancelOrder 撤单（契约 §4）。
func (f *Facade) CancelOrder(ctx context.Context, id OrderID) error {
	if id == "" {
		return fmt.Errorf("%w: empty order id", ErrPrecondition)
	}
	body := CancelOrderJSONRequestBody{OrderID: string(id)}
	resp, err := f.low.CancelOrder(ctx, body)
	if err != nil {
		return wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return wrapHTTPError(resp, respBody)
	}
	return nil
}

// GetOrder 查询单个订单（契约 §4）。
func (f *Facade) GetOrder(ctx context.Context, id OrderID) (*SdkOrder, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty order id", ErrPrecondition)
	}
	resp, err := f.low.GetOrder(ctx, string(id))
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, respBody)
	}

	var oo OpenOrder
	if err := jsonUnmarshal(respBody, &oo); err != nil {
		return nil, fmt.Errorf("%w: decode OpenOrder: %v", ErrUpstream, err)
	}
	return openOrderToSDK(&oo), nil
}

// ListOrders 列出订单（契约 §4）。
//
// 返回 (orders, nextCursor, error)；nextCursor == "LTE=" 表示已到末页。
func (f *Facade) ListOrders(ctx context.Context, filter OrderFilter) ([]SdkOrder, string, error) {
	params := &GetOrdersParams{}
	if filter.OrderID != "" {
		params.Id = strPtr(filter.OrderID)
	}
	if filter.MarketID != "" {
		params.Market = strPtr(filter.MarketID)
	}
	if filter.TokenID != "" {
		params.AssetId = strPtr(filter.TokenID)
	}
	if filter.NextCursor != "" {
		params.NextCursor = strPtr(filter.NextCursor)
	}

	resp, err := f.low.GetOrders(ctx, params)
	if err != nil {
		return nil, "", wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, "", wrapHTTPError(resp, respBody)
	}

	var or OrdersResponse
	if err := jsonUnmarshal(respBody, &or); err != nil {
		return nil, "", fmt.Errorf("%w: decode OrdersResponse: %v", ErrUpstream, err)
	}
	out := make([]SdkOrder, 0)
	if or.Data != nil {
		for i := range *or.Data {
			oo := (*or.Data)[i]
			out = append(out, *openOrderToSDK(&oo))
		}
	}
	cursor := ""
	if or.NextCursor != nil {
		cursor = *or.NextCursor
	}
	return out, cursor, nil
}

// GetBook 取订单簿快照（契约 §4）。tokenID 即 uint256 decimal 字符串。
func (f *Facade) GetBook(ctx context.Context, tokenID string) (*Book, error) {
	if tokenID == "" {
		return nil, fmt.Errorf("%w: empty token id", ErrPrecondition)
	}
	params := &GetBookParams{TokenId: tokenID}
	resp, err := f.low.GetBook(ctx, params)
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, respBody)
	}

	var summary OrderBookSummary
	if err := jsonUnmarshal(respBody, &summary); err != nil {
		return nil, fmt.Errorf("%w: decode OrderBookSummary: %v", ErrUpstream, err)
	}
	return bookSummaryToSDK(&summary, tokenID), nil
}

// GetTrades 查询成交（契约 §4）。filter.MakerAddress 必填。
//
// 返回 (trades, nextCursor, error)。
func (f *Facade) GetTrades(ctx context.Context, filter TradeFilter) ([]SdkTrade, string, error) {
	if filter.MakerAddress == "" {
		return nil, "", fmt.Errorf("%w: TradeFilter.MakerAddress required", ErrPrecondition)
	}
	params := &GetTradesParams{MakerAddress: filter.MakerAddress}
	if filter.TradeID != "" {
		params.Id = strPtr(filter.TradeID)
	}
	if filter.MarketID != "" {
		params.Market = strPtr(filter.MarketID)
	}
	if filter.TokenID != "" {
		params.AssetId = strPtr(filter.TokenID)
	}
	if filter.Before != nil {
		ts := float32(filter.Before.Unix())
		params.Before = &ts
	}
	if filter.After != nil {
		ts := float32(filter.After.Unix())
		params.After = &ts
	}
	if filter.NextCursor != "" {
		params.NextCursor = strPtr(filter.NextCursor)
	}

	resp, err := f.low.GetTrades(ctx, params)
	if err != nil {
		return nil, "", wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, "", wrapHTTPError(resp, respBody)
	}

	var tr TradesResponse
	if err := jsonUnmarshal(respBody, &tr); err != nil {
		return nil, "", fmt.Errorf("%w: decode TradesResponse: %v", ErrUpstream, err)
	}
	out := make([]SdkTrade, 0)
	if tr.Data != nil {
		for i := range *tr.Data {
			t := (*tr.Data)[i]
			out = append(out, *tradeToSDK(&t))
		}
	}
	cursor := ""
	if tr.NextCursor != nil {
		cursor = *tr.NextCursor
	}
	return out, cursor, nil
}

// ---------- 内部辅助 ----------

// computeAmounts 把 (price, size, side) 映射成 makerAmount / takerAmount（wire 字符串）。
//
// 简化：以 token 6 位精度（Polymarket USDC）为单位；BUY 时 maker 出 USDC（price*size）、
// 收 token；SELL 反之。Phase 6 的 signer 会按此规则重新校验。
func computeAmounts(req OrderReq) (string, string, error) {
	if req.Price.LessThanOrEqual(decimal.Zero) {
		return "", "", fmt.Errorf("%w: price must be > 0", ErrPrecondition)
	}
	if req.Size.LessThanOrEqual(decimal.Zero) {
		return "", "", fmt.Errorf("%w: size must be > 0", ErrPrecondition)
	}
	notional := req.Price.Mul(req.Size)
	switch req.Side {
	case SideBuy:
		return notional.String(), req.Size.String(), nil
	case SideSell:
		return req.Size.String(), notional.String(), nil
	default:
		return "", "", fmt.Errorf("%w: invalid side %q", ErrPrecondition, req.Side)
	}
}

func mapSide(s SdkSide) Side {
	switch s {
	case SideBuy:
		return BUY
	case SideSell:
		return SELL
	default:
		return Side(s)
	}
}

func mapOrderType(t SdkOrderType) OrderType {
	switch t {
	case OrderTypeLimit:
		return GTC
	case OrderTypeMarket:
		return FAK
	default:
		return GTC
	}
}

func mapOrderTypeReverse(t *OrderType) SdkOrderType {
	if t == nil {
		return ""
	}
	switch *t {
	case GTC, GTD:
		return OrderTypeLimit
	case FAK, FOK:
		return OrderTypeMarket
	default:
		return SdkOrderType(*t)
	}
}

func mapSideReverse(s *Side) SdkSide {
	if s == nil {
		return ""
	}
	switch *s {
	case BUY:
		return SideBuy
	case SELL:
		return SideSell
	default:
		return SdkSide(*s)
	}
}

func mapOrderStatusReverse(s *OrderStatus) SdkOrderStatus {
	if s == nil {
		return ""
	}
	switch *s {
	case ORDERSTATUSLIVE:
		return OrderStatusOpen
	case ORDERSTATUSMATCHED:
		return OrderStatusFilled
	case ORDERSTATUSCANCELED, ORDERSTATUSCANCELEDMARKETRESOLVED:
		return OrderStatusCancelled
	case ORDERSTATUSINVALID:
		return OrderStatusRejected
	default:
		return SdkOrderStatus(*s)
	}
}

// openOrderToSDK 把 generated OpenOrder 映射成 SDK SdkOrder。
func openOrderToSDK(oo *OpenOrder) *SdkOrder {
	if oo == nil {
		return nil
	}
	out := &SdkOrder{
		Side:      mapSideReverse(oo.Side),
		OrderType: mapOrderTypeReverse(oo.OrderType),
		Status:    mapOrderStatusReverse(oo.Status),
	}
	if oo.Id != nil {
		out.ID = OrderID(*oo.Id)
	}
	if oo.Market != nil {
		out.MarketID = *oo.Market
	}
	if oo.AssetId != nil {
		out.TokenID = *oo.AssetId
	}
	if oo.Price != nil {
		if d, err := decimal.NewFromString(*oo.Price); err == nil {
			out.Price = d
		}
	}
	if oo.OriginalSize != nil {
		if d, err := decimal.NewFromString(*oo.OriginalSize); err == nil {
			out.Size = d
		}
	}
	if oo.SizeMatched != nil {
		if d, err := decimal.NewFromString(*oo.SizeMatched); err == nil {
			out.Filled = d
		}
	}
	// Filled 计算（兼容 size matched 缺失时按 zero 处理；状态判定见 mapOrderStatusReverse）
	if out.Status == OrderStatusOpen && out.Filled.GreaterThan(decimal.Zero) {
		out.Status = OrderStatusPartiallyFilled
	}
	if oo.CreatedAt != nil {
		out.CreatedAt = time.Unix(int64(*oo.CreatedAt), 0).UTC()
	}
	out.UpdatedAt = out.CreatedAt
	return out
}

// bookSummaryToSDK 把 generated OrderBookSummary 映射成 SDK Book。
func bookSummaryToSDK(s *OrderBookSummary, tokenID string) *Book {
	if s == nil {
		return nil
	}
	out := &Book{TokenID: tokenID}
	if s.AssetId != nil && *s.AssetId != "" {
		out.TokenID = *s.AssetId
	}
	if s.Bids != nil {
		for _, lv := range *s.Bids {
			out.Bids = append(out.Bids, levelFromSummary(lv))
		}
	}
	if s.Asks != nil {
		for _, lv := range *s.Asks {
			out.Asks = append(out.Asks, levelFromSummary(lv))
		}
	}
	if s.Timestamp != nil {
		out.UpdateAt = time.Unix(int64(*s.Timestamp), 0).UTC()
	}
	return out
}

func levelFromSummary(s OrderSummary) Level {
	var price, size decimal.Decimal
	if s.Price != nil {
		if d, err := decimal.NewFromString(*s.Price); err == nil {
			price = d
		}
	}
	if s.Size != nil {
		if d, err := decimal.NewFromString(*s.Size); err == nil {
			size = d
		}
	}
	return Level{Price: price, Size: size}
}

// tradeToSDK 把 generated Trade 映射成 SDK SdkTrade。
func tradeToSDK(t *Trade) *SdkTrade {
	if t == nil {
		return nil
	}
	out := &SdkTrade{Side: mapSideReverse(t.Side)}
	if t.Id != nil {
		out.ID = *t.Id
	}
	if t.Market != nil {
		out.MarketID = *t.Market
	}
	if t.AssetId != nil {
		out.TokenID = *t.AssetId
	}
	if t.Price != nil {
		if d, err := decimal.NewFromString(*t.Price); err == nil {
			out.Price = d
		}
	}
	if t.Size != nil {
		if d, err := decimal.NewFromString(*t.Size); err == nil {
			out.Size = d
		}
	}
	if t.MatchTime != nil {
		// 上游发的是 ISO 8601 string；解析失败则置零
		if ts, err := time.Parse(time.RFC3339, *t.MatchTime); err == nil {
			out.MatchTime = ts
		}
	}
	if t.TakerOrderId != nil {
		out.OrderID = OrderID(*t.TakerOrderId)
	}
	if t.TraderSide != nil {
		out.TraderSide = string(*t.TraderSide)
	}
	if t.Status != nil {
		out.Status = string(*t.Status)
	}
	return out
}

// withClientOrderHeader 把 ClientOrder 透传到 X-Client-Order-Id（如非空）。
func withClientOrderHeader(clientOrder string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		if clientOrder != "" {
			req.Header.Set("X-Client-Order-Id", clientOrder)
		}
		return nil
	}
}

// drainBody 在 defer 中确保 body 被读完并关闭，避免 connection pool 泄漏。
func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// strPtr 返回 string 指针辅助。
func strPtr(s string) *string { return &s }
