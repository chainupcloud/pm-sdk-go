package gamma

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	editors := []RequestEditorFn{}
	if filter.ExcludeTagSlug != "" {
		editors = append(editors, queryEditor("exclude_tag_slug", filter.ExcludeTagSlug))
	}
	resp, err := f.low.GetEvents(ctx, params, editors...)
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

// ListSeries 列出 tenant-scoped series，并把上游 offset 分页转换为 next cursor。
func (f *Facade) ListSeries(ctx context.Context, filter SeriesFilter) ([]Series, string, error) {
	query := url.Values{}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Offset > 0 {
		query.Set("offset", strconv.Itoa(filter.Offset))
	}
	if filter.Order != "" {
		query.Set("order", filter.Order)
	}
	if filter.Ascending {
		query.Set("ascending", "true")
	}
	if filter.Slug != "" {
		query.Set("slug", filter.Slug)
	}
	if filter.Recurrence != "" {
		query.Set("recurrence", filter.Recurrence)
	}
	if filter.Closed != nil {
		query.Set("closed", strconv.FormatBool(*filter.Closed))
	}
	if filter.ExcludeEvents {
		query.Set("exclude_events", "true")
	}

	op := f.observe("ListSeries", "GET", "/series")
	resp, err := f.low.GetSeries(ctx, valuesEditor(query))
	op.done(resp, err)
	if err != nil {
		return nil, "", wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, "", wrapHTTPError(resp, body)
	}

	var wires []wireSeries
	if err := json.Unmarshal(body, &wires); err != nil {
		return nil, "", fmt.Errorf("%w: decode []Series: %v", errUpstream, err)
	}
	out := make([]Series, 0, len(wires))
	for i := range wires {
		out = append(out, *wireSeriesToSDK(&wires[i]))
	}
	cursor := ""
	if filter.Limit > 0 && len(wires) >= filter.Limit {
		cursor = strconv.Itoa(filter.Offset + filter.Limit)
	}
	return out, cursor, nil
}

// GetSeries 按 ID 查询一个 tenant-scoped series；excludeEvents 用于控制是否返回历史 events。
func (f *Facade) GetSeries(ctx context.Context, seriesID string, excludeEvents bool) (*Series, error) {
	if seriesID == "" {
		return nil, fmt.Errorf("%w: empty series id", errPrecondition)
	}
	editors := []RequestEditorFn{}
	if excludeEvents {
		editors = append(editors, queryEditor("exclude_events", "true"))
	}
	op := f.observe("GetSeries", "GET", "/series/{id}")
	resp, err := f.low.GetSeriesId(ctx, seriesID, editors...)
	op.done(resp, err)
	if err != nil {
		return nil, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, wrapHTTPError(resp, body)
	}
	var wire wireSeries
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("%w: decode Series: %v", errUpstream, err)
	}
	return wireSeriesToSDK(&wire), nil
}

// ListSeriesPeriods 列出一个 recurring series 的周期窗口。
func (f *Facade) ListSeriesPeriods(ctx context.Context, seriesID string, filter SeriesPeriodFilter) (SeriesPeriodPage, error) {
	if seriesID == "" {
		return SeriesPeriodPage{}, fmt.Errorf("%w: empty series id", errPrecondition)
	}
	req, err := NewGetSeriesIdRequest(f.low.Server, seriesID)
	if err != nil {
		return SeriesPeriodPage{}, fmt.Errorf("gamma: build series periods request: %w", err)
	}
	req.URL.Path = strings.TrimSuffix(req.URL.Path, "/") + "/periods"
	query := req.URL.Query()
	if filter.Closed != nil {
		query.Set("closed", strconv.FormatBool(*filter.Closed))
	}
	if filter.Limit > 0 {
		query.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Cursor != "" {
		query.Set("cursor", filter.Cursor)
	}
	req.URL.RawQuery = query.Encode()
	req = req.WithContext(ctx)
	if err := f.low.applyEditors(ctx, req, nil); err != nil {
		return SeriesPeriodPage{}, fmt.Errorf("gamma: edit series periods request: %w", err)
	}

	op := f.observe("ListSeriesPeriods", "GET", "/series/{id}/periods")
	resp, err := f.low.Client.Do(req)
	op.done(resp, err)
	if err != nil {
		return SeriesPeriodPage{}, wrapTransportError(ctx, err)
	}
	defer drainBody(resp)

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return SeriesPeriodPage{}, wrapHTTPError(resp, body)
	}
	var wire wireSeriesPeriodPage
	if err := json.Unmarshal(body, &wire); err != nil {
		return SeriesPeriodPage{}, fmt.Errorf("%w: decode SeriesPeriodPage: %v", errUpstream, err)
	}
	out := SeriesPeriodPage{Data: make([]SeriesPeriod, 0, len(wire.Data))}
	if wire.NextCursor != nil {
		out.NextCursor = *wire.NextCursor
	}
	for i := range wire.Data {
		out.Data = append(out.Data, *wireSeriesPeriodToSDK(&wire.Data[i]))
	}
	return out, nil
}

