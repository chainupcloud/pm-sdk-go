package gamma

import "time"

// SDK 层手写类型（契约 §5）。
//
// 命名约定：与 generated.go 不冲突的类型沿用契约名（Event / Market / Token /
// EventFilter / Level / Outcome 等）；与 oapi-codegen 输出冲突的类型用 Sdk 前缀。
//
// 契约偏差：契约 §5 把 ID 标注为 int64，但上游 gamma-service 实际 wire format
// 是 string（见 pm-cup2026/services/gamma-service/internal/models/models.go 的
// Event.ID / Market.ID）；为与上游对齐并贴近 Polymarket gamma 行业惯例，本 SDK
// 暴露 string ID。Phase 8 release 前会同步修订契约文档。

// Event 是预测市场事件（契约 §5）。
//
// 一个 Event 通常含多个 Market（如 "2026 World Cup Winner" 事件下每个国家是一个 Market）。
type Event struct {
	ID           string    `json:"id"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Description  string    `json:"description,omitempty"`
	Category     string    `json:"category,omitempty"`
	Active       bool      `json:"active"`
	Closed       bool      `json:"closed"`
	Archived     bool      `json:"archived"`
	StartDate    time.Time `json:"start_date,omitempty"`
	EndDate      time.Time `json:"end_date,omitempty"`
	CreationDate time.Time `json:"creation_date,omitempty"`
	Markets      []Market  `json:"markets,omitempty"`
}

// Market 是单个二元预测市场（契约 §5）。
//
// 字段语义：
//   - ID: 上游 gamma 数据库主键（数字字符串）
//   - ConditionID: 链上 conditional-tokens condition_id（0x...）
//   - YesTokenID / NoTokenID: 从上游 ClobTokenIDs JSON 数组解析得到（[0]=Yes, [1]=No）
//   - OutcomeIndex: 0=Yes, 1=No；契约层留作上层语义（本 Market 表示一个二元市场整体，
//     OutcomeIndex 用 Token.OutcomeIndex 区分）
type Market struct {
	ID              string    `json:"id"`
	ConditionID     string    `json:"condition_id"`
	Question        string    `json:"question,omitempty"`
	Slug            string    `json:"slug,omitempty"`
	YesTokenID      string    `json:"yes_token_id,omitempty"`
	NoTokenID       string    `json:"no_token_id,omitempty"`
	Active          bool      `json:"active"`
	Closed          bool      `json:"closed"`
	AcceptingOrders bool      `json:"accepting_orders"`
	EndDate         time.Time `json:"end_date,omitempty"`

	// v0.2.0-rc1 新增：上下游绑定元数据。
	// 上游 gamma-service Market response（P1.3.0 起）JOIN
	// predict_upstream_*_binding 三表后暴露这些字段，做市端据此把
	// 下游标识翻译为上游（Polymarket 等）标识。market 未配置 binding
	// 时这些字段为空字符串 / 空切片。
	EventID             string   `json:"event_id,omitempty"`               // 上游 gamma event id
	Outcomes            []string `json:"outcomes,omitempty"`               // ["Yes","No"]，同序 [YesTokenID, NoTokenID]
	UpstreamType        string   `json:"upstream_type,omitempty"`          // "polymarket" / "kalshi" / ...
	UpstreamMarketExtID string   `json:"upstream_market_ext_id,omitempty"` // 上游 market 标识（Polymarket: condition_id）
	UpstreamEventExtID  string   `json:"upstream_event_ext_id,omitempty"`  // 上游 event 标识（Polymarket: gamma event slug 或 id）
	// UpstreamTokenExtIDs 是 per-token 上游标识平行数组，同序 [Outcomes / YesTokenID,NoTokenID]。
	// 调用方拼整张 binding 行时一次 GetMarket 即可取齐 per-token 标识，无需逐 token 调 GetToken。
	// Token.UpstreamTokenExtID 是同一数据按 OutcomeIndex 取的单值视图。
	UpstreamTokenExtIDs []string `json:"upstream_token_ext_ids,omitempty"`
}

// Token 是单个 outcome token（契约 §5）。
//
// pm-cup2026 二元市场每个 Market 含两个 Token（Yes / No）。
type Token struct {
	ID           string `json:"id"`
	MarketID     string `json:"market_id"`
	OutcomeIndex int    `json:"outcome_index"` // 0=Yes, 1=No

	// v0.2.0-rc1 新增：上游 token 标识（Polymarket: clob token_id uint256 字符串）。
	// 来源是上游 Market.upstreamTokenExtIds 平行数组按 OutcomeIndex 取下标。
	// token 未配置 binding 时为空字符串。
	UpstreamTokenExtID string `json:"upstream_token_ext_id,omitempty"`
}

// EventFilter 是 ListEvents 查询条件（契约 §5）。
type EventFilter struct {
	Limit        int        // 每页条数（0 = 上游默认 20）
	Offset       int        // 分页偏移
	Order        string     // 排序字段：id, label, slug, created_at, updated_at
	Ascending    bool       // 是否升序
	Slug         string     // 按 slug 精确筛选
	TagID        int        // 按标签 ID 筛选
	Active       *bool      // 筛选活跃事件
	Closed       *bool      // 筛选已关闭事件
	Archived     *bool      // 筛选已归档事件
	StartDateMin *time.Time // 开始日期下限
	StartDateMax *time.Time // 开始日期上限
	EndDateMin   *time.Time // 结束日期下限
	EndDateMax   *time.Time // 结束日期上限
}
