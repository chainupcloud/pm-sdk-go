package gamma

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/chainupcloud/pm-sdk-go/pkg/clob"
	"github.com/chainupcloud/pm-sdk-go/pkg/obs"
)

// Facade 是 gamma 业务门面（契约 §5）。
//
// 内部组合 oapi-codegen 生成的低层 *Client（同 package 名 `gamma`）。
// 对外暴露 GetEvent / ListEvents / GetMarket / GetToken。
//
// 注意：generated.go 已 export `Client`，本门面命名为 `Facade` 避让；顶层
// pmsdkgo 包将持有 `Client.Gamma *gamma.Facade`。
//
// 上游 gamma-service 的 200 响应在 OpenAPI 中无 schema（裸 *http.Response），
// 所以解析逻辑全部由 facade 手写 json.Unmarshal 完成。
type Facade struct {
	low     *Client
	logger  obs.Logger
	metrics obs.Metrics
}

// FacadeOption 是 Facade 构造选项（预留；当前无 signer / no-auth 等可调）。
type FacadeOption func(*Facade)

// NewFacade 构造一个 Facade。server 为 gamma-service base URL（例如
// https://gamma-api.polymarket.com）；httpDoer 为 *http.Client 兼容实例
// （nil = http.DefaultClient）。
func NewFacade(server string, httpDoer HttpRequestDoer, opts ...FacadeOption) (*Facade, error) {
	clientOpts := []ClientOption{}
	if httpDoer != nil {
		clientOpts = append(clientOpts, WithHTTPClient(httpDoer))
	}
	low, err := NewClient(server, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("gamma: new client: %w", err)
	}
	f := &Facade{low: low, logger: obs.NopLogger{}, metrics: obs.NopMetrics{}}
	for _, opt := range opts {
		if opt != nil {
			opt(f)
		}
	}
	return f, nil
}

// GetEvent 查询单个事件（契约 §5）。
//
// 上游 GET /events/{id} 返回 wire-level Event JSON（见 pm-cup2026
// gamma-service models.Event）。
func (f *Facade) GetEvent(ctx context.Context, eventID string) (*Event, error) {
	if eventID == "" {
		return nil, fmt.Errorf("%w: empty event id", errPrecondition)
	}
	op := f.observe("GetEvent", "GET", "/events/{id}")
	resp, err := f.low.GetEventsId(ctx, eventID)
	op.done(resp, err)
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, body)
	}

	var wire wireEvent
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("%w: decode Event: %v", errUpstream, err)
	}
	return wireEventToSDK(&wire), nil
}

// ListEvents 列出事件（契约 §5）。
//
// 返回 (events, nextCursor, error)；nextCursor 编码下一页 offset，空字符串
// 表示没有更多页。上游 /events 是 offset/limit 分页（无 cursor），SDK 层把
// "下一页 offset" 编进 cursor 字段保持契约形态。
func (f *Facade) ListEvents(ctx context.Context, filter EventFilter) ([]Event, string, error) {
	params := &GetEventsParams{}
	if filter.Limit > 0 {
		params.Limit = intPtr(filter.Limit)
	}
	if filter.Offset > 0 {
		params.Offset = intPtr(filter.Offset)
	}
	if filter.Order != "" {
		params.Order = strPtr(filter.Order)
	}
	if filter.Ascending {
		b := true
		params.Ascending = &b
	}
	if filter.Slug != "" {
		params.Slug = strPtr(filter.Slug)
	}
	if filter.TagID > 0 {
		params.TagId = intPtr(filter.TagID)
	}
	if filter.Active != nil {
		params.Active = filter.Active
	}
	if filter.Closed != nil {
		params.Closed = filter.Closed
	}
	if filter.Archived != nil {
		params.Archived = filter.Archived
	}
	if filter.StartDateMin != nil {
		params.StartDateMin = filter.StartDateMin
	}
	if filter.StartDateMax != nil {
		params.StartDateMax = filter.StartDateMax
	}
	if filter.EndDateMin != nil {
		params.EndDateMin = filter.EndDateMin
	}
	if filter.EndDateMax != nil {
		params.EndDateMax = filter.EndDateMax
	}

	op := f.observe("ListEvents", "GET", "/events")
	resp, err := f.low.GetEvents(ctx, params)
	op.done(resp, err)
	if err != nil {
		return nil, "", wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, "", wrapHTTPError(resp, body)
	}

	var wires []wireEvent
	if err := json.Unmarshal(body, &wires); err != nil {
		return nil, "", fmt.Errorf("%w: decode []Event: %v", errUpstream, err)
	}
	out := make([]Event, 0, len(wires))
	for i := range wires {
		out = append(out, *wireEventToSDK(&wires[i]))
	}

	// nextCursor：当返回数等于请求 limit 时认为还有下一页，cursor 编码下一 offset。
	cursor := ""
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit > 0 && len(wires) >= limit {
		next := filter.Offset + limit
		cursor = fmt.Sprintf("%d", next)
	}
	return out, cursor, nil
}

// GetMarket 查询单个市场（契约 §5）。
func (f *Facade) GetMarket(ctx context.Context, marketID string) (*Market, error) {
	if marketID == "" {
		return nil, fmt.Errorf("%w: empty market id", errPrecondition)
	}
	op := f.observe("GetMarket", "GET", "/markets/{id}")
	resp, err := f.low.GetMarketsId(ctx, marketID)
	op.done(resp, err)
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, body)
	}

	var wire wireMarket
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("%w: decode Market: %v", errUpstream, err)
	}
	return wireMarketToSDK(&wire), nil
}