func queryEditor(key, value string) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		query := req.URL.Query()
		query.Set(key, value)
		req.URL.RawQuery = query.Encode()
		return nil
	}
}

func valuesEditor(values url.Values) RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		query := req.URL.Query()
		for key, vals := range values {
			for _, value := range vals {
				query.Add(key, value)
			}
		}
		req.URL.RawQuery = query.Encode()
		return nil
	}
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

// GetMarketByConditionID 按链上 condition_id 反查单个 market（v0.2.0-rc1 新增）。
//
// 上游没有 /markets/by-condition 端点；本方法通过
// POST /markets/information { conditionIds: [conditionID] } 反查，取返回的
// 第一个 Market。空结果 → 返回 ErrNotFound。
//
// 用途：做市端 fill_subscriber 把 condition_id 翻译成下游 market_id（C10），
// merge_runner 解析上游 condition_id（C11）。
func (f *Facade) GetMarketByConditionID(ctx context.Context, conditionID string) (*Market, error) {
	if conditionID == "" {
		return nil, fmt.Errorf("%w: empty condition id", errPrecondition)
	}
	body := PostMarketsInformationJSONRequestBody{
		"conditionIds": []string{conditionID},
	}
	op := f.observe("GetMarketByConditionID", "POST", "/markets/information")
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
	if len(wires) == 0 {
		return nil, fmt.Errorf("%w: market with condition id %s not found", errNotFound, conditionID)
	}
	return wireMarketToSDK(&wires[0]), nil
}

// BatchGetMarketsByIDs 按 market id 批量拉取 market（v0.2.0-rc1 新增）。
//
// 通过 POST /markets/information { id: [...] } 一次查多个 market；上游 id
// 过滤是数字数组，因此 marketIDs 须为数字字符串（与 SDK 其他位置的 string ID
// 形态一致）。任一非数字 id → 返回 ErrPrecondition。
//
// 空入参返回空切片、无错误。返回顺序由上游决定，不保证与入参一致。
//
// 用途：做市端 binding sync 的批量预取（cron 全量 refresh / admin 批量场景）。
func (f *Facade) BatchGetMarketsByIDs(ctx context.Context, marketIDs []string) ([]Market, error) {
	if len(marketIDs) == 0 {
		return []Market{}, nil
	}
	ids := make([]int64, 0, len(marketIDs))
	for _, raw := range marketIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: market id %q is not numeric", errPrecondition, raw)
		}
		ids = append(ids, id)
	}
	body := PostMarketsInformationJSONRequestBody{
		"id": ids,
	}
	op := f.observe("BatchGetMarketsByIDs", "POST", "/markets/information")
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
	out := make([]Market, 0, len(wires))
	for i := range wires {
		out = append(out, *wireMarketToSDK(&wires[i]))
	}
	return out, nil
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
			return &Token{
				ID: tokenID, MarketID: m.ID, OutcomeIndex: 0,
				UpstreamTokenExtID: upstreamTokenExtIDAt(&wires[i], 0),
			}, nil
		case m.NoTokenID:
			return &Token{
				ID: tokenID, MarketID: m.ID, OutcomeIndex: 1,
				UpstreamTokenExtID: upstreamTokenExtIDAt(&wires[i], 1),
			}, nil
		}
	}
	return nil, fmt.Errorf("%w: token %s not found in any market", errNotFound, tokenID)
}

// upstreamTokenExtIDAt 从 wireMarket.UpstreamTokenExtIDs 平行数组按 outcomeIndex
// 取上游 token 标识；数组缺失 / 越界 / 该位置为空时返回空字符串。
func upstreamTokenExtIDAt(w *wireMarket, outcomeIndex int) string {
	exts := parseFlexibleStringArray(w.UpstreamTokenExtIDs)
	if outcomeIndex < 0 || outcomeIndex >= len(exts) {
		return ""
	}
	return exts[outcomeIndex]
}

// ---------- wire 类型 + 映射 ----------

// wireEvent 对应上游 gamma-service models.Event 的 JSON 形态。
//
// 这里只声明 SDK Event 关心的字段；上游字段更多（Volume / Tags / Series 等）
// 暂不暴露，未来按需扩展。
type wireEvent struct {
	ID           string       `json:"id"`
	Slug         *string      `json:"slug"`
	Title        *string      `json:"title"`
	Description  *string      `json:"description"`
	Category     *string      `json:"category"`
	Active       *bool        `json:"active"`
	Closed       *bool        `json:"closed"`
	Archived     *bool        `json:"archived"`
	StartDate    *jsonTime    `json:"startDate"`
	EndDate      *jsonTime    `json:"endDate"`
	CreationDate *jsonTime    `json:"creationDate"`
	Markets      []wireMarket `json:"markets"`
}

type wireSeries struct {
	ID         string      `json:"id"`
	Slug       *string     `json:"slug"`
	Title      *string     `json:"title"`
	Ticker     *string     `json:"ticker"`
	SeriesType *string     `json:"seriesType"`
	Recurrence *string     `json:"recurrence"`
	Active     *bool       `json:"active"`
	Closed     *bool       `json:"closed"`
	Archived   *bool       `json:"archived"`
	Events     []wireEvent `json:"events"`
}

