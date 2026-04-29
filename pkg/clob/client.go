package clob

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"

	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
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
	low     *Client
	signer  signer.Signer
	logger  obs.Logger
	metrics obs.Metrics
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
	f := &Facade{low: low, logger: obs.NopLogger{}, metrics: obs.NopMetrics{}}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	return f, nil
}

// orderSigner 是 Facade 内部检测的可选高级接口。当 signer 同时实现 SignOrder
// （即注入了 *signer.PMCup26Signer）时，PlaceOrder 走完整 EIP-712 13-field
// Order 域签名路径；否则降级走 Phase 3 stub（payload = "marketID|tokenID|side|price|size|clientOrder"）
// 以保持 mock signer 单测向后兼容。
type orderSigner interface {
	SignOrder(ctx context.Context, order *signer.OrderForSigning) ([]byte, error)
	ScopeID() [32]byte
}

// PlaceOrder 下单（契约 §4）。
//
// 流程：
//  1. signer 必填；缺失返回 %w ErrSign
//  2. 构造 OrderForSigning（含 maker/signer/taker/scopeId/...）；
//     若 signer 实现 orderSigner（PMCup26Signer），调 SignOrder 拿 EIP-712
//     13-field 签名；否则降级 stub 签名（Phase 3 行为，保 mock signer 兼容）
//  3. 把签名 + 字段填入 generated.Order；调用 PostOrder
//  4. 非 2xx → wrapHTTPError；2xx → 解析 SendOrderResponse.OrderID
//
// 注意：Phase 6 仅替换签名 wiring，不改 OpenAPI 上 Order.Salt / Nonce / FeeRateBps
// 等字段缺省值；后端 verify 端在 OrderForVerification 里对应的零值由 SDK 显式填入
// 确保 hash 一致。后续 Phase 7 接入 contract test 时会按真实 staging 校验。
func (f *Facade) PlaceOrder(ctx context.Context, req OrderReq) (OrderID, error) {
	if f.signer == nil {
		return "", fmt.Errorf("%w: PlaceOrder requires signer (use clob.WithSigner)", ErrSign)
	}

	// SignatureType=POLY_GNOSIS_SAFE 必须显式 Maker（Safe 地址）；零值会让 wallet-subgraph
	// 把 Maker 当成未索引的 EOA → INSUFFICIENT_BALANCE。提前返 ErrPrecondition 避免坏单。
	if req.SignatureType == signer.SignatureTypePolyGnosisSafe && req.Maker == (common.Address{}) {
		return "", fmt.Errorf("%w: SignatureType=POLY_GNOSIS_SAFE requires non-zero Maker", ErrPrecondition)
	}

	makerAmount, takerAmount, err := computeAmounts(req)
	if err != nil {
		return "", err
	}

	side := mapSide(req.Side)
	otype := mapOrderType(req.OrderType)

	expiration := "0"
	expirationUnix := uint64(0)
	if req.Expiration != nil {
		expirationUnix = uint64(req.Expiration.Unix())
		expiration = strconv.FormatUint(expirationUnix, 10)
	}

	signerAddr := f.signer.Address()
	// makerAddr 是链上资产持有方：默认退化到 Signer EOA（v0.1.x 行为）；
	// POLY_GNOSIS_SAFE 模式下由调用方传入 Safe 地址。
	makerAddr := req.Maker
	if makerAddr == (common.Address{}) {
		makerAddr = common.HexToAddress(signerAddr)
	}
	makerAmountBig, _ := new(big.Int).SetString(makerAmount, 10)
	takerAmountBig, _ := new(big.Int).SetString(takerAmount, 10)
	tokenIDBig, _ := new(big.Int).SetString(req.TokenID, 10)
	if tokenIDBig == nil {
		tokenIDBig = new(big.Int)
	}
	if makerAmountBig == nil {
		makerAmountBig = new(big.Int)
	}
	if takerAmountBig == nil {
		takerAmountBig = new(big.Int)
	}

	var sig []byte
	scopeIDHex := ""
	// signatureType wire enum 与 req.SignatureType 同值：0=EOA / 1=POLY_PROXY / 2=POLY_GNOSIS_SAFE
	signatureType := mapSignatureTypeWire(req.SignatureType)
	saltStr := "0"
	nonceStr := "0"
	feeRateBpsStr := strconv.FormatInt(req.FeeRateBps, 10)

	if os, ok := f.signer.(orderSigner); ok {
		// 完整 EIP-712 路径
		scope := os.ScopeID()
		scopeIDHex = signer.ScopeIDToHex(scope)

		order := &signer.OrderForSigning{
			Salt:          big.NewInt(0),
			Maker:         makerAddr,
			Signer:        common.HexToAddress(signerAddr),
			Taker:         common.Address{},
			TokenID:       tokenIDBig,
			MakerAmount:   makerAmountBig,
			TakerAmount:   takerAmountBig,
			Expiration:    expirationUnix,
			Nonce:         0,
			FeeRateBps:    uint64(req.FeeRateBps),
			Side:          mapSdkSideToOrderSide(req.Side),
			SignatureType: req.SignatureType,
			ScopeID:       scope,
		}
		sig, err = os.SignOrder(ctx, order)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrSign, err)
		}
	} else {
		// 兼容 stub 路径（Phase 3 / mock signer 单测）
		payload := []byte(req.MarketID + "|" + req.TokenID + "|" + string(req.Side) + "|" +
			req.Price.String() + "|" + req.Size.String() + "|" + req.ClientOrder)
		sig, err = f.signer.Sign(ctx, payload)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrSign, err)
		}
	}

	order := Order{
		Maker:         makerAddr.Hex(),
		MakerAmount:   makerAmount,
		TakerAmount:   takerAmount,
		Side:          side,
		Signature:     "0x" + bytesToHex(sig),
		Signer:        signerAddr,
		Taker:         "0x0000000000000000000000000000000000000000",
		TokenID:       req.TokenID,
		Expiration:    &expiration,
		Salt:          &saltStr,
		Nonce:         &nonceStr,
		FeeRateBps:    &feeRateBpsStr,
		SignatureType: &signatureType,
	}
	if scopeIDHex != "" {
		order.ScopeId = &scopeIDHex
	}
	body := SendOrder{
		Order:     order,
		OrderType: &otype,
	}

	op := f.observe("PlaceOrder", "POST", "/order")
	resp, err := f.low.PostOrder(ctx, body, withClientOrderHeader(req.ClientOrder))
	op.done(resp, err)
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
	op := f.observe("CancelOrder", "DELETE", "/order")
	resp, err := f.low.CancelOrder(ctx, body)
	op.done(resp, err)
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
	op := f.observe("GetOrder", "GET", "/order/{id}")
	resp, err := f.low.GetOrder(ctx, string(id))
	op.done(resp, err)
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

	op := f.observe("ListOrders", "GET", "/orders")
	resp, err := f.low.GetOrders(ctx, params)
	op.done(resp, err)
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
	op := f.observe("GetBook", "GET", "/book")
	resp, err := f.low.GetBook(ctx, params)
	op.done(resp, err)
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

	op := f.observe("GetTrades", "GET", "/trades")
	resp, err := f.low.GetTrades(ctx, params)
	op.done(resp, err)
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

