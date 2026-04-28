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
}

// Token 是单个 outcome token（契约 §5）。
//
// pm-cup2026 二元市场每个 Market 含两个 Token（Yes / No）。
type Token struct {
	ID           string `json:"id"`
	MarketID     string `json:"market_id"`
	OutcomeIndex int    `json:"outcome_index"` // 0=Yes, 1=No
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