// GetToken 反查单个 outcome token 所属 market（契约 §5）。
//
// 上游 gamma-service 没有 /tokens/{id} 端点；token 信息是 Market 的派生字段
// （Market.ClobTokenIDs 是 JSON 数组字符串，[0]=Yes、[1]=No）。本方法通过
// POST /markets/information { clobTokenIds: [tokenID] } 反查所属 Market 并
// 推断 OutcomeIndex。
//
// 找不到对应 Market → 返回 ErrNotFound。
func (f *Facade) GetToken(ctx context.Context, tokenID string) (*Token, error) {
	if tokenID == "" {
		return nil, fmt.Errorf("%w: empty token id", errPrecondition)
	}
	body := PostMarketsInformationJSONRequestBody{
		"clobTokenIds": []string{tokenID},
	}
	op := f.observe("GetToken", "POST", "/markets/information")
	resp, err := f.low.PostMarketsInformation(ctx, body)
	op.done(resp, err)
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, respBody)
	}

	var wires []wireMarket
	if err := json.Unmarshal(respBody, &wires); err != nil {
		return nil, fmt.Errorf("%w: decode []Market: %v", errUpstream, err)
	}
	for i := range wires {
		m := wireMarketToSDK(&wires[i])
		switch tokenID {
		case m.YesTokenID:
			return &Token{ID: tokenID, MarketID: m.ID, OutcomeIndex: 0}, nil
		case m.NoTokenID:
			return &Token{ID: tokenID, MarketID: m.ID, OutcomeIndex: 1}, nil
		}
	}
	return nil, fmt.Errorf("%w: token %s not found in any market", errNotFound, tokenID)
}

// ---------- wire 类型 + 映射 ----------

// wireEvent 对应上游 gamma-service models.Event 的 JSON 形态。
//
// 这里只声明 SDK Event 关心的字段；上游字段更多（Volume / Tags / Series 等）
// 暂不暴露，未来按需扩展。
type wireEvent struct {
	ID           string          `json:"id"`
	Slug         *string         `json:"slug"`
	Title        *string         `json:"title"`
	Description  *string         `json:"description"`
	Category     *string         `json:"category"`
	Active       *bool           `json:"active"`
	Closed       *bool           `json:"closed"`
	Archived     *bool           `json:"archived"`
	StartDate    *jsonTime       `json:"startDate"`
	EndDate      *jsonTime       `json:"endDate"`
	CreationDate *jsonTime       `json:"creationDate"`
	Markets      []wireMarket    `json:"markets"`
}

// wireMarket 对应上游 gamma-service models.Market 的 JSON 形态。
type wireMarket struct {
	ID              string    `json:"id"`
	ConditionID     string    `json:"conditionId"`
	Question        *string   `json:"question"`
	Slug            *string   `json:"slug"`
	Active          *bool     `json:"active"`
	Closed          *bool     `json:"closed"`
	AcceptingOrders *bool     `json:"acceptingOrders"`
	EndDate         *jsonTime `json:"endDate"`
	// ClobTokenIDs 是 JSON 数组字符串（如 `"[\"123\",\"456\"]"`），需要二次 unmarshal。
	ClobTokenIDs *string `json:"clobTokenIds"`
}

func wireEventToSDK(w *wireEvent) *Event {
	if w == nil {
		return nil
	}
	out := &Event{ID: w.ID}
	if w.Slug != nil {
		out.Slug = *w.Slug
	}
	if w.Title != nil {
		out.Title = *w.Title
	}
	if w.Description != nil {
		out.Description = *w.Description
	}
	if w.Category != nil {
		out.Category = *w.Category
	}
	if w.Active != nil {
		out.Active = *w.Active
	}
	if w.Closed != nil {
		out.Closed = *w.Closed
	}
	if w.Archived != nil {
		out.Archived = *w.Archived
	}
	if w.StartDate != nil {
		out.StartDate = w.StartDate.Time
	}
	if w.EndDate != nil {
		out.EndDate = w.EndDate.Time
	}
	if w.CreationDate != nil {
		out.CreationDate = w.CreationDate.Time
	}
	for i := range w.Markets {
		out.Markets = append(out.Markets, *wireMarketToSDK(&w.Markets[i]))
	}
	return out
}

func wireMarketToSDK(w *wireMarket) *Market {
	if w == nil {
		return nil
	}
	out := &Market{ID: w.ID, ConditionID: w.ConditionID}
	if w.Question != nil {
		out.Question = *w.Question
	}
	if w.Slug != nil {
		out.Slug = *w.Slug
	}
	if w.Active != nil {
		out.Active = *w.Active
	}
	if w.Closed != nil {
		out.Closed = *w.Closed
	}
	if w.AcceptingOrders != nil {
		out.AcceptingOrders = *w.AcceptingOrders
	}
	if w.EndDate != nil {
		out.EndDate = w.EndDate.Time
	}
	if w.ClobTokenIDs != nil && *w.ClobTokenIDs != "" {
		var ids []string
		if err := json.Unmarshal([]byte(*w.ClobTokenIDs), &ids); err == nil {
			if len(ids) > 0 {
				out.YesTokenID = ids[0]
			}
			if len(ids) > 1 {
				out.NoTokenID = ids[1]
			}
		}
	}
	return out
}

// ---------- 内部辅助 ----------

// drainBody 在 defer 中确保 body 被读完并关闭，避免 connection pool 泄漏。
func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// 编译期断言：APIError 仍是 clob 包定义；本包不重新声明，避免类型分裂。
var _ = (*clob.APIError)(nil)
