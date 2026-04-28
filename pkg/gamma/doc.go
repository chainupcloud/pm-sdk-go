// Package gamma 封装 pm-cup2026 gamma-service 的 Event/Market/Token 元数据查询接口。
//
// 包内 generated.go 由 scripts/codegen.sh 通过 oapi-codegen 从上游 OpenAPI 生成，
// 提供低层 *Client、模型类型与 embedded spec。Phase 4 起将在本目录新增手写
// client.go / types.go，封装为契约 §5 的高层门面（GetEvent / ListEvents 等）。
package gamma