type wireSeriesPeriodPrice struct {
	Price     string   `json:"price"`
	Source    string   `json:"source"`
	SampledAt jsonTime `json:"sampledAt"`
}

type wireSeriesPeriod struct {
	ID          string                 `json:"id"`
	SeriesID    string                 `json:"seriesId"`
	EventID     string                 `json:"eventId"`
	MarketID    string                 `json:"marketId"`
	WindowStart jsonTime               `json:"windowStart"`
	WindowEnd   jsonTime               `json:"windowEnd"`
	Stage       string                 `json:"stage"`
	PriceToBeat *wireSeriesPeriodPrice `json:"priceToBeat"`
	FinalPrice  *wireSeriesPeriodPrice `json:"finalPrice"`
	Result      *string                `json:"result"`
	Event       *wireEvent             `json:"event"`
}

type wireSeriesPeriodPage struct {
	Data       []wireSeriesPeriod `json:"data"`
	NextCursor *string            `json:"nextCursor"`
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
	// token/outcome 字段兼容 JSON 数组与 JSON 数组字符串两种 wire 形态。
	ClobTokenIDs json.RawMessage `json:"clobTokenIds"`
	// v0.2.0-rc1 新增：上游 gamma-service P1.3.0 起暴露的字段。
	EventID string `json:"eventId"`
	// Outcomes 与 ClobTokenIDs 同样是 JSON 数组字符串（如 `"[\"Yes\",\"No\"]"`），需要二次 unmarshal。
	Outcomes            json.RawMessage `json:"outcomes"`
	UpstreamType        string          `json:"upstreamType"`
	UpstreamMarketExtID string          `json:"upstreamMarketExtId"`
	UpstreamEventExtID  string          `json:"upstreamEventExtId"`
	// UpstreamTokenExtIDs 是与 ClobTokenIDs / Outcomes 同序的 JSON 数组字符串，需要二次 unmarshal。
	UpstreamTokenExtIDs json.RawMessage `json:"upstreamTokenExtIds"`
}

func wireSeriesToSDK(w *wireSeries) *Series {
	if w == nil {
		return nil
	}
	out := &Series{ID: w.ID}
	if w.Slug != nil {
		out.Slug = *w.Slug
	}
	if w.Title != nil {
		out.Title = *w.Title
	}
	if w.Ticker != nil {
		out.Ticker = *w.Ticker
	}
	if w.SeriesType != nil {
		out.SeriesType = *w.SeriesType
	}
	if w.Recurrence != nil {
		out.Recurrence = *w.Recurrence
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
	for i := range w.Events {
		out.Events = append(out.Events, *wireEventToSDK(&w.Events[i]))
	}
	return out
}

func wireSeriesPeriodToSDK(w *wireSeriesPeriod) *SeriesPeriod {
	if w == nil {
		return nil
	}
	out := &SeriesPeriod{
		ID: w.ID, SeriesID: w.SeriesID, EventID: w.EventID, MarketID: w.MarketID,
		WindowStart: w.WindowStart.Time, WindowEnd: w.WindowEnd.Time, Stage: w.Stage, Result: w.Result,
	}
	if w.PriceToBeat != nil {
		out.PriceToBeat = &SeriesPeriodPrice{Price: w.PriceToBeat.Price, Source: w.PriceToBeat.Source, SampledAt: w.PriceToBeat.SampledAt.Time}
	}
	if w.FinalPrice != nil {
		out.FinalPrice = &SeriesPeriodPrice{Price: w.FinalPrice.Price, Source: w.FinalPrice.Source, SampledAt: w.FinalPrice.SampledAt.Time}
	}
	if w.Event != nil {
		out.Event = wireEventToSDK(w.Event)
	}
	return out
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
	out := &Market{
		ID:                  w.ID,
		ConditionID:         w.ConditionID,
		EventID:             w.EventID,
		UpstreamType:        w.UpstreamType,
		UpstreamMarketExtID: w.UpstreamMarketExtID,
		UpstreamEventExtID:  w.UpstreamEventExtID,
	}
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
	if ids := parseFlexibleStringArray(w.ClobTokenIDs); len(ids) > 0 {
		if len(ids) > 0 {
			out.YesTokenID = ids[0]
		}
		if len(ids) > 1 {
			out.NoTokenID = ids[1]
		}
	}
	out.Outcomes = parseFlexibleStringArray(w.Outcomes)
	out.UpstreamTokenExtIDs = parseFlexibleStringArray(w.UpstreamTokenExtIDs)
	return out
}

// parseJSONStringArray 把 gamma wire 上的 JSON 数组字符串
// （如 `"[\"a\",\"b\"]"`）解析为 []string；nil / 空 / 非法 JSON 返回 nil。
func parseJSONStringArray(raw *string) []string {
	if raw == nil || *raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(*raw), &out); err != nil {
		return nil
	}
	return out
}

func parseFlexibleStringArray(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil
	}
	return parseJSONStringArray(&encoded)
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