// usdcDecimals 是 Polymarket / pm-cup26 staging USDC + CTF 条件 token 的链上精度。
// CTFExchange 校验 makerAmount/takerAmount 时按此精度的 raw integer 解释，
// 与 GoPolymarket/polymarket-go-sdk 的 toFixedDecimal 对齐（issue #9）。
const usdcDecimals = int32(6)

// toBaseUnits 把 decimal 数量转为 6-decimal raw integer 字符串（floor 截断）。
// 流程：Truncate(6) 截到 6 位精度 → Shift(6) 左移 6 位 → Truncate(0) 取整 → String。
func toBaseUnits(d decimal.Decimal) string {
	return d.Truncate(usdcDecimals).Shift(usdcDecimals).Truncate(0).String()
}

// computeAmounts 把 (price, size, side) 映射成 makerAmount / takerAmount（wire 字符串）。
//
// 单位约定（issue #9）：以 token 6 位精度（Polymarket USDC + CTF token）为基准单位。
// BUY 时 maker 出 USDC（price*size），收 token；SELL 反之。两侧均按 6-decimal scaled
// raw integer 输出，CTFExchange 与 EIP-712 signer 按 base-unit big.Int 解释。
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
		return toBaseUnits(notional), toBaseUnits(req.Size), nil
	case SideSell:
		return toBaseUnits(req.Size), toBaseUnits(notional), nil
	default:
		return "", "", fmt.Errorf("%w: invalid side %q", ErrPrecondition, req.Side)
	}
}

// mapSignatureTypeWire 把 SDK 侧 signer.SignatureType 映射到 generated.OrderSignatureType
// 字符串枚举（"0"/"1"/"2"）。未知值降级到 EOA(0) 保持向后兼容。
func mapSignatureTypeWire(t signer.SignatureType) OrderSignatureType {
	switch t {
	case signer.SignatureTypePolyProxy:
		return N1
	case signer.SignatureTypePolyGnosisSafe:
		return N2
	default:
		return N0
	}
}

// mapSdkSideToOrderSide 把 SDK 侧 BUY/SELL 字符串映射到 signer.OrderSide 枚举（0/1）。
func mapSdkSideToOrderSide(s SdkSide) signer.OrderSide {
	if s == SideSell {
		return signer.OrderSideSell
	}
	return signer.OrderSideBuy
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
